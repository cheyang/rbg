/*
Round-4 verification harness for PR #491 (KEP-455: rbgset rolling update).
Reviewer B (Codex). Additive only — no production code is touched.

Round-4 delta reviewed: f711893c..5f14daaf ("kep: correct the description
related to Round 3", KEP-only, +71/-22). The delta:
  - narrows the v1alpha1 round-trip guarantee (groupTemplate labels/annotations
    cannot round-trip; rolling sets must be written through v1alpha2),
  - declares partition > replicas admitted with controller-side clamping
    (StatefulSet parity),
  - adds a canonical-payload requirement for ControllerRevision data
    (sorted keys; strategic-merge patch re-serialization hazard),
  - re-sizes surge from the live availability budget,
  - adds an orphan-adoption scenario row claiming parity with the legacy path,
  - widens the request writer to "every budgeted child write",
  - pins the requeue scope (unacknowledged request = only periodic requeue),
  - fixes the W1 wording ("admission check" -> "availability gate").

Polarity:
  - TestVerify_R4*_* (fix guards): PASS at 5f14daaf; FAIL at f711893c.
  - TestVerify_R4F11_* / TestVerify_R4Q4_*: findings reproduced at 5f14daaf
    (RED); flip green once the KEP is amended.
  - TestVerify_R4B*_*: code-fact contracts evaluated against the PR BASE tree
    (env RBG_BASE_ROOT), because the PR head's non-KEP code is the stale
    merge-base 0821cb5b while the KEP describes "the legacy path" of current
    main (a2e797c6). Skipped with a clear message when RBG_BASE_ROOT is unset.
*/
package pr491kep

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// kepSection returns the readme slice from heading `from` (inclusive) to
// heading `to` (exclusive). Duplicated small helper kept local to round 4 to
// avoid coupling to earlier round files' unexported helpers beyond unwrap.
func kepSectionR4(t *testing.T, from, to string) string {
	t.Helper()
	readme := kepReadme(t)
	i := strings.Index(readme, from)
	if i < 0 {
		t.Fatalf("KEP structure changed: heading %q not found", from)
	}
	rest := readme[i+len(from):]
	j := strings.Index(rest, to)
	if j < 0 {
		t.Fatalf("KEP structure changed: heading %q not found after %q", to, from)
	}
	return rest[:j]
}

// baseRoot resolves the PR base checkout for code-fact contracts.
func baseRoot(t *testing.T) string {
	t.Helper()
	root := os.Getenv("RBG_BASE_ROOT")
	if root == "" {
		t.Skip("RBG_BASE_ROOT unset: base-tree code-fact contract skipped " +
			"(point it at a checkout of the PR base branch, e.g. a2e797c6)")
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("RBG_BASE_ROOT %s has no go.mod: %v", root, err)
	}
	return root
}

func readBaseFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(baseRoot(t), rel))
	if err != nil {
		t.Fatalf("read base %s: %v", rel, err)
	}
	return string(b)
}

// ---------------------------------------------------------------------------
// R4-W1 fix guard (nit, round-3): the Recreate replicas-only paragraph must not
// call the runtime availability-floor evaluation an "admission check".
// PASS at 5f14daaf ("The same availability gate applies"); FAIL at f711893c
// ("The same admission check applies").
// ---------------------------------------------------------------------------
func TestVerify_R4W1_AvailabilityGateWording(t *testing.T) {
	readme := unwrap(kepReadme(t))
	if regexp.MustCompile(`(?i)the same admission check applies to \x60?Recreate`).MatchString(readme) {
		t.Errorf("W1 regressed: replicas-only paragraph again calls the runtime availability floor an 'admission check'")
	}
	if !regexp.MustCompile(`(?i)the same availability (gate|floor check) applies to \x60?Recreate`).MatchString(readme) {
		t.Errorf("W1 fix missing: expected 'The same availability gate applies to Recreate replicas-only in-place updates'")
	}
}

