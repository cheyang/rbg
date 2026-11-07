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

package verifyv1a1

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	workloadsv1alpha1 "sigs.k8s.io/rbgs/api/workloads/v1alpha1"
)

// All specs below are CONTRACT tests (they assert the intended correct
// behavior). On a head that loses the conversion-mediated gate or regresses
// the single-warning contract, they FAIL.

var _ = Describe("v1alpha1 deprecation contract after webhook removal", func() {
	// F-ver-1: enforcement is preserved. A v1alpha1 write introducing a new
	// deprecated workload use is still rejected — now by the v1alpha2 gate
	// reached through conversion (matchPolicy Equivalent), not by the removed
	// Exact-matched v1alpha1 webhook.
	It("rejects a v1alpha1 create with a new deprecated workload use", func() {
		rbg := legacyV1alpha1RBG("new-legacy-v1a1-rbg", "worker", "apps/v1", "StatefulSet")
		err := k8sClient.Create(ctx, rbg, &client.CreateOptions{DryRun: []string{metav1.DryRunAll}})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("new uses are not accepted"))
		// The v1alpha2 path's error carries the actionable migration hint.
		Expect(err.Error()).To(ContainSubstring("RoleInstanceSet"))
	})

	// F-ver-1, defaults branch: the v1alpha1 schema defaults an absent
	// spec.roles[].workload to apps/v1 StatefulSet, so even a role that never
	// named a workload is a new deprecated use and must be rejected. The
	// workload key has to be genuinely absent (kubectl-style YAML) — explicit
	// empty strings fail the pattern validation before defaulting runs.
	It("rejects a v1alpha1 create whose role workload is only schema-defaulted", func() {
		rbg := legacyV1alpha1RBG("defaulted-v1a1-rbg", "worker", "workloads.x-k8s.io/v1alpha2", "RoleInstanceSet")
		raw, err := json.Marshal(rbg)
		Expect(err).NotTo(HaveOccurred())
		var m map[string]interface{}
		Expect(json.Unmarshal(raw, &m)).To(Succeed())
		roles := m["spec"].(map[string]interface{})["roles"].([]interface{})
		delete(roles[0].(map[string]interface{}), "workload")

		u := &unstructured.Unstructured{Object: m}
		u.SetGroupVersionKind(schema.GroupVersionKind{
			Group: "workloads.x-k8s.io", Version: "v1alpha1", Kind: "RoleBasedGroup",
		})
		err = k8sClient.Create(ctx, u, &client.CreateOptions{DryRun: []string{metav1.DryRunAll}})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("new uses are not accepted"))
	})

	// F-ver-2: the rejection contract holds for RoleBasedGroupSet too, through
	// the spec.template.roles -> spec.groupTemplate.spec.roles conversion.
	It("rejects a v1alpha1 RoleBasedGroupSet create with a deprecated workload template", func() {
		set := &workloadsv1alpha1.RoleBasedGroupSet{
			ObjectMeta: metav1.ObjectMeta{Name: "new-legacy-v1a1-set", Namespace: verifyNamespace},
			Spec: workloadsv1alpha1.RoleBasedGroupSetSpec{
				Replicas: ptr.To(int32(1)),
				Template: legacyV1alpha1RBG("tpl", "worker", "apps/v1", "StatefulSet").Spec,
			},
		}
		err := k8sClient.Create(ctx, set, &client.CreateOptions{DryRun: []string{metav1.DryRunAll}})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("new uses are not accepted"))
	})

	// F-ver-3: warning contract. An accepted v1alpha1 write returns exactly ONE
	// warning — the CRD-level deprecationWarning — carrying both the removal
	// timeline and the replacement pointer. The removed webhook's
	// "migration guide" phrasing must not reappear.
	It("returns exactly one deprecation warning on an accepted v1alpha1 create", func() {
		rbg := legacyV1alpha1RBG("accepted-v1a1-rbg", "worker", "workloads.x-k8s.io/v1alpha2", "RoleInstanceSet")
		warnings.Reset()
		err := k8sClient.Create(ctx, rbg, &client.CreateOptions{DryRun: []string{metav1.DryRunAll}})
		Expect(err).NotTo(HaveOccurred())

		all := warnings.All()
		Expect(all).To(HaveLen(1), "expected exactly one Warning header, got: %v", all)
		Expect(all[0]).To(ContainSubstring("workloads.x-k8s.io/v1alpha1 RoleBasedGroup is deprecated"))
		Expect(all[0]).To(ContainSubstring("will be removed in 0.10.0"))
		Expect(all[0]).To(ContainSubstring("use workloads.x-k8s.io/v1alpha2 RoleBasedGroup"))
		Expect(all[0]).NotTo(ContainSubstring("migration guide"))
	})

	// F-ver-4: the seeded legacy object (deprecated workload type, created
	// before the gate) keeps its own type — a same-type update is grandfathered.
	It("still accepts a grandfathered v1alpha1 update that keeps its deprecated workload type", func() {
		current := &workloadsv1alpha1.RoleBasedGroup{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{
			Name: "legacy-rbg", Namespace: verifyNamespace,
		}, current)).To(Succeed())

		updated := current.DeepCopy()
		updated.Spec.Roles[0].Replicas = ptr.To(int32(3))
		warnings.Reset()
		Expect(k8sClient.Update(ctx, updated)).To(Succeed())

		all := warnings.All()
		Expect(all).To(HaveLen(1), "expected exactly one Warning header on update, got: %v", all)
		Expect(all[0]).To(ContainSubstring("will be removed in 0.10.0"))
	})

	// F-ver-4, new-use branch: switching between deprecated types provisions a
	// new deprecated workload and must stay rejected via the v1alpha1 API.
	It("rejects a v1alpha1 update that switches to a different deprecated workload type", func() {
		current := &workloadsv1alpha1.RoleBasedGroup{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{
			Name: "legacy-rbg", Namespace: verifyNamespace,
		}, current)).To(Succeed())

		updated := current.DeepCopy()
		updated.Spec.Roles[0].Workload = workloadsv1alpha1.WorkloadSpec{
			APIVersion: "apps/v1",
			Kind:       "Deployment",
		}
		err := k8sClient.Update(ctx, updated)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("new uses are not accepted"))
	})

	// F-ver-5: the v1alpha1 API remains fully readable (conversion back from
	// the v1alpha2 storage version) for the seeded object.
	It("reads the legacy object back through the v1alpha1 API", func() {
		current := &workloadsv1alpha1.RoleBasedGroup{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{
			Name: "legacy-rbg", Namespace: verifyNamespace,
		}, current)).To(Succeed())
		Expect(current.Spec.Roles).To(HaveLen(1))
		Expect(current.Spec.Roles[0].Workload.Kind).To(Equal("StatefulSet"))
		Expect(current.Spec.Roles[0].Workload.APIVersion).To(Equal("apps/v1"))
	})
})
