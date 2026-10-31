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

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/test/envtest/testutil"
)

var _ = Describe("Stateful RoleInstanceSet PreDelete", func() {
	const (
		timeout  = 25 * time.Second
		interval = 250 * time.Millisecond
		label    = "example.com/drain-protection"
	)

	var namespace string
	BeforeEach(func() {
		namespace = fmt.Sprintf("test-predelete-%d", time.Now().UnixNano())
		testutil.CreateNamespace(namespace)
	})
	AfterEach(func() {
		testutil.DeleteNamespace(namespace)
	})

	DescribeTable("waits before deleting and resumes when the handler is released",
		func(policy constants.PodManagementPolicyType) {
			set := newRolloutRecoverySet("hooked", namespace)
			set.Spec.Replicas = ptr.To[int32](2)
			set.Spec.PodManagementPolicy = policy
			set.Spec.Lifecycle = &workloadsv1alpha2.RoleInstanceSetLifecycle{
				PreDelete: &workloadsv1alpha2.RoleInstanceSetLifecycleHook{
					LabelsHandler: map[string]string{label: "hold"},
				},
			}
			Expect(testutil.K8sClient.Create(testutil.Ctx, set)).To(Succeed())
			setKey := client.ObjectKeyFromObject(set)
			for ordinal := range 2 {
				podKey := types.NamespacedName{Name: fmt.Sprintf("%s-%d-worker-0", set.Name, ordinal), Namespace: namespace}
				Eventually(func() error { return setPodReady(podKey, true) }, timeout, interval).Should(Succeed())
			}
			awaitRolloutSet(setKey, func(current *workloadsv1alpha2.RoleInstanceSet) bool {
				return current.Status.ReadyReplicas == 2
			})

			targetKey := types.NamespacedName{Name: set.Name + "-1", Namespace: namespace}
			target := &workloadsv1alpha2.RoleInstance{}
			Expect(testutil.K8sClient.Get(testutil.Ctx, targetKey, target)).To(Succeed())
			targetUID := target.UID
			before := target.DeepCopy()
			target.Labels[label] = "hold"
			Expect(testutil.K8sClient.Patch(testutil.Ctx, target, client.MergeFrom(before))).To(Succeed())
			updateRolloutSpec(setKey, func(current *workloadsv1alpha2.RoleInstanceSet) {
				current.Spec.Replicas = ptr.To[int32](1)
			})
			Eventually(func() (string, error) {
				if err := testutil.K8sClient.Get(testutil.Ctx, targetKey, target); err != nil {
					return "", err
				}
				return target.Labels[constants.RoleInstanceSetLifecycleStateKey], nil
			}, timeout, interval).Should(Equal(string(constants.RoleInstanceLifecycleStatePreparingDelete)))

			// Trigger another real reconcile, rather than relying only on the
			// object remaining present while no events are being processed.
			updateRolloutSpec(setKey, func(current *workloadsv1alpha2.RoleInstanceSet) {
				if current.Annotations == nil {
					current.Annotations = map[string]string{}
				}
				current.Annotations["example.com/reconcile"] = "again"
			})
			Consistently(func() (bool, error) {
				if err := testutil.K8sClient.Get(testutil.Ctx, targetKey, target); err != nil {
					return false, err
				}
				return target.UID == targetUID && target.DeletionTimestamp == nil && target.Labels[label] == "hold", nil
			}, 2*time.Second, interval).Should(BeTrue())

			// This metadata-only update must enqueue the owning RIS and
			// resume deletion without any additional replicas/template edit.
			before = target.DeepCopy()
			delete(target.Labels, label)
			Expect(testutil.K8sClient.Patch(testutil.Ctx, target, client.MergeFrom(before))).To(Succeed())
			Eventually(func() bool {
				return apierrors.IsNotFound(testutil.K8sClient.Get(testutil.Ctx, targetKey, &workloadsv1alpha2.RoleInstance{}))
			}, timeout, interval).Should(BeTrue())
			survivor := &workloadsv1alpha2.RoleInstance{}
			Expect(testutil.K8sClient.Get(testutil.Ctx, types.NamespacedName{Name: set.Name + "-0", Namespace: namespace}, survivor)).To(Succeed())
			Expect(survivor.DeletionTimestamp).To(BeNil())
		},
		Entry("Parallel", constants.ParallelPodManagement),
		Entry("OrderedReady", constants.OrderedReadyPodManagement),
	)
})