// ---------------------------------------------------------------------------
// R4-PARTITION fix guard + R4-F11 finding.
// Round 4 declares: admission does not bound partition by replicas; the
// validating rule does not match rolebasedgroupsets/scale; the controller
// resolves min(partition, replicas) (StatefulSet parity); and the scenario row
// points at the Appendix for the rationale.
// (a) guard: the clamp + admission stance is stated. PASS at 5f14daaf.
// (b) R4-F11 [finding, minor]: the scenario row says "Rationale in the
//
//	Appendix" but the Appendix carries no partition entry at all — a
//	dangling cross-reference. RED at 5f14daaf (reproduction); flips green
//	when an Appendix entry carrying the rationale is added.
//
// ---------------------------------------------------------------------------
func TestVerify_R4Partition_ClampAndAdmissionStated(t *testing.T) {
	readme := unwrap(kepReadme(t))
	if !regexp.MustCompile(`min\(partition, replicas\)`).MatchString(readme) {
		t.Errorf("round-4 partition clamp missing: expected controller resolves min(partition, replicas)")
	}
	if !regexp.MustCompile(`(?i)admission does not bound \x60?partition\x60? by \x60?replicas`).MatchString(readme) {
		t.Errorf("round-4 admission stance missing: expected 'admission does not bound partition by replicas'")
	}
	if !regexp.MustCompile(`rolebasedgroupsets/scale`).MatchString(readme) {
		t.Errorf("round-4 scale-subresource carve-out missing: validating rule must not match rolebasedgroupsets/scale")
	}
	if !regexp.MustCompile(`(?i)partition above \x60?replicas\x60? is admitted`).MatchString(readme) {
		t.Errorf("round-4 admission test-plan entry missing: partition above replicas admitted on both verbs")
	}
}

func TestVerify_R4F11_AppendixRationaleForPartitionClamp(t *testing.T) {
	scenario := kepSectionR4(t, "### Scenario Handling", "### Availability and Surge")
	if !strings.Contains(unwrap(scenario), "Rationale in the Appendix") {
		t.Skip("scenario row no longer points at the Appendix; harness-update needed")
	}
	appendix := kepSectionR4(t, "## Appendix", "## Alternatives")
	// An Appendix entry carrying the partition rationale must mention partition
	// AND the scale/writability reasoning (autoscaler / scale subresource /
	// stays writable). The percentage-partition timing entry (F7) does not
	// qualify: it covers resolution timing, not why partition > replicas is
	// admitted.
	rationale := regexp.MustCompile(`(?i)partition[^.\n]{0,200}(scale|autoscal|writable|writable for every later request|not bounded|above)`).MatchString(unwrap(appendix)) ||
		regexp.MustCompile(`(?i)(scale|autoscal)[^.\n]{0,200}partition`).MatchString(unwrap(appendix))
	if !rationale {
		t.Errorf("R4-F11 reproduced: scenario row says 'Rationale in the Appendix' but the Appendix has no partition-clamping entry (dangling cross-reference)")
	}
}

