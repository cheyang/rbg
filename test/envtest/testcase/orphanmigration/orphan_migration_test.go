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

package orphanmigration

// Integration-layer (envtest) verification harness for sgl-project/rbg PR #512
// (reviewer: codex). Additive only: no production code is modified.
//
// Both specs reproduce issue #511's RoleInstance-level state: a RoleInstance
// exists whose replacement Pod name is still held by a foreign-owned Pod (the
// StatefulSet -> RoleInstanceSet migration GC window), then the foreign Pod is
// garbage-collected.
//
// It[1] "creates the replacement Pod via error-driven retry" — the fix contract.
//   base (a0b11e6): FAILS — after the foreign Pod is deleted nothing re-triggers
//                   the reconcile (the deletion event's owner is the StatefulSet,
//                   not the RoleInstance) and Scale returned no error, so no
//                   replacement Pod appears within the timeout.
//   head (a8ef35f): PASSES — Scale returns an error, controller-runtime retries
//                   with exponential backoff, and a retry creates the
//                   replacement Pod shortly after the foreign Pod is removed.
//
// It[2] "clears FailedScale after recovery" — review finding F1 contract.
//   head (a8ef35f): FAILS — the FailedScale condition latched during the blocked
//                   window is never cleared: setInstanceConditions carries any
//                   live condition whose type is absent from the freshly
//                   computed set forward as if it were externally owned, so a
//                   recovered, Ready RoleInstance reports FailedScale=True
//                   indefinitely.

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/test/envtest/testutil"
)

const (
	// Short: the block must be visible quickly (FailedScale condition).
	blockedWindow = 4 * time.Second
	// Long enough to absorb the exponential backoff the rate limiter has
	// accumulated while the foreign Pod existed (a handful of retries ->
	// single-digit seconds), far below any informer resync period.
	recoveryTimeout = 60 * time.Second
	pollInterval    = 500 * time.Millisecond
)

var _ = Describe("RoleInstance replacement Pod retry after foreign same-name Pod GC", func() {
	var testNs string

	BeforeEach(func() {
		testNs = fmt.Sprintf("test-orphan-retry-%d", time.Now().UnixNano())
		testutil.CreateNamespace(testNs)
	})

	AfterEach(func() {
		testutil.DeleteNamespace(testNs)
	})

	It("creates the replacement Pod via error-driven retry after the foreign Pod is removed", func() {
		sc := newMigrationScenario(testNs)
		sc.createInstanceAndForeignPod()
		sc.expectBlockedWindow()
		sc.deleteForeignPod()
		sc.expectReplacementPod()
		Expect(sc.failedScaleSeen).To(BeTrue(),
			"the collision must surface as a FailedScale condition (head behavior; absent on base)")
	})

	It("clears the FailedScale condition after the replacement Pod is created", func() {
		sc := newMigrationScenario(testNs)
		sc.createInstanceAndForeignPod()
		sc.expectBlockedWindow()
		sc.deleteForeignPod()
		sc.expectReplacementPod()

		By("expecting the latched FailedScale condition to clear once scaling succeeds again")
		Eventually(func() bool {
			ri := &workloadsv1alpha2.RoleInstance{}
			if err := testutil.K8sClient.Get(testutil.Ctx, sc.instanceKey, ri); err != nil {
				return false
			}
			for _, c := range ri.Status.Conditions {
				if c.Type == workloadsv1alpha2.RoleInstanceFailedScale && c.Status == corev1.ConditionTrue {
					return false
				}
			}
			return true
		}, recoveryTimeout, pollInterval).Should(BeTrue(),
			"FailedScale is a controller-computed condition and must not stay True after recovery (finding F1)")
	})
})

// migrationScenario drives one RoleInstance through the foreign-pod collision
// window described in issue #511.
type migrationScenario struct {
	instanceKey     types.NamespacedName
	foreignPod      *corev1.Pod
	failedScaleSeen bool
}

