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

// Reviewer integration harness for PR #474 (RoleBasedGroupSet rolling update),
// run against a real API server with the full controller stack.
//
// envtest has no garbage collector, so a foreground delete of a child RBG
// would hang in Terminating forever (its RoleInstanceSets carry
// blockOwnerDeletion owner refs). simulateGC does what kube-controller-manager
// would do on a real cluster: delete the owned dependents and drop the
// foregroundDeletion finalizer. Without it the rollout stalls after the first
// deletion — an environment artifact, not a PR defect.
//
// Claims verified here (polarity in comments):
//   I1 (contract): a template change rolls out one child at a time under
//        maxUnavailable=1 and converges with RolloutComplete.
//   I2 (contract): enabling rolloutStrategy over legacy children whose content
//        already matches does not recreate them.
//   I3 (contract): with maxUnavailable=0 + maxSurge=1 the number of ready base
//        children never drops below spec.replicas while the rollout proceeds.

import (
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/test/envtest/testutil"
)

func verifyRollingRBGS(name, ns, image string, replicas int32, strategy *workloadsv1alpha2.GroupSetRolloutStrategy) *workloadsv1alpha2.RoleBasedGroupSet {
	return &workloadsv1alpha2.RoleBasedGroupSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: workloadsv1alpha2.RoleBasedGroupSetSpec{
			Replicas: ptr.To(replicas),
			GroupTemplate: workloadsv1alpha2.RoleBasedGroupTemplateSpec{
				Spec: workloadsv1alpha2.RoleBasedGroupSpec{
					Roles: []workloadsv1alpha2.RoleSpec{{
						Name:     "worker",
						Replicas: ptr.To(int32(0)),
						Pattern: workloadsv1alpha2.Pattern{
							StandalonePattern: &workloadsv1alpha2.StandalonePattern{
								TemplateSource: workloadsv1alpha2.TemplateSource{
									Template: ptr.To(nginxPodTemplateWithImage(image)),
								},
							},
						},
					}},
				},
			},
			RolloutStrategy: strategy,
		},
	}
}

func nginxPodTemplateWithImage(image string) corev1.PodTemplateSpec {
	t := nginxPodTemplate()
	t.Spec.Containers[0].Image = image
	return t
}

func verifyListChildren(set *workloadsv1alpha2.RoleBasedGroupSet) workloadsv1alpha2.RoleBasedGroupList {
	children := workloadsv1alpha2.RoleBasedGroupList{}
	ExpectWithOffset(1, testutil.K8sClient.List(
		testutil.Ctx, &children, client.InNamespace(set.Namespace), client.MatchingLabels{constants.GroupSetNameLabelKey: set.Name},
	)).Should(Succeed())
	return children
}

