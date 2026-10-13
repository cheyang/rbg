/*
Round-2 additions to the PR #491 verification harness (reviewer B / Codex).

Delta reviewed: 719851a65959ebc91157ce90651aaa911c26f30c (round-1 head)

	-> b03a7e67cf681599fb1c69537be2a2078ec35cf0 (round-2 head).

The round-2 head adds a "Child Rollout Barrier" design section, a scale
subresource interaction paragraph, an enumerated controller-owned-key exclusion
list, an appendix pinning five round-1 questions, and new test-plan entries.

Test polarity:
  - TestVerify_R2<Id>_* / updated TestVerify_F1 guards : contract tests for findings FIXED by the
    round-2 revision. They PASS at b03a7e6 and FAIL at 719851a (verified by
    running the suite grafted on both heads), so they double as regression
    guards for future rounds.
  - TestVerify_R2M1_* / TestVerify_R2N1_* : contract tests for findings STILL
    OPEN at b03a7e6. Red at head = reproduction.
  - TestVerify_N1Exhibit_* : deterministic discrete-event simulation of the
    consumer rule specified in the KEP vs the generation-gated variant. Always
    green; it is the mechanism exhibit for finding N1, not a polarity test.
*/
package pr491kep

import (
	"regexp"
	"strings"
	"testing"
)

func kepSection(t *testing.T, from, to string) string {
	t.Helper()
	readme := kepReadme(t)
	i := strings.Index(readme, from)
	if i < 0 {
		t.Fatalf("KEP section marker %q not found", from)
	}
	rest := readme[i:]
	j := strings.Index(rest, to)
	if j < 0 {
		t.Fatalf("KEP section end marker %q not found after %q", to, from)
	}
	return rest[:j]
}

// ---------------------------------------------------------------------------
// Guards for findings fixed by the round-2 revision (PASS at b03a7e6).
// ---------------------------------------------------------------------------

// R2-F2 (round-1 consensus F2, major): scale-subresource interaction of the
// redefined status.replicas is documented and HPA-during-surge is scoped out.
func TestVerify_R2F2_ScaleSubresourceInteractionDocumented(t *testing.T) {
	sec := kepSection(t, "### Status", "### Test Plan")
	if !strings.Contains(sec, "Scale subresource interaction") {
		t.Errorf("R2-F2 regression: Status section no longer documents the scale subresource interaction")
	}
	if !regexp.MustCompile(`(?i)HPA`).MatchString(sec) ||
		!regexp.MustCompile(`(?i)not a supported\s+configuration`).MatchString(sec) {
		t.Errorf("R2-F2 regression: HPA-during-surge is no longer explicitly scoped out")
	}
}

// R2-F3/F4 (round-1 consensus F3 major + F4 minor): one enumerated
// controller-owned-key list applied to revision hashing, drift comparison, and
// metadata sync, containing the keys the codebase actually injects, and
// syncRBGMetadata carrying them over.
func TestVerify_R2F3F4_ControllerOwnedKeysEnumeratedAndSynced(t *testing.T) {
	readme := kepReadme(t)
	if !regexp.MustCompile(`(?i)enumerated once and reused\s+for revision hashing, drift comparison, and metadata sync`).MatchString(readme) {
		t.Errorf("R2-F3/F4 regression: exclusion list is no longer enumerated once for hash+drift+sync")
	}
	for _, key := range []string{
		"rbg.workloads.x-k8s.io/discovery-config-mode",
		"conversion.workloads.x-k8s.io/v1alpha1-coordination",
		"conversion.workloads.x-k8s.io/v1alpha2-rollout-strategy",
		"conversion.workloads.x-k8s.io/v1alpha2-rollout-status",
	} {
		if !strings.Contains(readme, key) {
			t.Errorf("R2-F3/F4 regression: enumerated exclusion list lost key %q", key)
		}
	}
	if !regexp.MustCompile(`(?s)syncRBGMetadata.{0,220}?carries over the same enumerated controller-owned keys`).MatchString(readme) {
		t.Errorf("R2-F3 regression: syncRBGMetadata no longer specified to carry over the enumerated keys")
	}
}

