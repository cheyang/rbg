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

package sync

// Verification tests for suspected bugs raised in the review of PR #394
// ("feat: add exponential backoff for restart policy").
//
// These tests encode the INTENDED contract. On the PR head they are expected
// to FAIL — the failure is the reproduction/evidence that the bug is real.
// See docs/verification/pr394-restart-backoff/README.md.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// B1: calculateRestartDelay must never exceed the configured maxDelay cap, and
// must always return a positive value once the base delay is non-zero. The
// suspected bug is that int64 multiplication overflows before the cap check for
// restartCount >= ~59 (and the shift zeroes at >= 64), so the cap is bypassed
// and the function returns a non-positive / garbage value — which the caller
// (checkRestartBackoff) treats as "no backoff", silently disabling backoff for
// a long-running crashloop.
func TestRestartBackoffVerify_B1_Overflow(t *testing.T) {
	const base int32 = 30
	const max int32 = 600

	// NOTE (round 3): head 245884dd corrects the formula to base*2^(restartCount-1)
	// (the B5 fix) and now guards int64 overflow BEFORE the shift
	// (`rc>=62 || base > 1<<(62-rc)`). The whole window that used to overflow to 0 —
	// restartCount 59/60/61 and >=62 — now saturates at maxDelay. Under the corrected
	// formula the cap is first reached at count 6 (30*2^5=960>600); count 5 (=480) is
	// the last sub-cap value, so the "== max" contract below starts at 6. These counts
	// pin the previously-broken overflow region as fixed.
	counts := []int32{6, 27, 50, 58, 59, 60, 61, 62, 63, 64, 100}

	for _, rc := range counts {
		got := calculateRestartDelay(base, max, rc)
		t.Logf("calculateRestartDelay(base=%d, max=%d, restartCount=%d) = %d", base, max, rc, got)

		// Contract 1: result is bounded by the cap and strictly positive.
		assert.Greater(t, got, int32(0),
			"restartCount=%d: delay must stay positive (0 or negative disables backoff)", rc)
		assert.LessOrEqual(t, got, max,
			"restartCount=%d: delay must never exceed maxDelay cap", rc)

		// Contract 2: once the exponential term has passed the cap, the result
		// must equal the cap exactly.
		assert.Equal(t, max, got,
			"restartCount=%d: delay should be capped at maxDelay=%d", rc, max)
	}
}

// B1 (no-cap variant): when maxDelaySeconds == 0 the doc says "unbounded", but
// the function still must not overflow into a non-positive value; it should
// saturate at int32 max. Verify the guard holds for large counts.
func TestRestartBackoffVerify_B1_NoCapOverflow(t *testing.T) {
	const base int32 = 10
	for _, rc := range []int32{58, 59, 60, 63, 64, 100} {
		got := calculateRestartDelay(base, 0, rc)
		t.Logf("calculateRestartDelay(base=%d, max=0, restartCount=%d) = %d", base, rc, got)
		assert.Greater(t, got, int32(0),
			"restartCount=%d: uncapped delay must saturate positive, not overflow to <=0", rc)
	}
}

// B5: the realized first backoff must equal base, not 2*base.
//
// POLARITY (round 3): this began as a bug-canary asserting the buggy 2*base
// behavior. Head 245884dd fixed the off-by-one by switching the formula to
// base*2^(restartCount-1), so calculateRestartDelay(base,max,1) == base. The
// canary has therefore FLIPPED and is promoted to a CONTRACT test: it now asserts
// the intended "first realized delay == base" and will go red again if the
// off-by-one regresses. updateRestartTracking still increments to >=1 before the
// first backoff check, so the smallest count ever passed at runtime is 1 — the
// fix makes that produce exactly base rather than 2*base.
func TestRestartBackoffVerify_B5_OffByOne(t *testing.T) {
	const base int32 = 30
	const max int32 = 600

	// Fresh instance, first restart trigger.
	inst := &workloadsv1alpha2.RoleInstance{}
	assert.Equal(t, int32(0), inst.Status.RestartCount, "precondition: fresh count is 0")
	assert.Nil(t, inst.Status.LastRestartTime)

	updateRestartTracking(inst)

	// After the first trigger the persisted count is 1 (not 0) and a timestamp exists.
	assert.Equal(t, int32(1), inst.Status.RestartCount,
		"updateRestartTracking increments to 1 before any backoff check")
	assert.NotNil(t, inst.Status.LastRestartTime)

	// checkRestartBackoff only computes a delay when LastRestartTime != nil, which
	// is only true after >=1 trigger. So the smallest count ever passed to
	// calculateRestartDelay at runtime is 1.
	firstRealizedDelay := calculateRestartDelay(base, max, inst.Status.RestartCount)
	t.Logf("first realized backoff delay = %ds (base=%d)", firstRealizedDelay, base)

	// Fixed contract: the first realized delay equals base (30s), matching the
	// documented "first retry after base". The pre-fix value was 2*base (60s).
	assert.Equal(t, base, firstRealizedDelay,
		"first realized delay must equal base (fixed); 2*base would be the old off-by-one")
	assert.NotEqual(t, 2*base, firstRealizedDelay,
		"first realized delay must no longer be 2*base=%d (the off-by-one)", 2*base)
}

