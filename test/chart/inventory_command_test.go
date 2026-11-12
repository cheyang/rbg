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

package chart

// Verification harness, PR #552 round 3 (reviewer: codex).
//
// The chart README tells operators to decide the deprecated-workload-types
// cutoff from the output of one documented pipeline:
//
//	kubectl get rbg,rbgs -A -o json | jq -r '<program>'
//
// "empty output means no role still selects a deprecated workload type". The
// program is extracted byte-identically from the README and run against
// fixtures for every object shape the docs claim to cover: v1alpha1
// RoleBasedGroup (direct roles[].workload), v1alpha1 RoleBasedGroupSet
// (.spec.template.roles), and v1alpha2 annotation-stamped shapes for both
// parent kinds. The live-server run of the same verbatim pipeline against
// envtest lives in test/envtest/testcase/deprecationdocs.

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// extractInventoryJQ pulls the jq program out of the chart README's inventory
// command block, so a doc edit that breaks the program fails here. The block
// is `kubectl get rbg,rbgs -A -o json | jq -r '<program>'` with the program
// spanning multiple lines until the closing quote.
func extractInventoryJQ(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "deploy", "helm", "rbgs", "README.md"))
	if err != nil {
		t.Fatalf("read chart README: %v", err)
	}
	const marker = "kubectl get rbg,rbgs -A -o json | jq -r '"
	text := string(data)
	i := strings.Index(text, marker)
	if i < 0 {
		t.Fatalf("chart README no longer documents the inventory command (marker %q not found)", marker)
	}
	rest := text[i+len(marker):]
	end := strings.Index(rest, "'\n")
	if end < 0 {
		t.Fatalf("inventory jq program in chart README is not closed by a single quote")
	}
	return rest[:end]
}

func runJQ(t *testing.T, program, fixture string) []string {
	t.Helper()
	if _, err := exec.LookPath("jq"); err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("jq is required in CI: %v", err)
		}
		t.Skip("jq not installed; skipping inventory command test")
	}
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "test", "chart", "testdata", "inventory", fixture))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	cmd := exec.Command("jq", "-r", program)
	cmd.Stdin = strings.NewReader(string(data))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("documented jq program failed on %s: %v\n%s", fixture, err, out)
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	sort.Strings(lines)
	return lines
}

func TestInventoryCommandV1alpha1DirectWorkload(t *testing.T) {
	got := runJQ(t, extractInventoryJQ(t), "v1alpha1_direct_workload.json")
	want := []string{"RoleBasedGroup team-a/legacy-direct role=sts apps/v1/StatefulSet"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("v1alpha1 direct-workload shape:\n got: %q\nwant: %q", got, want)
	}
}

func TestInventoryCommandV1alpha1RBGSetTemplateRoles(t *testing.T) {
	got := runJQ(t, extractInventoryJQ(t), "v1alpha1_rbgs_template_roles.json")
	want := []string{
		"RoleBasedGroupSet team-b/legacy-v1a1-set role=dep apps/v1/Deployment",
		"RoleBasedGroupSet team-b/legacy-v1a1-set role=lws leaderworkerset.x-k8s.io/v1/LeaderWorkerSet",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("v1alpha1 RBGSet template shape (.spec.template.roles):\n got: %q\nwant: %q", got, want)
	}
}

func TestInventoryCommandV1alpha2AnnotationShapes(t *testing.T) {
	got := runJQ(t, extractInventoryJQ(t), "v1alpha2_annotation_shapes.json")
	want := []string{
		"RoleBasedGroup team-c/legacy-annotated role=old apps/v1/StatefulSet",
		"RoleBasedGroupSet team-d/legacy-annotated-set role=lws leaderworkerset.x-k8s.io/v1/LeaderWorkerSet",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("v1alpha2 annotation shapes:\n got: %q\nwant: %q", got, want)
	}
}

func TestInventoryCommandEdgeCases(t *testing.T) {
	got := runJQ(t, extractInventoryJQ(t), "edge_cases.json")
	// No roles key, empty roles, and an empty-string annotation (which the
	// controller's GetWorkloadType also treats as unset) must produce empty
	// output rather than an error or a spurious row.
	if len(got) != 0 {
		t.Errorf("edge-case fixture must produce empty output, got %q", got)
	}
}
