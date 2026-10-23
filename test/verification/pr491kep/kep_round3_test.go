/*
Round-3 additions to the PR #491 verification harness (head f711893c).
Reviewer B (Codex). Additive only — no production code is touched.

Round 3 replaced the round-2 consumer rule (absent-condition exception) with a
persistent request/acknowledgement mechanism:

  - the set advances a per-child rbg.workloads.x-k8s.io/rollout-request counter
    in the same resource-version-protected Update as the child spec/metadata/
    revision label;
  - the RBG controller writes status.observedRolloutRequest only after workload
    application succeeds and a direct recheck of every role workload passes;
  - groupSetChildReady requires request == acknowledgement whenever the
    annotation exists, in addition to the generation/Ready/condition gates;
    a missing condition never bypasses a pending request.

This file encodes:
  - remedy-neutral contracts for the round-2 N1 acceptance criterion
    (AC-491-bootstrap-window): a spec-bumped child must not count as ready
    before its controller has acknowledged the new request. The round-2
    contract TestVerify_R2N1_AbsentBarrierExceptionIsGenerationGated was
    remedy-shaped (it asserted the generation gate specifically) and is
    SUPERSEDED by TestVerify_R3N1_* here; it is kept in kep_round2_test.go as
    the historical reproduction record. Harness-update recorded in the
    manifest, per policy.
  - deterministic discrete-event exhibits that the specified request/ack rule
    holds the availability budget (the rollout path does not exist in code
    yet, so a simulation of the specified rule is the strongest available
    evidence class; the sim asserts the mechanism's teeth by including a
    no-request-gate control that must FAIL-safe... i.e. admit the window).
  - updated guards for F1 (barrier section restructured) and F3/F4 (key list
    extended 4 -> 6 with restated scope). The round-2 guards bit on exact
    round-2 phrasing; the round-3 guards assert substance.
  - contracts for the round-3 findings: R3M1 residual (conversion stash entry
  - two wording catches), F9 (request-writer scope vs drift repair), F10
    (replace-write strip fallback), Q3 (unbudgeted compatibility-annotation
    convergence).

Polarity: all tests here are contracts. RED at head = finding reproduced;
GREEN at head = verified fixed/addressed.
*/
package pr491kep

