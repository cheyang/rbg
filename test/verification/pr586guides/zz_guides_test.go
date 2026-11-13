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

// Review-verification harness for PR #586 (docs: v1alpha1->v1alpha2 migration
// guides). Exercises the guide text itself: the inventory command it ships,
// the links it adds, and EN/ZH parity. Polarity is marked per test.
// Additive only; no production code is touched.

package pr586guides

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// repoRoot resolves the repository root from this package's directory
// (test/verification/pr586guides).
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	return root
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// bashBlocks extracts the contents of ```bash fenced blocks from markdown.
func bashBlocks(md string) []string {
	var blocks []string
	lines := strings.Split(md, "\n")
	inBash := false
	var cur []string
	for _, ln := range lines {
		switch {
		case strings.TrimSpace(ln) == "```bash":
			inBash = true
			cur = nil
		case strings.HasPrefix(strings.TrimSpace(ln), "```") && inBash:
			inBash = false
			blocks = append(blocks, strings.Join(cur, "\n"))
		case inBash:
			cur = append(cur, ln)
		}
	}
	return blocks
}

// guideInventoryJq extracts the jq program of the role-inventory command from
// the English migration guide (the block containing "inventory="), so the
// harness always tests the command the guide actually ships.
func guideInventoryJq(t *testing.T) string {
	t.Helper()
	md := readRepoFile(t, "doc/migration-v1alpha1-to-v1alpha2.md")
	for _, b := range bashBlocks(md) {
		if !strings.Contains(b, "inventory=") {
			continue
		}
		start := strings.Index(b, "jq -r '")
		if start < 0 {
			t.Fatal("inventory block has no jq -r ' program")
		}
		rest := b[start+len("jq -r '"):]
		end := strings.LastIndex(rest, "'")
		if end < 0 {
			t.Fatal("inventory jq program not terminated")
		}
		return rest[:end]
	}
	t.Fatal("guide contains no inventory bash block")
	return ""
}

// inventoryFixture mimics `kubectl get rbg,rbgs -A -o json` covering every
// shape the guide's command claims to handle.
const inventoryFixture = `{
  "apiVersion": "v1",
  "kind": "List",
  "items": [
    {"kind":"RoleBasedGroup","metadata":{"name":"g1","namespace":"ns1"},
     "spec":{"roles":[
        {"name":"r-deploy","annotations":{"rbg.workloads.x-k8s.io/role-workload-type":"apps/v1/Deployment"}},
        {"name":"r-ris","annotations":{"rbg.workloads.x-k8s.io/role-workload-type":"workloads.x-k8s.io/v1alpha2/RoleInstanceSet"}},
        {"name":"r-default"}
     ]}},
    {"kind":"RoleBasedGroup","metadata":{"name":"g2","namespace":"ns1"},
     "spec":{"roles":[
        {"name":"r-sts","workload":{"apiVersion":"apps/v1","kind":"StatefulSet"},
         "annotations":{"rbg.workloads.x-k8s.io/role-workload-type":"workloads.x-k8s.io/v1alpha2/RoleInstanceSet"}},
        {"name":"r-is","workload":{"apiVersion":"workloads.x-k8s.io/v1alpha1","kind":"InstanceSet"}}
     ]}},
    {"kind":"RoleBasedGroupSet","metadata":{"name":"s1","namespace":"ns2"},
     "spec":{"groupTemplate":{"spec":{"roles":[
        {"name":"r-lws","annotations":{"rbg.workloads.x-k8s.io/role-workload-type":"leaderworkerset.x-k8s.io/v1/LeaderWorkerSet"}}
     ]}}}},
    {"kind":"RoleBasedGroupSet","metadata":{"name":"s2","namespace":"ns2"},
     "spec":{"template":{"roles":[
        {"name":"r-dep","workload":{"apiVersion":"apps/v1","kind":"Deployment"}}
     ]}}}
  ]
}`

