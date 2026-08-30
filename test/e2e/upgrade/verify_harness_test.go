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

// This file is the reviewer's verification harness for PR #444. It is additive
// (production/test code under review is untouched) and proves the detector gaps
// found in stage ① by calling the real, package-internal detector functions with
// synthetic snapshots — no cluster required.
//
// Polarity:
//   - contract: asserts the INTENDED behavior; FAILS on the current PR code, PASSES once
//     the author wires in the missing detector. A red result *is* the reproduction.
//   - canary:   asserts the CURRENT (gap) behavior; PASSES on the current PR code and
//     FLIPS TO RED once the gap is closed — invert it then.
//
// Run: go test ./test/e2e/upgrade/ -run TestVerifyHarness -v
// (the -run filter keeps the ginkgo suite, which needs a live cluster, from starting.)

package upgrade

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

// onePodSnapshot builds a single-RBG / single-role / single-pod snapshot so a detector
// can be exercised on exactly the field under test.
func onePodSnapshot(rbg, role, pod string, facts PodFacts) map[string]RBGSnapshot {
	return map[string]RBGSnapshot{
		rbg: {
			Name: rbg,
			Roles: map[string]map[string]PodFacts{
				role: {pod: facts},
			},
		},
	}
}

func podFacts(uid, node string, restarts map[string]int32, labels map[string]string, phase corev1.PodPhase) PodFacts {
	return PodFacts{
		UID:            types.UID(uid),
		NodeName:       node,
		RestartCounts:  restarts,
		Labels:         labels,
		Phase:          phase,
	}
}

func findingCount(fs *findings) int { return len(fs.sections) }

// hasFinding reports whether any detector recorded a problem.
func hasFinding(fs *findings) bool { return findingCount(fs) > 0 }

// --- H1: settle detectors individually don't catch label drift (retained guard) ----
//
// F1 was FIXED on head 125a2267: specs.go's phase-3 settle no longer uses a fixed
// 3-detector window — it now loops on waitQuiesced (two samples settleDuration apart
// must agree) before the before/after comparison. So the misattribution-while-moving
// concern F1 raised is addressed by a different mechanism than the proposed
// checkPodMetadataStable wiring.
//
// This test is RETAINED as a regression guard, not a live finding: it documents that the
// three settle detectors individually still don't catch a pure label drift (so a future
// change that re-introduces a fixed-window settle without the full detector set would
// slip past). F1 is marked Fixed in the manifest and is NOT counted as a live finding.
func TestVerifyHarness_H1_SettleGuardMissesLabelChurn_Canary(t *testing.T) {
	before := onePodSnapshot("rbg", "role", "pod-0",
		podFacts("uid-1", "node-a", map[string]int32{"app": 0}, map[string]string{"ord": "0"}, corev1.PodRunning))
	// Only the label drifted; identity, node and restart count are unchanged.
	after := onePodSnapshot("rbg", "role", "pod-0",
		podFacts("uid-1", "node-a", map[string]int32{"app": 0}, map[string]string{"ord": "REWRITTEN"}, corev1.PodRunning))

	// (a) The settle guard as wired today: the three detectors specs.go calls.
	settle := &findings{}
	checkSameRBGSet(settle, before, after)
	checkNoPodChurn(settle, before, after)
	checkNoRestarts(settle, before, after)
	if hasFinding(settle) {
		t.Fatalf("settle guard reported a label drift (%v) — if checkPodMetadataStable is now "+
			"wired into the settle path, invert this canary into a contract test", settle.sections)
	}

	// (b) The detector that would catch it exists and works — proving the gap is one of
	// wiring, not of a missing capability.
	full := &findings{}
	checkPodMetadataStable(full, before, after)
	if !hasFinding(full) {
		t.Fatal("checkPodMetadataStable did not report a label drift it should catch — harness is stale")
	}
}

// --- H2: checkNoRestarts ignores containers present in `after` but absent in `before` (contract)
//
// checkNoRestarts iterates beforeFacts.RestartCounts and looks each up in after. A
// container that APPEARED in after (e.g. an upgrade-added sidecar) is never visited, so
// an in-place update that injects a container without replacing the pod is invisible.
//
// Contract: the intended behavior is that a newly-appeared container IS reported. On the
// current PR code this FAILS (nothing is reported) — that red result is the reproduction.
func TestVerifyHarness_H2_RestartsIgnoresNewContainer_Contract(t *testing.T) {
	before := onePodSnapshot("rbg", "role", "pod-0",
		podFacts("uid-1", "node-a", map[string]int32{"app": 0}, nil, corev1.PodRunning))
	// A sidecar the upgrade injected, restart-free, pod identity intact.
	after := onePodSnapshot("rbg", "role", "pod-0",
		podFacts("uid-1", "node-a", map[string]int32{"app": 0, "sidecar": 0}, nil, corev1.PodRunning))

	fs := &findings{}
	checkNoRestarts(fs, before, after)
	if !hasFinding(fs) {
		t.Fatalf("checkNoRestarts did not report a newly-appeared container sidecar; " +
			"it only iterates before's containers, so `after`-only containers are invisible")
	}
}

