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

// pr418_f1_statefulset_test.go is a reviewer-private verification harness for
// https://github.com/sgl-project/rbg/pull/418 finding F1. It is NOT part of the PR.
//
// L2 (integration) layer: a real API server enforces the restored CEL rule and the real RBG
// controller builds the workload and the shared headless Service. That combination is what
// shows the guard gap end-to-end -- the API refuses an explicit LeaderOnly on a StatefulSet
// role, yet the controller applies LeaderOnly to that same role via its in-process default.
package rbg

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/test/envtest/testutil"
	wrappersv2 "sigs.k8s.io/rbgs/test/wrappers/v1alpha2"
)

var _ = Describe("PR418 F1 StatefulSet leaderWorkerPattern", func() {
	const (
		f1Timeout  = time.Second * 60
		f1Interval = time.Millisecond * 500
	)

	var testNs string

	BeforeEach(func() {
		testNs = fmt.Sprintf("pr418-f1-%d", time.Now().UnixNano())
		testutil.CreateNamespace(testNs)
	})

	AfterEach(func() {
		testutil.DeleteNamespace(testNs)
	})

	// stsLeaderWorkerRole builds a role that carries a leaderWorkerPattern but is pinned to the
	// StatefulSet workload type -- the combination the deleted-and-restored CEL rule is about.
	stsLeaderWorkerRole := func() workloadsv1alpha2.RoleSpec {
		return wrappersv2.BuildLeaderWorkerRole("role-1").
			WithWorkload("apps/v1", "StatefulSet").Obj()
	}

	It("Should accept an unset policy, reject an explicit LeaderOnly, and still narrow the "+
		"shared service selector to a label no StatefulSet pod carries", func() {
		By("rejecting an explicit LeaderOnly on this workload type (the CEL guard)")
		explicit := stsLeaderWorkerRole()
		explicit.LeaderWorkerPattern.SharedServiceSelection = ptr.To(
			workloadsv1alpha2.SharedServiceSelectionLeaderOnly,
		)
		rejected := wrappersv2.BuildBasicRoleBasedGroup("pr418-sts-explicit", testNs).
			WithRoles([]workloadsv1alpha2.RoleSpec{explicit}).Obj()
		err := testutil.K8sClient.Create(testutil.Ctx, rejected)
		Expect(err).Should(HaveOccurred())
		Expect(err.Error()).Should(ContainSubstring("only supported for RoleInstanceSet"))

		By("accepting the very same role when the field is simply left unset")
		role := stsLeaderWorkerRole()
		role.LeaderWorkerPattern.SharedServiceSelection = nil
		rbg := wrappersv2.BuildBasicRoleBasedGroup("pr418-sts-unset", testNs).
			WithRoles([]workloadsv1alpha2.RoleSpec{role}).Obj()
		Expect(testutil.K8sClient.Create(testutil.Ctx, rbg)).Should(Succeed())

		created := &workloadsv1alpha2.RoleBasedGroup{}
		Expect(testutil.K8sClient.Get(testutil.Ctx,
			types.NamespacedName{Name: "pr418-sts-unset", Namespace: testNs}, created)).Should(Succeed())
		Expect(created.Spec.Roles[0].LeaderWorkerPattern.SharedServiceSelection).Should(BeNil(),
			"the field stays unset: the default now lives in the controller, out of CEL's reach")

		By("waiting for the controller to create the StatefulSet")
		sts := &appsv1.StatefulSet{}
		Eventually(func() error {
			return testutil.K8sClient.Get(testutil.Ctx, types.NamespacedName{
				Name: created.GetWorkloadName(&created.Spec.Roles[0]), Namespace: testNs,
			}, sts)
		}, f1Timeout, f1Interval).Should(Succeed())

		By("confirming the StatefulSet pod template carries no component-name label")
		Expect(sts.Spec.Template.Labels).ShouldNot(HaveKey(constants.ComponentNameLabelKey),
			"StatefulSet pods are never labelled with a component name, so a selector keyed on "+
				"it cannot match any of them")

		By("reading the shared headless service the controller built")
		svc := &corev1.Service{}
		Eventually(func() error {
			return testutil.K8sClient.Get(testutil.Ctx, types.NamespacedName{
				Name: created.GetServiceName(&created.Spec.Roles[0]), Namespace: testNs,
			}, svc)
		}, f1Timeout, f1Interval).Should(Succeed())

		GinkgoWriter.Printf("F1 (L2) service selector: %v\npod template labels: %v\n",
			svc.Spec.Selector, sts.Spec.Template.Labels)

		// [CONTRACT] -- fails on the code under review.
		Expect(svc.Spec.Selector).ShouldNot(HaveKey(constants.ComponentNameLabelKey),
			"F1: the controller narrowed the shared service to component-name=leader on a "+
				"StatefulSet role, so the service can never have an endpoint -- and this is the "+
				"exact policy the API server just refused to let the user set explicitly")
	})
})
