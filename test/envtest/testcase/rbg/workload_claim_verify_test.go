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

package rbg

// Verification harness for PR #487 — integration layer (envtest: real API server, real
// RBG controller).
//
// Claims covered:
//   P0 (contract): the RBG must not report readiness for a role whose workload is a
//       leftover controlled by a foreign UID. FAILS on base 0821cb5b (that failure is
//       the premise reproduction), PASSES on the PR head.
//   F1 (contract): removing a role from the spec must still clean up that role's
//       workload while ANOTHER role's workload is stuck terminating. On the PR head this
//       FAILS: the ErrWorkloadNotClaimable return for a terminating-but-owned workload
//       aborts the reconcile before Step 9 (cleanup), so the removed role's workload is
//       never deleted. On base it PASSES.
//
// The file only uses public API surface, so it compiles on both base and head for the
// differential run.

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/test/envtest/testutil"
	wrappersv2 "sigs.k8s.io/rbgs/test/wrappers/v1alpha2"
)

const (
	verifyClaimTimeout  = time.Second * 40
	verifyClaimInterval = time.Millisecond * 250
)

// verifyClaimForeignUID is a UID that never existed in the cluster: exactly the owner a
// background-deleted RBG leaves behind on its workloads until the GC reclaims them.
const verifyClaimForeignUID = types.UID("11111111-2222-3333-4444-555555555555")