// --- H3: a pod flipping Phase Running->Failed is now reported (contract) ----------
//
// Round 2 canary (Phase was captured but never asserted) FLIPPED on the fix head
// (125a2267): a detector now compares Phase. Inverted to a contract that asserts the
// intended behavior — a Running->Failed flip under the same UID MUST be reported.
func TestVerifyHarness_H3_PhaseFlipReported_Contract(t *testing.T) {
	before := onePodSnapshot("rbg", "role", "pod-0",
		podFacts("uid-1", "node-a", map[string]int32{"app": 0}, map[string]string{"ord": "0"}, corev1.PodRunning))
	after := onePodSnapshot("rbg", "role", "pod-0",
		podFacts("uid-1", "node-a", map[string]int32{"app": 0}, map[string]string{"ord": "0"}, corev1.PodFailed))

	fs := &findings{}
	checkSameRBGSet(fs, before, after)
	checkNoPodChurn(fs, before, after)
	checkNoRestarts(fs, before, after)
	checkPodMetadataStable(fs, before, after)
	checkOwnersStable(fs, before, after, nil)
	checkNoRevisionExplosion(fs, before, after)
	checkStillReady(fs, before, after)
	if !hasFinding(fs) {
		t.Fatal("a Phase-only change (Running->Failed) was not reported — Phase is no longer " +
			"asserted; if this regressed, restore the Phase comparison")
	}
}

// --- H4: hasDefaultRestartDelays no longer folds an absent config (contract) ------
//
// Round 2 canary (absent field treated as matching default) FLIPPED on the fix head
// (125a2267): hasDefaultRestartDelays now requires both delay fields PRESENT and matching.
// Inverted to a contract asserting the intended behavior — an absent config must NOT be
// folded (a conversion bug that wrote one must surface, not hide behind the surviving
// restartPolicy string).
func TestVerifyHarness_H4_EmptyRestartConfigNotFolded_Contract(t *testing.T) {
	if hasDefaultRestartDelays(map[string]any{}) {
		t.Fatal("hasDefaultRestartDelays(no fields) returned true; an absent config is folded again " +
			"— if this regressed, restore the `!present` short-circuit")
	}
}

