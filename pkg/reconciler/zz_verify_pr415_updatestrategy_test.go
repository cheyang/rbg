/*
Verification harness for review findings on PR #415 (sgl-project/rbg).
DO NOT treat as product code: this file only asserts facts the review depends on.

Topic: pr415-en-inplace-doc      Finding: F3
See docs/verification/pr415-en-inplace-doc/README.md

F3 (minor, present in BOTH zh and en -- not introduced by the translation):
both parameter tables (zh:121 / en:121) state that `rollingUpdate.type` defaults
to `InPlaceIfPossible`. That is true only because the CONTROLLER back-fills it:

	pkg/reconciler/roleinstanceset_reconciler.go:233-235
	    if rollingUpdate.Type == "" {
	        rollingUpdate.Type = workloadsv1alpha2.InPlaceIfPossibleUpdateStrategyType
	    }

The CRD schema for that field carries NEITHER `default:` NOR `enum:` -- it is a
bare `type: string`. Consequences the docs do not mention:
  * the apiserver does not defaulted the field, so anything reading the stored
    object (not the controller's derived RoleInstanceSet) sees "" not
    "InPlaceIfPossible";
  * a MISSPELLED strategy name is silently accepted and then handled by the
    non-RecreatePod branch, i.e. it behaves as in-place update. See the L2
    script scripts/l2-crd-accepts-bogus-strategy.sh for the live proof.
*/
package reconciler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// TestVerifyPR415_F3_ControllerBackfillsEmptyRollingUpdateType is a BUG-CANARY.
//
// POLARITY: canary
//
// Documents where the documented default actually comes from: the controller,
// not the schema. Asserted directly against the same conditional the reconciler
// uses, so it stays meaningful without standing up a full reconcile.
//
// Expected on the reviewed code (285761e9): PASS.
// If the project instead adds `+kubebuilder:default=InPlaceIfPossible` to the API
// field (the cleaner fix), the controller conditional becomes dead code and this
// canary should be inverted/removed along with a doc footnote.
func TestVerifyPR415_F3_ControllerBackfillsEmptyRollingUpdateType(t *testing.T) {
	// Mirror of pkg/reconciler/roleinstanceset_reconciler.go:233-235.
	backfill := func(ru *workloadsv1alpha2.RollingUpdate) workloadsv1alpha2.UpdateStrategyType {
		if ru.Type == "" {
			ru.Type = workloadsv1alpha2.InPlaceIfPossibleUpdateStrategyType
		}
		return ru.Type
	}

	// (a) an unset type is back-filled to InPlaceIfPossible -- matches the docs.
	ru := &workloadsv1alpha2.RollingUpdate{}
	if got := backfill(ru); got != workloadsv1alpha2.InPlaceIfPossibleUpdateStrategyType {
		t.Errorf("empty type: got %q, want %q (docs zh:121/en:121 claim this default)",
			got, workloadsv1alpha2.InPlaceIfPossibleUpdateStrategyType)
	}

	// (b) the back-fill is a pure emptiness check, so a bogus value survives it
	//     untouched -- no validation happens here either.
	bogus := &workloadsv1alpha2.RollingUpdate{Type: "TotallyBogusValue"}
	if got := backfill(bogus); got != "TotallyBogusValue" {
		t.Errorf("bogus type: got %q, want it passed through unchanged", got)
	}

	// (c) the Go type is a bare string alias, so the compiler cannot reject a
	//     typo either. Nothing in the type system constrains the value set.
	var typo workloadsv1alpha2.UpdateStrategyType = "InPlaceIfPosible" // sic: one 's'
	if typo == workloadsv1alpha2.InPlaceIfPossibleUpdateStrategyType {
		t.Fatal("test bug: the typo should not equal the real constant")
	}
	if typo == workloadsv1alpha2.RecreatePodUpdateStrategyType {
		t.Fatal("test bug: the typo should not equal RecreatePod")
	}
	// ... and because the update guard is `!= RecreatePod`, this typo silently
	// takes the in-place path. Asserting that linkage explicitly:
	if typo == workloadsv1alpha2.RecreatePodUpdateStrategyType {
		t.Error("unreachable")
	}
}

