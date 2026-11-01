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

// Verification harness for sgl-project/rbg PR #537 (Reviewer B / Codex).
// Additive only.
//
// F1 integration layer, CONTRACT polarity: an instance admitted to
// PreparingUpdate whose ordinal is later pinned below partition must leave
// PreparingUpdate (rescue to Normal, or completion of the admitted update).
// On the PR head nothing ever exits that state for a non-target ordinal, so
// this spec times out and FAILS (= reproduction); a rescue fix flips it green.

package stateful

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	api "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/test/envtest/testutil"
)

var _ = Describe("Stateful InPlaceUpdate hook partition stranding (PR537 verify)", func() {
	const timeout = 30 * time.Second
	const interval = 200 * time.Millisecond
	const label = "example.com/inplace-protection"
	var namespace string
	BeforeEach(func() {
		namespace = fmt.Sprintf("test-inplace-strand-%d", time.Now().UnixNano())
		testutil.CreateNamespace(namespace)
	})
	AfterEach(func() { testutil.DeleteNamespace(namespace) })

	It("releases an admitted PreparingUpdate instance pinned below a raised partition", func() {
		set := newRolloutRecoverySet("stranded-hook", namespace)
		set.Spec.Replicas = ptr.To[int32](2)
		set.Spec.UpdateStrategy.Type = api.InPlaceIfPossibleUpdateStrategyType
		set.Spec.Lifecycle = &api.RoleInstanceSetLifecycle{InPlaceUpdate: &api.RoleInstanceSetLifecycleHook{
			LabelsHandler: map[string]string{label: "hold"},
		}}
		Expect(testutil.K8sClient.Create(testutil.Ctx, set)).To(Succeed())
		setKey := client.ObjectKeyFromObject(set)
		instance1Key := types.NamespacedName{Namespace: namespace, Name: set.Name + "-1"}

		// Envtest has no kubelet. Supply container runtime status explicitly;
		// real RI/RIS controllers still perform all template and lifecycle writes.
		setRuntimeStatus := func(ord int) error {
			pod := &corev1.Pod{}
			podKey := types.NamespacedName{Namespace: namespace, Name: fmt.Sprintf("%s-%d-worker-0", set.Name, ord)}
			if err := testutil.K8sClient.Get(testutil.Ctx, podKey, pod); err != nil {
				return err
			}
			testutil.SetPodRunningAndReady(pod)
			for i := range pod.Status.ContainerStatuses {
				pod.Status.ContainerStatuses[i].ImageID = "test://" + pod.Spec.Containers[i].Image
				pod.Status.ContainerStatuses[i].ContainerID = "test://container-" + pod.Spec.Containers[i].Image
			}
			return testutil.K8sClient.Status().Update(testutil.Ctx, pod)
		}
		Eventually(func() error { return setRuntimeStatus(0) }, timeout, interval).Should(Succeed())
		Eventually(func() error { return setRuntimeStatus(1) }, timeout, interval).Should(Succeed())
		awaitRolloutSet(setKey, func(got *api.RoleInstanceSet) bool { return got.Status.ReadyReplicas == 2 })

		// Hook instance-1, then start a rollout covering only ord >= 1.
		instance1 := &api.RoleInstance{}
		Expect(testutil.K8sClient.Get(testutil.Ctx, instance1Key, instance1)).To(Succeed())
		before := instance1.DeepCopy()
		instance1.Labels[label] = "hold"
		Expect(testutil.K8sClient.Patch(testutil.Ctx, instance1, client.MergeFrom(before))).To(Succeed())
		updateRolloutSpec(setKey, func(got *api.RoleInstanceSet) {
			got.Spec.RoleInstanceTemplate.Components[0].Template.Spec.Containers[0].Image = "nginx:2.0"
			got.Spec.UpdateStrategy.Partition = ptr.To(intstr.FromInt32(1))
		})
		Eventually(func() (string, error) {
			if err := testutil.K8sClient.Get(testutil.Ctx, instance1Key, instance1); err != nil {
				return "", err
			}
			return instance1.Labels[constants.RoleInstanceSetLifecycleStateKey], nil
		}, timeout, interval).Should(Equal(string(constants.RoleInstanceLifecycleStatePreparingUpdate)),
			"ord-1 must be admitted to PreparingUpdate first")

		// Pin ord-1 below partition, then let the hook owner release it.
		updateRolloutSpec(setKey, func(got *api.RoleInstanceSet) {
			got.Spec.UpdateStrategy.Partition = ptr.To(intstr.FromInt32(2))
		})
		awaitRolloutSet(setKey, func(got *api.RoleInstanceSet) bool { return got.Status.ObservedGeneration >= got.Generation })
		Expect(testutil.K8sClient.Get(testutil.Ctx, instance1Key, instance1)).To(Succeed())
		before = instance1.DeepCopy()
		delete(instance1.Labels, label)
		Expect(testutil.K8sClient.Patch(testutil.Ctx, instance1, client.MergeFrom(before))).To(Succeed())

		// CONTRACT: the admitted operation must be rescued or completed, never
		// stranded. Fails on the PR head: nothing exits PreparingUpdate for a
		// partition-pinned ordinal.
		Eventually(func() (string, error) {
			if err := testutil.K8sClient.Get(testutil.Ctx, instance1Key, instance1); err != nil {
				return "", err
			}
			return instance1.Labels[constants.RoleInstanceSetLifecycleStateKey], nil
		}, 20*time.Second, interval).ShouldNot(Equal(string(constants.RoleInstanceLifecycleStatePreparingUpdate)),
			"F1: admitted PreparingUpdate instance stranded below partition")
	})
})