// ---------------------------------------------------------------------------
// R4-CR fix guard: canonical-payload requirement for ControllerRevision data.
// PASS at 5f14daaf; FAIL at f711893c. The apiserver-side hazard mechanism is
// proven empirically by the envtest integration test
// TestVerify_R4CR_ControllerRevisionDataImmutableUnderPatch (layer L2) and by
// k8s v1.34.2 source (module cache): ValidateControllerRevisionUpdate marks
// Data immutable (pkg/apis/apps/validation/validation.go:366) via
// apiequality.Semantic.DeepEqual, which registers no RawExtension equality and
// therefore compares .Raw byte-wise (apimachinery/pkg/api/equality/semantic.go);
// the GC orphans built-in dependents with strategic merge patch first
// (pkg/controller/garbagecollector/patch.go:92), falling back to JSON merge
// patch only on UnsupportedMediaType.
// ---------------------------------------------------------------------------
func TestVerify_R4CR_CanonicalPayloadRequirement(t *testing.T) {
	sec := kepSectionR4(t, "### Revision Management", "### Scenario Handling")
	usec := unwrap(sec)
	checks := []struct{ name, re string }{
		{"canonical json stated", `(?i)canonical JSON`},
		{"sorted keys", `(?i)sorted keys`},
		{"not struct order", `(?i)not\s+Go struct field order`},
		{"data immutable byte-wise", `(?i)ControllerRevision\x60?\.data\x60? is immutable and compared byte-wise`},
		{"smp reserialization hazard", `(?i)strategic-merge patch re-serializes`},
		{"gc orphan connection", `(?i)garbage collection`},
		{"orphan finalizer consequence", `(?i)cascade=orphan`},
	}
	for _, c := range checks {
		if !regexp.MustCompile(c.re).MatchString(usec) {
			t.Errorf("R4-CR guard: Revision Management section lacks %s (/%s/)", c.name, c.re)
		}
	}
	tp := kepSectionR4(t, "### Test Plan", "## Appendix")
	utp := unwrap(tp)
	if !regexp.MustCompile(`(?i)canonical payload`).MatchString(utp) {
		t.Errorf("R4-CR guard: unit test-plan entry for canonical payload missing")
	}
	if !regexp.MustCompile(`(?i)struct field order is not already sorted`).MatchString(utp) {
		t.Errorf("R4-CR guard: fixture caveat (unsorted struct order, else the assertion proves nothing) missing")
	}
	if !regexp.MustCompile(`(?i)revision payload immutability`).MatchString(utp) {
		t.Errorf("R4-CR guard: envtest entry for revision payload immutability missing")
	}
}

// ---------------------------------------------------------------------------
// R4-ORPHAN: the new scenario row claims the rolling path's adoption matches
// the legacy path. Code-fact contract evaluated against the BASE tree
// (RBG_BASE_ROOT), because the PR head carries the stale merge-base code.
// PASS at base a2e797c6. FAIL (KEP/base drift) if the legacy path changes.
// ---------------------------------------------------------------------------
func TestVerify_R4Orphan_LegacyAdoptionFactsMatchKEP(t *testing.T) {
	src := readBaseFile(t, "internal/controller/workloads/rolebasedgroupset_controller.go")

	// (1) children are listed by this set's name label (orphans keep labels,
	//     so they remain discoverable).
	if !regexp.MustCompile(`GroupSetNameLabelKey, rbgset\.Name`).MatchString(src) ||
		!strings.Contains(src, "MatchingLabelsSelector") {
		t.Errorf("R4-ORPHAN base drift: set controller no longer lists children by GroupSetNameLabelKey")
	}
	// (2) adoptable := no controller reference AND this set's name label;
	//     owned := controller UID match.
	body := extractFuncBody(t, src, "isOwnedOrAdoptable")
	if !strings.Contains(body, "GetControllerOfNoCopy") ||
		!regexp.MustCompile(`GroupSetNameLabelKey\] == rbgset\.Name`).MatchString(body) ||
		!regexp.MustCompile(`ref\.UID == rbgset\.UID`).MatchString(body) {
		t.Errorf("R4-ORPHAN base drift: isOwnedOrAdoptable no longer implements " +
			"'no controller reference plus this set's name label' / UID match")
	}
	// (3) adoption happens in the reconcile listing loop, before scaleUp/sync
	//     (KEP: "adopts the orphans before any budgeted write").
	recIdx := strings.Index(src, "func (r *RoleBasedGroupSetReconciler) Reconcile")
	adoptIdx := strings.Index(src, "Adopted orphaned RoleBasedGroup")
	scaleIdx := strings.Index(src, "r.scaleUp(")
	if recIdx < 0 || adoptIdx < 0 || scaleIdx < 0 || !(recIdx < adoptIdx && adoptIdx < scaleIdx) {
		t.Errorf("R4-ORPHAN base drift: orphan adoption no longer precedes scaleUp in Reconcile")
	}
	// (4) create-collision path pre-checks the existing object and refuses a
	//     foreign-UID-controlled child (KEP: 'controlled by a different set UID
	//     stays an error' in the rolling path; legacy logs and skips).
	scaleBody := extractFuncBody(t, src, "scaleUp")
	if !strings.Contains(scaleBody, "ref.UID != rbgset.UID") {
		t.Errorf("R4-ORPHAN base drift: scaleUp no longer detects a foreign-UID-controlled child on collision")
	}

	// (5) KEP side: the row and the test-plan entries exist.
	row := kepSectionR4(t, "### Scenario Handling", "### Availability and Surge")
	urow := unwrap(row)
	if !regexp.MustCompile(`(?i)owned \x60?\*?\*?or\*?\*? adoptable|owned \*\*or adoptable\*\*`).MatchString(urow) &&
		!strings.Contains(urow, "adoptable") {
		t.Errorf("R4-ORPHAN guard: scenario row for orphan adoption missing")
	}
	if !strings.Contains(urow, "matching the legacy path") {
		t.Errorf("R4-ORPHAN guard: scenario row no longer claims legacy-path parity")
	}
	tp := unwrap(kepSectionR4(t, "### Test Plan", "## Appendix"))
	if !regexp.MustCompile(`(?i)orphan adoption`).MatchString(tp) {
		t.Errorf("R4-ORPHAN guard: unit test-plan entry for orphan adoption missing")
	}
	if !regexp.MustCompile(`(?i)cascade=orphan`).MatchString(tp) {
		t.Errorf("R4-ORPHAN guard: manual validation entry for --cascade=orphan missing")
	}
}