var _ = Describe("Workload claim verification (PR #487)", func() {
	var testNs string

	BeforeEach(func() {
		testNs = fmt.Sprintf("test-claim-%d", time.Now().UnixNano())
		testutil.CreateNamespace(testNs)
	})

	AfterEach(func() {
		testutil.DeleteNamespace(testNs)
	})

	// P0 — the premise, end to end: an RBG created while a leftover workload (controlled
	// by a foreign, nonexistent UID) sits at its role's workload name must not report
	// that leftover's readiness.
	//
	// The leftover is a Deployment, not a RoleInstanceSet, on purpose: envtest runs the
	// RoleInstanceSet instance controller, which rewrites a synthetic RIS status within
	// the same second and hides the false-Ready window. No apps controller runs under
	// envtest, so the Deployment's hand-seeded status sticks for the whole assertion —
	// exactly the real-cluster shape, where the leftover's pods keep serving and its
	// status stays ready until the garbage collector reclaims it.
	It("P0: must not report readiness inherited from a foreign-UID leftover workload", func() {
		rbgName := "claim-prem"
		roleName := "worker"

		rbg := wrappersv2.BuildBasicRoleBasedGroup(rbgName, testNs).WithRoles(
			[]workloadsv1alpha2.RoleSpec{
				wrappersv2.BuildStandaloneRole(roleName).
					WithWorkload("apps/v1", "Deployment").WithReplicas(1).Obj(),
			}).Obj()
		workloadName := rbg.GetWorkloadName(&rbg.Spec.Roles[0])

		// The leftover: controlled by a UID that no longer exists, fully ready.
		leftover := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      workloadName,
				Namespace: testNs,
				Labels: map[string]string{
					constants.GroupNameLabelKey: rbgName,
					constants.RoleNameLabelKey:  roleName,
				},
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion:         workloadsv1alpha2.GroupVersion.String(),
					Kind:               "RoleBasedGroup",
					Name:               rbgName,
					UID:                verifyClaimForeignUID,
					Controller:         ptr.To(true),
					BlockOwnerDeletion: ptr.To(true),
				}},
			},
			Spec: appsv1.DeploymentSpec{
				Replicas: ptr.To(int32(1)),
				Selector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"app": workloadName},
				},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": workloadName}},
					Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: "nginx"}}},
				},
			},
		}
		Expect(testutil.K8sClient.Create(testutil.Ctx, leftover)).Should(Succeed())
		leftover.Status = appsv1.DeploymentStatus{
			ObservedGeneration: 1, Replicas: 1, ReadyReplicas: 1, UpdatedReplicas: 1,
		}
		Expect(testutil.K8sClient.Status().Update(testutil.Ctx, leftover)).Should(Succeed())

		// Recreate the RBG of the same name, inside the GC window.
		Expect(testutil.K8sClient.Create(testutil.Ctx, rbg)).Should(Succeed())

		// Contract: the role status must stay at zero and the RBG must not go Ready off
		// a workload it does not control.
		Consistently(func() int32 {
			persisted := &workloadsv1alpha2.RoleBasedGroup{}
			if err := testutil.K8sClient.Get(testutil.Ctx,
				types.NamespacedName{Name: rbgName, Namespace: testNs}, persisted); err != nil {
				return 0
			}
			for _, rs := range persisted.Status.RoleStatuses {
				if rs.Name == roleName {
					return rs.ReadyReplicas
				}
			}
			return 0
		}, 10*time.Second, verifyClaimInterval).Should(BeZero(),
			"readiness must not be inherited from a leftover workload controlled by a foreign UID")

		persisted := &workloadsv1alpha2.RoleBasedGroup{}
		Expect(testutil.K8sClient.Get(testutil.Ctx,
			types.NamespacedName{Name: rbgName, Namespace: testNs}, persisted)).Should(Succeed())
		readyCond := func() string {
			p := &workloadsv1alpha2.RoleBasedGroup{}
			if err := testutil.K8sClient.Get(testutil.Ctx,
				types.NamespacedName{Name: rbgName, Namespace: testNs}, p); err != nil {
				return ""
			}
			for _, c := range p.Status.Conditions {
				if c.Type == "Ready" {
					return string(c.Status)
				}
			}
			return ""
		}
		Consistently(readyCond, 10*time.Second, verifyClaimInterval).ShouldNot(Equal("True"),
			"the RBG must not become Ready off a workload it does not control")
	})

	// F1 — the terminating blast radius: a workload owned by THIS RBG that is stuck
	// terminating (custom finalizer) must not block the cleanup of an unrelated removed
	// role's workload.
	It("F1: removing a role still cleans up its workload while another role's workload is stuck terminating", func() {
		rbgName := "claim-f1"

		buildRbg := func() *workloadsv1alpha2.RoleBasedGroup {
			return wrappersv2.BuildBasicRoleBasedGroup(rbgName, testNs).WithRoles(
				[]workloadsv1alpha2.RoleSpec{
					wrappersv2.BuildStandaloneRole("rolea").WithReplicas(1).Obj(),
					wrappersv2.BuildStandaloneRole("roleb").WithReplicas(1).Obj(),
				}).Obj()
		}
		rbg := buildRbg()
		Expect(testutil.K8sClient.Create(testutil.Ctx, rbg)).Should(Succeed())

		roleA := rbg.Spec.Roles[0]
		roleB := rbg.Spec.Roles[1]
		risAKey := types.NamespacedName{Name: rbg.GetWorkloadName(&roleA), Namespace: testNs}
		risBKey := types.NamespacedName{Name: rbg.GetWorkloadName(&roleB), Namespace: testNs}

		// Both workloads exist and are owned by the RBG.
		waitOwned := func(key types.NamespacedName) {
			Eventually(func() bool {
				ris := &workloadsv1alpha2.RoleInstanceSet{}
				if err := testutil.K8sClient.Get(testutil.Ctx, key, ris); err != nil {
					return false
				}
				persisted := &workloadsv1alpha2.RoleBasedGroup{}
				if err := testutil.K8sClient.Get(testutil.Ctx,
					types.NamespacedName{Name: rbgName, Namespace: testNs}, persisted); err != nil {
					return false
				}
				ref := metav1.GetControllerOf(ris)
				return ref != nil && ref.UID == persisted.UID
			}, verifyClaimTimeout, verifyClaimInterval).Should(BeTrue(),
				"workload %s should exist and be controlled by the RBG", key.Name)
		}
		waitOwned(risAKey)
		waitOwned(risBKey)

		// Stick rolea's workload in Terminating: add a finalizer, then delete it. The
		// finalizer is released via DeferCleanup so the suite stays tidy even when the
		// assertion below fails (which, on code carrying this regression, it does).
		risA := &workloadsv1alpha2.RoleInstanceSet{}
		Expect(testutil.K8sClient.Get(testutil.Ctx, risAKey, risA)).Should(Succeed())
		risA.Finalizers = append(risA.Finalizers, "verify-claim.example.com/stuck")
		Expect(testutil.K8sClient.Update(testutil.Ctx, risA)).Should(Succeed())
		Expect(testutil.K8sClient.Delete(testutil.Ctx, risA)).Should(Succeed())
		DeferCleanup(func() {
			stuck := &workloadsv1alpha2.RoleInstanceSet{}
			if err := testutil.K8sClient.Get(testutil.Ctx, risAKey, stuck); err == nil {
				stuck.Finalizers = nil
				_ = testutil.K8sClient.Update(testutil.Ctx, stuck)
			}
		})

		terminating := func() bool {
			ris := &workloadsv1alpha2.RoleInstanceSet{}
			if err := testutil.K8sClient.Get(testutil.Ctx, risAKey, ris); err != nil {
				return false
			}
			return ris.DeletionTimestamp != nil
		}
		Eventually(terminating, verifyClaimTimeout, verifyClaimInterval).Should(BeTrue(),
			"rolea's workload should be stuck terminating behind its finalizer")

		// Remove roleb from the spec. Its workload must be cleaned up — the cleanup path
		// (Step 9, deleteOrphanRoles -> CleanupOrphanedWorkloads) is independent of
		// rolea's workload state.
		persisted := &workloadsv1alpha2.RoleBasedGroup{}
		Expect(testutil.K8sClient.Get(testutil.Ctx,
			types.NamespacedName{Name: rbgName, Namespace: testNs}, persisted)).Should(Succeed())
		persisted.Spec.Roles = []workloadsv1alpha2.RoleSpec{roleA}
		Expect(testutil.K8sClient.Update(testutil.Ctx, persisted)).Should(Succeed())

		// Contract: roleb's workload is deleted even though rolea's workload is stuck.
		Eventually(func() bool {
			err := testutil.K8sClient.Get(testutil.Ctx, risBKey,
				&workloadsv1alpha2.RoleInstanceSet{})
			return apierrors.IsNotFound(err)
		}, 20*time.Second, verifyClaimInterval).Should(BeTrue(),
			"the removed role's workload must be cleaned up even while another role's workload is stuck terminating")
	})

	// Sanity anchor for the F1 differential: without a stuck workload, removing a role
	// does clean up its workload (so the F1 failure above is attributable to the stuck
	// terminating workload, not to the removal flow itself).
	It("F1-control: removing a role cleans up its workload when nothing is stuck", func() {
		rbgName := "claim-f1-ctl"

		rbg := wrappersv2.BuildBasicRoleBasedGroup(rbgName, testNs).WithRoles(
			[]workloadsv1alpha2.RoleSpec{
				wrappersv2.BuildStandaloneRole("rolea").WithReplicas(1).Obj(),
				wrappersv2.BuildStandaloneRole("roleb").WithReplicas(1).Obj(),
			}).Obj()
		Expect(testutil.K8sClient.Create(testutil.Ctx, rbg)).Should(Succeed())

		roleA := rbg.Spec.Roles[0]
		roleB := rbg.Spec.Roles[1]
		risBKey := types.NamespacedName{Name: rbg.GetWorkloadName(&roleB), Namespace: testNs}

		Eventually(func() bool {
			ris := &workloadsv1alpha2.RoleInstanceSet{}
			return testutil.K8sClient.Get(testutil.Ctx, risBKey, ris) == nil
		}, verifyClaimTimeout, verifyClaimInterval).Should(BeTrue())

		persisted := &workloadsv1alpha2.RoleBasedGroup{}
		Expect(testutil.K8sClient.Get(testutil.Ctx,
			types.NamespacedName{Name: rbgName, Namespace: testNs}, persisted)).Should(Succeed())
		persisted.Spec.Roles = []workloadsv1alpha2.RoleSpec{roleA}
		Expect(testutil.K8sClient.Update(testutil.Ctx, persisted)).Should(Succeed())

		Eventually(func() bool {
			err := testutil.K8sClient.Get(testutil.Ctx, risBKey,
				&workloadsv1alpha2.RoleInstanceSet{})
			return apierrors.IsNotFound(err)
		}, verifyClaimTimeout, verifyClaimInterval).Should(BeTrue())
	})
})
