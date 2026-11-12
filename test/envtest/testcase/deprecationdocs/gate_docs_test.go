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

package deprecationdocs

// Contract specs for the behavioral claims the PR #552 round-3 documentation
// makes (README.md, README-zh_CN.md, chart README, values.yaml, NOTES.txt),
// verified against the post-#556 composition: no v1alpha1 admission webhooks,
// v1alpha1 writes gated through conversion by the v1alpha2 validators.

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	workloadsv1alpha1 "sigs.k8s.io/rbgs/api/workloads/v1alpha1"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	wrappersv1 "sigs.k8s.io/rbgs/test/wrappers/v1alpha1"
	wrappersv2 "sigs.k8s.io/rbgs/test/wrappers/v1alpha2"
)

// newDefaultedV1alpha1RBG returns a v1alpha1 RoleBasedGroup whose role never
// mentions the workload key. The typed client cannot express this (Workload is
// a non-pointer struct, so it always serializes), so the object is built as
// unstructured — the same wire shape kubectl produces from a workload-free
// manifest. The CRD schema defaults spec.roles[].workload to
// apps/v1 StatefulSet for this shape.
func newDefaultedV1alpha1RBG(name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "workloads.x-k8s.io/v1alpha1",
		"kind":       "RoleBasedGroup",
		"metadata":   map[string]interface{}{"name": name, "namespace": docsNamespace},
		"spec": map[string]interface{}{
			"roles": []interface{}{
				map[string]interface{}{
					"name":     "test-role",
					"replicas": int64(1),
					"template": map[string]interface{}{
						"spec": map[string]interface{}{
							"containers": []interface{}{map[string]interface{}{"name": "nginx", "image": "nginx:latest"}},
						},
					},
				},
			},
		},
	}}
}