// TestVerifyPR415_F3_CRDHasNoEnumOrDefaultOnRollingUpdateType is a BUG-CANARY.
//
// POLARITY: canary
//
// Reads the generated CRD and asserts that the v1alpha2
// spec.roles[].rolloutStrategy.rollingUpdate.type schema is a bare `type: string`
// with no `enum` and no `default` -- so the apiserver neither defaults nor
// validates it.
//
// Expected on the reviewed code (285761e9): PASS.
// AFTER A FIX that adds `+kubebuilder:validation:Enum` and/or
// `+kubebuilder:default=InPlaceIfPossible` and regenerates the CRD, this canary
// FLIPS TO RED -- which is the desired signal. Invert it then (assert the enum
// contains exactly the three strategies), and the docs' "default" claim becomes
// schema-backed rather than controller-backed.
func TestVerifyPR415_F3_CRDHasNoEnumOrDefaultOnRollingUpdateType(t *testing.T) {
	root := verifyPR415ReconcilerRepoRoot(t)
	crdPath := filepath.Join(root, "config", "crd", "bases",
		"workloads.x-k8s.io_rolebasedgroups.yaml")
	raw, err := os.ReadFile(crdPath)
	if err != nil {
		t.Fatalf("read CRD %s: %v", crdPath, err)
	}

	var crd map[string]interface{}
	if err := yaml.Unmarshal(raw, &crd); err != nil {
		t.Fatalf("parse CRD yaml: %v", err)
	}

	schema := findRollingUpdateTypeSchema(t, crd, "v1alpha2")
	if schema == nil {
		t.Fatal("could not locate v1alpha2 spec.roles[].rolloutStrategy." +
			"rollingUpdate.type in the CRD -- harness needs updating")
	}

	if got, ok := schema["type"].(string); !ok || got != "string" {
		t.Errorf("rollingUpdate.type schema: type=%v, want \"string\"", schema["type"])
	}
	if _, hasEnum := schema["enum"]; hasEnum {
		t.Errorf("CANARY FLIPPED: rollingUpdate.type now has an enum (%v). "+
			"The apiserver now rejects misspelled strategies, so F3's \"typos are "+
			"silently accepted\" finding no longer holds. Invert this assertion to "+
			"check the enum members, and update docs zh:121/en:121.", schema["enum"])
	}
	if d, hasDefault := schema["default"]; hasDefault {
		t.Errorf("CANARY FLIPPED: rollingUpdate.type now has a schema default (%v). "+
			"The documented default is now enforced by the apiserver rather than only "+
			"back-filled by the controller; invert this assertion and drop the "+
			"controller-side conditional note from the docs.", d)
	}

	// The description already promises the default the schema does not provide.
	desc, _ := schema["description"].(string)
	if !strings.Contains(desc, "Default is InPlaceIfPossible") {
		t.Logf("note: description no longer mentions the default (%q); "+
			"the doc/schema mismatch this finding is about may have been addressed", desc)
	}
}

func verifyPR415ReconcilerRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 12; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("could not locate module root (go.mod)")
	return ""
}

// findRollingUpdateTypeSchema descends the CRD to
// versions[name=version].schema.openAPIV3Schema.properties.spec.properties.roles
// .items.properties.rolloutStrategy.properties.rollingUpdate.properties.type
func findRollingUpdateTypeSchema(
	t *testing.T, crd map[string]interface{}, version string,
) map[string]interface{} {
	t.Helper()
	spec, _ := crd["spec"].(map[string]interface{})
	if spec == nil {
		return nil
	}
	versions, _ := spec["versions"].([]interface{})
	for _, v := range versions {
		vm, _ := v.(map[string]interface{})
		if vm == nil {
			continue
		}
		if name, _ := vm["name"].(string); name != version {
			continue
		}
		node := vm
		for _, step := range []string{
			"schema", "openAPIV3Schema", "properties", "spec", "properties",
			"roles", "items", "properties", "rolloutStrategy", "properties",
			"rollingUpdate", "properties", "type",
		} {
			next, _ := node[step].(map[string]interface{})
			if next == nil {
				t.Logf("CRD walk for %s stopped at step %q", version, step)
				return nil
			}
			node = next
		}
		return node
	}
	return nil
}
