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

// zz_verify_pr487_leftover_test.go is the integration (envtest) layer of the
// verification harness for https://github.com/sgl-project/rbg/pull/487.
//
// envtest runs a real API server but NO garbage collector, which turns the PR's
// "short window" into a stable state: a workload left behind by a deleted same-named
// RBG (still controlled by the old UID) lingers forever, so the claim behavior of the
// reconciler can be observed deterministically.
//
// Focus strings for re-verify:
//   "leftover workload"                -> premise (P0), contract, red on base / green on head
//   "orphaned workload carrying the group label" -> adoption guard, green on both
//   "direct reconcile against a leftover"      -> write-path proof, green on both (different error text)
//   "discovery ConfigMap apply against a stale-controlled" -> residual-window canary, red on both (documents a gap the PR does not cover)

package rbg

import (
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	coreapplyv1 "k8s.io/client-go/applyconfigurations/core/v1"
	metaapplyv1 "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/pkg/reconciler"
	"sigs.k8s.io/rbgs/pkg/utils"
	"sigs.k8s.io/rbgs/test/envtest/testutil"
	wrappersv2 "sigs.k8s.io/rbgs/test/wrappers/v1alpha2"
)

const (
	zzStaleUID   = types.UID("zz-stale-rbg-uid-0000")
	zzCurrentUID = types.UID("zz-current-rbg-uid-1111")
)

// zzLeftoverDeployment builds the Deployment a background-deleted RBG would leave
// behind: still controlled by the previous RBG's UID, fully "ready" in status.
func zzLeftoverDeployment(name, ns, rbgName string, ownerUID types.UID, withOwner bool) *appsv1.Deployment {
	meta := metav1.ObjectMeta{
		Name:      name,
		Namespace: ns,
		Labels: map[string]string{
			constants.GroupNameLabelKey: rbgName,
			constants.RoleNameLabelKey:  "worker",
		},
	}
	if withOwner {
		meta.OwnerReferences = []metav1.OwnerReference{{
			APIVersion: workloadsv1alpha2.GroupVersion.String(),
			Kind:       "RoleBasedGroup",
			Name:       rbgName,
			UID:        ownerUID,
			Controller: ptr.To(true),
		}}
	}
	return &appsv1.Deployment{
		ObjectMeta: meta,
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(int32(1)),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "zz-leftover"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "zz-leftover"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "zz-leftover-marker", Image: "registry.k8s.io/pause:3.9"}},
				},
			},
		},
	}
}

func zzSeedDeploymentReady(ns, name string) {
	deploy := &appsv1.Deployment{}
	ExpectWithOffset(1, testutil.K8sClient.Get(testutil.Ctx, types.NamespacedName{Name: name, Namespace: ns}, deploy)).Should(Succeed())
	deploy.Status.ObservedGeneration = deploy.Generation
	deploy.Status.Replicas = 1
	deploy.Status.ReadyReplicas = 1
	deploy.Status.UpdatedReplicas = 1
	ExpectWithOffset(1, testutil.K8sClient.Status().Update(testutil.Ctx, deploy)).Should(Succeed())
}

func zzRBGReady(ns, name string) bool {
	rbg := &workloadsv1alpha2.RoleBasedGroup{}
	if err := testutil.K8sClient.Get(testutil.Ctx, types.NamespacedName{Name: name, Namespace: ns}, rbg); err != nil {
		return false
	}
	return apimeta.IsStatusConditionTrue(rbg.Status.Conditions, string(workloadsv1alpha2.RoleBasedGroupReady))
}

func zzLeftoverUntouched(ns, name string) bool {
	deploy := &appsv1.Deployment{}
	if err := testutil.K8sClient.Get(testutil.Ctx, types.NamespacedName{Name: name, Namespace: ns}, deploy); err != nil {
		return false
	}
	if len(deploy.Spec.Template.Spec.Containers) == 0 ||
		deploy.Spec.Template.Spec.Containers[0].Name != "zz-leftover-marker" {
		return false
	}
	for _, ref := range deploy.OwnerReferences {
		if ref.UID != zzStaleUID {
			return false
		}
	}
	return true
}

