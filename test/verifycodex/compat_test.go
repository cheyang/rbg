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

package verifycodex

import (
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha1 "sigs.k8s.io/rbgs/api/workloads/v1alpha1"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func podTemplate() *corev1.PodTemplateSpec {
	return &corev1.PodTemplateSpec{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "nginx", Image: "nginx:latest"}},
		},
	}
}

// v1alpha1RBG builds a v1alpha1 RoleBasedGroup whose role omits the workload
// field entirely (unstructured, exactly like a pre-0.9.0 user's YAML), so the
// API server applies the v1alpha1 schema default (apps/v1 StatefulSet).
func v1alpha1RBG(name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "workloads.x-k8s.io/v1alpha1",
		"kind":       "RoleBasedGroup",
		"metadata":   map[string]interface{}{"name": name, "namespace": gateNamespace},
		"spec": map[string]interface{}{
			"roles": []interface{}{
				map[string]interface{}{
					"name":     "worker",
					"replicas": int64(1),
					"template": map[string]interface{}{
						"spec": map[string]interface{}{
							"containers": []interface{}{
								map[string]interface{}{"name": "nginx", "image": "nginx:latest"},
							},
						},
					},
				},
			},
		},
	}}
}

// v1alpha2LegacySet builds a RoleBasedGroupSet whose template role carries the
// deprecated StatefulSet workload type — the pre-0.9.0 legacy shape.
func v1alpha2LegacySet(name string) *workloadsv1alpha2.RoleBasedGroupSet {
	return &workloadsv1alpha2.RoleBasedGroupSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: gateNamespace},
		Spec: workloadsv1alpha2.RoleBasedGroupSetSpec{
			Replicas: ptr.To(int32(1)),
			GroupTemplate: workloadsv1alpha2.RoleBasedGroupTemplateSpec{
				Spec: workloadsv1alpha2.RoleBasedGroupSpec{
					Roles: []workloadsv1alpha2.RoleSpec{{
						Name:     "worker",
						Replicas: ptr.To(int32(1)),
						Annotations: map[string]string{
							constants.RoleWorkloadTypeAnnotationKey: constants.StatefulSetWorkloadType,
						},
						Pattern: workloadsv1alpha2.Pattern{
							StandalonePattern: &workloadsv1alpha2.StandalonePattern{
								TemplateSource: workloadsv1alpha2.TemplateSource{Template: podTemplate()},
							},
						},
					}},
				},
			},
		},
	}
}

// childOf builds the RoleBasedGroup the RoleBasedGroupSet reconciler submits
// when it (re)creates a child of set: template roles copied verbatim plus the
// controller owner reference.
func childOf(set *workloadsv1alpha2.RoleBasedGroupSet, name string, uid types.UID) *workloadsv1alpha2.RoleBasedGroup {
	roles := make([]workloadsv1alpha2.RoleSpec, len(set.Spec.GroupTemplate.Spec.Roles))
	for i := range set.Spec.GroupTemplate.Spec.Roles {
		roles[i] = *set.Spec.GroupTemplate.Spec.Roles[i].DeepCopy()
	}
	return &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: gateNamespace,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: workloadsv1alpha2.GroupVersion.String(),
				Kind:       "RoleBasedGroupSet",
				Name:       set.Name,
				UID:        uid,
				Controller: ptr.To(true),
			}},
		},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{Roles: roles},
	}
}

func waitForGate(t *testing.T) {
	t.Helper()
	probe := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "gate-probe", Namespace: gateNamespace},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{Roles: []workloadsv1alpha2.RoleSpec{{
			Name:     "worker",
			Replicas: ptr.To(int32(1)),
			Annotations: map[string]string{
				constants.RoleWorkloadTypeAnnotationKey: constants.StatefulSetWorkloadType,
			},
			Pattern: workloadsv1alpha2.Pattern{
				StandalonePattern: &workloadsv1alpha2.StandalonePattern{
					TemplateSource: workloadsv1alpha2.TemplateSource{Template: podTemplate()},
				},
			},
		}}},
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		err := k8sClient.Create(testCtx, probe, &client.DryRunAll)
		if err != nil && strings.Contains(err.Error(), "new uses are not accepted") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("gate never became active; last err=%v", err)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// TestCompatibilityGate is the ordered integration scenario for PR #524. The
