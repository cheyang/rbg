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

// Verification harness for the review of PR #578 (KEP-576: compact discovery
// ConfigMap). Additive only: these tests pin down what the SHIPPED code
// actually does, so the KEP's "Shipped Baseline" and rollout/downgrade claims
// can be checked against evidence rather than prose. They intentionally assert
// current (documented-by-code) behavior; they are not bug canaries — a failure
// here after a future change means the codebase moved, not that the KEP text
// became true.

package discovery

import (
	"context"
	"regexp"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/yaml"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	wrappersv2 "sigs.k8s.io/rbgs/test/wrappers/v1alpha2"
)

func kep576FakeClient(t *testing.T, objs ...runtime.Object) *fake.ClientBuilder {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := workloadsv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatalf("add v1alpha2 scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add corev1 scheme: %v", err)
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objs...)
}

// F1 — KEP-576 "Shipped Baseline" #6 claims: "CustomComponentsPattern
// components are enumerated in spec declaration order, and roles in spec
// order." The component half is true (buildInstances iterates the spec slice).
// The role half is only true for `group.roles` (a []string built in spec
// order). The `roles:` section is a Go map (RolesInfo map[string]RoleInstances)
// marshaled through sigs.k8s.io/yaml -> encoding/json, so its keys are emitted
// in lexicographic (alphabetical) order, NOT spec declaration order.
//
// This matters because the KEP's compact format stores roles as an array "in
// spec declaration order" with "rendering preserves the shipped enumeration
// order", and its e2e gate is "field-by-field, order-sensitive equality" with
// the shipped output. For any RBG whose spec role order is not alphabetical,
// a renderer that follows the KEP text (spec order) cannot match the shipped
// file, and a renderer that matches the shipped file violates the KEP text.
func TestKEP576_Baseline_RolesSectionIsAlphabeticalNotSpecOrder(t *testing.T) {
	rbg := wrappersv2.BuildBasicRoleBasedGroup("demo", "default").Obj()
	// Spec declaration order deliberately NOT alphabetical.
	rbg.Spec.Roles = []workloadsv1alpha2.RoleSpec{
		wrappersv2.BuildStandaloneRole("zz-role").Obj(),
		wrappersv2.BuildStandaloneRole("aa-role").Obj(),
	}

	client := kep576FakeClient(t, rbg).Build()
	out, err := NewConfigBuilder(client, rbg, nil).Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	t.Logf("rendered shipped config.yaml:\n%s", string(out))

	// (a) group.roles preserves spec declaration order.
	var parsed struct {
		Group struct {
			Roles []string `yaml:"roles"`
		} `yaml:"group"`
	}
	if err := yaml.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("unmarshal built config: %v", err)
	}
	if len(parsed.Group.Roles) != 2 || parsed.Group.Roles[0] != "zz-role" || parsed.Group.Roles[1] != "aa-role" {
		t.Errorf("group.roles = %v, want spec declaration order [zz-role aa-role]", parsed.Group.Roles)
	}

	// (b) the roles: *mapping* is emitted alphabetically, not in spec order.
	aaIdx := regexp.MustCompile(`(?m)^\s+aa-role:$`).FindStringIndex(string(out))
	zzIdx := regexp.MustCompile(`(?m)^\s+zz-role:$`).FindStringIndex(string(out))
	if aaIdx == nil || zzIdx == nil {
		t.Fatalf("roles section missing expected keys; output:\n%s", string(out))
	}
	if aaIdx[0] > zzIdx[0] {
		t.Errorf("roles: section emitted in spec declaration order (zz-role before aa-role); "+
			"expected alphabetical map-marshal order (aa-role before zz-role). Output:\n%s", string(out))
	}
	t.Logf("roles: section key order is alphabetical (aa-role @%d < zz-role @%d), group.roles is spec order",
		aaIdx[0], zzIdx[0])
}

