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

// zz_verify_pr400_maxskew_test.go — VERIFICATION HARNESS, not a product test.
//
// Part of docs/verification/pr400-coordinated-policy-doc/ (review of
// https://github.com/sgl-project/rbg/pull/400, head ab396534).
//
// Covers finding F3: rollingUpdate.maxSkew integers are treated as PERCENTAGES,
// not as an absolute instance count, contradicting the guide added by PR #400.
//
// The symbol under test (calculateCoordinationUpdatedReplicasBound) is
// unexported, hence this test lives in package workloads.
//
// ============================ POLARITY: canary ==============================
// Asserts the CURRENT behaviour, which contradicts the doc. PASSES on the code
// under review; FLIPS TO RED once the base changes from the hardcoded 100 to the
// role's replica count. On a flip: if the fix direction was "change the
// IMPLEMENTATION", invert the assertions (integer maxSkew = instance count) and
// re-label POLARITY: contract. If the fix direction was "change the DOCS", this
// canary should have stayed green — investigate.
// ============================================================================

package workloads

import (
	"fmt"
	"testing"

	"k8s.io/apimachinery/pkg/util/intstr"
)

// TestVerifyPR400_F3_RollingUpdateMaxSkewIntegerIsPercentNotCount
//
// POLARITY: canary
//
// DOC CLAIM (guide zh:134 & zh:182 & zh:196, en:136 & en:184 & en:198):
//
//	rollingUpdate.maxSkew: 1
//	"两个角色之间的更新进度差异最多为 1 个实例"
//	"更新过程中，两个角色之间已更新实例数差异不超过 1"
//	(EN: "the update progress difference between the two roles is at most 1 instance")
//
// ACTUAL: internal/controller/workloads/rolebasedgroup_controller.go:1487 does
//
//	maxSkewPercent, _ := intstr.GetScaledValueFromIntOrPercent(&maxSkew, 100, true)
//
// The scaling base is the literal constant 100 — NOT the role's replica count.
// GetScaledValueFromIntOrPercent returns an Int value verbatim and scales a
// String percentage against the base, so with base=100 an integer `1` and the
// string `"1%"` are indistinguishable. maxSkew is therefore always a percentage
// of desired replicas; there is no way to express "at most 1 instance".
//
// With the guide's own numbers (4 prefill, 4 decode, maxSkew: 1) the tolerated
// window is 1% of 4 = 0.04 replicas, which rounds to 0 — i.e. LOCKSTEP, far
// stricter than the documented "at most 1 instance".
func TestVerifyPR400_F3_RollingUpdateMaxSkewIntegerIsPercentNotCount(t *testing.T) {
	// Every integer maxSkew must produce byte-identical bounds to its "N%" twin.
	// That equality IS the bug: it proves the integer is read as a percentage.
	pairs := []struct {
		intForm int32
		strForm string
	}{
		{1, "1%"},
		{10, "10%"},
		{25, "25%"},
		{100, "100%"},
	}

	// A spread of (refUpdated, refDesired, requestDesired) triples, including the
	// guide's own 4/4 topology and the doc body's 5/10 asymmetric one.
	shapes := []struct {
		refUpdated, refDesired, requestDesired int32
	}{
		{0, 4, 4},
		{1, 4, 4},
		{2, 4, 4},
		{3, 4, 4},
		{4, 4, 4},
		{1, 5, 10},
		{3, 5, 10},
		{7, 10, 5},
		{50, 100, 100},
	}

	for _, p := range pairs {
		t.Run(fmt.Sprintf("int_%d_equals_%s", p.intForm, p.strForm), func(t *testing.T) {
			iv := intstr.FromInt32(p.intForm)
			sv := intstr.FromString(p.strForm)
			for _, sh := range shapes {
				loI, hiI := calculateCoordinationUpdatedReplicasBound(
					iv, sh.refUpdated, sh.refDesired, sh.requestDesired)
				loS, hiS := calculateCoordinationUpdatedReplicasBound(
					sv, sh.refUpdated, sh.refDesired, sh.requestDesired)

				t.Logf("refUpdated=%d refDesired=%d requestDesired=%d :: maxSkew=%d -> [%d,%d] | maxSkew=%q -> [%d,%d]",
					sh.refUpdated, sh.refDesired, sh.requestDesired,
					p.intForm, loI, hiI, p.strForm, loS, hiS)

				if loI != loS || hiI != hiS {
					t.Fatalf("CANARY FLIPPED: maxSkew=%d gave [%d,%d] but maxSkew=%q gave [%d,%d] "+
						"(refUpdated=%d refDesired=%d requestDesired=%d). The integer form is no "+
						"longer treated as a percentage — the base is presumably now the role's "+
						"replica count. Invert this test to a contract test asserting an integer "+
						"maxSkew means an absolute instance count.",
						p.intForm, loI, hiI, p.strForm, loS, hiS,
						sh.refUpdated, sh.refDesired, sh.requestDesired)
				}
			}
		})
	}
	t.Logf("DIVERGENCE: integer and percentage maxSkew are indistinguishable, so " +
		"`rollingUpdate.maxSkew: 1` means 1%%, NOT '1 instance' as the guide states.")
}