var _ = Describe("RoleBasedGroupSet rolling update (PR #474 verify harness)", func() {
	const (
		timeout  = time.Second * 60
		interval = time.Millisecond * 200
	)

	var testNs string

	BeforeEach(func() {
		testNs = fmt.Sprintf("test-rbgs-rolling-%d", time.Now().UnixNano())
		testutil.CreateNamespace(testNs)
	})

	AfterEach(func() {
		testutil.DeleteNamespace(testNs)
	})

	// simulateGC finishes a foreground deletion the way kube-controller-manager
	// would: remove the dependents that block owner deletion, then drop the
	// finalizer so the API server actually removes the object.
	simulateGC := func(child *workloadsv1alpha2.RoleBasedGroup) {
		risList := &workloadsv1alpha2.RoleInstanceSetList{}
		Expect(testutil.K8sClient.List(testutil.Ctx, risList, client.InNamespace(child.Namespace))).Should(Succeed())
		for i := range risList.Items {
			ris := &risList.Items[i]
			for _, ref := range ris.OwnerReferences {
				if ref.UID == child.UID {
					Expect(testutil.K8sClient.Delete(testutil.Ctx, ris)).Should(Succeed())
					break
				}
			}
		}
		fresh := &workloadsv1alpha2.RoleBasedGroup{}
		Eventually(func() error {
			if err := testutil.K8sClient.Get(testutil.Ctx, client.ObjectKeyFromObject(child), fresh); err != nil {
				return nil // gone already
			}
			if len(fresh.Finalizers) == 0 {
				return nil
			}
			fresh.Finalizers = nil
			return testutil.K8sClient.Update(testutil.Ctx, fresh)
		}, timeout, interval).Should(Succeed())
	}

	// driveRollout steps the rollout forward until every child carries image,
	// simulating GC whenever a child is terminating, and asserting the pacing
	// invariant: at most `budget` base ordinals are unavailable at any moment.
	driveRollout := func(set *workloadsv1alpha2.RoleBasedGroupSet, image string, budget, readyFloor int) {
		// ready counts every non-terminating, Ready child (base AND surge):
		// the controller's budget and the e2e's zero-unavailability claim are
		// both stated over all serving groups, and a ready surge group serves.
		Eventually(func() bool {
			children := verifyListChildren(set)
			ready := 0
			done := true
			terminatingBase := map[string]bool{}
			for i := range children.Items {
				child := &children.Items[i]
				if !child.DeletionTimestamp.IsZero() {
					terminatingBase[child.Name] = true
					done = false
					simulateGC(child)
					continue
				}
				if child.Status.ObservedGeneration >= child.Generation &&
					apimeta.IsStatusConditionTrue(child.Status.Conditions, string(workloadsv1alpha2.RoleBasedGroupReady)) {
					ready++
				}
			}
			unavailable := len(terminatingBase)
			for index := 0; index < int(*set.Spec.Replicas); index++ {
				name := fmt.Sprintf("%s-%d", set.Name, index)
				if terminatingBase[name] {
					continue
				}
				child := &workloadsv1alpha2.RoleBasedGroup{}
				if err := testutil.K8sClient.Get(testutil.Ctx, types.NamespacedName{Name: name, Namespace: set.Namespace}, child); err != nil {
					unavailable++
					done = false
					continue
				}
				if child.Spec.Roles[0].Pattern.StandalonePattern.TemplateSource.Template.Spec.Containers[0].Image != image {
					done = false
				}
			}
			ExpectWithOffset(1, unavailable).To(BeNumerically("<=", budget),
				"rollout exceeded the unavailability budget")
			if readyFloor > 0 {
				ExpectWithOffset(1, ready).To(BeNumerically(">=", readyFloor),
					"ready serving groups (base+surge) dropped below spec.replicas")
			}
			return done
		}, timeout, interval).Should(BeTrue())
	}

	waitChildrenReady := func(set *workloadsv1alpha2.RoleBasedGroupSet) map[string]types.UID {
		uids := map[string]types.UID{}
		Eventually(func() int {
			children := verifyListChildren(set)
			ready := 0
			for i := range children.Items {
				child := &children.Items[i]
				uids[child.Name] = child.UID
				if child.Status.ObservedGeneration >= child.Generation &&
					apimeta.IsStatusConditionTrue(child.Status.Conditions, string(workloadsv1alpha2.RoleBasedGroupReady)) {
					ready++
				}
			}
			return ready
		}, timeout, interval).Should(Equal(int(*set.Spec.Replicas)))
		return uids
	}

	It("I1: recreates one group at a time under maxUnavailable=1 and completes", func() {
		set := verifyRollingRBGS("paced", testNs, "nginx:v1", 3, &workloadsv1alpha2.GroupSetRolloutStrategy{
			Type:           workloadsv1alpha2.RecreateStrategyType,
			MaxUnavailable: ptr.To(intstr.FromInt32(1)),
		})
		Expect(testutil.K8sClient.Create(testutil.Ctx, set)).Should(Succeed())
		waitChildrenReady(set)

		fresh := &workloadsv1alpha2.RoleBasedGroupSet{}
		Expect(testutil.K8sClient.Get(testutil.Ctx, client.ObjectKeyFromObject(set), fresh)).Should(Succeed())
		fresh.Spec.GroupTemplate.Spec.Roles[0].Pattern.StandalonePattern.TemplateSource.Template = ptr.To(nginxPodTemplateWithImage("nginx:v2"))
		Expect(testutil.K8sClient.Update(testutil.Ctx, fresh)).Should(Succeed())

		driveRollout(set, "nginx:v2", 1, 0)

		// The rollout reports complete: Rolling=False/RolloutComplete, current
		// revision advanced to the update revision.
		completed := &workloadsv1alpha2.RoleBasedGroupSet{}
		Eventually(func() string {
			Expect(testutil.K8sClient.Get(testutil.Ctx, client.ObjectKeyFromObject(set), completed)).Should(Succeed())
			cond := apimeta.FindStatusCondition(completed.Status.Conditions, string(workloadsv1alpha2.RoleBasedGroupSetRolling))
			if cond == nil {
				return ""
			}
			return string(cond.Status) + "/" + cond.Reason
		}, timeout, interval).Should(Equal("False/RolloutComplete"))
		Expect(completed.Status.UpdatedReplicas).To(Equal(int32(3)))
		Expect(completed.Status.CurrentRevision).To(Equal(completed.Status.UpdateRevision))
	})

	It("I2: enabling rolloutStrategy over matching legacy children does not recreate them", func() {
		set := verifyRollingRBGS("migrate", testNs, "nginx:v1", 3, nil)
		Expect(testutil.K8sClient.Create(testutil.Ctx, set)).Should(Succeed())
		uids := waitChildrenReady(set)

		fresh := &workloadsv1alpha2.RoleBasedGroupSet{}
		Expect(testutil.K8sClient.Get(testutil.Ctx, client.ObjectKeyFromObject(set), fresh)).Should(Succeed())
		fresh.Spec.RolloutStrategy = &workloadsv1alpha2.GroupSetRolloutStrategy{
			Type:           workloadsv1alpha2.RecreateStrategyType,
			MaxUnavailable: ptr.To(intstr.FromInt32(1)),
		}
		Expect(testutil.K8sClient.Update(testutil.Ctx, fresh)).Should(Succeed())

		// Give the controller several rollout ticks; nothing may be deleted.
		Consistently(func() int {
			children := verifyListChildren(set)
			terminating := 0
			for i := range children.Items {
				if !children.Items[i].DeletionTimestamp.IsZero() {
					terminating++
				}
			}
			return terminating
		}, 5*time.Second, interval).Should(BeZero())

		for name, uid := range uids {
			child := &workloadsv1alpha2.RoleBasedGroup{}
			Expect(testutil.K8sClient.Get(testutil.Ctx, types.NamespacedName{Name: name, Namespace: testNs}, child)).Should(Succeed())
			Expect(child.UID).To(Equal(uid), "child %s must keep its UID", name)
		}

		// The revision bookkeeping is initialized from the legacy children.
		migrated := &workloadsv1alpha2.RoleBasedGroupSet{}
		Eventually(func() string {
			Expect(testutil.K8sClient.Get(testutil.Ctx, client.ObjectKeyFromObject(set), migrated)).Should(Succeed())
			return migrated.Status.CurrentRevision
		}, timeout, interval).ShouldNot(BeEmpty())
	})

	It("I3: maxUnavailable=0 with surge keeps ready base groups at spec.replicas", func() {
		set := verifyRollingRBGS("surge", testNs, "nginx:v1", 3, &workloadsv1alpha2.GroupSetRolloutStrategy{
			Type:           workloadsv1alpha2.RecreateStrategyType,
			MaxUnavailable: ptr.To(intstr.FromInt32(0)),
			MaxSurge:       ptr.To(intstr.FromInt32(1)),
		})
		Expect(testutil.K8sClient.Create(testutil.Ctx, set)).Should(Succeed())
		waitChildrenReady(set)

		fresh := &workloadsv1alpha2.RoleBasedGroupSet{}
		Expect(testutil.K8sClient.Get(testutil.Ctx, client.ObjectKeyFromObject(set), fresh)).Should(Succeed())
		fresh.Spec.GroupTemplate.Spec.Roles[0].Pattern.StandalonePattern.TemplateSource.Template = ptr.To(nginxPodTemplateWithImage("nginx:v2"))
		Expect(testutil.K8sClient.Update(testutil.Ctx, fresh)).Should(Succeed())

		// budget=1 here only tolerates the terminating window of the delete
		// itself; the ready floor is the actual zero-unavailability claim.
		driveRollout(set, "nginx:v2", 1, 3)
	})
})