// ---------------------------------------------------------------------------
// R4-SURGE fix guard: surge sized from the live availability budget.
// PASS at 5f14daaf; FAIL at f711893c (declared-budget sizing).
// ---------------------------------------------------------------------------
func TestVerify_R4Surge_LiveBudgetSizingStated(t *testing.T) {
	readme := unwrap(kepReadme(t))
	if !regexp.MustCompile(`(?i)surge is sized from the live availability budget`).MatchString(readme) {
		t.Errorf("R4-SURGE guard: live-budget sizing statement missing")
	}
	if !regexp.MustCompile(`(?i)still\s+serving\*?\*? need cover`).MatchString(readme) {
		t.Errorf("R4-SURGE guard: 'only in-scope base groups that are still serving need cover' missing")
	}
	// worked examples: (4,5,0) -> four; maxUnavailable=1 -> three.
	if !regexp.MustCompile(`(?i)need at most four surge groups, not five, and with \x60?maxUnavailable=1\x60? the same batch needs three`).MatchString(readme) {
		t.Errorf("R4-SURGE guard: worked examples (maxUnavailable=0 -> four; =1 -> three) missing")
	}
	// the failure-below-partition critique and the e2e opposite-case entry.
	if !regexp.MustCompile(`(?i)held back below the partition, which no rollout touches`).MatchString(readme) {
		t.Errorf("R4-SURGE guard: below-partition failure critique missing")
	}
	tp := unwrap(kepSectionR4(t, "### Test Plan", "## Appendix"))
	if !regexp.MustCompile(`(?i)a failure below the partition must not reduce the batch to no surge`).MatchString(tp) {
		t.Errorf("R4-SURGE guard: updated surge-accounting unit entry missing")
	}
	if !regexp.MustCompile(`(?i)held back below the partition has already spent that budget`).MatchString(tp) {
		t.Errorf("R4-SURGE guard: e2e opposite-case entry missing")
	}
}