// F2 — KEP-576's rollout table models the legacy population as "per-role
// ConfigMaps, direct mount (unchanged)". On current main (and shipped
// v0.7.0/v0.8.0, since #188 removed the mode-aware branch) InjectConfig
// mounts the RBG-level `<rbg>` ConfigMap for EVERY stateful role regardless
// of the pinned discovery mode, while reconcileRefinedDiscoveryConfigMap is
// the only creator of that object and returns early unless mode == refine
// (internal/controller/workloads/rolebasedgroup_controller.go). So a
// legacy-mode RBG's rebuilt pod template mounts a ConfigMap the controller
// never creates.
func TestKEP576_Baseline_LegacyModeStillMountsGroupLevelConfigMap(t *testing.T) {
	rbg := wrappersv2.BuildBasicRoleBasedGroup("demo", "default").Obj()
	rbg.SetDiscoveryConfigMode(constants.LegacyDiscoveryConfigMode)
	role := rbg.Spec.Roles[0]

	podTemplate := wrappersv2.BuildBasicPodTemplateSpec()
	injector := NewDefaultInjector(nil, kep576FakeClient(t, rbg).Build())
	if err := injector.InjectConfig(context.Background(), &podTemplate, rbg, &role); err != nil {
		t.Fatalf("InjectConfig() error = %v", err)
	}

	var found *corev1.Volume
	for i := range podTemplate.Spec.Volumes {
		if podTemplate.Spec.Volumes[i].Name == "rbg-cluster-config" {
			found = &podTemplate.Spec.Volumes[i]
		}
	}
	if found == nil || found.ConfigMap == nil {
		t.Fatalf("expected rbg-cluster-config ConfigMap volume to be injected, got %+v", podTemplate.Spec.Volumes)
	}
	if found.ConfigMap.Name != rbg.Name {
		t.Errorf("legacy-mode RBG mounts ConfigMap %q, want RBG-level %q (per-role %q would match the KEP's legacy row)",
			found.ConfigMap.Name, rbg.Name, rbg.GetWorkloadName(&role))
	}
	t.Logf("legacy-mode RBG injects volume for ConfigMap %q (RBG-level), which no code path creates in legacy mode",
		found.ConfigMap.Name)
}

// F3 — KEP-576 "Committed Membership" claims the compact format "does not
// project the user-facing rbg.workloads.x-k8s.io/excluded-ordinals annotation;
// it encodes the committed child target". The shipped memberOrdinals it says
// it mirrors DOES project valid exclusion input before the child
// RoleInstanceSet exists: with no child yet, replicas=3 and
// excluded-ordinals="1" plan initial membership [0 2 3] (note ordinal 3 >=
// replicas, per KEP-499). A compact builder that wrote only dense/committed
// values on first reconcile would diverge from the enumeration builder for
// exactly the RBGs created with a valid exclusion annotation.
func TestKEP576_Baseline_InitialMembershipProjectsExclusionInput(t *testing.T) {
	rbg := wrappersv2.BuildBasicRoleBasedGroup("demo", "default").Obj()
	role := wrappersv2.BuildStandaloneRole("prefill").Obj()
	role.Replicas = ptr.To(int32(3))
	role.Annotations = map[string]string{
		constants.ExcludedOrdinalsAnnotationKey: "1",
	}
	rbg.Spec.Roles = []workloadsv1alpha2.RoleSpec{role}

	// No child RoleInstanceSet exists yet (first reconcile, pre-creation).
	client := kep576FakeClient(t, rbg).Build()
	b := NewConfigBuilder(client, rbg, &rbg.Spec.Roles[0])
	members, err := b.memberOrdinals(&rbg.Spec.Roles[0])
	if err != nil {
		t.Fatalf("memberOrdinals() error = %v", err)
	}
	want := []int{0, 2, 3}
	if len(members) != len(want) {
		t.Fatalf("memberOrdinals() = %v, want %v (valid exclusion input projected before child creation)", members, want)
	}
	for i := range want {
		if members[i] != want[i] {
			t.Fatalf("memberOrdinals() = %v, want %v (valid exclusion input projected before child creation)", members, want)
		}
	}
	if members[len(members)-1] < 3 {
		t.Errorf("expected a member ordinal >= replicas (KEP-499 semantics), got %v", members)
	}
	t.Logf("initial membership with excluded-ordinals=1, replicas=3, no child RIS: %v (user exclusion input IS projected)", members)
}
