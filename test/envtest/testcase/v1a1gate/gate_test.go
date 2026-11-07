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

package v1a1gate

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	workloadsv1alpha1 "sigs.k8s.io/rbgs/api/workloads/v1alpha1"
	"sigs.k8s.io/rbgs/test/envtest/testutil"
	wrappersv1 "sigs.k8s.io/rbgs/test/wrappers/v1alpha1"
)

var _ = Describe("v1alpha1 writes after the v1alpha1 admission webhooks are removed", Ordered, func() {
	BeforeEach(func() {
		testutil.GateWarnings.Reset()
	})

	It("rejects a new v1alpha1 RoleBasedGroup with an explicit deprecated workload type via the v1alpha2 gate", func() {
		rbg := wrappersv1.BuildBasicRoleBasedGroup("new-deprecated-rbg", testutil.V1alpha1GateNamespace).
			WithRoles([]workloadsv1alpha1.RoleSpec{legacyStatefulV1alpha1Role("worker")}).Obj()
		err := testutil.K8sClient.Create(testutil.Ctx, rbg)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("new uses are not accepted"),
			"the rejection must come from the v1alpha2 deprecated-workload gate after conversion")
		Expect(err.Error()).To(ContainSubstring("RoleInstanceSet"),
			"the v1alpha2 gate's error must carry the actionable migration hint")
	})

	It("rejects a new v1alpha1 RoleBasedGroup that relies on v1alpha1 schema defaulting (apps/v1 StatefulSet)", func() {
		// The Go typed client cannot omit workload (WorkLoadSpec is a struct, so
		// omitempty still serializes `{}`, which the pattern rule rejects). A real
		// kubectl manifest simply leaves the field out, so this spec submits the
		// object unstructured, exactly what a kubectl apply puts on the wire.
		role := roleInstanceSetV1alpha1Role("worker")
		roleMap, err := k8sruntime.DefaultUnstructuredConverter.ToUnstructured(&role)
		Expect(err).NotTo(HaveOccurred())
		delete(roleMap, "workload")
		obj := &unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "workloads.x-k8s.io/v1alpha1",
			"kind":       "RoleBasedGroup",
			"metadata": map[string]interface{}{
				"name":      "defaulted-rbg",
				"namespace": testutil.V1alpha1GateNamespace,
			},
			"spec": map[string]interface{}{
				"roles": []interface{}{roleMap},
			},
		}}
		err = testutil.K8sClient.Create(testutil.Ctx, obj)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("new uses are not accepted"),
			"a v1alpha1 role that omits workload defaults to apps/v1 StatefulSet and must be rejected after conversion")
	})

	It("accepts a v1alpha1 RoleBasedGroup that names RoleInstanceSet and returns exactly one deprecation warning", func() {
		rbg := wrappersv1.BuildBasicRoleBasedGroup("migrated-rbg", testutil.V1alpha1GateNamespace).
			WithRoles([]workloadsv1alpha1.RoleSpec{roleInstanceSetV1alpha1Role("worker")}).Obj()
		Expect(testutil.K8sClient.Create(testutil.Ctx, rbg)).To(Succeed())

		deprecationWarnings := testutil.GateWarnings.Containing("deprecated")
		Expect(deprecationWarnings).To(HaveLen(1),
			"with the webhook warning gone, exactly one warning (the CRD deprecationWarning) must remain; got %v",
			testutil.GateWarnings.All())
		Expect(deprecationWarnings[0]).To(ContainSubstring("workloads.x-k8s.io/v1alpha1 RoleBasedGroup is deprecated"))
		Expect(deprecationWarnings[0]).To(ContainSubstring("will be removed in 0.10.0"),
			"the CRD warning must now carry the removal timeline the webhook warning used to add")
		Expect(deprecationWarnings[0]).To(ContainSubstring("use workloads.x-k8s.io/v1alpha2 RoleBasedGroup"))
	})

	It("keeps the grandfathered legacy object updatable through v1alpha1", func() {
		stored := &workloadsv1alpha1.RoleBasedGroup{}
		Expect(testutil.K8sClient.Get(testutil.Ctx,
			types.NamespacedName{Name: legacyRBG.Name, Namespace: legacyRBG.Namespace}, stored)).To(Succeed())
		stored.Spec.Roles[0].Replicas = ptr.To(int32(2))
		Expect(testutil.K8sClient.Update(testutil.Ctx, stored)).To(Succeed(),
			"a type-exact update of a pre-gate object must remain accepted (v1alpha2 grandfathering)")
	})

	It("rejects switching the legacy role to another deprecated workload type through v1alpha1", func() {
		stored := &workloadsv1alpha1.RoleBasedGroup{}
		Expect(testutil.K8sClient.Get(testutil.Ctx,
			types.NamespacedName{Name: legacyRBG.Name, Namespace: legacyRBG.Namespace}, stored)).To(Succeed())
		stored.Spec.Roles[0].Workload = workloadsv1alpha1.WorkloadSpec{
			APIVersion: "apps/v1",
			Kind:       "Deployment",
		}
		err := testutil.K8sClient.Update(testutil.Ctx, stored)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("new uses are not accepted"))
	})

	It("rejects a new v1alpha1 RoleBasedGroupSet whose template uses a deprecated workload type", func() {
		set := wrappersv1.BuildBasicRoleBasedGroupSet("new-deprecated-set", testutil.V1alpha1GateNamespace).Obj()
		set.Spec.Template.Roles = []workloadsv1alpha1.RoleSpec{legacyStatefulV1alpha1Role("member")}
		err := testutil.K8sClient.Create(testutil.Ctx, set)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("new uses are not accepted"))
	})

	It("leaves v1alpha1 deletes ungated", func() {
		rbg := wrappersv1.BuildBasicRoleBasedGroup("deletable-rbg", testutil.V1alpha1GateNamespace).
			WithRoles([]workloadsv1alpha1.RoleSpec{roleInstanceSetV1alpha1Role("worker")}).Obj()
		Expect(testutil.K8sClient.Create(testutil.Ctx, rbg)).To(Succeed())
		stored := &workloadsv1alpha1.RoleBasedGroup{}
		Expect(testutil.K8sClient.Get(testutil.Ctx,
			types.NamespacedName{Name: rbg.Name, Namespace: rbg.Namespace}, stored)).To(Succeed())
		Expect(testutil.K8sClient.Delete(testutil.Ctx, stored)).To(Succeed())
	})
})
