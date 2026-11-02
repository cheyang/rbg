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

package convprobe

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/types"

	workloadsv1alpha1 "sigs.k8s.io/rbgs/api/workloads/v1alpha1"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	"sigs.k8s.io/rbgs/test/envtest/testutil"
	wrappersv1 "sigs.k8s.io/rbgs/test/wrappers/v1alpha1"
)

// contractNS duplicates the contract suite's namespace as a literal so this
// probe compiles against the round-2 head too (where the constant was
// unexported). The value must match testutil's declaration.
const contractNS = "deprecation-v1a1-contract"

var _ = Describe("conversion engagement probe", Ordered, func() {
	// CONTRACT (round-3 advisory C4431130427): the contract suite must exercise
	// the production v1alpha1 -> v1alpha2 translation, not version-only
	// conversion. Observable: only the production conversion webhook
	// (convertRoleV1alpha1ToV2) writes the role-workload-type annotation; with
	// version-only (None) conversion the read-back role carries no such key.
	It("stores a v1alpha1 write through the production conversion", func() {
		rbg := wrappersv1.BuildBasicRoleBasedGroup("conv-probe", contractNS).
			WithRoles([]workloadsv1alpha1.RoleSpec{
				wrappersv1.BuildBasicRole("worker").WithRoleInstanceSet().Obj(),
			}).Obj()
		Expect(testutil.K8sClient.Create(testutil.Ctx, rbg)).To(Succeed())

		stored := &workloadsv1alpha2.RoleBasedGroup{}
		Expect(testutil.K8sClient.Get(
			testutil.Ctx, types.NamespacedName{Name: "conv-probe", Namespace: contractNS}, stored,
		)).To(Succeed())
		Expect(stored.Spec.Roles).NotTo(BeEmpty())
		Expect(stored.Spec.Roles[0].Annotations).To(
			HaveKeyWithValue(constants.RoleWorkloadTypeAnnotationKey, "workloads.x-k8s.io/v1alpha2/RoleInstanceSet"),
			"the conversion-only annotation proves the production v1alpha1->v1alpha2 translation ran",
		)
	})
})