var _ = Describe("Documented 0.9.0 gate behavior (post-#556 composition)", func() {

	It("rejects a new v1alpha1 RoleBasedGroup that relies on the schema-defaulted workload [README: rejected in both modes]", func() {
		warnings.Reset()
		err := k8sClient.Create(ctx, newDefaultedV1alpha1RBG("defaulted-create"))
		if noWebhook {
			Expect(err).NotTo(HaveOccurred(),
				"control: without the gate the schema-defaulted create is admitted (proves the rejections below come from the gate)")
			By("control: the schema default really fired and conversion stamped the deprecated type")
			stored := &workloadsv1alpha2.RoleBasedGroup{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: docsNamespace, Name: "defaulted-create"}, stored)).To(Succeed())
			Expect(stored.Spec.Roles[0].Annotations).To(HaveKeyWithValue(workloadAnn, stsWorkload),
				"the API server must apply the apps/v1 StatefulSet default and conversion must stamp it; got %v", stored.Spec.Roles[0].Annotations)
			return
		}
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("deprecated"),
			"compat and strict mode must both reject the schema-defaulted StatefulSet as a new use; got: %v", err)
	})

	It("admits a v1alpha1 RoleBasedGroup that names RoleInstanceSet explicitly, with the CRD deprecation warning on the wire [README: every v1alpha1 request warns]", func() {
		rbg := wrappersv1.BuildBasicRoleBasedGroup("explicit-ris", docsNamespace).Obj()
		rbg.Spec.Roles[0].Workload = workloadsv1alpha1.WorkloadSpec{APIVersion: "workloads.x-k8s.io/v1alpha2", Kind: "RoleInstanceSet"}
		warnings.Reset()
		Expect(k8sClient.Create(ctx, rbg)).To(Succeed())
		Expect(warnings.containSubstring("deprecated")).To(BeTrue(),
			"a v1alpha1 write must return the CRD deprecation warning; got %v", warnings.All())
		Expect(warnings.containSubstring("removed in 0.10.0")).To(BeTrue(),
			"the warning must name the documented 0.10.0 removal timeline; got %v", warnings.All())

		By("warning on a v1alpha1 read as well (README: EVERY v1alpha1 request)")
		warnings.Reset()
		list := &workloadsv1alpha1.RoleBasedGroupList{}
		Expect(k8sClient.List(ctx, list, client.InNamespace(docsNamespace))).To(Succeed())
		Expect(warnings.containSubstring("deprecated")).To(BeTrue(),
			"a v1alpha1 list must return the CRD deprecation warning; got %v", warnings.All())
	})

	It("does not warn on v1alpha2 requests [chart README: Exact/Equivalent routing keeps v1alpha2 unwarned]", func() {
		warnings.Reset()
		modern := wrappersv2.BuildBasicRoleBasedGroup("modern-unwarned", docsNamespace).Obj()
		Expect(k8sClient.Create(ctx, modern)).To(Succeed())
		Expect(warnings.containSubstring("deprecated")).To(BeFalse(),
			"no deprecation warning may be attached to a v1alpha2 request; got %v", warnings.All())
	})

	It("rejects a new deprecated use submitted as v1alpha2 (annotation) [README: new uses rejected]", func() {
		if noWebhook {
			Skip("control mode: gate not installed")
		}
		rbg := wrappersv2.BuildBasicRoleBasedGroup("new-deprecated-v1a2", docsNamespace).Obj()
		rbg.Spec.Roles[0].Annotations = map[string]string{workloadAnn: stsWorkload}
		err := k8sClient.Create(ctx, rbg)
		Expect(err).To(HaveOccurred())
		if strictMode {
			Expect(err.Error()).To(ContainSubstring("not enabled on this cluster"))
		} else {
			Expect(err.Error()).To(ContainSubstring("new uses are not accepted"))
		}
	})

	It("keeps an existing deprecated role writable under its exact type, and rejects switching it to another deprecated type [README: type-exact]", func() {
		if noWebhook {
			Skip("control mode: gate not installed")
		}
		if strictMode {
			Skip("grandfathering exists only in compatibility mode; strict mode is covered by the strict-mode specs")
		}
		rbg := &workloadsv1alpha1.RoleBasedGroup{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: docsNamespace, Name: seededRBGName}, rbg)).To(Succeed())

		By("same-type update is admitted (grandfathering)")
		rbg.Spec.Roles[0].Workload = workloadsv1alpha1.WorkloadSpec{APIVersion: "apps/v1", Kind: "StatefulSet"}
		Expect(k8sClient.Update(ctx, rbg)).To(Succeed())

		By("switching StatefulSet -> Deployment is a new use and is rejected")
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: docsNamespace, Name: seededRBGName}, rbg)).To(Succeed())
		rbg.Spec.Roles[0].Workload = workloadsv1alpha1.WorkloadSpec{APIVersion: "apps/v1", Kind: "Deployment"}
		err := k8sClient.Update(ctx, rbg)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("new uses are not accepted"))
	})

	It("exempts children a RoleBasedGroupSet creates from its existing legacy template, but nothing else [chart README]", func() {
		if noWebhook {
			Skip("control mode: gate not installed")
		}
		if strictMode {
			Skip("the exemption exists only in compatibility mode")
		}
		set := &workloadsv1alpha2.RoleBasedGroupSet{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: docsNamespace, Name: seededRBGSetName}, set)).To(Succeed())
		templateRole := set.Spec.GroupTemplate.Spec.Roles[0]

		By("controller-shaped child create inherits the template's deprecated type")
		child := wrappersv2.BuildBasicRoleBasedGroup("legacy-set-child", docsNamespace).Obj()
		child.OwnerReferences = []metav1.OwnerReference{{
			APIVersion: workloadsv1alpha2.GroupVersion.String(),
			Kind:       "RoleBasedGroupSet",
			Name:       set.Name,
			UID:        set.UID,
			Controller: ptr(true),
		}}
		child.Spec.Roles[0].Name = templateRole.Name
		child.Spec.Roles[0].Annotations = map[string]string{workloadAnn: stsWorkload}
		Expect(k8sClient.Create(ctx, child)).To(Succeed(),
			"a replacement child matching the legacy template's role name and type must be admitted")

		By("the same deprecated create with a forged owner reference (wrong UID) is rejected")
		forged := child.DeepCopy()
		forged.Name = "legacy-set-child-forged"
		forged.ResourceVersion = ""
		forged.OwnerReferences[0].UID = "00000000-0000-0000-0000-000000000000"
		err := k8sClient.Create(ctx, forged)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("new uses are not accepted"))

		By("the same deprecated create with no owner reference is rejected")
		orphan := child.DeepCopy()
		orphan.Name = "legacy-set-child-orphan"
		orphan.ResourceVersion = ""
		orphan.OwnerReferences = nil
		err = k8sClient.Create(ctx, orphan)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("new uses are not accepted"))
	})

	It("rejects introducing a deprecated workload type through a RoleBasedGroupSet template edit [chart README]", func() {
		if noWebhook {
			Skip("control mode: gate not installed")
		}
		set := &workloadsv1alpha1.RoleBasedGroupSet{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: docsNamespace, Name: seededRBGSetName}, set)).To(Succeed())

		By("same-type template update is admitted")
		if strictMode {
			Expect(k8sClient.Update(ctx, set)).NotTo(Succeed(), "strict mode rejects even a same-type template update")
		} else {
			Expect(k8sClient.Update(ctx, set)).To(Succeed())
		}

		By("switching the template role to another deprecated type is rejected")
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: docsNamespace, Name: seededRBGSetName}, set)).To(Succeed())
		set.Spec.Template.Roles[0].Workload = workloadsv1alpha1.WorkloadSpec{APIVersion: "apps/v1", Kind: "Deployment"}
		err := k8sClient.Update(ctx, set)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("deprecated"))
	})
})

var _ = Describe("Strict mode (controller.deprecatedWorkloadTypes.enabled=false)", func() {
	It("rejects even an unchanged re-apply of an existing deprecated object [chart README: no exemption when false]", func() {
		if !strictMode {
			Skip("strict-mode spec")
		}
		rbg := &workloadsv1alpha1.RoleBasedGroup{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: docsNamespace, Name: seededRBGName}, rbg)).To(Succeed())
		err := k8sClient.Update(ctx, rbg)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("deprecated"),
			"strict mode rejects every remaining use, including an unchanged re-apply; got: %v", err)
	})

	It("still admits RoleInstanceSet [NOTES.txt: only RoleInstanceSet is accepted]", func() {
		if !strictMode {
			Skip("strict-mode spec")
		}
		rbg := wrappersv1.BuildBasicRoleBasedGroup("strict-ris", docsNamespace).Obj()
		rbg.Spec.Roles[0].Workload = workloadsv1alpha1.WorkloadSpec{APIVersion: "workloads.x-k8s.io/v1alpha2", Kind: "RoleInstanceSet"}
		Expect(k8sClient.Create(ctx, rbg)).To(Succeed())
	})
})

func ptr(b bool) *bool { return &b }
