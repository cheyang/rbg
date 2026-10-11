/*
Copyright 2025 The RBG Authors.

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

// Package cislices is the reviewer verification harness for PR #494
// (test(ci): split full E2E suite into parallel slices).
//
// It re-derives, without a cluster, exactly which Ginkgo specs each CI slice
// selects (ginkgo --dry-run walks the spec tree without running any setup),
// and turns the review findings into tests:
//
//   - TestMatrixSlicesCoverLegacyDefaultSuite  CONTRACT  (guard, F2): the union
//     of the 8 matrix-slice label filters must equal the legacy default-suite
//     filter `!volcano && !scheduler-plugins`. Base CI was fail-open (an
//     unlabeled spec still ran); the matrix is fail-closed, so this guard is
//     what makes a future unlabeled spec loud instead of silently uncovered.
//   - TestManifestSliceRunsOnlySmokeSubset     CANARY    (F1): the manifest
//     installation job now selects only its smoke/install/rbac/webhook subset,
//     not the full legacy suite the base manifest job ran. Passes while the
//     reduction is present; flips red if the manifest filter is widened.
//   - TestMatrixSliceTimeoutEqualsJobTimeout   CANARY    (F3): every matrix
//     slice sets go-test E2E_TIMEOUT from the same matrix.timeout_minutes that
//     bounds the job, leaving no headroom for cluster setup. Flips red when
//     headroom is introduced.
//   - TestVolcanoDiagnosticsQueryKubePodGroupCRD CANARY  (F4): the volcano
//     job's diagnostics step queries the kube-scheduler PodGroup CRD instead
//     of Volcano's. Flips red when the resource is fixed.
//
// This package is intentionally outside ./test/e2e itself so `make test-e2e`
// (which runs `go test ./test/e2e/` only) never triggers it; run it via
// `go test ./test/e2e/cislices/ -v -timeout 15m`.
package cislices

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// legacyDefaultFilter is the base-branch default-suite filter: every spec the
// old monolithic e2e-test job (and the old manifest job) ran.
const legacyDefaultFilter = "!volcano && !scheduler-plugins"

const workflowRelPath = ".github/workflows/e2e-test.yml"

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate repo root (go.mod) above " + dir)
		}
		dir = parent
	}
}

func workflowText(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), workflowRelPath))
	if err != nil {
		t.Fatalf("read %s: %v", workflowRelPath, err)
	}
	return string(b)
}

// matrixFilters extracts the label filters of the e2e-matrix strategy include
// list (the `filter: "..."` entries).
func matrixFilters(t *testing.T) []string {
	t.Helper()
	re := regexp.MustCompile(`(?m)^\s*filter:\s*"([^"]+)"\s*$`)
	var out []string
	for _, m := range re.FindAllStringSubmatch(workflowText(t), -1) {
		out = append(out, m[1])
	}
	if len(out) == 0 {
		t.Fatal("no matrix `filter:` entries found in the workflow; workflow shape changed?")
	}
	return out
}

// labelFilterUsage returns every E2E_LABEL_FILTER='...' value in the workflow
// (manifest / volcano / scheduler-plugins jobs) keyed by a lowercase label for
// logging.
func labelFilterUsages(t *testing.T) []string {
	t.Helper()
	re := regexp.MustCompile(`E2E_LABEL_FILTER='([^']+)'`)
	var out []string
	for _, m := range re.FindAllStringSubmatch(workflowText(t), -1) {
		out = append(out, m[1])
	}
	return out
}

func manifestJobFilter(t *testing.T) string {
	t.Helper()
	job := jobSection(t, "e2e-manifest:")
	re := regexp.MustCompile(`E2E_LABEL_FILTER='([^']+)'`)
	m := re.FindStringSubmatch(job)
	if m == nil {
		t.Fatal("e2e-manifest job no longer passes E2E_LABEL_FILTER (workflow shape changed)")
	}
	return m[1]
}

// jobSection returns the text of one top-level job (from its `  <name>:` key to
// the next 2-space-indented key).
func jobSection(t *testing.T, jobKey string) string {
	t.Helper()
	wf := workflowText(t)
	start := strings.Index(wf, "\n  "+jobKey)
	if start < 0 {
		t.Fatalf("job key %q not found in workflow", jobKey)
	}
	rest := wf[start+1:]
	// Skip past the job's own key line, then cut at the next 2-space-indented
	// key (the next top-level job).
	newline := strings.IndexByte(rest, '\n')
	if newline < 0 {
		return rest
	}
	body := rest[newline+1:]
	re := regexp.MustCompile(`(?m)^  [a-zA-Z0-9_-]+:\s*$`)
	locs := re.FindStringIndex(body)
	end := len(rest)
	if locs != nil {
		end = newline + 1 + locs[0]
	}
	return rest[:end]
}

// selectedSpecs dry-runs the e2e suite under a label filter and returns the
// full texts ("container hierarchy + leaf") of the specs Ginkgo selects.
func selectedSpecs(t *testing.T, filter string) map[string]bool {
	t.Helper()
	root := repoRoot(t)
	report := filepath.Join(t.TempDir(), "report.json")
	cmd := exec.Command("go", "test", "./test/e2e/",
		"-run", "^TestE2E$", "--ginkgo.dry-run",
		"--ginkgo.label-filter="+filter,
		"--ginkgo.json-report="+report,
		"-timeout", "5m")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dry-run under filter %q failed: %v\n%s", filter, err, out)
	}
	b, err := os.ReadFile(report)
	if err != nil {
		t.Fatalf("ginkgo json report missing: %v", err)
	}
	var reportWrapper []struct {
		SpecReports []struct {
			LeafNodeType            string   `json:"LeafNodeType"`
			State                   string   `json:"State"`
			LeafNodeText            string   `json:"LeafNodeText"`
			ContainerHierarchyTexts []string `json:"ContainerHierarchyTexts"`
		} `json:"SpecReports"`
	}
	if err := json.Unmarshal(b, &reportWrapper); err != nil {
		t.Fatalf("parse ginkgo json report: %v", err)
	}
	if len(reportWrapper) != 1 {
		t.Fatalf("unexpected ginkgo report wrapper count %d", len(reportWrapper))
	}
	selected := map[string]bool{}
	for _, s := range reportWrapper[0].SpecReports {
		if s.LeafNodeType == "It" && s.State == "passed" {
			full := strings.TrimSpace(strings.Join(s.ContainerHierarchyTexts, " ") + " " + s.LeafNodeText)
			selected[full] = true
		}
	}
	return selected
}

// TestMatrixSlicesCoverLegacyDefaultSuite is the coverage guard (CONTRACT):
// the union of the matrix slice filters must exactly equal the legacy default
// suite, so no spec silently drops out of CI (fail-closed routing) and no
// volcano/scheduler-plugins spec leaks into the default-scheduler slices.
func TestMatrixSlicesCoverLegacyDefaultSuite(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode: label-coverage dry-run skipped")
	}
	filters := matrixFilters(t)
	legacy := selectedSpecs(t, legacyDefaultFilter)
	union := map[string]bool{}
	for i, f := range filters {
		specs := selectedSpecs(t, f)
		t.Logf("matrix slice %d: filter=%q -> %d specs", i+1, f, len(specs))
		for s := range specs {
			union[s] = true
		}
	}
	var lost, gained []string
	for s := range legacy {
		if !union[s] {
			lost = append(lost, s)
		}
	}
	for s := range union {
		if !legacy[s] {
			gained = append(gained, s)
		}
	}
	sort.Strings(lost)
	sort.Strings(gained)
	t.Logf("legacy default-suite specs: %d; matrix union: %d", len(legacy), len(union))
	if len(lost) > 0 {
		t.Errorf("F2 guard: %d spec(s) covered by the legacy default filter run in NO matrix slice (fail-closed routing; add a routing label or a slice):", len(lost))
		for _, s := range lost {
			t.Errorf("  LOST: %s", s)
		}
	}
	if len(gained) > 0 {
		t.Errorf("F2 guard: %d spec(s) run by a matrix slice but excluded from the legacy default filter (scheduler-specific leak):", len(gained))
		for _, s := range gained {
			t.Errorf("  LEAK: %s", s)
		}
	}
}

// TestManifestSliceRunsOnlySmokeSubset is the repro for finding F1 (CANARY):
// it asserts the *current* (reduced) behavior — the manifest installation job
// selects only a small subset of the specs the base manifest job ran. If the
// manifest filter is widened (the fix), this canary flips red and must be
// inverted into a contract test.
func TestManifestSliceRunsOnlySmokeSubset(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode: label-coverage dry-run skipped")
	}
	filter := manifestJobFilter(t)
	legacy := selectedSpecs(t, legacyDefaultFilter)
	subset := selectedSpecs(t, filter)
	var foreign []string
	for s := range subset {
		if !legacy[s] {
			foreign = append(foreign, s)
		}
	}
	sort.Strings(foreign)
	if len(foreign) > 0 {
		t.Fatalf("manifest filter selects specs outside the legacy default suite: %v", foreign)
	}
	t.Logf("F1: manifest job filter %q selects %d of the %d legacy default-suite specs (base manifest job ran all %d)",
		filter, len(subset), len(legacy), len(legacy))
	if len(subset) >= len(legacy)/2 {
		t.Fatalf("F1 canary FLIPPED: manifest slice now selects %d/%d specs — the coverage reduction is fixed; invert this canary into a contract test", len(subset), len(legacy))
	}
	// Canary body: the reduction is present (this is the observed defect).
	if len(subset) < len(legacy)/2 {
		t.Logf("F1 canary: reduction still present (%d < %d)", len(subset), len(legacy))
	}
}

// TestMatrixSliceTimeoutEqualsJobTimeout is the repro for finding F3 (CANARY):
// it asserts the current wiring where the go-test timeout equals the job
// timeout for every matrix slice, leaving no headroom for cluster setup. If
// headroom is introduced, the canary flips red.
func TestMatrixSliceTimeoutEqualsJobTimeout(t *testing.T) {
	wf := workflowText(t)
	// The run step wires E2E_TIMEOUT straight from matrix.timeout_minutes.
	reWiring := regexp.MustCompile(`E2E_TIMEOUT=\$\{\{ *matrix\.timeout_minutes *\}\}m`)
	if !reWiring.MatchString(wf) {
		t.Fatalf("F3 canary FLIPPED: E2E_TIMEOUT is no longer wired directly from matrix.timeout_minutes — headroom exists; invert this canary")
	}
	// And every matrix entry sets a timeout_minutes that is only used for both.
	entries := regexp.MustCompile(`(?m)^\s*timeout_minutes:\s*(\d+)\s*$`).FindAllStringSubmatch(
		jobSection(t, "e2e-matrix:"), -1)
	if len(entries) < 8 {
		t.Fatalf("expected >=8 matrix timeout_minutes entries, got %d (workflow shape changed?)", len(entries))
	}
	for _, e := range entries {
		t.Logf("F3 canary: slice job timeout = E2E_TIMEOUT = %sm, no setup headroom", e[1])
	}
}

// TestVolcanoDiagnosticsQueryKubePodGroupCRD is the repro for finding F4
// (CANARY): the volcano job's Collect diagnostics step greps
// podgroups.scheduling.x-k8s.io (kube-scheduler PodGroup CRD), which does not
// exist on a Volcano cluster (Volcano uses scheduling.volcano.sh). Flips red
// when the diagnostics are fixed.
func TestVolcanoDiagnosticsQueryKubePodGroupCRD(t *testing.T) {
	job := jobSection(t, "e2e-volcano-gang:")
	if strings.Contains(job, "podgroups.scheduling.volcano.sh") && !strings.Contains(job, "podgroups.scheduling.x-k8s.io") {
		t.Fatalf("F4 canary FLIPPED: volcano diagnostics now query the Volcano PodGroup CRD — fixed; invert this canary")
	}
	if !strings.Contains(job, "podgroups.scheduling.x-k8s.io") {
		t.Fatalf("F4: volcano job diagnostics no longer query any PodGroup CRD (workflow shape changed?)")
	}
	t.Log("F4 canary: volcano job diagnostics still query podgroups.scheduling.x-k8s.io (kube PodGroup CRD, absent on Volcano clusters)")
}

// TestSliceFiltersParseSanity is a cheap smoke check that the filters extracted
// from the workflow are the expected shape (guards against silent regex drift
// of the harness itself).
func TestSliceFiltersParseSanity(t *testing.T) {
	filters := matrixFilters(t)
	if len(filters) != 8 {
		t.Fatalf("expected 8 matrix slice filters, got %d: %v", len(filters), filters)
	}
	usages := labelFilterUsages(t)
	if len(usages) != 4 {
		t.Fatalf("expected 4 E2E_LABEL_FILTER usages (matrix + manifest/volcano/scheduler-plugins), got %d: %v", len(usages), usages)
	}
	if usages[0] != "${{ matrix.filter }}" {
		t.Fatalf("expected the matrix run step to pass E2E_LABEL_FILTER='${{ matrix.filter }}' first, got %q", usages[0])
	}
}

// TestWorkflowBiteGuardSelfCheck documents the harness-bites check: mutating
// one matrix filter to a nonexistent label must make
// TestMatrixSlicesCoverLegacyDefaultSuite fail. Run manually after grafting:
//
//	sed -i 's/filter: "lifecycle &&/filter: "lifecycle-nonexistent \&\&/' .github/workflows/e2e-test.yml
//	go test ./test/e2e/cislices/ -run TestMatrixSlicesCoverLegacyDefaultSuite   # must FAIL
//	git checkout .github/workflows/e2e-test.yml
func TestWorkflowBiteGuardSelfCheck(t *testing.T) {
	t.Skip("documentation only — see the comment body for the manual bite check")
}