var _ = Describe("PR487 workload ownership claim (envtest)", func() {
	const (
		zzTimeout  = time.Second * 45
		zzInterval = time.Millisecond * 500
	)

	var testNs string

	BeforeEach(func() {
		testNs = fmt.Sprintf("zz-pr487-%d", time.Now().UnixNano())
		testutil.CreateNamespace(testNs)
	})

	AfterEach(func() {
		testutil.DeleteNamespace(testNs)
	})

	// P0 premise, contract polarity: a same-named RBG must NOT report Ready from a
	// leftover workload controlled by its predecessor's UID.
	//   base: RED  — the RBG goes Ready=True on the leftover's seeded 1/1 status
	//   head: GREEN — role status reads 0/0, Ready stays False, leftover untouched
	It("must not report Ready from a leftover workload of a previous incarnation", func() {
		rbgName := "zz-leftover-rbg"
		role := wrappersv2.BuildStandaloneRole("worker").WithReplicas(1).
			WithWorkload("apps/v1", "Deployment").Obj()

		leftover := zzLeftoverDeployment(rbgName+"-worker", testNs, rbgName, zzStaleUID, true)
		Expect(testutil.K8sClient.Create(testutil.Ctx, leftover)).Should(Succeed())
		zzSeedDeploymentReady(testNs, leftover.Name)

		rbg := wrappersv2.BuildBasicRoleBasedGroup(rbgName, testNs).
			WithRoles([]workloadsv1alpha2.RoleSpec{role}).Obj()
		Expect(testutil.K8sClient.Create(testutil.Ctx, rbg)).Should(Succeed())

		// The leftover must never be rewritten or re-owned, on any ref.
		Consistently(zzLeftoverUntouched(testNs, leftover.Name), 10*time.Second, zzInterval).
			Should(BeTrue(), "the leftover workload must keep its spec and its stale owner reference")

		// The contract: no Ready=True off a workload this RBG does not control.
		Consistently(func() bool { return zzRBGReady(testNs, rbgName) }, 10*time.Second, zzInterval).
			Should(BeFalse(),
				"P0 violated: RBG reported Ready from a workload left by its previous incarnation")

		// The role status must read zero while the workload is unclaimed.
		persisted := &workloadsv1alpha2.RoleBasedGroup{}
		Expect(testutil.K8sClient.Get(testutil.Ctx, types.NamespacedName{Name: rbgName, Namespace: testNs}, persisted)).Should(Succeed())
		for _, rs := range persisted.Status.RoleStatuses {
			Expect(rs.ReadyReplicas).To(Equal(int32(0)),
				"P0 violated: role status inherited readyReplicas from the leftover")
		}
	})

	// Adoption guard, contract polarity, green on base and head: an orphaned workload
	// carrying the group label is adopted in place (second commit of the PR).
	It("adopts an orphaned workload carrying the group label", func() {
		rbgName := "zz-orphan-rbg"
		role := wrappersv2.BuildStandaloneRole("worker").WithReplicas(1).
			WithWorkload("apps/v1", "Deployment").Obj()

		orphan := zzLeftoverDeployment(rbgName+"-worker", testNs, rbgName, "", false)
		Expect(testutil.K8sClient.Create(testutil.Ctx, orphan)).Should(Succeed())
		zzSeedDeploymentReady(testNs, orphan.Name)

		rbg := wrappersv2.BuildBasicRoleBasedGroup(rbgName, testNs).
			WithRoles([]workloadsv1alpha2.RoleSpec{role}).Obj()
		Expect(testutil.K8sClient.Create(testutil.Ctx, rbg)).Should(Succeed())

		persisted := &workloadsv1alpha2.RoleBasedGroup{}
		Expect(testutil.K8sClient.Get(testutil.Ctx, types.NamespacedName{Name: rbgName, Namespace: testNs}, persisted)).Should(Succeed())

		Eventually(func() bool {
			adopted := &appsv1.Deployment{}
			if err := testutil.K8sClient.Get(testutil.Ctx, types.NamespacedName{Name: orphan.Name, Namespace: testNs}, adopted); err != nil {
				return false
			}
			controller := metav1.GetControllerOf(adopted)
			return controller != nil && controller.UID == persisted.UID
		}, zzTimeout, zzInterval).Should(BeTrue(), "the orphan must be adopted by the re-created RBG")

		adopted := &appsv1.Deployment{}
		Expect(testutil.K8sClient.Get(testutil.Ctx, types.NamespacedName{Name: orphan.Name, Namespace: testNs}, adopted)).Should(Succeed())
		Expect(adopted.UID).To(Equal(orphan.UID), "adoption must not recreate the workload")
	})

	// Write-path proof, contract polarity, green on base and head for different
	// reasons (recorded in results/): on base the apiserver rejects the apply
	// (two controller refs -> 422); on head the reconciler refuses before any write.
	It("direct reconcile against a leftover fails without mutating it", func() {
		rbgName := "zz-direct-rbg"
		role := wrappersv2.BuildStandaloneRole("worker").WithReplicas(1).
			WithWorkload("apps/v1", "Deployment").Obj()

		leftover := zzLeftoverDeployment(rbgName+"-worker", testNs, rbgName, zzStaleUID, true)
		Expect(testutil.K8sClient.Create(testutil.Ctx, leftover)).Should(Succeed())

		// In-memory RBG only: never persisted, so the running manager stays out of this.
		rbg := wrappersv2.BuildBasicRoleBasedGroup(rbgName, testNs).
			WithRoles([]workloadsv1alpha2.RoleSpec{role}).Obj()
		rbg.UID = zzCurrentUID

		rec := reconciler.NewDeploymentReconciler(scheme.Scheme, testutil.K8sClient)
		err := rec.Reconciler(testutil.Ctx, rbg, &role, nil, "zz-verify-revision-hash")
		Expect(err).To(HaveOccurred(), "reconciling over a foreign-controlled workload must fail")
		GinkgoWriter.Printf("ZZ-EVIDENCE direct-reconcile error: %v\n", err)
		fmt.Printf("ZZ-EVIDENCE direct-reconcile error: %v\n", err)

		persisted := &appsv1.Deployment{}
		Expect(testutil.K8sClient.Get(testutil.Ctx, types.NamespacedName{Name: leftover.Name, Namespace: testNs}, persisted)).Should(Succeed())
		Expect(persisted.OwnerReferences).To(HaveLen(1))
		Expect(persisted.OwnerReferences[0].UID).To(Equal(zzStaleUID),
			"the failed reconcile must not have added this RBG as a controller")
	})

	// Residual-window canary (documents a gap the PR deliberately does not close):
	// the refined discovery ConfigMap is named after the RBG and applied with a
	// controller=true owner reference and no claim check, so in the same leftover
	// window its apply still collides with the stale-controlled ConfigMap.
	// Red on base AND head until the claim/adopt treatment is extended there.
	It("discovery ConfigMap apply against a stale-controlled ConfigMap still conflicts", func() {
		cmName := "zz-cm-rbg"
		staleCM := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      cmName,
				Namespace: testNs,
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: workloadsv1alpha2.GroupVersion.String(),
					Kind:       "RoleBasedGroup",
					Name:       cmName,
					UID:        zzStaleUID,
					Controller: ptr.To(true),
				}},
			},
			Data: map[string]string{"config.yaml": "stale: true"},
		}
		Expect(testutil.K8sClient.Create(testutil.Ctx, staleCM)).Should(Succeed())

		// Same shape as RoleBasedGroupReconciler.reconcileRefinedDiscoveryConfigMap.
		cmApplyConfig := coreapplyv1.ConfigMap(cmName, testNs).
			WithData(map[string]string{"config.yaml": "stale: false"}).
			WithOwnerReferences(
				metaapplyv1.OwnerReference().
					WithAPIVersion(workloadsv1alpha2.GroupVersion.String()).
					WithKind("RoleBasedGroup").
					WithName(cmName).
					WithUID(zzCurrentUID).
					WithBlockOwnerDeletion(true).
					WithController(true),
			)

		err := utils.PatchObjectApplyConfiguration(testutil.Ctx, testutil.K8sClient, cmApplyConfig, utils.PatchSpec)
		GinkgoWriter.Printf("ZZ-EVIDENCE configmap-apply error: %v\n", err)
		fmt.Printf("ZZ-EVIDENCE configmap-apply error: %v\n", err)
		Expect(err).To(HaveOccurred())
		Expect(strings.Contains(err.Error(), "Only one reference")).To(BeTrue(),
			"expected the single-controller validation to reject the apply")

		persisted := &corev1.ConfigMap{}
		Expect(testutil.K8sClient.Get(testutil.Ctx, types.NamespacedName{Name: cmName, Namespace: testNs}, persisted)).Should(Succeed())
		Expect(persisted.OwnerReferences).To(HaveLen(1))
		Expect(persisted.OwnerReferences[0].UID).To(Equal(zzStaleUID))
	})
})