// R2-F5 (round-1 codex F2, minor): restart-safe checkpoint storage named.
func TestVerify_R2F5_CheckpointStorageNamed(t *testing.T) {
	if !regexp.MustCompile(`groupset-observed-replicas`).MatchString(kepReadme(t)) ||
		!regexp.MustCompile(`(?i)stored as an\s+annotation`).MatchString(kepReadme(t)) {
		t.Errorf("R2-F5 regression: checkpoint storage location no longer named")
	}
}

// R2-F6 (minor): numeric revision field declared informational-only.
func TestVerify_R2F6_NumericRevisionInformationalOnly(t *testing.T) {
	readme := kepReadme(t)
	if !regexp.MustCompile(`(?i)informational only`).MatchString(readme) ||
		!regexp.MustCompile(`name\s+equality, not by the numeric field`).MatchString(readme) {
		t.Errorf("R2-F6 regression: numeric ControllerRevision field no longer declared informational-only")
	}
}

// R2-F7 (minor): percentage-partition resolution timing pinned.
func TestVerify_R2F7_PartitionResolutionTimingPinned(t *testing.T) {
	if !regexp.MustCompile(`(?i)resolved against the\s+current ` + "`spec.replicas`" + `\s+at reconcile time`).MatchString(kepReadme(t)) {
		t.Errorf("R2-F7 regression: partition resolution timing no longer pinned")
	}
}

// R2-Q1 (question): admission verdict for explicit maxUnavailable: 0 under
// InPlaceUpdate — the webhook rejects it.
func TestVerify_R2Q1_AdmissionVerdictForExplicitZero(t *testing.T) {
	readme := kepReadme(t)
	if !regexp.MustCompile(`(?i)webhook rejects it`).MatchString(readme) {
		t.Errorf("R2-Q1 regression: admission verdict for explicit maxUnavailable: 0 under InPlaceUpdate no longer stated")
	}
}

// R2-Q2 (question): operator-downgrade stance written down.
func TestVerify_R2Q2_DowngradeStanceStated(t *testing.T) {
	if !regexp.MustCompile(`(?i)Operator-downgrade stance`).MatchString(kepReadme(t)) {
		t.Errorf("R2-Q2 regression: operator-downgrade stance no longer stated")
	}
}

// ---------------------------------------------------------------------------
// Still-open findings (FAIL at b03a7e6 = reproduction).
// ---------------------------------------------------------------------------

// R2-M1 residual (minor): the test plan still has no entry covering the
// v1alpha1 full-object round-trip stash/restore of spec.rolloutStrategy and
// the rollout status fields — the conversion-webhook risk row has no test.
// Polarity: contract. RED at head = reproduction.
func TestVerify_R2M1_V1alpha1RoundTripCoveredByTestPlan(t *testing.T) {
	plan := kepSection(t, "### Test Plan", "## Appendix")
	if !regexp.MustCompile(`(?i)round.?trip`).MatchString(plan) {
		t.Errorf("R2-M1 reproduced: test plan has no v1alpha1 round-trip stash/restore entry " +
			"(the conversion-webhook risk row is untested)")
	}
}

// R2-N1 (major, new this round): the consumer rule's absent-condition
// exception is not generation-gated. As specified, "absent -> ready" also
// fires in the window between the set bumping a legacy child's spec
// (generation N -> N+1) and the child controller's first reconcile of N+1
// (which would publish RollingUpdateInProgress=True per the producer
// invariant). In that window the set observes: matches-update (it wrote the
// label itself), stale Ready=True, barrier absent -> spends the next budget
// slot against a child that has not started rolling — the stale-capacity
// failure the barrier exists to prevent, on exactly the legacy-bootstrap
// population the exception serves. Fix: treat absent as ready only when the
// child's status.observedGeneration >= child.generation (the field exists and
// is stamped every reconcile), or demand the condition's presence.
// Polarity: contract. RED at head = reproduction.
func TestVerify_R2N1_AbsentBarrierExceptionIsGenerationGated(t *testing.T) {
	sec := kepSection(t, "### Child Rollout Barrier", "### Status")
	gated := regexp.MustCompile(`(?i)absent[^,.\n]{0,80}(and|provided|only (when|if)|if)[^.\n]{0,140}(observed ?generation|ObservedGeneration)`)
	absentNotReady := regexp.MustCompile(`(?i)absent (condition )?(is treated as|counts as|means)[^.\n]{0,40}not ready`)
	demandPresence := regexp.MustCompile(`(?i)(requires?|demands?) the condition'?s? presence`)
	if !gated.MatchString(sec) && !absentNotReady.MatchString(sec) && !demandPresence.MatchString(sec) {
		t.Errorf("R2-N1 reproduced: the consumer rule treats an absent RollingUpdateInProgress condition as ready " +
			"without gating on the child's status.observedGeneration, so a just-bumped legacy child " +
			"(barrier not yet stamped) reads as ready and the set can spend the next budget slot on stale capacity")
	}
}

