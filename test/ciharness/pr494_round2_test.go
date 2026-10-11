// Round-2 (incremental) additions to the PR #494 verification harness.
//
// The PR head is unchanged this round (ecd1120a); these tests make two ledger findings
// that round 1 carried as static/cross-critique evidence runnable on this branch too:
//
//	S2 (AC4) — the e2e-manifest job cut manifest-install coverage from the full legacy
//	           default suite to a 12-spec smoke subset. Contract: parity is restored OR
//	           the "full coverage" claim is withdrawn and the cut documented.
//	S3 (AC5) — the matrix is fail-closed on routing labels with no automated guard.
//	           Contract: CI contains a step that fails when slice union drifts from the
//	           legacy suite. A passing mechanism demo shows why the guard is needed.
//
// Additive only; production code untouched.
package ciharness

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// ---------- S2 / AC4: manifest-install path coverage ----------

var fullCoverageClaimRE = regexp.MustCompile(`(?i)full (e2e )?coverage`)
var manifestScopeNoteRE = regexp.MustCompile(`(?i)manifest.{0,300}(smoke|subset|reduc|only|12)|((smoke|subset|reduc|only).{0,300}manifest)`)

// TestPR494_ManifestJobKeepsBroadCoverageOrDocumentsCut encodes AC4: the committed
// deploy/kubectl/manifests.yaml install (a user-facing release artifact) must either keep
// behavioral coverage parity with the base (whose e2e-test-manifest job ran the FULL legacy
// default suite via bare `make test-e2e`), or the reduction must be explicitly documented
// with the "full coverage" claim withdrawn.
func TestPR494_ManifestJobKeepsBroadCoverageOrDocumentsCut(t *testing.T) {
	legacy := dryRunSpecs(t, oldWorldFilters()["old-default(monolith+manifest)"])
	manifestFilter, ok := literalJobLabelFilters(t)["e2e-manifest"]
	if !ok || manifestFilter == "" {
		t.Fatalf("could not extract the e2e-manifest job label filter from e2e-test.yml")
	}
	manifest := dryRunSpecs(t, manifestFilter)
	t.Logf("legacy default suite: %d specs; e2e-manifest job selects: %d (filter %q)",
		len(legacy), len(manifest), manifestFilter)

	missing := minus(t, legacy, manifest)
	if len(missing) == 0 {
		t.Logf("coverage parity restored: manifest job covers the full legacy suite")
		return
	}
	t.Logf("manifest path lost coverage of %d/%d legacy specs, e.g.:", len(missing), len(legacy))
	for i, s := range missing {
		if i >= 8 {
			t.Logf("  ... and %d more", len(missing)-8)
			break
		}
		t.Logf("  - %s (%s)", s.text, s.id)
	}

	// Coverage is cut; the contract then requires the cut to be documented and the
	// "full coverage" claim withdrawn in the plan doc.
	plan := readFile(t, "test/e2e/ci-optimization-plan.md")
	claimsFull := fullCoverageClaimRE.MatchString(plan)
	documentsCut := manifestScopeNoteRE.MatchString(plan)
	if claimsFull || !documentsCut {
		t.Errorf("AC4 violated: e2e-manifest runs %d/%d legacy specs and the cut is not documented "+
			"(full-coverage claim present: %v, manifest-scope note present: %v)",
			len(manifest), len(legacy), claimsFull, documentsCut)
		return
	}
	t.Logf("coverage cut is documented and the full-coverage claim is withdrawn — contract met")
}

// ---------- S3 / AC5: fail-closed routing needs a CI guard ----------

// dryRunSpecsAllowEmpty is dryRunSpecs without the non-empty guard: it reports the raw
// selection count and the go-test exit status, for demonstrating empty-suite behavior.
func dryRunSpecsAllowEmpty(t *testing.T, filter string) (nSpecs int, exitOK bool, out string) {
	t.Helper()
	cmd := exec.Command("go", "test", "./test/e2e/", "-count=1",
		"-ginkgo.dry-run", "--ginkgo.label-filter="+filter, "-timeout", "3m")
	cmd.Dir = repoRoot(t)
	cmd.Env = append(os.Environ(), "KUBECONFIG="+fakeKubeconfig(t))
	b, err := cmd.CombinedOutput()
	out = string(b)
	nSpecs = strings.Count(out, "• [0.000 seconds]")
	return nSpecs, err == nil, out
}

// TestPR494_UnlabeledSpecGetsZeroCoverageAndStaysGreen is a MECHANISM DEMO (not a fix
// detector — it passes at the PR head by design). It shows the failure mode behind S3:
// a spec carrying none of the routing labels matches no matrix slice, ginkgo dry-runs the
// empty selection, and `go test` still exits 0. Today every spec is labeled, so the demo
// selects 0 specs; the day a contributor adds an unlabeled spec, its count silently moves
// to exactly that spec and every job stays green — nothing in CI asserts the union.
func TestPR494_UnlabeledSpecGetsZeroCoverageAndStaysGreen(t *testing.T) {
	none := "!(lifecycle || workload || update || rollout || feature || webhook || rbac || scheduler || v1alpha1 || volcano || scheduler-plugins)"
	n, exitOK, out := dryRunSpecsAllowEmpty(t, none)
	t.Logf("filter %q selected %d specs; go test exit ok: %v", none, n, exitOK)
	if !exitOK {
		t.Fatalf("mechanism changed: ginkgo/go test now FAILS on an empty label selection — "+
			"re-evaluate S3's premise.\n--- output ---\n%s", out)
	}
	if n > 0 {
		t.Logf("note: %d specs currently carry NO routing label — they run in no matrix slice "+
			"and CI stays green (S3 materialized; see TestPR494_MatrixUnionCoversOldSuite)", n)
	}
}

// guardWiredRE accepts the plausible shapes of a CI coverage guard: a make target, a script,
// or an inline step that dry-runs label filters and diffs the union against the legacy suite.
var guardWiredRE = regexp.MustCompile(`(?i)(test-e2e[a-z-]*cover|coverage[- ]guard|union.{0,40}cover|cover.{0,40}union|label[- ]coverage|ginkgo\.dry-run.{0,160}(lost|dropped|legacy))`)

// TestPR494_MatrixCoverageGuardWiredInCI encodes AC5 (S3's fix contract): some CI step in
// e2e-test.yml must fail when the matrix union stops covering the legacy default suite, so
// a spec without a routing label cannot vanish silently. FAILS at the PR head: the only
// union-coverage check today is a manual step listed in ci-optimization-plan.md.
func TestPR494_MatrixCoverageGuardWiredInCI(t *testing.T) {
	wf := readFile(t, ".github/workflows/e2e-test.yml")
	if !guardWiredRE.MatchString(wf) {
		t.Errorf("AC5 violated: e2e-test.yml has no automated union-coverage guard step " +
			"(matrix routing is fail-closed on routing labels; only a manual checklist item " +
			"in test/e2e/ci-optimization-plan.md watches for drift)")
	}
}
