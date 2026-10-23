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

package verifylws510

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	lwsv1 "sigs.k8s.io/lws/api/leaderworkerset/v1"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	wrappersv2 "sigs.k8s.io/rbgs/test/wrappers/v1alpha2"
)

const (
	vtimeout  = 45 * time.Second
	vinterval = 500 * time.Millisecond
)

// writeLWSStatusLikeRealController writes exactly the status the real LWS
// v0.7.0 controller produces for a fully rolled-out group set (see
// makeCondition in the vendored lws module: Type/Status/Reason/Message/
// LastTransitionTime only, never ObservedGeneration).
func writeLWSStatusLikeRealController(key types.NamespacedName, stampObservedGeneration bool) {
	lws := &lwsv1.LeaderWorkerSet{}
	ExpectWithOffset(1, k8sClient.Get(ctx, key, lws)).To(Succeed())
	available := metav1.Condition{
		Type:               string(lwsv1.LeaderWorkerSetAvailable),
		Status:             metav1.ConditionTrue,
		Reason:             "AllGroupsReady",
		Message:            "All replicas are ready",
		LastTransitionTime: metav1.Now(),
	}
	if stampObservedGeneration {
		available.ObservedGeneration = lws.Generation
	}
	lws.Status.Replicas = 1
	lws.Status.ReadyReplicas = 1
	lws.Status.UpdatedReplicas = 1
	lws.Status.Conditions = []metav1.Condition{available}
	ExpectWithOffset(1, k8sClient.Status().Update(ctx, lws)).To(Succeed())
}

var _ = Describe("PR510 F1: RBG Ready with a LeaderWorkerSet role", func() {
	var ns *corev1.Namespace

	BeforeEach(func() {
		ns = &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "verify-lws-"}}
		Expect(k8sClient.Create(ctx, ns)).To(Succeed())
	})

	AfterEach(func() {
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, ns))).To(Succeed())
	})

	It("contract: RBG reports Ready once the LWS controller reports the role available", func() {
		rbg := wrappersv2.BuildBasicRoleBasedGroup("lws-ready", ns.Name).
			WithRoles([]workloadsv1alpha2.RoleSpec{
				wrappersv2.BuildLeaderWorkerRole("role-1").
					WithWorkload("leaderworkerset.x-k8s.io/v1", "LeaderWorkerSet").Obj(),
			}).Obj()
		Expect(k8sClient.Create(ctx, rbg)).To(Succeed())

		lwsKey := types.NamespacedName{Namespace: ns.Name, Name: "lws-ready-role-1"}
		Eventually(func() error {
			return k8sClient.Get(ctx, lwsKey, &lwsv1.LeaderWorkerSet{})
		}, vtimeout, vinterval).Should(Succeed(), "RBG controller should create the LWS")

		// The real LWS controller rolls the groups out and reports Available.
		writeLWSStatusLikeRealController(lwsKey, false)

		// Contract: the RBG must observe the converged role and report Ready.
		Eventually(func(g Gomega) {
			cur := &workloadsv1alpha2.RoleBasedGroup{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(rbg), cur)).To(Succeed())
			ready := apimeta.FindStatusCondition(cur.Status.Conditions, string(workloadsv1alpha2.RoleBasedGroupReady))
			g.Expect(ready).NotTo(BeNil())
			g.Expect(ready.Status).To(Equal(metav1.ConditionTrue),
				"RBG Ready must become True once LWS reports the role converged; phase=%s", cur.Status.Phase)
		}, vtimeout, vinterval).Should(Succeed())
	})

	It("control: an LWS that stamps condition observedGeneration reports Ready", func() {
		rbg := wrappersv2.BuildBasicRoleBasedGroup("lws-ready-stamped", ns.Name).
			WithRoles([]workloadsv1alpha2.RoleSpec{
				wrappersv2.BuildLeaderWorkerRole("role-1").
					WithWorkload("leaderworkerset.x-k8s.io/v1", "LeaderWorkerSet").Obj(),
			}).Obj()
		Expect(k8sClient.Create(ctx, rbg)).To(Succeed())

		lwsKey := types.NamespacedName{Namespace: ns.Name, Name: "lws-ready-stamped-role-1"}
		Eventually(func() error {
			return k8sClient.Get(ctx, lwsKey, &lwsv1.LeaderWorkerSet{})
		}, vtimeout, vinterval).Should(Succeed())

		writeLWSStatusLikeRealController(lwsKey, true)

		Eventually(func(g Gomega) {
			cur := &workloadsv1alpha2.RoleBasedGroup{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(rbg), cur)).To(Succeed())
			g.Expect(apimeta.IsStatusConditionTrue(cur.Status.Conditions, string(workloadsv1alpha2.RoleBasedGroupReady))).To(BeTrue())
		}, vtimeout, vinterval).Should(Succeed())
	})
})
