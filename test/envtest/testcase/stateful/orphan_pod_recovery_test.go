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

// Reviewer verification harness for PR #512 (Reviewer A, Claude).
//
// Controller-level contract for issue #511: during a StatefulSet ->
// RoleInstanceSet migration the new RoleInstance's stable Pod name is
// squatted by a Pod still controlled by the removed legacy StatefulSet.
//
// Fixture: the migration collision is simulated by re-pointing the live
// Pod's controller ownerRef at a (legacy) StatefulSet. This is exactly the
// API-server state the new RoleInstance sees after the legacy workload is
// deleted but before garbage collection removes its Pod — same name, foreign
// controller. The pod event handler (instance_event_handler.go) maps the
// ownerRef transition to a RoleInstance reconcile, so the first reconcile
// after the swap observes the collision deterministically (no create/delete
// race).
//
// TEST POLARITY (contract): must PASS on the PR head. On the merge-base
// (a0b11e6) the first It() fails — Scale returned (false, nil) for a
// foreign-owned same-name Pod, so no FailedScale condition is recorded — and
// the second It() fails — nothing maps the foreign Pod's deletion to the
// RoleInstance, so the replacement Pod is never created without a manual
// reconcile: the exact symptom reported in issue #511.

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/test/envtest/testutil"
)