// ---------------------------------------------------------------------------
// N1 mechanism exhibit: discrete-event simulation of the specified consumer
// rule vs the generation-gated variant. Deterministic, no cluster.
//
// Model (parameters documented per scenario):
//   - N legacy children (predate the feature): gen=1, statusObservedGen=1
//     (stamped by the old controller's updateRBGStatus), barrier condition
//     absent, Ready=True (stale across the spec bump — the F1 premise),
//     matchesUpdate=false.
//   - The set rolls in descending ordinal order with maxUnavailable=1: it may
//     bump child i (write new spec + groupset-revision label, gen -> gen+1)
//     only when #{bumped children not ready} < 1.
//   - The set re-reconciles every tick (its own child writes re-trigger it via
//     Owns(&RoleBasedGroup{})); it acts before the child controllers within a
//     tick (its reconcile only re-lists children; the child's does per-role
//     workload reads and a status write — the heavy side of the race).
//   - A child controller reconciles a bumped child L ticks after the bump:
//     stamps statusObservedGen=gen and RollingUpdateInProgress=True@gen
//     (producer invariant), rolls for R ticks, then publishes False@gen.
// ---------------------------------------------------------------------------

type barrierCond struct {
	present     bool
	false_      bool // status==False when true, True otherwise
	observedGen int64
}

type simChild struct {
	gen               int64
	statusObservedGen int64
	cond              barrierCond
	matchesUpdate     bool
	baseReady         bool
	bumped            bool
	reconcileDue      int // tick at which the child controller next reconciles; -1 = none
	rolling           bool
	rollTicksLeft     int
	everRolled        bool
}

type simParams struct {
	nChildren      int
	maxUnavailable int
	childLatency   int  // ticks between set bump and child controller reconcile (L)
	rollDuration   int  // ticks a child roll takes once started (R)
	controllerUp   bool // false = child RBG controller down (never stamps)
	gatedAbsence   bool // true = proposed fix: absent requires statusObservedGen >= gen
	ticks          int
}

type simResult struct {
	maxConcurrentRolling int
	completed            bool
	completedTick        int
	bumpedTotal          int
	rolledTotal          int
}

func (c *simChild) ready(gated bool) bool {
	if !c.baseReady || !c.matchesUpdate {
		return false
	}
	if !c.cond.present {
		// absent exception: ungated (KEP as written) vs generation-gated (fix)
		return !gated || c.statusObservedGen >= c.gen
	}
	return c.cond.observedGen >= c.gen && c.cond.false_
}