// inventoryFixtureUnknown adds a role whose annotation value matches none of
// the deprecated types the guide knows about.
const inventoryFixtureUnknown = `{
  "apiVersion": "v1",
  "kind": "List",
  "items": [
    {"kind":"RoleBasedGroup","metadata":{"name":"g9","namespace":"ns9"},
     "spec":{"roles":[
        {"name":"r-unknown","annotations":{"rbg.workloads.x-k8s.io/role-workload-type":"example.com/v1/FooSet"}}
     ]}}
  ]
}`

func runInventory(t *testing.T, fixture string) []string {
	t.Helper()
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not installed; install jq to run the inventory harness")
	}
	cmd := exec.Command("jq", "-r", guideInventoryJq(t))
	cmd.Stdin = strings.NewReader(fixture)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("guide inventory jq failed: %v\n%s", err, out)
	}
	var lines []string
	for _, ln := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if ln != "" {
			lines = append(lines, ln)
		}
	}
	return lines
}

// CONTRACT: the guide's inventory command detects every deprecated shape it
// documents, in both API encodings, with v1alpha1's workload field taking
// precedence over a stale annotation.
func TestGuideInventoryDetectsAllDocumentedShapes(t *testing.T) {
	got := runInventory(t, inventoryFixture)
	want := []string{
		"RoleBasedGroup ns1/g1 role=r-deploy apps/v1/Deployment",
		"RoleBasedGroup ns1/g2 role=r-sts apps/v1/StatefulSet",
		"RoleBasedGroup ns1/g2 role=r-is workloads.x-k8s.io/v1alpha1/InstanceSet",
		"RoleBasedGroupSet ns2/s1 role=r-lws leaderworkerset.x-k8s.io/v1/LeaderWorkerSet",
		"RoleBasedGroupSet ns2/s2 role=r-dep apps/v1/Deployment",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("inventory output mismatch\ngot:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// Finding F1 — CANARY. The guide's interpretation table contains a row
// "Any other value -> Unknown compatibility value -> Stop and review", but the
// shipped jq `select(...)` only passes the four known deprecated forms, so an
// unknown selector is silently filtered out and the promised row is
// unreachable. This canary PASSES while the gap exists; it must FLIP (be
// inverted) once the guide either adds a catch-all to the command or drops the
// unreachable row.
func TestGuideInventoryUnknownValueUnreachableRow_Canary(t *testing.T) {
	got := runInventory(t, inventoryFixtureUnknown)
	unknownFiltered := len(got) == 0
	md := readRepoFile(t, "doc/migration-v1alpha1-to-v1alpha2.md")
	rowPromised := strings.Contains(md, "Any other value")
	if !unknownFiltered || !rowPromised {
		t.Fatalf("F1 gap appears fixed (unknownFiltered=%v, rowPromised=%v); invert this canary",
			unknownFiltered, rowPromised)
	}
	t.Log("F1 confirmed: unknown selectors are silently filtered while the guide's table promises to surface them")
}

// CONTRACT: every relative markdown link added by PR #586 resolves inside the
// repository.
func TestGuideLinksResolve(t *testing.T) {
	links := map[string][]string{
		"README.md":                  {"doc/migration-v1alpha1-to-v1alpha2.md"},
		"README-zh_CN.md":            {"doc/zh/migration-v1alpha1-to-v1alpha2.md"},
		"deploy/helm/rbgs/README.md": {"../../../doc/migration-v1alpha1-to-v1alpha2.md"},
		"doc/TOC.md":                 {"./migration-v1alpha1-to-v1alpha2.md", "./zh/migration-v1alpha1-to-v1alpha2.md"},
		"doc/reference/variables.md": {"../migration-v1alpha1-to-v1alpha2.md"},
	}
	for src, targets := range links {
		for _, target := range targets {
			resolved := filepath.Join(repoRoot(t), filepath.Dir(src), target)
			if _, err := os.Stat(resolved); err != nil {
				t.Errorf("link from %s to %s does not resolve (%s)", src, target, resolved)
			}
		}
	}
}

// CONTRACT: the Chinese guide has the same section structure (headings at the
// same line positions) and the same command blocks (ignoring comment lines,
// which are translated) as the English guide.
func TestGuideChineseParity(t *testing.T) {
	en := readRepoFile(t, "doc/migration-v1alpha1-to-v1alpha2.md")
	zh := readRepoFile(t, "doc/zh/migration-v1alpha1-to-v1alpha2.md")
	headings := func(md string) []int {
		var pos []int
		for i, ln := range strings.Split(md, "\n") {
			if strings.HasPrefix(ln, "#") {
				pos = append(pos, i+1)
			}
		}
		return pos
	}
	enH, zhH := headings(en), headings(zh)
	if len(enH) != len(zhH) {
		t.Fatalf("heading count diverged: en=%d zh=%d", len(enH), len(zhH))
	}
	for i := range enH {
		if enH[i] != zhH[i] {
			t.Fatalf("heading %d position diverged: en line %d vs zh line %d", i, enH[i], zhH[i])
		}
	}
	stripComments := func(blocks []string) []string {
		var out []string
		for _, b := range blocks {
			var kept []string
			for _, ln := range strings.Split(b, "\n") {
				if strings.HasPrefix(strings.TrimSpace(ln), "#") {
					continue
				}
				kept = append(kept, ln)
			}
			out = append(out, strings.Join(kept, "\n"))
		}
		return out
	}
	enB, zhB := stripComments(bashBlocks(en)), stripComments(bashBlocks(zh))
	if len(enB) != len(zhB) {
		t.Fatalf("bash block count diverged: en=%d zh=%d", len(enB), len(zhB))
	}
	for i := range enB {
		if enB[i] != zhB[i] {
			t.Errorf("bash block %d diverged:\nen:\n%s\nzh:\n%s", i, enB[i], zhB[i])
		}
	}
}

// CONTRACT (chart claim shared by README/NOTES/guide): the compatibility gate
// defaults to enabled during the 0.9.0 migration window.
func TestChartGateDefaultsToEnabled(t *testing.T) {
	values := readRepoFile(t, "deploy/helm/rbgs/values.yaml")
	re := regexp.MustCompile(`(?m)deprecatedWorkloadTypes:\s*\n\s*enabled:\s*true`)
	if !re.MatchString(values) {
		t.Fatal("guide/NOTES claim controller.deprecatedWorkloadTypes.enabled defaults to true; values.yaml disagrees")
	}
}

// Finding F2 — CANARY. doc/dev/how_to_release.md adds a release checklist item
// asking maintainers to confirm "the Helm README, chart NOTES, and root
// READMEs ... link the English and Chinese migration guides", but
// deploy/helm/rbgs/templates/NOTES.txt contains no migration-guide link, so
// the checklist is unsatisfiable as written for NOTES. PASSES while the gap
// exists; FLIP once the checklist is reworded or NOTES links the guide.
func TestReleaseChecklistNotesLinkGap_Canary(t *testing.T) {
	checklist := readRepoFile(t, "doc/dev/how_to_release.md")
	notes := readRepoFile(t, "deploy/helm/rbgs/templates/NOTES.txt")
	if !checklistMentions(checklist) || !notesDescribesGate(notes) {
		t.Skip("checklist or NOTES gate description changed shape; re-evaluate F2 by hand")
	}
	checklistClaims := checklistClaimsLink(checklist)
	notesLinks := strings.Contains(notes, "migration-v1alpha1-to-v1alpha2")
	if !checklistClaims || notesLinks {
		t.Fatalf("F2 gap appears fixed (checklistClaims=%v, notesLinks=%v); invert this canary", checklistClaims, notesLinks)
	}
	t.Log("F2 confirmed: checklist requires NOTES to link the migration guides, but NOTES has no such link")
}

func checklistMentions(s string) bool {
	return strings.Contains(s, "API deprecation release checklist")
}

func checklistClaimsLink(s string) bool {
	return strings.Contains(s, "chart NOTES") && strings.Contains(s, "link")
}

func notesDescribesGate(s string) bool {
	return strings.Contains(s, "deprecatedWorkloadTypes.enabled")
}
