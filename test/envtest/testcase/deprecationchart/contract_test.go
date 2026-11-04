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

package deprecationchart

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	workloadsv1alpha1 "sigs.k8s.io/rbgs/api/workloads/v1alpha1"
	"sigs.k8s.io/rbgs/test/envtest/testutil"
	wrappersv1 "sigs.k8s.io/rbgs/test/wrappers/v1alpha1"
)

// The specs assert the chart README ("Rejected writes include" list) and the
// root README flag table for controller.deprecatedWorkloadTypes.enabled=true:
// the compatibility window keeps existing deprecated uses reconcilable and
// rejects new ones, while every v1alpha1 write warns.
//
// chartGrandfathered and chartSwitch are seeded BEFORE the chart-rendered
// webhooks are installed, so they carry a persisted StatefulSet role the way a
// pre-0.9.0 installation would.
var (
	chartGrandfathered *workloadsv1alpha1.RoleBasedGroup
	chartSwitch        *workloadsv1alpha1.RoleBasedGroup
)

func seedGrandfatheredObjects() {
	deprecated := func() workloadsv1alpha1.WorkloadSpec {
		return workloadsv1alpha1.WorkloadSpec{
			APIVersion: "apps/v1",
			Kind:       "StatefulSet",
		}
	}

	chartGrandfathered = wrappersv1.BuildBasicRoleBasedGroup("chart-grandfathered", testutil.ChartContractNamespace).
		WithRoles([]workloadsv1alpha1.RoleSpec{
			func() workloadsv1alpha1.RoleSpec {
				role := wrappersv1.BuildBasicRole("worker").Obj()
				role.Workload = deprecated()
				return role
			}(),
		}).Obj()
	Expect(testutil.K8sClient.Create(testutil.Ctx, chartGrandfathered)).To(Succeed())

	chartSwitch = wrappersv1.BuildBasicRoleBasedGroup("chart-switch", testutil.ChartContractNamespace).
		WithRoles([]workloadsv1alpha1.RoleSpec{
			func() workloadsv1alpha1.RoleSpec {
				role := wrappersv1.BuildBasicRole("worker").Obj()
				role.Workload = deprecated()
				return role
			}(),
		}).Obj()
	Expect(testutil.K8sClient.Create(testutil.Ctx, chartSwitch)).To(Succeed())
}

var _ = Describe("chart-rendered deprecation gate", Ordered, func() {
	BeforeEach(func() {
		testutil.ContractWarnings.Reset()
	})

	It("warns on a v1alpha1 write through the chart-rendered webhooks", func() {
		rbg := buildRBG("chart-warn")
		Expect(testutil.K8sClient.Create(testutil.Ctx, rbg)).To(Succeed())
		Expect(testutil.ContractWarnings.Containing("v1alpha1 is deprecated")).NotTo(BeEmpty())
		Expect(testutil.ContractWarnings.Containing("removed in 0.10.0")).NotTo(BeEmpty())
	})

	It("rejects a new deprecated workload use through the chart-rendered webhooks", func() {
		rbg := buildRBG("chart-reject-new-use")
		rbg.Spec.Roles[0].Workload = workloadsv1alpha1.WorkloadSpec{
			APIVersion: "apps/v1",
			Kind:       "StatefulSet",
		}
		err := testutil.K8sClient.Create(testutil.Ctx, rbg)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("new uses are not accepted"))
	})

	It("keeps an existing deprecated use writable (type-exact exemption)", func() {
		rbg := &workloadsv1alpha1.RoleBasedGroup{}
		Expect(testutil.K8sClient.Get(
			testutil.Ctx, objectKey(chartGrandfathered.Name), rbg,
		)).To(Succeed())

		rbg.Spec.Roles[0].Replicas = ptr.To(int32(3))
		Expect(testutil.K8sClient.Update(testutil.Ctx, rbg)).To(Succeed(),
			"a grandfathered deprecated use must stay updatable in the compatibility window")
	})

	It("rejects switching between deprecated workload types", func() {
		rbg := &workloadsv1alpha1.RoleBasedGroup{}
		Expect(testutil.K8sClient.Get(
			testutil.Ctx, objectKey(chartSwitch.Name), rbg,
		)).To(Succeed())

		// StatefulSet -> Deployment: both deprecated, but the switch provisions
		// a new deprecated workload and counts as a new use.
		rbg.Spec.Roles[0].Workload = workloadsv1alpha1.WorkloadSpec{
			APIVersion: "apps/v1",
			Kind:       "Deployment",
		}
		err := testutil.K8sClient.Update(testutil.Ctx, rbg)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("new uses are not accepted"))
	})

	It("rejects a deprecated type in a new set template role", func() {
		set := wrappersv1.BuildBasicRoleBasedGroupSet("chart-set", testutil.ChartContractNamespace).Obj()
		role := wrappersv1.BuildBasicRole("member").Obj()
		role.Workload = workloadsv1alpha1.WorkloadSpec{
			APIVersion: "apps/v1",
			Kind:       "StatefulSet",
		}
		set.Spec.Template.Roles = []workloadsv1alpha1.RoleSpec{role}
		err := testutil.K8sClient.Create(testutil.Ctx, set)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("new uses are not accepted"))
	})
})

func objectKey(name string) types.NamespacedName {
	return types.NamespacedName{Name: name, Namespace: testutil.ChartContractNamespace}
}

func buildRBG(name string) *workloadsv1alpha1.RoleBasedGroup {
	return wrappersv1.BuildBasicRoleBasedGroup(name, testutil.ChartContractNamespace).
		WithRoles([]workloadsv1alpha1.RoleSpec{
			wrappersv1.BuildBasicRole("worker").Obj(),
		}).Obj()
}