import (
	"regexp"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// R3-N1 contracts (fix verification). PASS at f711893c; FAIL at b03a7e6
// (reproduction baseline: the request/ack mechanism did not exist).
// ---------------------------------------------------------------------------

// unwrap collapses markdown line wrapping (newline + continuation indent) into
// single spaces so prose regexes do not depend on where the author wrapped.
var unwrapRe = regexp.MustCompile(`\s+`)

func unwrap(s string) string { return unwrapRe.ReplaceAllString(s, " ") }

func kepSectionUnwrapped(t *testing.T, from, to string) string {
	t.Helper()
	return unwrap(kepSection(t, from, to))
}

// TestVerify_R3N1_RequestAckGateClosesBootstrapWindow asserts the
// remedy-neutral round-2 acceptance criterion: a child the set has bumped
// cannot count as ready before its controller has observed/acknowledged the
// bump, under EVERY set-initiated in-place update — including metadata-only
// writes that do not change metadata.generation (the case a pure generation
// gate cannot cover).
func TestVerify_R3N1_RequestAckGateClosesBootstrapWindow(t *testing.T) {
	readme := unwrap(kepReadme(t))
	barrier := kepSectionUnwrapped(t, "### Child Rollout Barrier", "### Status")

	// (1) Request writer: every in-place child update advances the request in
	// the SAME Update as spec/metadata/revision label (atomicity: no window
	// where the spec landed but the request did not).
	if !regexp.MustCompile(`(?s)rollout-request.{0,400}same resource-version\s+protected ` + "`Update`" + `[^.]{0,80}spec, metadata, and set revision label`).MatchString(barrier) {
		t.Errorf("R3-N1: request writer must advance the rollout-request in the same Update as spec/metadata/revision label")
	}
	// (2) Consumer: whenever a request exists it must match the ack.
	avail := kepSectionUnwrapped(t, "### Availability and Surge", "### Child Rollout Barrier")
	if !regexp.MustCompile(`(?i)rollout request exists, its value must match\s+` + "`status.observedRolloutRequest`").MatchString(avail) {
		t.Errorf("R3-N1: consumer rule no longer requires request == acknowledgement when the annotation exists")
	}
	// (3) A missing condition must never bypass a pending request.
	if !regexp.MustCompile(`(?i)never bypasses? a pending request`).MatchString(avail) ||
		!regexp.MustCompile(`(?i)missing condition cannot (bypass|release) an issued request`).MatchString(readme) {
		t.Errorf("R3-N1: the absent-condition fallback is not subordinated to a pending request")
	}
	// (4) The legacy fallback is restricted to children the set has never
	// requested (i.e. never bumped): any set bump writes a request.
	if !regexp.MustCompile(`(?i)legacy child without a request retains the existing readiness fallback`).MatchString(barrier) {
		t.Errorf("R3-N1: legacy fallback is not restricted to never-requested children")
	}
	// (5) A -> B -> A: repeated content receives a NEW request, never reuses an
	// old acknowledgement (content-addressed revisions make generations/labels
	// collide on revert).
	if !regexp.MustCompile(`(?i)A -> B -> A.{0,80}new request rather than reusing an old acknowledgement`).MatchString(barrier) {
		t.Errorf("R3-N1: A->B->A revert is not pinned to a fresh request")
	}
	// (6) Persistence: request + ack live on the child object, so a set
	// controller restart or leader handover cannot release the budget.
	if !regexp.MustCompile(`(?i)persist on the child object, so controller restart or leader handover does not release the budget`).MatchString(barrier) {
		t.Errorf("R3-N1: request/ack persistence across controller restart is not stated")
	}
	// (7) The ack is written only after application + a direct recheck, so a
	// child whose controller never runs (crashloop, RBAC) can never self-certify.
	if !regexp.MustCompile(`(?i)acknowledges only after[^.]{0,160}succeed`).MatchString(barrier) ||
		!regexp.MustCompile(`(?i)directly rechecks every role workload`).MatchString(barrier) {
		t.Errorf("R3-N1: acknowledgement is not gated on post-application workload rechecks")
	}
}

// TestVerify_R3N1_RiskRowAndBootstrapEntryAligned asserts the round-2 ask's
// second half: the risk row and the bootstrap test-plan entry must teach the
// corrected rule, so implementation PRs do not codify the ungated behavior.
func TestVerify_R3N1_RiskRowAndBootstrapEntryAligned(t *testing.T) {
	risks := kepSectionUnwrapped(t, "### Risks and Mitigations", "## Design Details")
	if !regexp.MustCompile(`(?i)request`).MatchString(risks) ||
		!regexp.MustCompile(`(?i)acknowledge`).MatchString(risks) {
		t.Errorf("R3-N1: risk row for the metadata-only/Ready window no longer names the request/acknowledgement handling")
	}
	plan := kepSectionUnwrapped(t, "### Test Plan", "## Appendix")
	if !regexp.MustCompile(`(?i)never-requested legacy child retains the\s+readiness fallback, but a missing condition cannot bypass an issued request`).MatchString(plan) {
		t.Errorf("R3-N1: bootstrap barrier test entry does not pin the corrected (request-subordinated) fallback")
	}
}

// ---------------------------------------------------------------------------
// R3-N1 exhibits: discrete-event simulation of the SPECIFIED round-3 rule
// (request/ack + generation + condition) vs a no-request control.
// ---------------------------------------------------------------------------

type r3Child struct {
	gen               int64
	statusObservedGen int64
	cond              barrierCond
	matchesUpdate     bool
	baseReady         bool
	request           string // "" = never requested (legacy fallback population)
	ack               string
	counter           int
	bumped            bool
	reconcileDue      int
	rolling           bool
	rollTicksLeft     int
	everRolled        bool
}

// ready models the round-3 groupSetChildReady as specified. requestGate=false
// is the control: the same rule minus the request/ack clause (round-2 rule +
// generation gate), included to prove the exhibit has teeth.
func (c *r3Child) ready(requestGate bool) bool {
	if !c.baseReady || !c.matchesUpdate {
		return false
	}
	if c.statusObservedGen < c.gen { // generation gate
		return false
	}
	if requestGate && c.request != "" && c.request != c.ack { // pending request
		return false
	}
	if c.cond.present {
		return c.cond.observedGen >= c.gen && c.cond.false_
	}
	return true // legacy fallback
}

type r3Params struct {
	nChildren      int
	maxUnavailable int
	childLatency   int
	rollDuration   int
	controllerUp   bool
	requestGate    bool
	metadataOnly   bool // bump writes request but does not change generation
	revert         bool // after first completion, revert template (A->B->A) and keep rolling
	ticks          int
}

type r3Result struct {
	maxConcurrentRolling int
	completed            bool
	completedTick        int
	bumpedTotal          int
	rolledTotal          int
	revertReadyWindow    int // ticks a reverted child read ready before its controller acked
}

func simulateR3(p r3Params) r3Result {
	children := make([]*r3Child, p.nChildren)
	for i := range children {
		children[i] = &r3Child{gen: 1, statusObservedGen: 1, baseReady: true, matchesUpdate: false, reconcileDue: -1}
	}
	res := r3Result{completedTick: -1}
	reverted := false
	for tick := 0; tick < p.ticks; tick++ {
		// --- set acts first (cheap reconcile wins the race) ---
		// in-flight = acted-on (still matching) children that have not completed;
		// an outdated child the set has not touched yet does not consume budget.
		inFlight := 0
		for _, c := range children {
			if c.matchesUpdate && !c.ready(p.requestGate) {
				inFlight++
			}
		}
		allReady := true
		for _, c := range children {
			if !c.ready(p.requestGate) {
				allReady = false
			}
		}
		if allReady {
			if !p.revert || reverted {
				res.completed = true
				res.completedTick = tick
				break
			}
			// A -> B -> A: revert the template. matchesUpdate flips false; the
			// next set action re-bumps with a FRESH request (counter++), never
			// reusing the old acknowledgement.
			reverted = true
			for _, c := range children {
				c.matchesUpdate = false
			}
		}
		if inFlight < p.maxUnavailable {
			// bump the highest outdated ordinal (descending rollout order)
			for i := p.nChildren - 1; i >= 0; i-- {
				c := children[i]
				if !c.matchesUpdate {
					// the bump: same Update carries spec/metadata/label + request
					c.bumped = true
					if !p.metadataOnly {
						c.gen++
					}
					c.matchesUpdate = true
					c.counter++
					c.request = itoa(c.counter)
					res.bumpedTotal++
					if p.controllerUp {
						c.reconcileDue = tick + p.childLatency
					}
					break
				}
			}
		}
		// --- child controllers act ---
		concurrent := 0
		for _, c := range children {
			if p.controllerUp && c.reconcileDue >= 0 && c.reconcileDue <= tick {
				c.reconcileDue = -1
				c.statusObservedGen = c.gen
				if !c.rolling {
					// first reconcile of the request: workloads stale -> True
					c.cond = barrierCond{present: true, false_: false, observedGen: c.gen}
					c.rolling = true
					c.rollTicksLeft = p.rollDuration
					c.everRolled = true
					res.rolledTotal++
				}
			}
			if c.rolling {
				c.rollTicksLeft--
				if c.rollTicksLeft == 0 {
					c.rolling = false
					c.cond = barrierCond{present: true, false_: true, observedGen: c.gen}
					// acknowledgement only after application + recheck pass
					c.ack = c.request
				}
			}
			if c.rolling {
				concurrent++
			}
			// measure the hazard window on reverted children: reads ready while
			// its controller has not acknowledged the fresh request
			if reverted && c.matchesUpdate && c.request != c.ack && c.ready(p.requestGate) {
				res.revertReadyWindow++
			}
		}
		if concurrent > res.maxConcurrentRolling {
			res.maxConcurrentRolling = concurrent
		}
	}
	return res
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [8]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}

// Exhibit A (spec change): 4 legacy children, maxUnavailable=1, latency L=1,
// roll R=3. The specified request/ack rule caps concurrency at the budgeted 1
// and completes; identical to the round-2 gated rule's safety, but now also
// covering the metadata-only window (exhibit C).
func TestVerify_R3N1Exhibit_RequestAckHoldsBudgetUnderLatency(t *testing.T) {
	res := simulateR3(r3Params{nChildren: 4, maxUnavailable: 1, childLatency: 1, rollDuration: 3, controllerUp: true, requestGate: true, ticks: 80})
	if res.maxConcurrentRolling != 1 {
		t.Errorf("R3 exhibit A broken: request/ack rule should cap concurrency at maxUnavailable=1, got %d", res.maxConcurrentRolling)
	}
	if !res.completed {
		t.Errorf("R3 exhibit A broken: request/ack rule should still complete the rollout")
	}
	t.Logf("request/ack rule: maxConcurrentRolling=%d (budget 1), completed=%v at tick %d, rolled=%d",
		res.maxConcurrentRolling, res.completed, res.completedTick, res.rolledTotal)
}

// Exhibit B (child controller down): the set must stall after the first bump
// and never declare completion — the round-2 silent-stale-completion exhibit
// under the new rule.
func TestVerify_R3N1Exhibit_RequestAckFailsSafeWhenChildControllerDown(t *testing.T) {
	res := simulateR3(r3Params{nChildren: 4, maxUnavailable: 1, childLatency: 1, rollDuration: 3, controllerUp: false, requestGate: true, ticks: 60})
	if res.completed {
		t.Errorf("R3 exhibit B broken: request/ack rule must NOT complete with the child controller down")
	}
	if res.bumpedTotal != 1 {
		t.Errorf("R3 exhibit B broken: request/ack rule should stall after the first bump, got bumped=%d", res.bumpedTotal)
	}
	t.Logf("request/ack rule, child controller down: completed=%v, bumped=%d (fail-safe stall)", res.completed, res.bumpedTotal)
}

// Exhibit C (metadata-only change, generation unchanged): the decisive case
// for the request mechanism. A user-annotation change (revision-relevant but
// generation-invariant) leaves gen, Ready=True, and a False@gen condition all
// VALID-looking; without the request gate the bumped child reads ready in the
// same tick and the set outruns the child controllers (control, must show the
// hazard); with the gate the child is held until ack.
func TestVerify_R3N1Exhibit_MetadataOnlyChangeHeldByRequestAlone(t *testing.T) {
	control := simulateR3(r3Params{nChildren: 4, maxUnavailable: 1, childLatency: 1, rollDuration: 3, controllerUp: true, requestGate: false, metadataOnly: true, ticks: 60})
	if control.maxConcurrentRolling < 2 {
		t.Errorf("R3 exhibit C control broken: without the request gate a metadata-only bump should admit >=2 concurrent rolls (gen/condition gates are all satisfied), got %d", control.maxConcurrentRolling)
	}
	gated := simulateR3(r3Params{nChildren: 4, maxUnavailable: 1, childLatency: 1, rollDuration: 3, controllerUp: true, requestGate: true, metadataOnly: true, ticks: 80})
	if gated.maxConcurrentRolling != 1 || !gated.completed {
		t.Errorf("R3 exhibit C broken: request gate should hold metadata-only rolls to budget 1 and complete, got concurrency=%d completed=%v", gated.maxConcurrentRolling, gated.completed)
	}
	t.Logf("metadata-only change: control concurrency=%d (hazard), request-gated concurrency=%d, completed=%v",
		control.maxConcurrentRolling, gated.maxConcurrentRolling, gated.completed)
}

// Exhibit D (A -> B -> A revert mid/after rollout): the revert must issue a
// FRESH request; the old acknowledgement must never validate the reverted
// child. revertReadyWindow counts ticks a reverted, unacked child reads ready.
func TestVerify_R3N1Exhibit_ABARevertNeverReusesAcknowledgement(t *testing.T) {
	res := simulateR3(r3Params{nChildren: 3, maxUnavailable: 1, childLatency: 1, rollDuration: 2, controllerUp: true, requestGate: true, revert: true, ticks: 120})
	if res.revertReadyWindow != 0 {
		t.Errorf("R3 exhibit D broken: reverted child read ready for %d ticks before ack — old acknowledgement reused", res.revertReadyWindow)
	}
	if !res.completed {
		t.Errorf("R3 exhibit D broken: A->B->A revert should still complete")
	}
	t.Logf("A->B->A: revertReadyWindow=%d ticks, completed=%v, bumped=%d rolled=%d", res.revertReadyWindow, res.completed, res.bumpedTotal, res.rolledTotal)
}

// ---------------------------------------------------------------------------
// Updated guards for round-2 fixed findings whose contract text moved.
// ---------------------------------------------------------------------------

// R3-F1 guard: the barrier design section survives restructuring. The round-2
// guard asserted the round-2 markers (**Producer.**/**Consumer.**); round 3
// restructured the section (Rollout condition / Request writer /
// Acknowledgement writer / Consumer and recovery). Assert substance.
func TestVerify_R3F1_BarrierSectionPresent(t *testing.T) {
	sec := kepSectionUnwrapped(t, "### Child Rollout Barrier", "### Status")
	for _, want := range []string{
		"**Rollout condition.**",      // producer
		"**Request writer.**",         // set-side request
		"**Acknowledgement writer.**", // child-side ack
		"**Consumer and recovery.**",  // consumer gate
		"groupSetChildReady",
		"apiReader", // uncached reads retained
	} {
		if !strings.Contains(sec, want) {
			t.Errorf("R3-F1 regression: Child Rollout Barrier section lacks %q", want)
		}
	}
	if !regexp.MustCompile(`(?i)before workload application`).MatchString(sec) {
		t.Errorf("R3-F1 regression: producer ordering invariant (status before workload application) no longer stated")
	}
}

// R3-F3/F4 guard: the enumerated system-key list (now 6 keys) still governs
// revision hashing, drift comparison, AND metadata-sync preservation, and
// syncRBGMetadata preserves child-side values for them.
func TestVerify_R3F3F4_SystemKeysEnumeratedForHashDriftSync(t *testing.T) {
	readme := unwrap(kepReadme(t))
	for _, key := range []string{
		"rbg.workloads.x-k8s.io/discovery-config-mode",
		"rbg.workloads.x-k8s.io/rollout-request",
		"conversion.workloads.x-k8s.io/v1alpha1-coordination",
		"conversion.workloads.x-k8s.io/v1alpha1-pod-group-policy",
		"conversion.workloads.x-k8s.io/v1alpha2-rollout-strategy",
		"conversion.workloads.x-k8s.io/v1alpha2-rollout-status",
	} {
		if !strings.Contains(readme, key) {
			t.Errorf("R3-F3/F4 regression: enumerated system-key list lost key %q", key)
		}
	}
	if !regexp.MustCompile(`(?i)enumerated system annotations from revision hashing and content drift comparison`).MatchString(readme) {
		t.Errorf("R3-F3 regression: exclusion scope (revision hashing + drift comparison) no longer stated")
	}
	if !regexp.MustCompile(`(?i)same key set identifies values to preserve during metadata sync`).MatchString(readme) {
		t.Errorf("R3-F3 regression: metadata-sync preservation scope no longer tied to the same key set")
	}
	if !regexp.MustCompile(`(?i)syncRBGMetadata.{0,80}preserves child-side values for the enumerated system keys`).MatchString(readme) {
		t.Errorf("R3-F3 regression: syncRBGMetadata no longer specified to preserve the enumerated keys")
	}
	if !regexp.MustCompile(`(?i)never copied from the template`).MatchString(readme) {
		t.Errorf("R3-F3 regression: rollout-request template-copy prohibition missing")
	}
}

// ---------------------------------------------------------------------------
// Round-3 findings (RED at head = reproduced).
// ---------------------------------------------------------------------------

// R3-M1 residual: (a) a conversion round-trip entry now exists but is scoped
// to request annotations/acknowledgements — the risk-row stash/restore surface
// (spec.rolloutStrategy + stashed v1alpha2-only rollout status fields) still
// has no test-plan entry; (b) the two round-1 Reviewer-C wording catches are
// still present ("higher ordinal is not released until its downstream..." —
// backwards: descending order means the LOWER ordinal waits for the HIGHER
// one's completion; and "recreated from the old template, not the current
// one" — contradicts the KEP's own current=old/update=new revision
// terminology).
func TestVerify_R3M1_ConversionStashAndWordingCatches(t *testing.T) {
	plan := kepSectionUnwrapped(t, "### Test Plan", "## Appendix")
	if !regexp.MustCompile(`(?i)round.?trip`).MatchString(plan) {
		t.Errorf("R3-M1: test plan lost the conversion round-trip entry")
	}
	if !regexp.MustCompile(`(?i)(stash|restore)[^\n]{0,120}(rolloutStrategy|rollout strategy)|(rolloutStrategy|rollout strategy)[^\n]{0,120}(stash|restore|round.?trip)`).MatchString(plan) {
		t.Errorf("R3-M1 reproduced: test plan still has no entry covering the v1alpha1 stash/restore of spec.rolloutStrategy / v1alpha2-only rollout status fields (the conversion-webhook risk row)")
	}
	if regexp.MustCompile(`(?i)higher ordinal is not released until its downstream`).MatchString(plan) {
		t.Errorf("R3-M1 wording catch 1 still present: in-place ordering sentence is backwards (descending order: a LOWER ordinal waits for the HIGHER ordinal's completion)")
	}
	if regexp.MustCompile(`recreated from the old template, not the current one`).MatchString(plan) {
		t.Errorf("R3-M1 wording catch 2 still present: contradicts the KEP's revision terminology (recovery uses the CURRENT revision's template, not the UPDATE one)")
	}
}

// R3-F9 (minor, new): the request writer is scoped to "an in-place child
// template update". The drift-repair path ("Overwriting the whole child spec
// reconciles the drift back to the template", incl. the one-shot upgrade
// rewrite) is also a set-initiated in-place child spec write, but the KEP
// never says whether it advances the request. If it does not, a repaired
// child whose workloads are rolling back to the template reads ready through
// the stale ack (request==ack, gen observed, Ready=True), and the set can
// spend budget elsewhere — the N1 window through a side door. Fix: one clause
// pinning that every set-initiated in-place child spec/metadata write (repair
// included) advances the request, or explicitly scoping repair out with a
// reason.
func TestVerify_R3F9_RequestWriterScopeCoversRepairWrites(t *testing.T) {
	readme := unwrap(kepReadme(t))
	scoped := regexp.MustCompile(`(?i)(every|any) set-initiated[^.\n]{0,120}(write|update)[^.\n]{0,120}request`).MatchString(readme) ||
		regexp.MustCompile(`(?i)(drift|repair|rewrite)[^.\n]{0,160}(advance|issue|bump)[^.\n]{0,40}request`).MatchString(readme) ||
		regexp.MustCompile(`(?i)request[^.\n]{0,80}(advance|issue)[^.\n]{0,80}(drift|repair|rewrite)`).MatchString(readme)
	if !scoped {
		t.Errorf("R3-F9 reproduced: request-writer scope does not state whether drift-repair / one-shot upgrade rewrite writes advance the rollout-request")
	}
}

// R3-F10 (minor, new): no stated fallback when an out-of-band replace-style
// write strips the rollout-request annotation from a child mid-rollout
// (kubectl replace / GitOps Replace=true on the child). The set-side
// checkpoint annotation got the same residual in round 2 (F5 nit). After a
// strip, request=="" -> the consumer falls back to the legacy rule while the
// child's workloads may still be rolling: the N1 window reopens for that
// child. Fix: one fallback line (e.g. treat present-ack-but-absent-request as
// not-ready, or have drift repair re-issue the request).
func TestVerify_R3F10_RequestStripFallbackStated(t *testing.T) {
	readme := unwrap(kepReadme(t))
	stated := regexp.MustCompile(`(?i)(strip|drop|remove|lost)[^.\n]{0,120}(rollout-request|request annotation)[^.\n]{0,160}(fallback|treat|re-issue|not ready)`).MatchString(readme) ||
		regexp.MustCompile(`(?i)(rollout-request|request annotation)[^.\n]{0,120}(strip|drop|remove|lost)[^.\n]{0,160}(fallback|treat|re-issue|not ready)`).MatchString(readme)
	if !stated {
		t.Errorf("R3-F10 reproduced: no fallback stated for a replace-style write stripping the rollout-request annotation from a child")
	}
}

// R3-Q3 (question, new): unversioned compatibility annotations "converge in
// place on settled, partition-held, and retained surge children, independently
// of revision changes and rollout budgets", and no request is issued on that
// path. Code-verified: the v1alpha1-coordination annotation drives the
// CoordinatedPolicy, which drives role replicas (handleCoordinationStrategies
// -> reconcileRoles). A coordination change therefore scales every child at
// once with no availability budget — while the SAME replica change via the
// v1alpha2 template is budget-paced ("In-place scaling is not a separate,
// unbounded update path"). The KEP should say why the unbudgeted legacy path
// is acceptable (e.g. coordination is capacity-following, treated like other
// external scaling inputs), or gate it.
func TestVerify_R3Q3_UnbudgetedConvergenceJustified(t *testing.T) {
	readme := unwrap(kepReadme(t))
	justified := regexp.MustCompile(`(?i)(budget)[^.\n]{0,160}(deliberate|intentional|capacity-following|external (scaling )?input|accept)`).MatchString(readme) ||
		regexp.MustCompile(`(?i)(converge|convergence)[^.\n]{0,160}(budget)[^.\n]{0,160}(deliberate|intentional|because|safe)`).MatchString(readme)
	if !justified {
		t.Errorf("R3-Q3 reproduced: unbudgeted in-place convergence of compatibility annotations (incl. replica-driving v1alpha1-coordination) is stated without any justification or availability consequence")
	}
}