// ---------------------------------------------------------------------------
// R4-Q4 [question, minor]: the new surge scenario row says a failed group
// "never becomes an update candidate", while the availability paragraph
// (:both strategies) says acting on an already-unready outdated group does not
// consume budget and budget-blocked candidates must not prevent repair of
// lower, unready ordinals. If an IN-SCOPE group fails mid-rollout, is it
// repaired budget-free (availability paragraph) or excluded until organic
// recovery (surge row)? The row's absolute phrasing also stalls
// RolloutComplete whenever an in-scope group never recovers — possibly
// intended (fail-safe), but the text should say so.
// RED at 5f14daaf (ambiguity reproduced); flips green when clarified.
// ---------------------------------------------------------------------------
func TestVerify_R4Q4_FailedGroupCandidacyClarified(t *testing.T) {
	readme := unwrap(kepReadme(t))
	row := regexp.MustCompile(`(?i)never becomes an update candidate`).MatchString(readme)
	if !row {
		t.Skip("surge row rephrased; harness-update needed")
	}
	// Clarified iff the candidacy statement is scoped (e.g. 'while it remains
	// unready', 'below the partition', 'until it recovers') or reconciled with
	// the budget-free repair path within the same sentence/row.
	clarified := regexp.MustCompile(`(?i)never becomes an update candidate[^.\n]{0,160}(while|until|unless|below the partition|recover)`).MatchString(readme) ||
		regexp.MustCompile(`(?i)(while|until|unless)[^.\n]{0,120}never becomes an update candidate`).MatchString(readme) ||
		regexp.MustCompile(`(?i)never becomes an update candidate[^.\n]{0,160}budget`).MatchString(readme)
	if !clarified {
		t.Errorf("R4-Q4 reproduced: 'never becomes an update candidate' is unscoped and collides with the budget-free repair of already-unready outdated groups")
	}
}

// ---------------------------------------------------------------------------
// R4-V1A1 fix guard: the round-trip guarantee is narrowed (not lossless for
// groupTemplate labels/annotations) consistently in the risk row, the scenario
// table, and the Appendix; no unqualified lossless claim remains; the
// conversion test entry stays scoped to request/ack fields (both-version-native).
// PASS at 5f14daaf; FAIL at f711893c (unqualified "lossless" claim).
// ---------------------------------------------------------------------------
func TestVerify_R4V1alpha1_NotLosslessStatedConsistently(t *testing.T) {
	readme := unwrap(kepReadme(t))
	if regexp.MustCompile(`(?i)keeps round-trips lossless`).MatchString(readme) {
		t.Errorf("R4-V1A1 guard: unqualified lossless claim still present in risk row")
	}
	n := len(regexp.MustCompile(`(?i)groupTemplate.{0,20}(labels|annotations)`).FindAllString(readme, -1))
	if n < 3 {
		t.Errorf("R4-V1A1 guard: expected the groupTemplate metadata caveat in risk row + scenario table + appendix (found %d mentions)", n)
	}
	if !regexp.MustCompile(`(?i)rolling sets must be written through v1alpha2|write rolling sets through v1alpha2`).MatchString(readme) {
		t.Errorf("R4-V1A1 guard: 'write rolling sets through v1alpha2' guidance missing")
	}
	// the conversion unit entry must stay scoped to the both-version-native
	// fields (request annotation / acknowledgement), not a general guarantee.
	tp := unwrap(kepSectionR4(t, "### Test Plan", "## Appendix"))
	if !regexp.MustCompile(`(?i)request annotations and acknowledgements round-trip`).MatchString(tp) {
		t.Errorf("R4-V1A1 guard: conversion test entry no longer scoped to request/ack round-trip; harness-update needed")
	}
}

// ---------------------------------------------------------------------------
// R4-REQUEUE fix guard: the unacknowledged request is the barrier's only
// periodic requeue; neither controller polls. PASS at 5f14daaf; FAIL at
// f711893c (requeue mentioned without the scope pin).
// ---------------------------------------------------------------------------
func TestVerify_R4Requeue_ScopeStated(t *testing.T) {
	readme := unwrap(kepReadme(t))
	if !regexp.MustCompile(`(?i)only periodic requeue`).MatchString(readme) {
		t.Errorf("R4-REQUEUE guard: 'only periodic requeue' pin missing from the acknowledgement paragraph")
	}
	if !regexp.MustCompile(`(?i)RollingUpdateInProgress=True\x60? by itself requeues nothing`).MatchString(readme) {
		t.Errorf("R4-REQUEUE guard: condition-alone-requeues-nothing statement missing")
	}
	tp := unwrap(kepSectionR4(t, "### Test Plan", "## Appendix"))
	if !regexp.MustCompile(`(?i)requeue scope`).MatchString(tp) || !regexp.MustCompile(`(?i)neither controller polls`).MatchString(tp) {
		t.Errorf("R4-REQUEUE guard: requeue-scope test-plan entry missing")
	}
}
