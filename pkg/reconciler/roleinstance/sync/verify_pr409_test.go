/*
Copyright 2026 The RBG Authors.
Licensed under the Apache License, Version 2.0 (the "License");
*/

// Verification harness for sgl-project/rbg PR #409.
// ADDITIVE ONLY — no production code is modified by this file.
//
// Polarity legend (see docs/verification/pr409-restart-backoff-improvements):
//   CANARY   = asserts the CURRENT (buggy) behavior. PASSES on PR head;
//              FLIPS TO FAIL when the documented fix lands (then invert it).
//   CONTRACT = asserts the INTENDED behavior. FAILS on PR head; PASSES when fixed.
package sync

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// recreateInstance builds a RoleInstance with RestartPolicy=Recreate, base=30/max=600,
// Ready=True, generation matched, revisions converged — the "steady state" baseline.
func vpr409_recreateInstance() *workloadsv1alpha2.RoleInstance {
	inst := &workloadsv1alpha2.RoleInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "vpr409", Namespace: "default"},
		Spec: workloadsv1alpha2.RoleInstanceSpec{
			RestartPolicy: workloadsv1alpha2.RestartPolicyConfig{
				Type:            workloadsv1alpha2.RecreateRoleInstanceOnPodRestart,
				BaseDelaySeconds: ptr.To(int32(30)),
				MaxDelaySeconds:  ptr.To(int32(600)),
			},
			Components: []workloadsv1alpha2.RoleInstanceComponent{{Name: "main", Size: ptr.To(int32(1))}},
		},
		Status: workloadsv1alpha2.RoleInstanceStatus{
			ObservedGeneration: 1,
			CurrentRevision:    "rev-1",
			UpdateRevision:     "rev-1",
			Conditions: []workloadsv1alpha2.RoleInstanceCondition{
				{Type: workloadsv1alpha2.RoleInstanceReady, Status: corev1.ConditionTrue},
			},
		},
	}
	inst.Generation = 1
	return inst
}

func vpr409_failedPod() *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "vpr409-pod-0"},
		Status:     corev1.PodStatus{Phase: corev1.PodFailed},
	}
}

// Finding #1 (TODO "Issue #4"): the slow path in checkRestartBackoff applies backoff
// even when shouldRecreateInstance returned false ONLY because a spec change / rolling
// update is in progress (Generation != ObservedGeneration). This FREEZES the update:
// checkRestartBackoff returns >0, and Scale() then requeues and skips all scaling.
//
// CANARY: on PR head this returns >0 (frozen). The proposed fix ("only apply slow-path
// backoff when shouldRecreateInstance returned false due to wasInstanceReady, not
// Generation/Revision mismatch") makes it return 0 — this test then FLIPS to fail.
func TestVerifyPR409_F1_SlowPathFreezesSpecChange_CANARY(t *testing.T) {
	c := &realControl{}
	inst := vpr409_recreateInstance()
	// Spec change in flight: a new generation the controller hasn't observed yet.
	inst.Generation = 2 // ObservedGeneration stays 1
	// A prior restart happened recently (backoff clock is armed).
	now := metav1.Now()
	inst.Status.LastRestartTime = &now
	inst.Status.RestartCount = 1

	failed := vpr409_failedPod()

	// Isolate the cause: shouldRecreateInstance is false PURELY due to the
	// generation mismatch (instance is Ready, revisions converged, LastRestartTime set).
	require.False(t, shouldRecreateInstance(inst, []*corev1.Pod{failed}, nil),
		"precondition: recreate is skipped because a spec change is in progress")

	got := c.checkRestartBackoff(inst, nil, []*corev1.Pod{failed}, nil)

	// CANARY assertion (current buggy behavior): the update is frozen by backoff.
	assert.Greater(t, got, time.Duration(0),
		"CANARY(F1): backoff freezes the spec change until the window expires; "+
			"expected >0 on buggy PR head, will flip to 0 once the TODO fix lands")
}

