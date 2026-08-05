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

// zz_verify_pr400_doc_claims_test.go — VERIFICATION HARNESS, not a product test.
//
// Part of docs/verification/pr400-coordinated-policy-doc/ (review of
// https://github.com/sgl-project/rbg/pull/400, head ab396534).
//
// PR #400 is a *documentation* PR adding
// doc/best-practice/{zh,en}/07-configuring-coordinated-policy{,-guide}.md.
// Each test below pins one documentation assertion against the real behaviour of
// pkg/coordination/coordinationscaling, so the divergence is reproducible.
//
// ============================ POLARITY: canary ==============================
// EVERY test in this file is a BUG-CANARY. It asserts the CURRENT (implemented)
// behaviour, which CONTRADICTS what PR #400's docs claim. They therefore PASS on
// the code under review.
//
// After a fix lands these tests FLIP TO RED. That is the expected, designed
// signal — not a regression in the harness. When one flips, decide the fix
// direction first, then act:
//
//   * Fix direction "change the DOCS" (docs are wrong, implementation is right):
//     the behaviour did not change, so a flip means something else moved.
//     Investigate; the canary should have stayed green.
//   * Fix direction "change the IMPLEMENTATION" (behaviour was wrong):
//     invert the assertion — promote the doc's claimed value to the expected
//     value and re-label the test `POLARITY: contract`.
//
// See docs/verification/pr400-coordinated-policy-doc/README.md for the
// observed-vs-expected table and the per-finding fix sketches.
// ============================================================================

package coordinationscaling