// phases run in order against one envtest: legacy objects are seeded BEFORE the
// gate is installed (the production upgrade order), then the gate switches on.
//
// Polarity: every step except the F6 canary is a contract test (red = bug).
// The F6 canary pins the CURRENT observed behavior (owner-reference forgery
// with a known UID is admitted); a fix that hardens the exemption flips it red.
func TestCompatibilityGate(t *testing.T) {
	ensureNamespace(t)

	// ---- Phase 0 (seed pre-gate state, mirrors a pre-0.9.0 cluster) ----
	legacyRBG := v1alpha1RBG("legacy-rbg")
	if err := k8sClient.Create(testCtx, legacyRBG); err != nil {
		t.Fatalf("seed v1alpha1 legacy RBG: %v", err)
	}
	// Prove the stored object really carries the deprecated type after
	// server-side defaulting + conversion to the v1alpha2 storage version.
	stored := &workloadsv1alpha2.RoleBasedGroup{}
	if err := k8sClient.Get(testCtx, types.NamespacedName{Name: "legacy-rbg", Namespace: gateNamespace}, stored); err != nil {
		t.Fatalf("read back as v1alpha2: %v", err)
	}
	if got := stored.Spec.Roles[0].GetWorkloadType(); got != constants.StatefulSetWorkloadType {
		t.Fatalf("stored legacy role workload = %q, want %q", got, constants.StatefulSetWorkloadType)
	}

	legacySet := v1alpha2LegacySet("legacy-set")
	if err := k8sClient.Create(testCtx, legacySet); err != nil {
		t.Fatalf("seed legacy set: %v", err)
	}
	if err := k8sClient.Get(testCtx, types.NamespacedName{Name: "legacy-set", Namespace: gateNamespace}, legacySet); err != nil {
		t.Fatalf("re-get legacy set: %v", err)
	}

	// ---- Switch the 0.9.0 gate on ----
	installGateWebhooks(t)
	waitForGate(t)

	// F1 (carried from PR #509 review): the set controller's replacement child
	// of an un-migrated legacy template must be admitted (contract).
	t.Run("F1 replacement child of legacy set is admitted", func(t *testing.T) {
		child := childOf(legacySet, "legacy-set-0", legacySet.UID)
		if err := k8sClient.Create(testCtx, child); err != nil {
			t.Fatalf("replacement child create rejected: %v", err)
		}
	})

	// Control for the two exemption cases above/below: the same child without
	// any owner reference is a new use and must be rejected (proves the
	// exemption path — not a gate hole — admitted them).
	t.Run("control: identical child without owner reference is rejected", func(t *testing.T) {
		child := childOf(legacySet, "bare-child", legacySet.UID)
		child.OwnerReferences = nil
		err := k8sClient.Create(testCtx, child)
		if err == nil || !strings.Contains(err.Error(), "new uses are not accepted") {
			t.Fatalf("expected new-use rejection, got: %v", err)
		}
	})

	// F6 (new finding, CANARY): an unrelated user create that forges the set's
	// controller reference with the correct (readable) UID is admitted today.
	// The admission docstring claims "a forged owner reference ... grants
	// nothing"; this pins the observed behavior — it grants the exemption to
	// anyone who can read the parent's UID.
	t.Run("F6 canary: forged owner reference with known UID is admitted", func(t *testing.T) {
		forged := childOf(legacySet, "forged-child", legacySet.UID)
		forged.Spec.Roles[0].Pattern.StandalonePattern.TemplateSource.Template.Spec.Containers[0].Image = "busybox:latest"
		if err := k8sClient.Create(testCtx, forged); err != nil {
			t.Fatalf("bypass closed? forged create now rejected: %v", err)
		}
	})

	// E1 (carried evidence): a grandfathered v1alpha1 object stays writable in
	// compat mode, through the real conversion + Exact-match webhook path, and
	// the client receives the deprecation admission warning.
	t.Run("grandfathered v1alpha1 update admitted with warning", func(t *testing.T) {
		warnings.reset()
		cur := &workloadsv1alpha1.RoleBasedGroup{}
		if err := warnClient.Get(testCtx, types.NamespacedName{Name: "legacy-rbg", Namespace: gateNamespace}, cur); err != nil {
			t.Fatalf("get legacy rbg: %v", err)
		}
		if cur.Spec.Roles[0].Workload.Kind != "StatefulSet" {
			t.Fatalf("oldObj-converted workload = %q, want StatefulSet (exemption depends on it)", cur.Spec.Roles[0].Workload.Kind)
		}
		cur.Spec.Roles[0].Replicas = ptr.To(int32(2))
		if err := warnClient.Update(testCtx, cur); err != nil {
			t.Fatalf("grandfathered update rejected: %v", err)
		}
		if !warnings.contains("v1alpha1 is deprecated") {
			t.Fatalf("no deprecation warning surfaced to the client; got %v", warnings.text)
		}
	})

	// Gate contract: a renamed role is a new use and must be rejected even on
	// the grandfathered object (also the bites-control for the update above).
	t.Run("renamed deprecated role on legacy object is rejected", func(t *testing.T) {
		cur := &workloadsv1alpha1.RoleBasedGroup{}
		if err := k8sClient.Get(testCtx, types.NamespacedName{Name: "legacy-rbg", Namespace: gateNamespace}, cur); err != nil {
			t.Fatalf("get: %v", err)
		}
		cur.Spec.Roles[0].Name = "renamed"
		err := k8sClient.Update(testCtx, cur)
		if err == nil || !strings.Contains(err.Error(), "new uses are not accepted") {
			t.Fatalf("expected new-use rejection, got: %v", err)
		}
	})

	// New v1alpha1 create relying on the StatefulSet schema default is rejected.
	t.Run("new v1alpha1 create with defaulted workload is rejected", func(t *testing.T) {
		err := k8sClient.Create(testCtx, v1alpha1RBG("new-legacy-rbg"))
		if err == nil || !strings.Contains(err.Error(), "new uses are not accepted") {
			t.Fatalf("expected new-use rejection, got: %v", err)
		}
	})

	// F3 (carried from PR #509 review): once a role has migrated off the
	// deprecated type, re-introducing it must be rejected — the admission
	// request's oldObj, not a stale cache copy, decides.
	t.Run("F3 re-introducing deprecated type after migration is rejected", func(t *testing.T) {
		cur := &workloadsv1alpha1.RoleBasedGroup{}
		if err := k8sClient.Get(testCtx, types.NamespacedName{Name: "legacy-rbg", Namespace: gateNamespace}, cur); err != nil {
			t.Fatalf("get: %v", err)
		}
		cur.Spec.Roles[0].Workload = workloadsv1alpha1.WorkloadSpec{
			APIVersion: "workloads.x-k8s.io/v1alpha2", Kind: "RoleInstanceSet",
		}
		if err := k8sClient.Update(testCtx, cur); err != nil {
			t.Fatalf("migration update to RoleInstanceSet must be admitted, got: %v", err)
		}
		back := &workloadsv1alpha1.RoleBasedGroup{}
		if err := k8sClient.Get(testCtx, types.NamespacedName{Name: "legacy-rbg", Namespace: gateNamespace}, back); err != nil {
			t.Fatalf("re-get: %v", err)
		}
		back.Spec.Roles[0].Workload = workloadsv1alpha1.WorkloadSpec{
			APIVersion: "apps/v1", Kind: "StatefulSet",
		}
		err := k8sClient.Update(testCtx, back)
		if err == nil || !strings.Contains(err.Error(), "new uses are not accepted") {
			t.Fatalf("re-introduction of StatefulSet must be rejected, got: %v", err)
		}
	})

	// Stale-owner isolation: after the set template migrates to
	// RoleInstanceSet, an old-shape (deprecated) child claiming the same owner
	// must no longer be exempted.
	t.Run("migrated parent stops exempting old-shape children", func(t *testing.T) {
		cur := &workloadsv1alpha2.RoleBasedGroupSet{}
		if err := k8sClient.Get(testCtx, types.NamespacedName{Name: "legacy-set", Namespace: gateNamespace}, cur); err != nil {
			t.Fatalf("get set: %v", err)
		}
		delete(cur.Spec.GroupTemplate.Spec.Roles[0].Annotations, constants.RoleWorkloadTypeAnnotationKey)
		if err := k8sClient.Update(testCtx, cur); err != nil {
			t.Fatalf("migrate set template: %v", err)
		}
		stale := v1alpha2LegacySet("legacy-set") // rebuild the OLD template shape
		child := childOf(stale, "legacy-set-1", cur.UID)
		err := k8sClient.Create(testCtx, child)
		if err == nil || !strings.Contains(err.Error(), "new uses are not accepted") {
			t.Fatalf("stale-shape child of migrated set must be rejected, got: %v", err)
		}
	})
}
