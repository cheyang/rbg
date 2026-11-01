/*
Copyright 2026 The RBG Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package stateful

// Reviewer harness (Claude) for sgl-project/rbg PR #536 — finding F1 (envtest layer).
//
// POLARITY: contract test. FAILS on the PR head (bug present) and PASSES once
// the PreparingDelete -> Normal "regret" transition releases the lifecycle
// readiness block (see the unit-layer twin in
// pkg/reconciler/roleinstance/lifecycle_regret_claude_test.go).
//
// This drives the real RoleInstance controller in envtest: after a scale-down
// is regretted (state -> Normal with markPodNotReady=false, what
// managePreparingDelete does at scale.go:185), the instance and its Pods must
// become Ready again.

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	api "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	instancelifecycle "sigs.k8s.io/rbgs/pkg/inplace/instance/lifecycle"
	"sigs.k8s.io/rbgs/test/envtest/testutil"
)

var _ = Describe("RoleInstance lifecycle regret release", func() {
	const timeout = 25 * time.Second
	const interval = 200 * time.Millisecond
	var namespace string
	BeforeEach(func() {
		namespace = fmt.Sprintf("test-lifecycle-regret-%d", time.Now().UnixNano())
		testutil.CreateNamespace(namespace)
	})
	AfterEach(func() { testutil.DeleteNamespace(namespace) })

	It("must unblock the instance and its Pods after PreparingDelete is regretted", func() {
		template := newRolloutRecoverySet("regret", namespace).Spec.RoleInstanceTemplate.RoleInstanceSpec
		instance := &api.RoleInstance{
			ObjectMeta: metav1.ObjectMeta{Name: "regret-0", Namespace: namespace, Labels: map[string]string{
				constants.RoleTypeLabelKey: string(constants.ComponentsTemplateType),
			}},
			Spec: template,
		}
		Expect(testutil.K8sClient.Create(testutil.Ctx, instance)).To(Succeed())
		key := client.ObjectKeyFromObject(instance)
		podKey := types.NamespacedName{Namespace: namespace, Name: "regret-0-worker-0"}
		Eventually(func() error { return setPodReady(podKey, true) }, timeout, interval).Should(Succeed())

		readyState := func(want corev1.ConditionStatus) error {
			got := &api.RoleInstance{}
			if err := testutil.K8sClient.Get(testutil.Ctx, key, got); err != nil {
				return err
			}
			for _, condition := range got.Status.Conditions {
				if condition.Type == api.RoleInstanceReady {
					if condition.Status != want {
						return fmt.Errorf("RI Ready=%s, want %s", condition.Status, want)
					}
					return nil
				}
			}
			return fmt.Errorf("RI Ready missing")
		}
		podGate := func() (corev1.ConditionStatus, error) {
			pod := &corev1.Pod{}
			if err := testutil.K8sClient.Get(testutil.Ctx, podKey, pod); err != nil {
				return "", err
			}
			for _, condition := range pod.Status.Conditions {
				if condition.Type == constants.InstancePodReadyConditionType {
					return condition.Status, nil
				}
			}
			return "", fmt.Errorf("pod gate missing")
		}

		Eventually(func() error { return readyState(corev1.ConditionTrue) }, timeout, interval).Should(Succeed())

		// Scale-down with a PreDelete hook that marks NotReady.
		control := instancelifecycle.New(testutil.K8sClient)
		Eventually(func() error {
			if err := testutil.K8sClient.Get(testutil.Ctx, key, instance); err != nil {
				return err
			}
			_, _, err := control.UpdateRoleInstanceLifecycle(instance, constants.RoleInstanceLifecycleStatePreparingDelete, true)
			return err
		}, timeout, interval).Should(Succeed())
		Eventually(func() error {
			if err := readyState(corev1.ConditionFalse); err != nil {
				return err
			}
			status, err := podGate()
			if err != nil {
				return err
			}
			if status != corev1.ConditionFalse {
				return fmt.Errorf("pod gate=%s, want False", status)
			}
			return nil
		}, timeout, interval).Should(Succeed())

		// Regret the scale-down: the set no longer has MarkNotReady, so
		// managePreparingDelete moves the instance back to Normal with
		// markPodNotReady=false.
		Eventually(func() error {
			if err := testutil.K8sClient.Get(testutil.Ctx, key, instance); err != nil {
				return err
			}
			_, _, err := control.UpdateRoleInstanceLifecycle(instance, constants.RoleInstanceLifecycleStateNormal, false)
			return err
		}, timeout, interval).Should(Succeed())

		// Contract: the block must be released.
		Eventually(func() error { return readyState(corev1.ConditionTrue) }, timeout, interval).Should(Succeed())
		Eventually(func() error {
			status, err := podGate()
			if err != nil {
				return err
			}
			if status != corev1.ConditionTrue {
				return fmt.Errorf("pod gate=%s, want True", status)
			}
			return nil
		}, timeout, interval).Should(Succeed())
	})
})