// Control for F1: with generation matched (no spec change), a failed pod + recent
// restart legitimately yields backoff. This is CORRECT behavior and must stay green
// before and after the fix — it proves the F1 canary isolates the spec-change path.
func TestVerifyPR409_F1_Control_LegitimateBackoffStillApplies(t *testing.T) {
	c := &realControl{}
	inst := vpr409_recreateInstance() // Generation == ObservedGeneration == 1
	now := metav1.Now()
	inst.Status.LastRestartTime = &now
	inst.Status.RestartCount = 1
	failed := vpr409_failedPod()

	require.True(t, shouldRecreateInstance(inst, []*corev1.Pod{failed}, nil),
		"precondition: a genuine crash on a converged instance does trigger recreate")
	got := c.checkRestartBackoff(inst, nil, []*corev1.Pod{failed}, nil)
	assert.Greater(t, got, time.Duration(0),
		"legitimate backoff on a real crash (control, stays green after fix)")
}

// Finding #3 (TODO "Issue #2"): once LastRestartTime is set it is never cleared, so the
// "not-Ready bypass" in shouldRecreateInstance is PERMANENT — even an ancient restart
// timestamp keeps bypassing wasInstanceReady().
//
// CANARY: on PR head, a not-Ready instance whose only restart happened long ago still
// returns true (recreate). The proposed fix (clear LastRestartTime after a sustained
// Ready period) would re-enable the not-Ready guard → return false → this FLIPS to fail.
func TestVerifyPR409_F3_PermanentLastRestartTimeBypass_CANARY(t *testing.T) {
	inst := vpr409_recreateInstance()
	// Not Ready anymore.
	inst.Status.Conditions = []workloadsv1alpha2.RoleInstanceCondition{
		{Type: workloadsv1alpha2.RoleInstanceReady, Status: corev1.ConditionFalse},
	}
	require.False(t, wasInstanceReady(inst), "precondition: instance is not Ready")
	// Ancient restart, far beyond any stability window (1 hour).
	old := metav1.NewTime(time.Now().Add(-1 * time.Hour))
	inst.Status.LastRestartTime = &old

	failed := vpr409_failedPod()

	// CANARY: bypass still active despite the ancient timestamp.
	assert.True(t, shouldRecreateInstance(inst, []*corev1.Pod{failed}, nil),
		"CANARY(F3): ancient LastRestartTime still bypasses the not-Ready guard "+
			"(bypass is permanent); flips to false once LastRestartTime is cleared after stability")

	// Contrast: with no prior restart, the not-Ready guard blocks recreate.
	inst2 := inst.DeepCopy()
	inst2.Status.LastRestartTime = nil
	assert.False(t, shouldRecreateInstance(inst2, []*corev1.Pod{failed}, nil),
		"baseline: not-Ready + no prior restart is correctly gated (guard active)")
}

// Finding #2 (fix-intent, CONTRACT): ClearRestarting removes the in-memory cache entry so
// a new same-name instance is not blocked by isAlreadyRestarting. This should PASS on PR
// head (it verifies the fix already present in this PR) and stay green after any fix.
func TestVerifyPR409_F2_ClearRestartingUnblocksSameName_CONTRACT(t *testing.T) {
	c := &realControl{} // apiReader nil → slow path returns false
	inst := vpr409_recreateInstance()

	// Simulate an in-progress restart marking the cache (as setRestartingCondition does).
	setRestartingCondition(inst)
	assert.True(t, c.isAlreadyRestarting(nil, inst),
		"precondition: instance is marked restarting in the cache")

	// The deletion path calls ClearRestarting (new in this PR).
	c.ClearRestarting(inst)

	// A freshly created same-name instance (empty status, no persisted condition) must
	// not be considered restarting.
	fresh := vpr409_recreateInstance()
	assert.False(t, c.isAlreadyRestarting(nil, fresh),
		"CONTRACT(F2): after ClearRestarting a new same-name instance is not blocked")
}