var _ = Describe("Migration orphan Pod recovery (issue #511 / PR #512)", func() {
	const (
		timeout  = 30 * time.Second
		interval = 250 * time.Millisecond
	)

	var testNs string

	BeforeEach(func() {
		testNs = fmt.Sprintf("test-orphan-mig-%d", time.Now().UnixNano())
		testutil.CreateNamespace(testNs)
	})

	AfterEach(func() {
		testutil.DeleteNamespace(testNs)
	})

	// reachCollision creates a stateful RoleInstanceSet with one instance and
	// its Pod, then re-points the Pod's controller ownerRef at a legacy
	// StatefulSet, leaving the API in the post-migration pre-GC state.
	reachCollision := func(setName string) (types.NamespacedName, types.UID, types.NamespacedName) {
		set := newRolloutRecoverySet(setName, testNs)
		Expect(testutil.K8sClient.Create(testutil.Ctx, set)).To(Succeed())

		instanceKey := types.NamespacedName{Name: setName + "-0", Namespace: testNs}
		instance := &workloadsv1alpha2.RoleInstance{}
		Eventually(func() error {
			return testutil.K8sClient.Get(testutil.Ctx, instanceKey, instance)
		}, timeout, interval).Should(Succeed(), "RoleInstance should be created")

		podKey := types.NamespacedName{Name: setName + "-0-worker-0", Namespace: testNs}
		pod := &corev1.Pod{}
		Eventually(func() error {
			return testutil.K8sClient.Get(testutil.Ctx, podKey, pod)
		}, timeout, interval).Should(Succeed(), "initial Pod should be created")
		Expect(metav1.IsControlledBy(pod, instance)).To(BeTrue(),
			"the initial Pod must be owned by the RoleInstance before simulating the migration")

		// Swap the controller ownerRef to the legacy StatefulSet: same stable
		// name, foreign controller. This both simulates the migration state and
		// deterministically triggers a reconcile (ownerRef transition event).
		foreign := pod.DeepCopy()
		foreign.OwnerReferences = []metav1.OwnerReference{{
			APIVersion: "apps/v1",
			Kind:       "StatefulSet",
			Name:       setName + "-legacy",
			UID:        types.UID("legacy-statefulset-uid"),
			Controller: ptr.To(true),
		}}
		Expect(testutil.K8sClient.Patch(testutil.Ctx, foreign, client.MergeFrom(pod))).To(Succeed())
		return instanceKey, instance.UID, podKey
	}

	It("records FailedScale and retries while a foreign-owned Pod squats the stable name", func() {
		instanceKey, _, podKey := reachCollision("orphan-cond")

		Eventually(func() (string, error) {
			instance := &workloadsv1alpha2.RoleInstance{}
			if err := testutil.K8sClient.Get(testutil.Ctx, instanceKey, instance); err != nil {
				return "", err
			}
			for _, cond := range instance.Status.Conditions {
				if cond.Type == workloadsv1alpha2.RoleInstanceFailedScale && cond.Status == corev1.ConditionTrue {
					return cond.Message, nil
				}
			}
			return "", fmt.Errorf("no FailedScale condition yet")
		}, timeout, interval).Should(ContainSubstring("is not controlled by RoleInstance"),
			"the foreign same-name Pod must surface as a FailedScale condition, not be silently skipped")

		// While the collision persists the controller must neither adopt nor
		// delete the foreign Pod, nor wedge itself in any other way.
		Consistently(func() ([]metav1.OwnerReference, error) {
			pod := &corev1.Pod{}
			if err := testutil.K8sClient.Get(testutil.Ctx, podKey, pod); err != nil {
				return nil, err
			}
			return pod.OwnerReferences, nil
		}, 4*time.Second, interval).Should(HaveLen(1),
			"the foreign Pod must not be adopted, replaced, or deleted while it awaits GC")
	})

	It("creates the replacement Pod after the foreign Pod is GC'd, without a manual reconcile", func() {
		instanceKey, instanceUID, podKey := reachCollision("orphan-gc")

		// Wait until the controller has actually observed the collision, so the
		// retry loop is provably engaged before the foreign Pod disappears.
		Eventually(func() (bool, error) {
			instance := &workloadsv1alpha2.RoleInstance{}
			if err := testutil.K8sClient.Get(testutil.Ctx, instanceKey, instance); err != nil {
				return false, err
			}
			for _, cond := range instance.Status.Conditions {
				if cond.Type == workloadsv1alpha2.RoleInstanceFailedScale && cond.Status == corev1.ConditionTrue {
					return true, nil
				}
			}
			return false, nil
		}, timeout, interval).Should(BeTrue(), "collision observed before GC")

		// GC removes the foreign Pod. Its deletion event maps to the legacy
		// StatefulSet, NOT to the RoleInstance — so only the controller's own
		// error-driven retry can create the replacement. No manual reconcile
		// (annotation touch, status touch) is performed anywhere in this test.
		foreign := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: podKey.Name, Namespace: podKey.Namespace}}
		Expect(testutil.K8sClient.Delete(testutil.Ctx, foreign)).To(Succeed())
		Eventually(func() error {
			return testutil.K8sClient.Get(testutil.Ctx, podKey, &corev1.Pod{})
		}, timeout, interval).ShouldNot(Succeed(), "foreign Pod should be gone")

		Eventually(func() (bool, error) {
			pod := &corev1.Pod{}
			if err := testutil.K8sClient.Get(testutil.Ctx, podKey, pod); err != nil {
				return false, err
			}
			return string(metav1.GetControllerOf(pod).UID) == string(instanceUID), nil
		}, timeout, interval).Should(BeTrue(),
			"the replacement Pod must be created and owned by the same RoleInstance via the retry loop alone")

		// Round-2 contract (head 9d1e7654): FailedScale is transient — once the
		// replacement Pod exists, a healthy reconcile drops the stale condition
		// instead of latching it True forever (review: FAILEDSCALE-LATCH).
		Eventually(func() (bool, error) {
			instance := &workloadsv1alpha2.RoleInstance{}
			if err := testutil.K8sClient.Get(testutil.Ctx, instanceKey, instance); err != nil {
				return false, err
			}
			for _, cond := range instance.Status.Conditions {
				if cond.Type == workloadsv1alpha2.RoleInstanceFailedScale {
					return cond.Status != corev1.ConditionTrue, nil
				}
			}
			return true, nil
		}, timeout, interval).Should(BeTrue(), "FailedScale must clear after recovery")

		instance := &workloadsv1alpha2.RoleInstance{}
		Expect(testutil.K8sClient.Get(testutil.Ctx, instanceKey, instance)).To(Succeed())
		Expect(instance.UID).To(Equal(instanceUID), "the RoleInstance must not be recreated in the process")
	})
})
