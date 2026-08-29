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

// --- H1: settle guard omits the metadata detector (contract for the gap) ----------
//
// specs.go's phase-3 settle guard runs only checkSameRBGSet + checkNoPodChurn +
// checkNoRestarts. A pod whose IDENTITY is intact but whose LABELS the controller is
// still rewriting passes that guard, so the suite cannot say "the controller is still
// moving things" — it falls through to the before-vs-after comparison and blames the
// upgrade. The detector that *would* catch it (checkPodMetadataStable) exists and works;
// it is simply not wired into the settle path.
//
// This is a canary: it PASSES today (the settle guard reports nothing for a pure label
// drift) and flips red once the author adds checkPodMetadataStable to the settle path.
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

// --- H3: PodFacts.Phase and OwnerUIDs are captured but never asserted on (canary) ----
//
// No detector compares pod Phase between snapshots. A pod that flips Running -> Failed
// without being deleted (same UID) is invisible to checkNoPodChurn; this exercises every
// non-framework detector on a snapshot that differs ONLY in Phase.
//
// Canary: PASSES today (Phase is not asserted anywhere); flips red once a Phase detector
// is added — invert it then.
func TestVerifyHarness_H3_PhaseNotAsserted_Canary(t *testing.T) {
	before := onePodSnapshot("rbg", "role", "pod-0",
		podFacts("uid-1", "node-a", map[string]int32{"app": 0}, map[string]string{"ord": "0"}, corev1.PodRunning))
	after := onePodSnapshot("rbg", "role", "pod-0",
		podFacts("uid-1", "node-a", map[string]int32{"app": 0}, map[string]string{"ord": "0"}, corev1.PodFailed))

	// Every detector the upgrade/settle paths call that does not need a live client.
	fs := &findings{}
	checkSameRBGSet(fs, before, after)
	checkNoPodChurn(fs, before, after)
	checkNoRestarts(fs, before, after)
	checkPodMetadataStable(fs, before, after)
	checkOwnersStable(fs, before, after, nil) // nil bumps = no recorded generation rewrites
	checkNoRevisionExplosion(fs, before, after)
	checkStillReady(fs, before, after)
	if hasFinding(fs) {
		t.Fatalf("a Phase-only change (Running->Failed) was reported (%v) — if Phase is now "+
			"asserted, invert this canary into a contract test", fs.sections)
	}
}

// --- H4: hasDefaultRestartDelays folds an empty/absent config (canary) --------------
//
// hasDefaultRestartDelays returns true when a delay field is ABSENT (treated as matching
// the default). The restartPolicyConfig fold then deletes restartPolicyConfig from the
// stored spec, so a conversion bug that writes an empty config is hidden behind the
// surviving restartPolicy string.
//
// Canary: PASSES today (absent == default == fold applies); flips red once the fix
// requires both fields to be PRESENT and matching.
func TestVerifyHarness_H4_EmptyRestartConfigFolds_Canary(t *testing.T) {
	// Both delay fields absent — the condition under which an empty config would be folded.
	if !hasDefaultRestartDelays(map[string]any{}) {
		t.Fatal("hasDefaultRestartDelays(no fields) returned false; the fold no longer applies to " +
			"an absent config — if the fix now requires fields to be present, invert this canary")
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

// --- H6: RBGSnapshot.Generation is captured but never compared (canary) ------------
//
// captureRBG records rbg.Generation into RBGSnapshot.Generation (snapshot.go:167),
// but that field is only ever written out in the dump (snapshot.go:1197) — no detector
// reads it. The owner-generation check in checkOwnersStable (snapshot.go:842) compares
// ownerFacts.Generation (the owning RoleInstanceSet/Deployment/etc.), a DIFFERENT field.
// So an RBG whose own .Generation increments across the upgrade — meaning the stored
// spec was rewritten, the exact mechanism behind the #433 revision-serialization
// spurious rollout — is never flagged.
//
// Canary: PASSES today (the RBG's own generation is not asserted anywhere); flips red
// once a detector compares RBGSnapshot.Generation before->after — invert it then.
func TestVerifyHarness_H6_RBGGenerationNeverCompared_Canary(t *testing.T) {
	const rbg = "rbg"
	base := onePodSnapshot(rbg, "role", "pod-0",
		podFacts("uid-1", "node-a", map[string]int32{"app": 0}, map[string]string{"ord": "0"}, corev1.PodRunning))

	// before: generation 1. after: everything identical except the RBG's own generation
	// bumped to 2 (stored spec rewritten). Pods, owners, UID, everything else unchanged.
	before := map[string]RBGSnapshot{rbg: base[rbg]}
	afterSnap := base[rbg]
	afterSnap.Generation = 2
	after := map[string]RBGSnapshot{rbg: afterSnap}
	if before[rbg].Generation == after[rbg].Generation {
		t.Fatal("test setup: generation did not actually change")
	}

	// Every non-framework detector. None reads RBGSnapshot.Generation.
	fs := &findings{}
	checkSameRBGSet(fs, before, after)
	checkNoPodChurn(fs, before, after)
	checkNoRestarts(fs, before, after)
	checkPodMetadataStable(fs, before, after)
	checkOwnersStable(fs, before, after, nil)
	checkNoRevisionExplosion(fs, before, after)
	checkStillReady(fs, before, after)
	if hasFinding(fs) {
		t.Fatalf("a Generation-only change (1->2) was reported (%v) — if RBGSnapshot.Generation "+
			"is now compared, invert this canary into a contract test", fs.sections)
	}
}

// --- H7: ownerSources omits ScalingAdapter (canary) -------------------------------
//
// ownerSources() returns the workload kinds an RBG can own and that the suite lists
// when it captures owners. ScalingAdapter is not among them, so an RBG backed by a
// ScalingAdapter has its owning workload never captured — its owner generation and
// UID are invisible to checkOwnersStable. This is a capture-path gap (the field is
// absent from the snapshot, so it cannot be diffed with a unit harness); this canary
// runs the real ownerSources() and asserts the omission, flipping red once the kind
// is added.
func TestVerifyHarness_H7_OwnerSourcesOmitsScalingAdapter_Canary(t *testing.T) {
	for _, src := range ownerSources() {
		if strings.Contains(src.kind, "ScalingAdapter") {
			t.Fatalf("ownerSources now lists %s — the capture-path gap is closed, "+
				"invert this canary into a contract test that a ScalingAdapter-backed RBG "+
				"has its owner captured", src.kind)
		}
	}
}