// TestVerifyPR400_F3_GuideScenarioMaxSkew1IsLockstepNotOneInstance
//
// POLARITY: canary
//
// Quantifies the practical consequence for the guide's exact scenario
// (guide zh:128-136: prefill 4 replicas, decode 4 replicas, maxSkew: 1).
//
// The guide promises a window of +/- 1 instance. The implementation computes a
// window of +/- 1% of 4 replicas = +/- 0.04, which rounds to +/- 0 — the two
// roles are pinned to identical updated counts (lockstep). So a user following
// the guide gets a materially stricter rollout than documented.
//
// AFTER A FIX (base = role replicas): the window becomes exactly +/-1 and this
// FLIPS RED. Invert to a contract test asserting width == 1 on each side.
func TestVerifyPR400_F3_GuideScenarioMaxSkew1IsLockstepNotOneInstance(t *testing.T) {
	maxSkew := intstr.FromInt32(1) // exactly what guide zh:134 / en:136 shows
	const refDesired, requestDesired int32 = 4, 4

	for refUpdated := int32(0); refUpdated <= refDesired; refUpdated++ {
		lower, upper := calculateCoordinationUpdatedReplicasBound(
			maxSkew, refUpdated, refDesired, requestDesired)

		// Doc claim: the other role may sit anywhere within +/-1 of refUpdated.
		docLower := max32(refUpdated-1, 0)
		docUpper := min32(refUpdated+1, requestDesired)

		t.Logf("refUpdated=%d/%d :: DOC CLAIMS window [%d,%d] (+/-1 instance) | OBSERVED [%d,%d]",
			refUpdated, refDesired, docLower, docUpper, lower, upper)

		// Observed: the window collapses onto refUpdated itself (lockstep).
		if lower != refUpdated || upper != refUpdated {
			t.Fatalf("CANARY FLIPPED: expected lockstep window [%d,%d] for maxSkew=1 with "+
				"4 replicas, got [%d,%d]. If maxSkew=1 now means 1 instance, invert this to a "+
				"contract test asserting [%d,%d].",
				refUpdated, refUpdated, lower, upper, docLower, docUpper)
		}
		if lower == docLower && upper == docUpper && docLower != docUpper {
			t.Fatalf("CANARY FLIPPED: observed window [%d,%d] now matches the documented "+
				"+/-1-instance window. Invert this test to a contract test.", lower, upper)
		}
	}
	t.Logf("DIVERGENCE: guide's `maxSkew: 1` with 4 replicas yields a +/-0 (lockstep) window, " +
		"not the documented +/-1 instance.")
}

func max32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

func min32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}