func simulate(p simParams) simResult {
	children := make([]*simChild, p.nChildren)
	for i := range children {
		children[i] = &simChild{gen: 1, statusObservedGen: 1, baseReady: true, reconcileDue: -1}
	}
	res := simResult{completedTick: -1}
	for tick := 0; tick < p.ticks; tick++ {
		// --- set acts first within the tick (cheap reconcile wins the race) ---
		inFlight := 0
		for _, c := range children {
			if c.bumped && !c.ready(p.gatedAbsence) {
				inFlight++
			}
		}
		allReady := true
		for _, c := range children {
			if !c.ready(p.gatedAbsence) {
				allReady = false
			}
		}
		if allReady {
			res.completed = true
			res.completedTick = tick
			break
		}
		if inFlight < p.maxUnavailable {
			// bump the highest unbumped ordinal (descending rollout order)
			for i := p.nChildren - 1; i >= 0; i-- {
				if !children[i].bumped {
					c := children[i]
					c.bumped = true
					c.gen++
					c.matchesUpdate = true // set writes the revision label in the same update
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
			if p.controllerUp && c.reconcileDue == tick {
				c.reconcileDue = -1
				c.statusObservedGen = c.gen
				if !c.rolling && !c.everRolled {
					// first reconcile of the bumped generation: workloads still
					// old -> producer invariant publishes True, roll starts
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
				}
			}
			if c.rolling {
				concurrent++
			}
		}
		if concurrent > res.maxConcurrentRolling {
			res.maxConcurrentRolling = concurrent
		}
	}
	return res
}

// Exhibit A: normal controller latency (L=1), roll duration R=3, 4 legacy
// children, maxUnavailable=1. The specified rule lets the set outrun the child
// controllers and hold 2 children mid-roll at once; the generation-gated rule
// keeps concurrency at the budgeted 1.
func TestVerify_N1Exhibit_SpecifiedRuleAdmitsConcurrentRolls(t *testing.T) {
	base := simParams{nChildren: 4, maxUnavailable: 1, childLatency: 1, rollDuration: 3, controllerUp: true, ticks: 60}

	specified := simulate(base)
	if specified.maxConcurrentRolling < 2 {
		t.Errorf("exhibit A broken: specified rule should admit >=2 concurrent child rolls with maxUnavailable=1, got %d", specified.maxConcurrentRolling)
	}
	t.Logf("specified rule: maxConcurrentRolling=%d (budget 1), completed=%v at tick %d",
		specified.maxConcurrentRolling, specified.completed, specified.completedTick)

	fixed := simulate(simParams{nChildren: 4, maxUnavailable: 1, childLatency: 1, rollDuration: 3, controllerUp: true, gatedAbsence: true, ticks: 60})
	if fixed.maxConcurrentRolling != 1 {
		t.Errorf("exhibit A broken: generation-gated rule should cap concurrency at maxUnavailable=1, got %d", fixed.maxConcurrentRolling)
	}
	if !fixed.completed {
		t.Errorf("exhibit A broken: generation-gated rule should still complete the rollout")
	}
	t.Logf("gated rule: maxConcurrentRolling=%d, completed=%v at tick %d", fixed.maxConcurrentRolling, fixed.completed, fixed.completedTick)
}

// Exhibit B: child RBG controller down (crashloop / RBAC broken) while the set
// rolls. The specified rule marches through every ordinal — absent stays
// "ready" — and declares RolloutComplete with zero children actually rolled
// (silent stale completion). The gated rule blocks after the first bump.
func TestVerify_N1Exhibit_SpecifiedRuleAdmitsSilentStaleCompletion(t *testing.T) {
	specified := simulate(simParams{nChildren: 4, maxUnavailable: 1, childLatency: 1, rollDuration: 3, controllerUp: false, ticks: 60})
	if !specified.completed {
		t.Errorf("exhibit B broken: specified rule should (wrongly) complete with the child controller down")
	}
	if specified.rolledTotal != 0 || specified.bumpedTotal != 4 {
		t.Errorf("exhibit B broken: specified rule should bump all 4 children while 0 actually roll, got bumped=%d rolled=%d", specified.bumpedTotal, specified.rolledTotal)
	}
	t.Logf("specified rule, child controller down: rollout declared complete with bumped=%d rolled=%d", specified.bumpedTotal, specified.rolledTotal)

	gated := simulate(simParams{nChildren: 4, maxUnavailable: 1, childLatency: 1, rollDuration: 3, controllerUp: false, gatedAbsence: true, ticks: 60})
	if gated.completed {
		t.Errorf("exhibit B broken: generation-gated rule must NOT complete with the child controller down")
	}
	if gated.bumpedTotal != 1 {
		t.Errorf("exhibit B broken: generation-gated rule should stall after the first bump, got bumped=%d", gated.bumpedTotal)
	}
	t.Logf("gated rule, child controller down: completed=%v, bumped=%d (fail-safe stall)", gated.completed, gated.bumpedTotal)
}