import (
	"fmt"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/intstr"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// newPolicyRule builds a CoordinatedPolicyRule for the given roles with the
// given scaling maxSkew / progression. A nil maxSkew means "omit the field".
func newPolicyRule(roles []string, maxSkew *intstr.IntOrString,
	progression workloadsv1alpha2.ScalingProgression) *workloadsv1alpha2.CoordinatedPolicyRule {
	return &workloadsv1alpha2.CoordinatedPolicyRule{
		Name:  "verify-pr400",
		Roles: roles,
		Strategy: workloadsv1alpha2.CoordinatedPolicyStrategy{
			Scaling: &workloadsv1alpha2.ScalingCoordinationStrategy{
				MaxSkew:     maxSkew,
				Progression: progression,
			},
		},
	}
}

func strSkew(s string) *intstr.IntOrString { v := intstr.FromString(s); return &v }
func intSkew(i int32) *intstr.IntOrString  { v := intstr.FromInt32(i); return &v }

// simulateBatches drives CalculateTargetReplicas repeatedly, feeding each
// round's output back in as the next round's CurrentReplicas, and marking every
// replica scheduled+ready so progression gating never stalls the simulation.
// It returns the observed per-batch {role: replicas} snapshots (batch 0 = the
// initial state) and stops when a round produces no change or maxRounds is hit.
func simulateBatches(t *testing.T, scaler *CoordinationScaler,
	desired map[string]int32, start map[string]int32, maxRounds int) []map[string]int32 {
	t.Helper()

	cur := make(map[string]int32, len(start))
	for k, v := range start {
		cur[k] = v
	}
	history := []map[string]int32{copyMap(cur)}

	for round := 0; round < maxRounds; round++ {
		states := make(map[string]RoleScalingState, len(desired))
		for role, d := range desired {
			states[role] = RoleScalingState{
				RoleName:        role,
				DesiredReplicas: d,
				CurrentReplicas: cur[role],
				// fully scheduled and ready => progression gate always open
				ScheduledReplicas: cur[role],
				ReadyReplicas:     cur[role],
			}
		}
		next, err := scaler.CalculateTargetReplicas(states)
		if err != nil {
			t.Fatalf("round %d: CalculateTargetReplicas returned error: %v", round, err)
		}
		if mapsEqual(cur, next) {
			break // converged
		}
		cur = copyMap(next)
		history = append(history, copyMap(cur))
	}
	return history
}

func copyMap(m map[string]int32) map[string]int32 {
	out := make(map[string]int32, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func mapsEqual(a, b map[string]int32) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// fmtBatch renders a batch snapshot deterministically, e.g. "{decode:2 prefill:1}".
func fmtBatch(m map[string]int32, order []string) string {
	parts := make([]string, 0, len(order))
	for _, k := range order {
		parts = append(parts, fmt.Sprintf("%s:%d", k, m[k]))
	}
	return "{" + strings.Join(parts, " ") + "}"
}

func fmtHistory(h []map[string]int32, order []string) string {
	parts := make([]string, 0, len(h))
	for _, b := range h {
		parts = append(parts, fmtBatch(b, order))
	}
	return strings.Join(parts, " -> ")
}

// ---------------------------------------------------------------------------
// F1 (blocker): scaling.maxSkew does NOT accept an absolute number
// ---------------------------------------------------------------------------

// TestVerifyPR400_F1_ScalingMaxSkewRejectsAbsoluteValue
//
// POLARITY: canary
//
// DOC CLAIM (zh:158 / en:165, parameter table for strategy.scaling.maxSkew):
//
//	"可以是绝对值（如 2）或百分比（如 "10%"）"
//	"Can be an absolute number (e.g. 2) or a percentage (e.g. "10%")"
//
// The Go doc comment on ScalingCoordinationStrategy.MaxSkew
// (api/workloads/v1alpha2/coordinatedpolicy_types.go:86-87) and the generated
// CRD description make the same claim.
//
// ACTUAL: parsePercentage() requires a trailing '%'. An absolute value makes
// NewCoordinationScalerFromPolicy fail with
// `failed to parse maxSkew: percentage string must end with '%'`. That error is
// returned all the way up through Reconcile (`return ctrl.Result{}, err`), so a
// single absolute maxSkew ABORTS THE WHOLE RBG RECONCILE — it does not merely
// fall back to a default. The CRD does not catch it either
// (x-kubernetes-int-or-string: true) — see
// scripts/l2-crd-accepts-absolute-maxskew.sh for the L2 evidence.
//
// NOTE ON AMBIGUITY: because the API doc comment AND the CRD description also
// promise absolute-value support, the "correct" resolution is genuinely open —
// the fix may be to teach the implementation absolute values (e.g. via
// intstr.GetScaledValueFromIntOrPercent) OR to correct the docs + comment +
// CRD description. Hence canary rather than contract.
//
// AFTER A FIX: if absolute values become supported, this test FLIPS TO RED.
// Invert it (absolute values must be accepted and scale correctly) and re-label
// it POLARITY: contract. If instead the docs/comment were corrected, this test
// stays green and needs no change.
func TestVerifyPR400_F1_ScalingMaxSkewRejectsAbsoluteValue(t *testing.T) {
	cases := []struct {
		name string
		skew *intstr.IntOrString
		// docClaim: what PR #400's parameter table says happens
		docClaimAccepted bool
		// observed: what the implementation actually does today
		observedAccepted bool
		observedErrSub   string
	}{
		{
			name:             "absolute 2 (the exact example in the doc table)",
			skew:             intSkew(2),
			docClaimAccepted: true,
			observedAccepted: false,
			observedErrSub:   "percentage string must end with '%'",
		},
		{
			name:             "absolute 0",
			skew:             intSkew(0),
			docClaimAccepted: true,
			observedAccepted: false,
			observedErrSub:   "percentage string must end with '%'",
		},
		{
			name:             "absolute 5 (the example in the Go doc comment)",
			skew:             intSkew(5),
			docClaimAccepted: true,
			observedAccepted: false,
			observedErrSub:   "percentage string must end with '%'",
		},
		{
			name:             "string without percent sign",
			skew:             strSkew("2"),
			docClaimAccepted: true,
			observedAccepted: false,
			observedErrSub:   "percentage string must end with '%'",
		},
		{
			name:             "percentage 10% (works, per doc)",
			skew:             strSkew("10%"),
			docClaimAccepted: true,
			observedAccepted: true,
		},
		{
			name:             "omitted (defaults to 100%)",
			skew:             nil,
			docClaimAccepted: true,
			observedAccepted: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rule := newPolicyRule([]string{"prefill", "decode"}, tc.skew,
				workloadsv1alpha2.OrderScheduledProgression)
			scaler, err := NewCoordinationScalerFromPolicy(rule)

			gotAccepted := err == nil
			if gotAccepted != tc.observedAccepted {
				t.Fatalf("CANARY FLIPPED: accepted=%v, canary expected accepted=%v (err=%v).\n"+
					"This means maxSkew parsing behaviour CHANGED. If absolute values are now "+
					"supported, invert this test to a contract test (see file header).",
					gotAccepted, tc.observedAccepted, err)
			}

			if !tc.observedAccepted {
				if err == nil || !strings.Contains(err.Error(), tc.observedErrSub) {
					t.Fatalf("expected error containing %q, got %v", tc.observedErrSub, err)
				}
				t.Logf("OBSERVED: maxSkew=%v REJECTED with %q  (DOC CLAIMS: accepted)",
					tc.skew, err)
				if tc.docClaimAccepted {
					t.Log("DIVERGENCE: doc/api-comment/CRD-description all promise absolute-value " +
						"support; implementation requires a '%' suffix.")
				}
				return
			}
			if scaler == nil {
				t.Fatalf("expected non-nil scaler for maxSkew=%v", tc.skew)
			}
			t.Logf("OBSERVED: maxSkew=%v accepted, parsed maxSkew fraction = %v",
				tc.skew, scaler.maxSkew)
		})
	}
}

// TestVerifyPR400_F1_AbsoluteMaxSkewErrorReachesCaller
//
// POLARITY: canary
//
// Shows the blast radius of F1: the error is not swallowed or defaulted. Any
// caller constructing a scaler from a policy with an absolute maxSkew gets a
// hard error and a nil scaler, which the RBG reconciler surfaces as a failed
// Reconcile (requeued with error) for the ENTIRE RoleBasedGroup — including
// roles not covered by the coordinated policy.
//
// AFTER A FIX: flips red if absolute values become valid. See file header.
func TestVerifyPR400_F1_AbsoluteMaxSkewErrorReachesCaller(t *testing.T) {
	rule := newPolicyRule([]string{"prefill", "decode"}, intSkew(2),
		workloadsv1alpha2.OrderScheduledProgression)

	scaler, err := NewCoordinationScalerFromPolicy(rule)
	if err == nil {
		t.Fatalf("CANARY FLIPPED: absolute maxSkew=2 is now accepted (scaler=%+v). "+
			"Invert to a contract test asserting absolute values scale correctly.", scaler)
	}
	if scaler != nil {
		t.Fatalf("expected nil scaler alongside the error, got %+v", scaler)
	}
	// The wrapping matters: it is what the reconciler logs/returns.
	if !strings.Contains(err.Error(), "failed to parse maxSkew") {
		t.Fatalf("expected the error to be wrapped as 'failed to parse maxSkew', got %v", err)
	}
	t.Logf("OBSERVED: NewCoordinationScalerFromPolicy -> (nil, %q); "+
		"the RBG Reconcile returns this error and aborts reconciliation of the whole RBG.", err)
}

// ---------------------------------------------------------------------------
// F2 (major): coordinated scaling is NOT limited to first deployment
// ---------------------------------------------------------------------------

// TestVerifyPR400_F2_ScalingThrottlesScaleUpFromSteadyState
//
// POLARITY: canary
//
// DOC CLAIM (zh:86 & zh:94, en:90 & en:99):
//
//	"scaling 仅在首次部署阶段生效，部署完成后不影响后续的弹性伸缩行为"
//	"协作伸缩仅影响首次部署阶段的副本创建速度，不影响线上运行时的弹性伸缩行为。首次部署完成后，该策略不再生效。"
//	(EN: "scaling only takes effect during initial deployment and does not affect
//	subsequent autoscaling behaviour")
//
// ACTUAL: there is no "first deployment" gate anywhere in the code path.
// CalculateTargetReplicas only compares current/desired ratios, so a scale-up
// from a fully-converged steady state (e.g. an HPA or a RoleBasedGroupScalingAdapter
// raising replicas) is throttled exactly the same way as the initial rollout.
//
// Concrete case exercised below (the one measured during review): steady state
// prefill 5/5 + decode 10/10, then desired is raised to prefill 20 / decode 40
// with maxSkew 10%. The doc implies both roles jump straight to their new
// targets; the implementation returns {prefill:7, decode:14}.
//
// AFTER A FIX: if a genuine first-deployment gate is added, this flips red.
// Invert it (steady-state scale-up must reach the new target in one step) and
// re-label POLARITY: contract. If instead the docs are corrected to say scaling
// applies to all scaling events, this stays green.
func TestVerifyPR400_F2_ScalingThrottlesScaleUpFromSteadyState(t *testing.T) {
	const (
		prefill = "prefill"
		decode  = "decode"
	)
	order := []string{prefill, decode}

	rule := newPolicyRule([]string{prefill, decode}, strSkew("10%"),
		workloadsv1alpha2.OrderScheduledProgression)
	scaler, err := NewCoordinationScalerFromPolicy(rule)
	if err != nil {
		t.Fatalf("unexpected error building scaler: %v", err)
	}

	cases := []struct {
		name    string
		desired map[string]int32
		current map[string]int32
		// docClaim: per the doc, a post-deployment scaling event is unthrottled,
		// i.e. the target equals desired immediately.
		docClaimTarget map[string]int32
		// observedTarget: what the implementation actually returns.
		observedTarget map[string]int32
	}{
		{
			name:           "steady state 5/5 + 10/10, desired raised to 20/40 (scale UP)",
			desired:        map[string]int32{prefill: 20, decode: 40},
			current:        map[string]int32{prefill: 5, decode: 10},
			docClaimTarget: map[string]int32{prefill: 20, decode: 40},
			observedTarget: map[string]int32{prefill: 7, decode: 14},
		},
		{
			name:           "steady state 4/4 + 2/2, desired raised to 8/4 (scale UP)",
			desired:        map[string]int32{prefill: 8, decode: 4},
			current:        map[string]int32{prefill: 4, decode: 2},
			docClaimTarget: map[string]int32{prefill: 8, decode: 4},
			// observedTarget filled in from the first measured run (see
			// results/l1-scaler.txt); do NOT hand-compute this.
			observedTarget: map[string]int32{prefill: 5, decode: 3},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			states := make(map[string]RoleScalingState, len(tc.desired))
			for role, d := range tc.desired {
				states[role] = RoleScalingState{
					RoleName:          role,
					DesiredReplicas:   d,
					CurrentReplicas:   tc.current[role],
					ScheduledReplicas: tc.current[role],
					ReadyReplicas:     tc.current[role],
				}
			}
			got, err := scaler.CalculateTargetReplicas(states)
			if err != nil {
				t.Fatalf("CalculateTargetReplicas: %v", err)
			}

			t.Logf("current=%s desired=%s", fmtBatch(tc.current, order), fmtBatch(tc.desired, order))
			t.Logf("DOC CLAIMS (scaling inactive after first deployment): %s",
				fmtBatch(tc.docClaimTarget, order))
			t.Logf("OBSERVED (throttled all the same):                    %s", fmtBatch(got, order))

			if mapsEqual(got, tc.docClaimTarget) {
				t.Fatalf("CANARY FLIPPED: steady-state scale-up is no longer throttled "+
					"(got %s == doc claim). A first-deployment gate appears to have been added; "+
					"invert this test to a contract test.", fmtBatch(got, order))
			}
			if !mapsEqual(got, tc.observedTarget) {
				t.Fatalf("CANARY DRIFTED: expected observed %s, got %s. Throttling still happens "+
					"but the arithmetic changed; re-baseline this canary.",
					fmtBatch(tc.observedTarget, order), fmtBatch(got, order))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// F4 (major): the batch-2 walkthrough in the doc body is arithmetically wrong
// ---------------------------------------------------------------------------

// TestVerifyPR400_F4_Batch2WalkthroughIsWrong
//
// POLARITY: canary
//
// DOC CLAIM (zh:169-172 / en:176-179, the "协作伸缩过程（maxSkew: 10%）" table):
// with prefill desired 5, decode desired 10, maxSkew "10%":
//
//	batch 1: prefill 0 -> 1 (20%), decode 0 -> 1 (10%)   [CORRECT]
//	batch 2: prefill 1 -> 2 (40%), decode 1 -> 3 (30%)   [WRONG]
//
// ACTUAL batch 2 is {prefill:1, decode:2}. prefill stays at 1 because its
// progress (1/5 = 20%) already sits at minProgress(10%) + maxSkew(10%) = 20%,
// so the `rp.progress >= maxAllowedProgress` branch keeps it pinned. decode
// advances to ceil(0.20 * 10) = 2, not 3.
//
// AFTER A FIX: this flips red only if the scaling arithmetic itself changes.
// The likely resolution is correcting the doc table, in which case this canary
// stays green and becomes the authoritative reference for the corrected table.
func TestVerifyPR400_F4_Batch2WalkthroughIsWrong(t *testing.T) {
	const (
		prefill = "prefill"
		decode  = "decode"
	)
	order := []string{prefill, decode}
	desired := map[string]int32{prefill: 5, decode: 10}

	rule := newPolicyRule([]string{prefill, decode}, strSkew("10%"),
		workloadsv1alpha2.OrderScheduledProgression)
	scaler, err := NewCoordinationScalerFromPolicy(rule)
	if err != nil {
		t.Fatalf("unexpected error building scaler: %v", err)
	}

	history := simulateBatches(t, scaler, desired, map[string]int32{prefill: 0, decode: 0}, 30)
	t.Logf("desired=%s maxSkew=10%%", fmtBatch(desired, order))
	t.Logf("OBSERVED batch sequence: %s", fmtHistory(history, order))

	steps := []struct {
		batch        int // index into history
		docClaim     map[string]int32
		observed     map[string]int32
		docIsCorrect bool
		docCitation  string
	}{
		{
			batch:        1,
			docClaim:     map[string]int32{prefill: 1, decode: 1},
			observed:     map[string]int32{prefill: 1, decode: 1},
			docIsCorrect: true,
			docCitation:  "zh:170 / en:177 (batch 1)",
		},
		{
			batch:        2,
			docClaim:     map[string]int32{prefill: 2, decode: 3},
			observed:     map[string]int32{prefill: 1, decode: 2},
			docIsCorrect: false,
			docCitation:  "zh:171 / en:178 (batch 2)",
		},
	}

	for _, st := range steps {
		if st.batch >= len(history) {
			t.Fatalf("CANARY DRIFTED: simulation produced only %d batches, need batch %d. "+
				"Sequence: %s", len(history)-1, st.batch, fmtHistory(history, order))
		}
		got := history[st.batch]
		t.Logf("batch %d  DOC CLAIMS %s (%s)  |  OBSERVED %s  -> doc %s",
			st.batch, fmtBatch(st.docClaim, order), st.docCitation,
			fmtBatch(got, order),
			map[bool]string{true: "CORRECT", false: "WRONG"}[st.docIsCorrect])

		if !mapsEqual(got, st.observed) {
			t.Fatalf("CANARY FLIPPED/DRIFTED at batch %d: expected observed %s, got %s. "+
				"The scaling arithmetic changed; re-baseline (and if it now matches the doc "+
				"claim %s, invert this test to a contract test).",
				st.batch, fmtBatch(st.observed, order), fmtBatch(got, order),
				fmtBatch(st.docClaim, order))
		}
		if st.docIsCorrect && !mapsEqual(got, st.docClaim) {
			t.Fatalf("batch %d was supposed to match the doc but did not: doc=%s observed=%s",
				st.batch, fmtBatch(st.docClaim, order), fmtBatch(got, order))
		}
		if !st.docIsCorrect && mapsEqual(got, st.docClaim) {
			t.Fatalf("CANARY FLIPPED at batch %d: observed %s now MATCHES the doc claim. "+
				"Invert this test to a contract test.", st.batch, fmtBatch(got, order))
		}
	}
}

// ---------------------------------------------------------------------------
// F5 (major): the guide's "1 decode + 2 prefill per batch" cadence is wrong
// ---------------------------------------------------------------------------

// TestVerifyPR400_F5_GuideBatchCadenceIsWrong
//
// POLARITY: canary
//
// DOC CLAIM (guide zh:103 / en:105, "预期输出" for the coordinated-scaling-demo):
//
//	"每次创建 1 decode 和 2 prefill"
//	("1 decode and 2 prefill are created each time")
//
// The guide's own manifest (guide zh:40-41, 51, 68) is prefill replicas 4,
// decode replicas 2, maxSkew "25%", progression OrderReady.
//
// ACTUAL cadence for that exact configuration:
//
//	{prefill:0 decode:0} -> {1,1} -> {2,1} -> {3,2} -> {4,2}  (converged)
//
// No batch ever adds 1 decode AND 2 prefill together. The largest single-batch
// prefill delta is 1.
//
// AFTER A FIX: most likely the guide text is corrected, leaving this canary
// green as the reference cadence. It flips red only if the scaling arithmetic
// changes — then re-baseline or invert per the file header.
func TestVerifyPR400_F5_GuideBatchCadenceIsWrong(t *testing.T) {
	const (
		prefill = "prefill"
		decode  = "decode"
	)
	order := []string{prefill, decode}
	desired := map[string]int32{prefill: 4, decode: 2}

	// Exactly the guide's manifest: maxSkew "25%", progression OrderReady.
	rule := newPolicyRule([]string{prefill, decode}, strSkew("25%"),
		workloadsv1alpha2.OrderReadyProgression)
	scaler, err := NewCoordinationScalerFromPolicy(rule)
	if err != nil {
		t.Fatalf("unexpected error building scaler: %v", err)
	}

	history := simulateBatches(t, scaler, desired, map[string]int32{prefill: 0, decode: 0}, 30)

	wantSequence := []map[string]int32{
		{prefill: 0, decode: 0},
		{prefill: 1, decode: 1},
		{prefill: 2, decode: 1},
		{prefill: 3, decode: 2},
		{prefill: 4, decode: 2},
	}

	t.Logf("guide manifest: prefill=4 decode=2 maxSkew=25%% progression=OrderReady")
	t.Logf("DOC CLAIMS (guide zh:103 / en:105): each batch creates 1 decode + 2 prefill")
	t.Logf("OBSERVED batch sequence:            %s", fmtHistory(history, order))

	// Compare the measured sequence against the recorded baseline. The baseline
	// was captured from a real run (results/l1-scaler.txt) under this harness's
	// feeding model (every replica marked scheduled+ready each round), so it is
	// only meaningful for that model — see simulateBatches.
	if len(history) != len(wantSequence) {
		t.Fatalf("CANARY DRIFTED: expected %d snapshots, got %d (%s)",
			len(wantSequence), len(history), fmtHistory(history, order))
	}
	for i := range wantSequence {
		if !mapsEqual(history[i], wantSequence[i]) {
			t.Fatalf("CANARY DRIFTED at batch %d: expected %s, got %s (full: %s)",
				i, fmtBatch(wantSequence[i], order), fmtBatch(history[i], order),
				fmtHistory(history, order))
		}
	}

	// The core assertion: no batch matches the documented "+1 decode, +2 prefill".
	for i := 1; i < len(history); i++ {
		dPrefill := history[i][prefill] - history[i-1][prefill]
		dDecode := history[i][decode] - history[i-1][decode]
		t.Logf("  batch %d delta: prefill +%d, decode +%d", i, dPrefill, dDecode)
		if dPrefill == 2 && dDecode == 1 {
			t.Fatalf("CANARY FLIPPED: batch %d actually adds 1 decode + 2 prefill, matching the "+
				"guide's claim. Invert this test to a contract test.", i)
		}
	}
	t.Log("DIVERGENCE: no batch adds 1 decode + 2 prefill; max prefill delta per batch is 1.")
}

// ---------------------------------------------------------------------------
// F6 (minor): progression has no CRD default; omitting it != OrderScheduled
// ---------------------------------------------------------------------------

// TestVerifyPR400_F6_OmittedProgressionIsNotOrderScheduled
//
// POLARITY: canary
//
// DOC CLAIM (zh:159 / en:166, parameter table):
//
//	strategy.scaling.progression — default value `OrderScheduled`
//
// ACTUAL: ScalingCoordinationStrategy.Progression carries only `+optional` and
// `+kubebuilder:validation:Enum={OrderScheduled,OrderReady}` — no
// `+kubebuilder:default`. The generated CRD confirms no `default:` key. So an
// omitted progression is persisted and read back as the empty string "".
//
// canProceedToNextBatch()'s switch only has cases for OrderScheduled and
// OrderReady, so "" matches NEITHER and the batch gate is SKIPPED ENTIRELY.
// That is observably different from OrderScheduled, which blocks a batch while
// ScheduledReplicas < CurrentReplicas.
//
// getProgressionType() does return OrderScheduledProgression as a fallback, but
// only when Strategy.Scaling is nil — i.e. never on the path where a user wrote
// a scaling block and merely omitted progression.
//
// AFTER A FIX: if `+kubebuilder:default=OrderScheduled` is added (or the switch
// gets a default case), this flips red. Invert it (omitted progression must
// behave exactly like OrderScheduled) and re-label POLARITY: contract. If the
// doc's default column is corrected instead, this stays green.
func TestVerifyPR400_F6_OmittedProgressionIsNotOrderScheduled(t *testing.T) {
	const (
		prefill = "prefill"
		decode  = "decode"
	)
	order := []string{prefill, decode}

	// A state where the batch gate MATTERS: prefill has 2 replicas but only 1 is
	// scheduled, so OrderScheduled must hold the line.
	states := map[string]RoleScalingState{
		prefill: {
			RoleName:          prefill,
			DesiredReplicas:   5,
			CurrentReplicas:   2,
			ScheduledReplicas: 1, // <- one replica still Pending
			ReadyReplicas:     1,
		},
		decode: {
			RoleName:          decode,
			DesiredReplicas:   10,
			CurrentReplicas:   2,
			ScheduledReplicas: 2,
			ReadyReplicas:     2,
		},
	}

	cases := []struct {
		name        string
		progression workloadsv1alpha2.ScalingProgression
		// gateHolds: true => targets == current (batch gate blocked progress)
		observedGateHolds bool
	}{
		{
			name:              "explicit OrderScheduled (gate blocks: 1 of 2 prefill unscheduled)",
			progression:       workloadsv1alpha2.OrderScheduledProgression,
			observedGateHolds: true,
		},
		{
			name:              "explicit OrderReady (gate blocks: 1 of 2 prefill not ready)",
			progression:       workloadsv1alpha2.OrderReadyProgression,
			observedGateHolds: true,
		},
		{
			name:              `omitted -> "" (NO CRD default; switch matches no case; gate SKIPPED)`,
			progression:       workloadsv1alpha2.ScalingProgression(""),
			observedGateHolds: false,
		},
	}

	results := map[string]map[string]int32{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rule := newPolicyRule([]string{prefill, decode}, strSkew("10%"), tc.progression)
			scaler, err := NewCoordinationScalerFromPolicy(rule)
			if err != nil {
				t.Fatalf("unexpected error building scaler: %v", err)
			}
			// Sanity: getProgressionType must echo back exactly what was set — it
			// does NOT substitute a default when Strategy.Scaling is non-nil.
			if got := scaler.getProgressionType(); got != tc.progression {
				t.Fatalf("CANARY FLIPPED: getProgressionType() returned %q for a stored value of %q "+
					"— a default now appears to be applied. Invert this test to a contract test.",
					got, tc.progression)
			}

			got, err := scaler.CalculateTargetReplicas(states)
			if err != nil {
				t.Fatalf("CalculateTargetReplicas: %v", err)
			}
			results[string(tc.progression)] = got

			current := map[string]int32{prefill: 2, decode: 2}
			gateHeld := mapsEqual(got, current)
			t.Logf("progression=%-16q -> targets %s  (gate held = %v)",
				tc.progression, fmtBatch(got, order), gateHeld)

			if gateHeld != tc.observedGateHolds {
				t.Fatalf("CANARY FLIPPED: progression=%q gateHeld=%v, canary expected %v "+
					"(targets %s). If an omitted progression now defaults to OrderScheduled, "+
					"invert this test to a contract test.",
					tc.progression, gateHeld, tc.observedGateHolds, fmtBatch(got, order))
			}
		})
	}

	// The headline divergence: "" and OrderScheduled produce DIFFERENT targets,
	// so the documented default is not what an omitted field actually does.
	empty := results[""]
	scheduled := results[string(workloadsv1alpha2.OrderScheduledProgression)]
	if empty == nil || scheduled == nil {
		t.Fatalf("subtests did not record both results: %+v", results)
	}
	t.Log("DOC CLAIMS: omitted progression defaults to OrderScheduled (zh:159 / en:166)")
	t.Logf("OBSERVED: omitted(\"\") -> %s   vs   OrderScheduled -> %s",
		fmtBatch(empty, order), fmtBatch(scheduled, order))
	if mapsEqual(empty, scheduled) {
		t.Fatalf("CANARY FLIPPED: omitted progression now behaves identically to OrderScheduled. "+
			"Invert this test to a contract test (%s == %s).",
			fmtBatch(empty, order), fmtBatch(scheduled, order))
	}
	t.Log("DIVERGENCE: an omitted progression SKIPS the batch gate entirely; " +
		"OrderScheduled holds it. No +kubebuilder:default exists on the field.")
}