func newMigrationScenario(ns string) *migrationScenario {
	return &migrationScenario{
		instanceKey: types.NamespacedName{Name: "migrate-role-0", Namespace: ns},
	}
}

func (s *migrationScenario) createInstanceAndForeignPod() {
	By("creating a RoleInstance (PodTemplate type, size 1)")
	instance := &workloadsv1alpha2.RoleInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.instanceKey.Name,
			Namespace: s.instanceKey.Namespace,
			Labels: map[string]string{
				constants.RoleTypeLabelKey: string(constants.PodTemplateTemplateType),
			},
		},
		Spec: workloadsv1alpha2.RoleInstanceSpec{
			Components: []workloadsv1alpha2.RoleInstanceComponent{{
				Name: "role",
				Size: ptr.To(int32(1)),
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{{Name: "main", Image: "rbg:latest"}},
					},
				},
			}},
		},
	}
	ExpectWithOffset(1, testutil.K8sClient.Create(testutil.Ctx, instance)).To(Succeed())

	By("creating a same-name Pod owned by a legacy StatefulSet (the migration orphan window)")
	s.foreignPod = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.instanceKey.Name, // PodTemplate type: pod name == instance name
			Namespace: s.instanceKey.Namespace,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "apps/v1",
				Kind:       "StatefulSet",
				Name:       "legacy-role",
				UID:        "old-statefulset-uid",
				Controller: ptr.To(true),
			}},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "main", Image: "rbg:latest"}},
		},
	}
	ExpectWithOffset(1, testutil.K8sClient.Create(testutil.Ctx, s.foreignPod)).To(Succeed())
}

func (s *migrationScenario) expectBlockedWindow() {
	By("observing the blocked window: the foreign Pod is neither adopted nor replaced")
	Consistently(func() int {
		podList := &corev1.PodList{}
		if err := testutil.K8sClient.List(testutil.Ctx, podList, client.InNamespace(s.instanceKey.Namespace)); err != nil {
			return -1
		}
		return len(podList.Items)
	}, blockedWindow, pollInterval).Should(Equal(1),
		"the foreign Pod must neither be adopted nor replaced while it exists")

	// Observe (do not yet assert) whether the collision surfaces as a
	// FailedScale condition. On the base branch it never does, because
	// Scale silently skips the foreign Pod and returns no error.
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline) && !s.failedScaleSeen; {
		ri := &workloadsv1alpha2.RoleInstance{}
		if err := testutil.K8sClient.Get(testutil.Ctx, s.instanceKey, ri); err == nil {
			for _, c := range ri.Status.Conditions {
				if c.Type == workloadsv1alpha2.RoleInstanceFailedScale && c.Status == corev1.ConditionTrue {
					s.failedScaleSeen = true
					break
				}
			}
		}
		time.Sleep(pollInterval)
	}
}

func (s *migrationScenario) deleteForeignPod() {
	By("deleting the foreign Pod (as garbage collection would)")
	ExpectWithOffset(1, testutil.K8sClient.Delete(testutil.Ctx, s.foreignPod)).To(Succeed())
	Eventually(func() bool {
		err := testutil.K8sClient.Get(testutil.Ctx, client.ObjectKeyFromObject(s.foreignPod), &corev1.Pod{})
		return apierrors.IsNotFound(err)
	}, recoveryTimeout, pollInterval).Should(BeTrue())
}

func (s *migrationScenario) expectReplacementPod() {
	By("expecting an error-driven retry to create the replacement Pod without any manual trigger")
	Eventually(func() bool {
		pod := &corev1.Pod{}
		if err := testutil.K8sClient.Get(testutil.Ctx, s.instanceKey, pod); err != nil {
			return false
		}
		ri := &workloadsv1alpha2.RoleInstance{}
		if err := testutil.K8sClient.Get(testutil.Ctx, s.instanceKey, ri); err != nil {
			return false
		}
		return metav1.IsControlledBy(pod, ri)
	}, recoveryTimeout, pollInterval).Should(BeTrue(),
		"the replacement Pod must be created by a controller retry, not a manual reconcile (issue #511)")
}
