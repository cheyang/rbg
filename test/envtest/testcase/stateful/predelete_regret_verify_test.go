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

// Review harness for sgl-project/rbg#535 — finding F1 (bug-canary).
// A Stateful instance that entered PreparingDelete through a blocked
// scale-down keeps that lifecycle state forever when the scale-down is
// canceled: no stateful code path returns an in-range instance to Normal
// (stateless mode has managePreparingDelete for exactly this). These specs
// PASS while the gap exists; a fix must FLIP them to red (then invert the
// assertion to `Equal(Normal)` as a contract).
package stateful

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/test/envtest/testutil"
)

const (
	verifyF1Timeout  = 25 * time.Second
	verifyF1Interval = 250 * time.Millisecond
	verifyF1Label    = "example.com/drain-protection"
)

func verifyF1RegretScenario(policy constants.PodManagementPolicyType) {
	namespace := fmt.Sprintf("test-predelete-regret-%d", time.Now().UnixNano())
	testutil.CreateNamespace(namespace)
	defer testutil.DeleteNamespace(namespace)

	set := newRolloutRecoverySet("regret", namespace)
	set.Spec.Replicas = ptr.To[int32](2)
	set.Spec.PodManagementPolicy = policy
	set.Spec.Lifecycle = &workloadsv1alpha2.RoleInstanceSetLifecycle{
		PreDelete: &workloadsv1alpha2.RoleInstanceSetLifecycleHook{
			LabelsHandler: map[string]string{verifyF1Label: "hold"},
		},
	}
	Expect(testutil.K8sClient.Create(testutil.Ctx, set)).To(Succeed())
	setKey := client.ObjectKeyFromObject(set)
	for ordinal := range 2 {
		podKey := types.NamespacedName{Name: fmt.Sprintf("%s-%d-worker-0", set.Name, ordinal), Namespace: namespace}
		Eventually(func() error { return setPodReady(podKey, true) }, verifyF1Timeout, verifyF1Interval).Should(Succeed())
	}
	awaitRolloutSet(setKey, func(current *workloadsv1alpha2.RoleInstanceSet) bool {
		return current.Status.ReadyReplicas == 2
	})

	targetKey := types.NamespacedName{Name: set.Name + "-1", Namespace: namespace}
	target := &workloadsv1alpha2.RoleInstance{}
	Expect(testutil.K8sClient.Get(testutil.Ctx, targetKey, target)).To(Succeed())
	targetUID := target.UID

	// Hook the instance and scale down: deletion is blocked.
	before := target.DeepCopy()
	target.Labels[verifyF1Label] = "hold"
	Expect(testutil.K8sClient.Patch(testutil.Ctx, target, client.MergeFrom(before))).To(Succeed())
	updateRolloutSpec(setKey, func(current *workloadsv1alpha2.RoleInstanceSet) {
		current.Spec.Replicas = ptr.To[int32](1)
	})
	Eventually(func() (string, error) {
		if err := testutil.K8sClient.Get(testutil.Ctx, targetKey, target); err != nil {
			return "", err
		}
		return target.Labels[constants.RoleInstanceSetLifecycleStateKey], nil
	}, verifyF1Timeout, verifyF1Interval).Should(Equal(string(constants.RoleInstanceLifecycleStatePreparingDelete)))

	// Regret: scale back up before releasing the hook. The instance is in
	// range again and should return to Normal — today nothing resets it.
	updateRolloutSpec(setKey, func(current *workloadsv1alpha2.RoleInstanceSet) {
		current.Spec.Replicas = ptr.To[int32](2)
	})
	Consistently(func() (string, error) {
		if err := testutil.K8sClient.Get(testutil.Ctx, targetKey, target); err != nil {
			return "", err
		}
		if target.UID != targetUID || target.DeletionTimestamp != nil {
			return "replaced-or-deleting", nil
		}
		return target.Labels[constants.RoleInstanceSetLifecycleStateKey], nil
	}, 5*time.Second, verifyF1Interval).Should(Equal(string(constants.RoleInstanceLifecycleStatePreparingDelete)),
		"F1 canary: the regretted instance never leaves PreparingDelete")
}

var _ = Describe("Verify F1: Stateful PreDelete regret", func() {
	It("F1 regret keeps stale PreparingDelete state [Parallel]", func() {
		verifyF1RegretScenario(constants.ParallelPodManagement)
	})
	It("F1 regret keeps stale PreparingDelete state [OrderedReady]", func() {
		verifyF1RegretScenario(constants.OrderedReadyPodManagement)
	})
})
