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

package discovery

// KEP-531 round-3 verification pins (reviewer-side, additive only).
//
// The round-3 KEP rewrite (d84004a2) leans on two main-code facts:
//
//   1. F-SSA-ENV-PREMISE: the Patch Grammar justifies its new "final env
//      normalization" with "server-side apply, whose env list is keyed by
//      name and rejects duplicates". The served RIS CRD makes that false:
//      roleInstanceTemplate is x-kubernetes-preserve-unknown-fields: true
//      and the CRD declares no x-kubernetes-list-type: map on any list, so
//      SSA treats env as an atomic list and never rejects duplicate names.
//      TestKEP531_Verify_RISTemplateEnvIsNotMapKeyed pins the schema.
//
//   2. The normalization the KEP specifies must reproduce what main's
//      built-in env injectors already do: InjectEnv/InjectLeaderWorkerSetEnv
//      call mergeEnvVars on EVERY container, and mergeEnvVars dedupes the
//      whole existing list last-wins — so main's fully rendered containers
//      are already duplicate-free BEFORE any apply. Duplicate env names are
//      handed to apply only on the init-container path (InjectEnv scans
//      podSpec.Spec.Containers only), which no SSA rejection stops.
//      TestKEP531_Verify_BuiltInEnvInjectorDedupesWholeContainerEnv pins it.
//
// Round-4 update (0c51bc0e): the KEP removed the "final env normalization"
// entirely — patch output now lands verbatim (never reordered or deduped)
// and resolves by standard kubelet last-occurrence semantics, so fact 1 is
// now cited as "SSA never rejects env duplicates, hence no normalization is
// needed" rather than justifying a dedupe step. Both unit pins remain valid
// main-behavior facts. A new round-4 pin,
// TestKEP531_Verify_V1alpha1EmptyComponentsConvertsToStandalone, records the
// premise of F-V1ALPHA1-CEL-EMPTY-COMPONENTS.

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	workloadsv1alpha1 "sigs.k8s.io/rbgs/api/workloads/v1alpha1"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// TestKEP531_Verify_V1alpha1EmptyComponentsConvertsToStandalone pins the
// code premise of F-V1ALPHA1-CEL-EMPTY-COMPONENTS (round-4 nit): the KEP
// (README "Validation Model") says the v1alpha1 mirror of the cross-field CEL
// rule substitutes `!has(self.leaderWorkerSet) && !has(self.components)` for
// v1alpha2's `has(self.standalonePattern)`, "matching the Hub conversion's
// pattern inference". The conversion's inference is `len(components) > 0`
// (default → standalone), while CEL's has() is TRUE for a present-but-empty
// list — `components: []` is writable (no MinItems) and converts to
// standalonePattern, yet the predicate would classify it as non-standalone.
// This test passes while that divergence exists on main; it is the runnable
// premise for the question, not a main-code bug.
func TestKEP531_Verify_V1alpha1EmptyComponentsConvertsToStandalone(t *testing.T) {
	_, hasField := reflect.TypeOf(workloadsv1alpha1.RoleSpec{}).FieldByName("StandalonePattern")
	assert.False(t, hasField,
		"v1alpha1 RoleSpec is expected to have no StandalonePattern field; "+
			"if it gained one, the KEP's v1alpha1 CEL predicate note is stale")

	src := &workloadsv1alpha1.RoleBasedGroup{
		Spec: workloadsv1alpha1.RoleBasedGroupSpec{
			Roles: []workloadsv1alpha1.RoleSpec{
				{
					Name:       "r",
					Components: []workloadsv1alpha1.InstanceComponent{}, // present but empty — writable (no MinItems)
				},
			},
		},
	}
	dst := &workloadsv1alpha2.RoleBasedGroup{}
	if err := src.ConvertTo(dst); err != nil {
		t.Fatalf("v1alpha1 → Hub conversion failed: %v", err)
	}
	assert.NotNil(t, dst.Spec.Roles[0].Pattern.StandalonePattern,
		"the Hub conversion infers standalone for a present-but-empty components list "+
			"(len>0 switch default), while the KEP's v1alpha1 CEL predicate "+
			"!has(self.components) classifies the same object as non-standalone — "+
			"the write-surface asymmetry of F-V1ALPHA1-CEL-EMPTY-COMPONENTS")
}

// TestKEP531_Verify_RISTemplateEnvIsNotMapKeyed pins the served RIS CRD
// schema premise of F-SSA-ENV-PREMISE: the pod template carried by
// RoleInstanceSet is opaque (preserve-unknown-fields) and the CRD declares
// no map-keyed list, so server-side apply cannot "reject duplicates" on env.
// It passes while the premise holds on the base; a red result means the CRD
// gained a map-keyed env list (which would itself break the legacy shapes
// that render duplicate env names today — see the sidecar_builder pins).
func TestKEP531_Verify_RISTemplateEnvIsNotMapKeyed(t *testing.T) {
	crdPath := filepath.Join("..", "..", "config", "crd", "bases",
		"workloads.x-k8s.io_roleinstancesets.yaml")
	data, err := os.ReadFile(crdPath)
	if err != nil {
		t.Skipf("RIS CRD not found at %s: %v", crdPath, err)
	}
	crd := string(data)

	assert.Contains(t, crd, "x-kubernetes-preserve-unknown-fields: true",
		"roleInstanceTemplate is expected to be an opaque (preserve-unknown-fields) subtree; "+
			"if it gained a structural schema, re-check the F-SSA-ENV-PREMISE premise")
	assert.NotContains(t, crd, "x-kubernetes-list-type: map",
		"no list in the served RIS CRD is map-keyed; env under the opaque template is "+
			"applied atomically by SSA, which never rejects duplicate env names")
	assert.NotContains(t, crd, "env:",
		"the opaque roleInstanceTemplate subtree carries no env schema at all")
}

// TestKEP531_Verify_BuiltInEnvInjectorDedupesWholeContainerEnv pins the
// main-side dedupe the KEP's final normalization must reproduce: the legacy
// containers merge appends the override env unconditionally (see the
// sidecar_builder pins — [X=template, X=legacy] survives the sidecar stage),
// and the built-in common-env injector then dedupes the WHOLE container env
// list last-wins on every container. Main's rendered containers are therefore
// duplicate-free before apply — by the injector, not by SSA.
func TestKEP531_Verify_BuiltInEnvInjectorDedupesWholeContainerEnv(t *testing.T) {
	existing := []v1.EnvVar{
		{Name: "X", Value: "template"},
		{Name: "FOO", Value: "keep"},
		{Name: "X", Value: "legacy-override"}, // appended by the legacy merge
	}
	injected := []v1.EnvVar{{Name: "RBG_ROLE_NAME", Value: "lg"}}

	merged := mergeEnvVars(existing, injected)

	var xs []string
	for _, e := range merged {
		if e.Name == "X" {
			xs = append(xs, e.Value)
		}
	}
	assert.Len(t, xs, 1, "mergeEnvVars collapses same-name entries in the whole existing list")
	assert.Equal(t, "legacy-override", xs[0], "last-wins keeps the legacy override value")
	assert.Contains(t, merged, v1.EnvVar{Name: "FOO", Value: "keep"})
	assert.Contains(t, merged, v1.EnvVar{Name: "RBG_ROLE_NAME", Value: "lg"})
}