// --- H5: the mid-rollout fixture is excluded from every detector (canary) ---------
//
// specs.go puts the mid-rollout RBG into the `mutated` list and passes it as the
// skip set to BOTH the phase-3 settle comparison (`exclude(first, mutated...)`) and
// `runDetectors(..., mutated)`. So the only thing the suite asserts about the
// mid-rollout pods is countSurvivors — which checks only "is this UID still here",
// nothing about restart counts, labels, phase or owner refs.
//
// Consequence: if the upgrade disturbs a mid-rollout pod in any way OTHER than
// deleting-and-recreating it under a new UID (restart count bumped, label rewritten,
// phase flipped to Failed), countSurvivors still returns the partition count and no
// detector ever sees the pod — the suite goes green on a real regression.
//
// This test reproduces that silent pass end to end:
//   (a) countSurvivors reports the full partition despite the drift,
//   (b) the settle detectors, run with the mid-rollout RBG in the skip list exactly
//       as specs.go does, report nothing,
//   (c) the SAME detectors run WITHOUT the skip DO catch the drift — proving the gap
//       is the exclusion, not a missing capability.
//
// Canary: PASSES today (the mid-rollout drift is invisible); flips red once the
// suite stops blanket-excluding the mid-rollout fixture and runs the detectors on it
// (or adds a dedicated mid-rollout drift detector) — invert it then.
func TestVerifyHarness_H5_MidRolloutExcludedFromAllDetectors_Canary(t *testing.T) {
	const rbg = "midroll"
	before := onePodSnapshot(rbg, "role", "pod-0",
		podFacts("uid-1", "node-a", map[string]int32{"app": 0}, map[string]string{"ord": "0"}, corev1.PodRunning))
	// Same UID (survivor), but the upgrade bumped the app container's restart count
	// AND rewrote a label — the kind of disturbance countSurvivors cannot see.
	after := onePodSnapshot(rbg, "role", "pod-0",
		podFacts("uid-1", "node-a", map[string]int32{"app": 3}, map[string]string{"ord": "REWRITTEN"}, corev1.PodRunning))

	midRollPodsBefore := before[rbg].Roles["role"]
	midRollPodsAfter := after[rbg].Roles["role"]

	// (a) The partition check the suite DOES run: survives == partition despite drift.
	survivors := countSurvivors(midRollPodsBefore, midRollPodsAfter)
	if survivors != len(midRollPodsBefore) {
		t.Fatalf("countSurvivors saw %d survivors, want %d (the drift should be invisible to it)",
			survivors, len(midRollPodsBefore))
	}

	// (b) The phase-3 settle path exactly as specs.go wires it: exclude the mid-rollout
	// RBG from both samples, then the three settle detectors. They see nothing.
	quietFirst := exclude(before, rbg)
	quietAfter := exclude(after, rbg)
	settle := &findings{}
	checkSameRBGSet(settle, quietFirst, quietAfter)
	checkNoPodChurn(settle, quietFirst, quietAfter)
	checkNoRestarts(settle, quietFirst, quietAfter)
	if hasFinding(settle) {
		t.Fatalf("settle detectors reported something on excluded mid-rollout RBG (%v) — "+
			"if the exclusion is gone, invert this canary", settle.sections)
	}

	// (c) The drift IS detectable: the same detectors run on the un-excluded samples
	// catch it. (checkNoRestarts flags the bumped restart count; checkPodMetadataStable
	// the label.) This proves the gap is the blanket exclusion, not a blind detector.
	seen := &findings{}
	checkNoRestarts(seen, before, after)
	checkPodMetadataStable(seen, before, after)
	if !hasFinding(seen) {
		t.Fatal("the mid-rollout drift was not detectable even without exclusion — harness is stale")
	}
}

// --- H6: RBGSnapshot.Generation is now compared (contract) ------------------------
//
// Round 2 canary (Generation captured but never compared) FLIPPED on the fix head
// (125a2267): checkOwnersStable now compares afterSnap.Generation - beforeSnap.Generation.
// Inverted to a contract asserting the intended behavior — an RBG whose own .Generation
// increments across the upgrade (stored spec rewritten, the #433 mechanism) MUST be
// reported.
func TestVerifyHarness_H6_RBGGenerationCompared_Contract(t *testing.T) {
	const rbg = "rbg"
	base := onePodSnapshot(rbg, "role", "pod-0",
		podFacts("uid-1", "node-a", map[string]int32{"app": 0}, map[string]string{"ord": "0"}, corev1.PodRunning))

	before := map[string]RBGSnapshot{rbg: base[rbg]}
	afterSnap := base[rbg]
	afterSnap.Generation = 2
	after := map[string]RBGSnapshot{rbg: afterSnap}
	if before[rbg].Generation == after[rbg].Generation {
		t.Fatal("test setup: generation did not actually change")
	}

	fs := &findings{}
	checkSameRBGSet(fs, before, after)
	checkNoPodChurn(fs, before, after)
	checkNoRestarts(fs, before, after)
	checkPodMetadataStable(fs, before, after)
	checkOwnersStable(fs, before, after, nil)
	checkNoRevisionExplosion(fs, before, after)
	checkStillReady(fs, before, after)
	if !hasFinding(fs) {
		t.Fatal("a Generation-only change (0->2) was not reported — RBGSnapshot.Generation is " +
			"no longer compared; if this regressed, restore the generation check in checkOwnersStable")
	}
}

// --- H7: ownerSources now lists ScalingAdapter (contract) -------------------------
//
// Round 2 canary (ownerSources omitted ScalingAdapter) FLIPPED on the fix head
// (125a2267): the kind is now listed. Inverted to a contract asserting the intended
// behavior — a ScalingAdapter-backed RBG's owning workload MUST be captured so
// checkOwnersStable can see its UID/generation churn.
func TestVerifyHarness_H7_OwnerSourcesListsScalingAdapter_Contract(t *testing.T) {
	found := false
	for _, src := range ownerSources() {
		if strings.Contains(src.kind, "ScalingAdapter") {
			found = true
		}
	}
	if !found {
		t.Fatal("ownerSources does not list a ScalingAdapter kind — a ScalingAdapter-backed RBG's " +
			"owner is not captured; if this regressed, restore the ScalingAdapter list entry")
	}
}
