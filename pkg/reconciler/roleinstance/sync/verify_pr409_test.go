/*
Copyright 2026 The RBG Authors.
Licensed under the Apache License, Version 2.0 (the "License");
*/

// Verification harness for sgl-project/rbg PR #409.
// ADDITIVE ONLY — no production code is modified by this file.
//
// Round 2: the author fixed F1 and F3 (commit 9ce22679). The two canaries that
// documented the bugs have been INVERTED into CONTRACT tests that assert the
// fixed behavior, so they now guard against regressions.
//
// Polarity legend (see docs/verification/pr409-restart-backoff-improvements):
//   CONTRACT = asserts the INTENDED behavior. Green on fixed code; red if it regresses.
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

// vpr409_recreateInstance builds a RoleInstance with RestartPolicy=Recreate, base=30/max=600,
// Ready=True, generation matched, revisions converged — the "steady state" baseline.
func vpr409_recreateInstance() *workloadsv1alpha2.RoleInstance {
	inst := &workloadsv1alpha2.RoleInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "vpr409", Namespace: "default"},
		Spec: workloadsv1alpha2.RoleInstanceSpec{
			RestartPolicy: workloadsv1alpha2.RestartPolicyConfig{
				Type:             workloadsv1alpha2.RecreateRoleInstanceOnPodRestart,
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

func vpr409_notReady(inst *workloadsv1alpha2.RoleInstance) {
	inst.Status.Conditions = []workloadsv1alpha2.RoleInstanceCondition{
		{Type: workloadsv1alpha2.RoleInstanceReady, Status: corev1.ConditionFalse},
	}
}

// Finding #1 (CONTRACT, was a canary — fixed in 9ce22679): when a spec change / rolling
// update is in progress (Generation != ObservedGeneration), checkRestartBackoff must NOT
// freeze the reconcile. It should return 0 so the update can proceed.
func TestVerifyPR409_F1_SpecChangeNotFrozen_CONTRACT(t *testing.T) {
	c := &realControl{}
	inst := vpr409_recreateInstance()
	inst.Generation = 2 // ObservedGeneration stays 1 → spec change in flight
	now := metav1.Now()
	inst.Status.LastRestartTime = &now
	inst.Status.RestartCount = 1
	failed := vpr409_failedPod()

	require.False(t, shouldRecreateInstance(inst, []*corev1.Pod{failed}, nil),
		"precondition: recreate is skipped because a spec change is in progress")

	got := c.checkRestartBackoff(inst, nil, []*corev1.Pod{failed}, nil)
	assert.Equal(t, time.Duration(0), got,
		"CONTRACT(F1): backoff must not freeze a spec change / rolling update")
}

// Also cover the rolling-update variant: CurrentRevision != UpdateRevision.
func TestVerifyPR409_F1_RollingUpdateNotFrozen_CONTRACT(t *testing.T) {
	c := &realControl{}
	inst := vpr409_recreateInstance()
	inst.Status.CurrentRevision = "rev-1"
	inst.Status.UpdateRevision = "rev-2" // rolling update in progress
	now := metav1.Now()
	inst.Status.LastRestartTime = &now
	inst.Status.RestartCount = 1
	failed := vpr409_failedPod()

	got := c.checkRestartBackoff(inst, nil, []*corev1.Pod{failed}, nil)
	assert.Equal(t, time.Duration(0), got,
		"CONTRACT(F1): backoff must not freeze a rolling update (revision mismatch)")
}

// Control for F1: with generation matched (no spec change), a genuine crash still backs off.
// Must stay green — proves the F1 contract isolates the spec-change path.
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
		"legitimate backoff on a real crash still applies")
}

// Finding #3 (CONTRACT, was a canary — fixed in 9ce22679): the not-Ready bypass in
// shouldRecreateInstance is now BOUNDED to the stability window (hasRecentRestart), not
// permanent. Verifies both sides: an ancient restart no longer bypasses the guard, but a
// recent restart still does (deadlock prevention preserved).
func TestVerifyPR409_F3_BoundedBypass_CONTRACT(t *testing.T) {
	failed := vpr409_failedPod()

	t.Run("ancient restart + not Ready: guard re-armed, no recreate", func(t *testing.T) {
		inst := vpr409_recreateInstance()
		vpr409_notReady(inst)
		old := metav1.NewTime(time.Now().Add(-1 * time.Hour)) // > max(1200s,10min)=20min window
		inst.Status.LastRestartTime = &old
		require.False(t, hasRecentRestart(inst), "precondition: restart is outside the stable window")
		assert.False(t, shouldRecreateInstance(inst, []*corev1.Pod{failed}, nil),
			"CONTRACT(F3): stale restart no longer bypasses the not-Ready guard")
	})

	t.Run("recent restart + not Ready: bypass still active, recreate (deadlock prevention)", func(t *testing.T) {
		inst := vpr409_recreateInstance()
		vpr409_notReady(inst)
		recent := metav1.NewTime(time.Now().Add(-1 * time.Minute))
		inst.Status.LastRestartTime = &recent
		require.True(t, hasRecentRestart(inst), "precondition: restart is within the stable window")
		assert.True(t, shouldRecreateInstance(inst, []*corev1.Pod{failed}, nil),
			"CONTRACT(F3): a recent restart still bypasses the not-Ready guard")
	})

	t.Run("no prior restart + not Ready: guard active, no recreate", func(t *testing.T) {
		inst := vpr409_recreateInstance()
		vpr409_notReady(inst)
		inst.Status.LastRestartTime = nil
		assert.False(t, shouldRecreateInstance(inst, []*corev1.Pod{failed}, nil),
			"baseline: not-Ready + no prior restart is correctly gated")
	})
}

// Finding #2 (CONTRACT): ClearRestarting removes the in-memory cache entry so a new
// same-name instance is not blocked by isAlreadyRestarting. Confirms the shipped fix.
func TestVerifyPR409_F2_ClearRestartingUnblocksSameName_CONTRACT(t *testing.T) {
	c := &realControl{} // apiReader nil → slow path returns false
	inst := vpr409_recreateInstance()

	setRestartingCondition(inst)
	assert.True(t, c.isAlreadyRestarting(nil, inst),
		"precondition: instance is marked restarting in the cache")

	c.ClearRestarting(inst)

	fresh := vpr409_recreateInstance()
	assert.False(t, c.isAlreadyRestarting(nil, fresh),
		"CONTRACT(F2): after ClearRestarting a new same-name instance is not blocked")
}