// B5 corollary: at runtime the count passed to calculateRestartDelay is always
// >=1 (updateRestartTracking increments before the first check). Head 245884dd
// added an explicit `restartCount <= 0 => 0` guard, so the count==0 branch now
// returns 0 rather than base — harmless because it is never reached at runtime.
func TestRestartBackoffVerify_B5_Count0IsUnreachableAtRuntime(t *testing.T) {
	const base int32 = 30
	// Round 3: count 0 now returns 0 (explicit guard), not base.
	assert.Equal(t, int32(0), calculateRestartDelay(base, 600, 0))

	// ... but updateRestartTracking guarantees the persisted count is >=1 the
	// moment a restart is recorded, so checkRestartBackoff (which requires
	// LastRestartTime != nil) never observes count 0 together with a timestamp.
	inst := &workloadsv1alpha2.RoleInstance{
		Status: workloadsv1alpha2.RoleInstanceStatus{
			// Simulate a prior stable period so the reset branch runs.
			RestartCount:    3,
			LastRestartTime: &metav1.Time{Time: time.Now().Add(-24 * time.Hour)}, // ~1 day ago
		},
	}
	updateRestartTracking(inst)
	assert.GreaterOrEqual(t, inst.Status.RestartCount, int32(1),
		"count is always >=1 after a trigger, even right after a stable-period reset")
}

// B4: a negative baseDelaySeconds (accepted because RoleInstanceSpec lacks a
// Minimum=0 CRD validation, unlike the parent patterns) produces a negative
// delay, and checkRestartBackoff then reports "no wait" — silently bypassing
// backoff. This test drives the actual checkRestartBackoff decision path.
func TestRestartBackoffVerify_B4_NegativeDelayBypass(t *testing.T) {
	c := &realControl{} // apiReader nil => uses in-object status directly

	newInstance := func(base int32) *workloadsv1alpha2.RoleInstance {
		now := metav1.Now()
		return &workloadsv1alpha2.RoleInstance{
			ObjectMeta: metav1.ObjectMeta{Name: "ri", Namespace: "default", Generation: 1},
			Spec: workloadsv1alpha2.RoleInstanceSpec{
				// PR #394 refactored the backoff knobs into RestartPolicyConfig
				// (RoleInstanceSpec.BaseDelaySeconds/MaxDelaySeconds removed).
				RestartPolicy: workloadsv1alpha2.RestartPolicyConfig{
					Type:             workloadsv1alpha2.RecreateRoleInstanceOnPodRestart,
					BaseDelaySeconds: ptr.To(base),
					MaxDelaySeconds:  ptr.To(int32(600)),
				},
				Components: []workloadsv1alpha2.RoleInstanceComponent{
					{Name: "main", Size: ptr.To(int32(1))},
				},
			},
			Status: workloadsv1alpha2.RoleInstanceStatus{
				ObservedGeneration: 1,
				CurrentRevision:    "rev-1",
				UpdateRevision:     "rev-1",
				// A recent restart so the backoff window would still be open
				// for a sane positive delay.
				RestartCount:    1,
				LastRestartTime: &now,
				Conditions: []workloadsv1alpha2.RoleInstanceCondition{
					{Type: workloadsv1alpha2.RoleInstanceReady, Status: corev1.ConditionTrue},
				},
			},
		}
	}
	failedPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pod-0"},
		Status:     corev1.PodStatus{Phase: corev1.PodFailed},
	}

	// Control: a sane positive base delays (backoff window open) => remaining > 0.
	// Round 3 (245884dd) changed the signature to checkRestartBackoff(instance,
	// fresh, pods, inactive); fresh==nil here (unit test has no apiReader), so the
	// in-object status is used directly.
	posRemaining := c.checkRestartBackoff(newInstance(30), nil, []*corev1.Pod{failedPod}, nil)
	assert.Greater(t, posRemaining, time.Duration(0),
		"sanity: with base=30 and a just-now restart, backoff must be pending")

	// Negative base: the delay math goes negative, so checkRestartBackoff reports
	// "no wait" — backoff is bypassed even though a delay was configured.
	negRemaining := c.checkRestartBackoff(newInstance(-30), nil, []*corev1.Pod{failedPod}, nil)
	t.Logf("checkRestartBackoff with base=-30 => %v (expected >0 if validated)", negRemaining)
	assert.Greater(t, negRemaining, time.Duration(0),
		"negative baseDelaySeconds should NOT bypass backoff; a negative delay disables it")
}
