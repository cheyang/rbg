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

package workloads

// Reviewer verification harness for PR #478 (verify/warmup-target-wait-claude).
//
// This file is intentionally self-contained (no references to symbols introduced
// by the PR: "TargetReady" and requeue constants are spelled as literals) so it
// compiles both at the PR head and at the merge base, which lets the same tests
// demonstrate the premise symptoms on the base branch and the fixed behavior at
// head.
//
// Test polarity:
//   - TestVerifyC*  contract tests: green at head. C1/C2 are RED at the base
//     branch (they reproduce premise problems 1 and 2 of the PR).
//   - TestVerifyF*  bug canaries for findings F1/F2: RED at head while the bug
//     is present; green at base (the base code reaches a terminal state).
//   - TestVerifyN*  round-2/3 contracts: N1 (F4a false completion + F7 truthful
//     reason/startTime), N2 (creation-anchored timeout), N3 (F6 stale-terminal
//     bypass), N4 (F4b mid-run unscheduled gap + replacement warming), N5 (F5
//     orphan active Pod vs the timeout safety net). N1/N4/N5 were RED at the
//     round-2 head 60b1b7cb and are GREEN at b0915b21 (round 3).
//   - TestVerifyR4* round-4 contracts (head 92cb7ef): R4-F9 (collapsed-settle
//     honors maxFailedNodes) and R4-Q4 (per-role readiness deficits; surplus
//     scheduled Pods in one role cannot mask a Pending Pod in another). Both RED
//     at the round-3 head b0915b21 and GREEN at 92cb7ef. N1's TargetReady
//     assertion was updated to Unknown (the round-4 Q2 contract).

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func verify478Spec(rbgName string) workloadsv1alpha2.RoleBasedGroupWarmupSpec {
	return workloadsv1alpha2.RoleBasedGroupWarmupSpec{
		TargetRoleBasedGroup: &workloadsv1alpha2.TargetRoleBasedGroup{
			Name: rbgName,
			Roles: map[string]workloadsv1alpha2.WarmupActions{
				"worker": {ImagePreload: &workloadsv1alpha2.ImagePreloadAction{Images: []string{"busybox:1.36"}}},
			},
		},
	}
}

// verify478RBG builds a target RoleBasedGroup whose spec lists the "worker" role
// (one replica). Round 2 harness-update: commit 60b1b7cb derives the expected pod
// count from the RBG *spec* (countTargetPodsNotReady), so an RBG fixture without
// spec roles is no longer a realistic target — a real RBG always lists its roles,
// and a role-less RBG has no pods at all. The round-1 fixture (empty Spec) made C2
// misfire at the new head: expected=0 means no wait is needed even though a pod
// exists; with a realistic spec the wait triggers as the PR intends.
func verify478RBG(name string) *workloadsv1alpha2.RoleBasedGroup {
	return verify478RBGReplicas(name, 1)
}

func verify478RBGReplicas(name string, replicas int32) *workloadsv1alpha2.RoleBasedGroup {
	return &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{
			Roles: []workloadsv1alpha2.RoleSpec{{Name: "worker", Replicas: ptr.To(replicas)}},
		},
	}
}

func verify478TargetPod(rbgName, podName, node string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: "default",
			Labels: map[string]string{
				constants.GroupNameLabelKey: rbgName,
				constants.RoleNameLabelKey:  "worker",
			},
		},
		Spec: corev1.PodSpec{NodeName: node},
	}
}

func verify478Get(t *testing.T, r *RoleBasedGroupWarmupReconciler, name string) *workloadsv1alpha2.RoleBasedGroupWarmup {
	t.Helper()
	obj := &workloadsv1alpha2.RoleBasedGroupWarmup{}
	if err := r.Get(context.Background(), types.NamespacedName{Name: name, Namespace: "default"}, obj); err != nil {
		t.Fatalf("get warmup %s: %v", name, err)
	}
	return obj
}

// C1 — premise problem 1 (fixed at head): a missing target RoleBasedGroup must be
// waited on, not terminally failed, and the Warmup must proceed once the target
// appears. RED at base (base fails immediately with Failed/InvalidTarget).
func TestVerifyC1MissingTargetWaitsThenRecovers(t *testing.T) {
	ctx := context.Background()
	warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{Name: "v478-c1", Namespace: "default", UID: "uid-v478-c1"},
		Spec:       verify478Spec("v478-c1-rbg"),
	}
	r := newWarmupReconciler(warmup)

	result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(warmup)})
	if err != nil {
		t.Fatalf("reconcile with missing target: %v", err)
	}
	after := verify478Get(t, r, warmup.Name)
	if after.Status.Phase != workloadsv1alpha2.WarmupJobPhaseRunning {
		t.Fatalf("while target is missing, phase should be Running, got %q (conditions=%#v)",
			after.Status.Phase, after.Status.Conditions)
	}
	if cond := apimeta.FindStatusCondition(after.Status.Conditions, "TargetReady"); cond == nil ||
		cond.Status != metav1.ConditionFalse || cond.Reason != "RoleBasedGroupNotFound" {
		t.Fatalf("expected TargetReady=False/RoleBasedGroupNotFound, got %#v", after.Status.Conditions)
	}
	if result.RequeueAfter == 0 {
		t.Fatalf("expected a requeue while waiting for the target, got %#v", result)
	}

	// Target appears with a scheduled Pod; the Warmup must proceed.
	if err := r.Create(ctx, verify478RBG("v478-c1-rbg")); err != nil {
		t.Fatalf("create rbg: %v", err)
	}
	if err := r.Create(ctx, verify478TargetPod("v478-c1-rbg", "v478-c1-worker-0", "node-1")); err != nil {
		t.Fatalf("create target pod: %v", err)
	}
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(warmup)}); err != nil {
		t.Fatalf("reconcile after target appears: %v", err)
	}
	done := verify478Get(t, r, warmup.Name)
	if done.Status.Phase == workloadsv1alpha2.WarmupJobPhaseFailed {
		t.Fatalf("warmup must not fail once the target exists, got %#v", done.Status.Conditions)
	}
	pods := &corev1.PodList{}
	if err := r.List(ctx, pods, client.MatchingLabels{LabelWarmupName: warmup.Name}); err != nil {
		t.Fatalf("list warmup pods: %v", err)
	}
	if len(pods.Items) != 1 {
		t.Fatalf("expected 1 warmup Pod once the target is ready, got %d", len(pods.Items))
	}
}

// C2 — premise problem 2 (fixed at head): an existing target whose selected Pods
// are all unscheduled must wait instead of reporting Completed/NoNodesMatched.
// RED at base (base completes with NoNodesMatched).
func TestVerifyC2UnscheduledTargetPodsWaitInsteadOfCompleting(t *testing.T) {
	ctx := context.Background()
	warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{Name: "v478-c2", Namespace: "default", UID: "uid-v478-c2"},
		Spec:       verify478Spec("v478-c2-rbg"),
	}
	r := newWarmupReconciler(warmup, verify478RBG("v478-c2-rbg"),
		verify478TargetPod("v478-c2-rbg", "v478-c2-worker-0", "")) // unscheduled

	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(warmup)}); err != nil {
		t.Fatalf("reconcile with unscheduled target pod: %v", err)
	}
	after := verify478Get(t, r, warmup.Name)
	if after.Status.Phase == workloadsv1alpha2.WarmupJobPhaseCompleted {
		t.Fatalf("unscheduled target Pods must not complete the Warmup (desired=%d, conditions=%#v)",
			after.Status.Desired, after.Status.Conditions)
	}
	if cond := apimeta.FindStatusCondition(after.Status.Conditions, "Complete"); cond != nil && cond.Reason == "NoNodesMatched" {
		t.Fatalf("must not report NoNodesMatched while selected Pods are unscheduled: %#v", cond)
	}
	if cond := apimeta.FindStatusCondition(after.Status.Conditions, "TargetReady"); cond == nil ||
		cond.Status != metav1.ConditionFalse || cond.Reason != "TargetPodsNotScheduled" {
		t.Fatalf("expected TargetReady=False/TargetPodsNotScheduled, got %#v", after.Status.Conditions)
	}
}

// C3 — bounded wait: a missing target must still fail terminally once
// globalTimeoutSeconds (measured from creation, since no Pod exists) has elapsed.
func TestVerifyC3MissingTargetFailsAfterGlobalTimeoutFromCreation(t *testing.T) {
	ctx := context.Background()
	timeout := int64(60)
	warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "v478-c3",
			Namespace:         "default",
			UID:               "uid-v478-c3",
			CreationTimestamp: metav1.NewTime(time.Now().Add(-10 * time.Minute)),
		},
		Spec: verify478Spec("v478-c3-rbg"),
	}
	warmup.Spec.Policies = &workloadsv1alpha2.WarmupPolicies{GlobalTimeoutSeconds: &timeout}
	r := newWarmupReconciler(warmup)

	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(warmup)}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	after := verify478Get(t, r, warmup.Name)
	if after.Status.Phase != workloadsv1alpha2.WarmupJobPhaseFailed {
		t.Fatalf("expected Failed after the creation-time deadline lapsed, got %q", after.Status.Phase)
	}
	if cond := apimeta.FindStatusCondition(after.Status.Conditions, "Failed"); cond == nil || cond.Reason != "InvalidTarget" {
		t.Fatalf("expected Failed/InvalidTarget, got %#v", after.Status.Conditions)
	}
}

// C4 — NoNodesMatched semantics narrowed, not removed: a target RBG whose selected
// roles have no Pods at all still completes immediately with NoNodesMatched, and a
// pending Pod in an UNselected role must not block it.
func TestVerifyC4NoPodsAtAllStillCompletesNoNodesMatched(t *testing.T) {
	ctx := context.Background()
	warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{Name: "v478-c4", Namespace: "default", UID: "uid-v478-c4"},
		Spec:       verify478Spec("v478-c4-rbg"),
	}
	routerPending := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "v478-c4-router-0",
			Namespace: "default",
			Labels: map[string]string{
				constants.GroupNameLabelKey: "v478-c4-rbg",
				constants.RoleNameLabelKey:  "router", // not selected by the Warmup
			},
		},
		Spec: corev1.PodSpec{NodeName: ""},
	}
	r := newWarmupReconciler(warmup, verify478RBGReplicas("v478-c4-rbg", 0), routerPending)

	result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(warmup)})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	after := verify478Get(t, r, warmup.Name)
	if after.Status.Phase != workloadsv1alpha2.WarmupJobPhaseCompleted {
		t.Fatalf("expected Completed (no Pods in selected roles), got %q (conditions=%#v)",
			after.Status.Phase, after.Status.Conditions)
	}
	if cond := apimeta.FindStatusCondition(after.Status.Conditions, "Complete"); cond == nil || cond.Reason != "NoNodesMatched" {
		t.Fatalf("expected Complete/NoNodesMatched, got %#v", after.Status.Conditions)
	}
	if result.RequeueAfter != 0 {
		t.Fatalf("unselected pending Pod must not cause a wait requeue, got %#v", result)
	}
}

// C5 — controller-side image validation still closes the #466 admission gap after
// the CRD CEL rule is dropped: blank/whitespace image => Failed/InvalidWarmupSpec,
// zero Pods created.
func TestVerifyC5BlankImageFailsInControllerNoPods(t *testing.T) {
	ctx := context.Background()
	for _, image := range []string{"", " \t"} {
		warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
			ObjectMeta: metav1.ObjectMeta{Name: "v478-c5", Namespace: "default", UID: types.UID("v478-c5-" + image)},
			Spec: workloadsv1alpha2.RoleBasedGroupWarmupSpec{
				TargetNodes: &workloadsv1alpha2.TargetNodes{
					NodeNames: []string{"node-1"},
					WarmupActions: workloadsv1alpha2.WarmupActions{
						CustomizedAction: &workloadsv1alpha2.CustomizedAction{
							Containers: []corev1.Container{{Name: "c1", Image: image}},
						},
					},
				},
			},
		}
		r := newWarmupReconciler(warmup)
		if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(warmup)}); err != nil {
			t.Fatalf("reconcile (image=%q): %v", image, err)
		}
		after := verify478Get(t, r, warmup.Name)
		if after.Status.Phase != workloadsv1alpha2.WarmupJobPhaseFailed {
			t.Fatalf("image=%q: expected Failed, got %q", image, after.Status.Phase)
		}
		if cond := apimeta.FindStatusCondition(after.Status.Conditions, "Failed"); cond == nil || cond.Reason != "InvalidWarmupSpec" {
			t.Fatalf("image=%q: expected Failed/InvalidWarmupSpec, got %#v", image, after.Status.Conditions)
		}
		pods := &corev1.PodList{}
		if err := r.List(ctx, pods, client.InNamespace("default")); err != nil {
			t.Fatalf("list pods: %v", err)
		}
		if len(pods.Items) != 0 {
			t.Fatalf("image=%q: no Pod must be created, got %d", image, len(pods.Items))
		}
	}
}

// F1 (canary, red at head while the bug is present) — a Warmup whose Pods have all
// succeeded must reach a terminal phase even if its target RoleBasedGroup is
// deleted afterwards. At head, markTargetNotReady keeps the Warmup Running
// forever (unbounded when globalTimeoutSeconds is unset), so the work that
// already finished is never recorded and TTL/cleanup never runs.
func TestVerifyF1TargetDeletedAfterPodsSucceededReachesTerminal(t *testing.T) {
	ctx := context.Background()
	warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{Name: "v478-f1", Namespace: "default", UID: "uid-v478-f1"},
		Spec:       verify478Spec("v478-f1-rbg"),
	}
	rbg := verify478RBG("v478-f1-rbg")
	r := newWarmupReconciler(warmup, rbg, verify478TargetPod("v478-f1-rbg", "v478-f1-worker-0", "node-1"))

	// Create the warmup Pod and let it succeed.
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(warmup)}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	warmupPods := &corev1.PodList{}
	if err := r.List(ctx, warmupPods, client.MatchingLabels{LabelWarmupName: warmup.Name}); err != nil {
		t.Fatalf("list warmup pods: %v", err)
	}
	if len(warmupPods.Items) != 1 {
		t.Fatalf("expected 1 warmup Pod, got %d", len(warmupPods.Items))
	}
	succeeded := warmupPods.Items[0].DeepCopy()
	succeeded.Status.Phase = corev1.PodSucceeded
	if err := r.Status().Update(ctx, succeeded); err != nil {
		t.Fatalf("mark warmup Pod succeeded: %v", err)
	}

	// Sanity: while the target still exists, the Warmup completes.
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(warmup)}); err != nil {
		t.Fatalf("reconcile after pod success: %v", err)
	}
	if got := verify478Get(t, r, warmup.Name).Status.Phase; got != workloadsv1alpha2.WarmupJobPhaseCompleted {
		t.Fatalf("sanity: expected Completed while target exists, got %q", got)
	}

	// Now the failure scenario with a fresh Warmup whose Pod succeeded while the
	// target was deleted: phase must still reach a terminal state.
	warmup2 := &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{Name: "v478-f1b", Namespace: "default", UID: "uid-v478-f1b"},
		Spec:       verify478Spec("v478-f1b-rbg"),
	}
	rbg2 := verify478RBG("v478-f1b-rbg")
	r2 := newWarmupReconciler(warmup2, rbg2, verify478TargetPod("v478-f1b-rbg", "v478-f1b-worker-0", "node-1"))
	if _, err := r2.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(warmup2)}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	pods2 := &corev1.PodList{}
	if err := r2.List(ctx, pods2, client.MatchingLabels{LabelWarmupName: warmup2.Name}); err != nil {
		t.Fatalf("list warmup pods: %v", err)
	}
	succeeded2 := pods2.Items[0].DeepCopy()
	succeeded2.Status.Phase = corev1.PodSucceeded
	if err := r2.Status().Update(ctx, succeeded2); err != nil {
		t.Fatalf("mark warmup Pod succeeded: %v", err)
	}
	if err := r2.Delete(ctx, rbg2); err != nil {
		t.Fatalf("delete target rbg: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := r2.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(warmup2)}); err != nil {
			t.Fatalf("reconcile after target deletion: %v", err)
		}
	}
	after := verify478Get(t, r2, warmup2.Name)
	if after.Status.Phase != workloadsv1alpha2.WarmupJobPhaseCompleted && after.Status.Phase != workloadsv1alpha2.WarmupJobPhaseFailed {
		t.Fatalf("F1: after all Pods succeeded and the target was deleted, the Warmup must reach a terminal "+
			"phase, but it is stuck in %q (Succeeded=%d, conditions=%#v) — unbounded wait, TTL never applies",
			after.Status.Phase, after.Status.Succeeded, after.Status.Conditions)
	}
	if after.Status.Succeeded != 1 {
		t.Fatalf("F1: finished work must be recorded, Succeeded=%d", after.Status.Succeeded)
	}
}

// F2 (canary, red at head while the bug is present) — while a Warmup waits for a
// newly-appeared unscheduled selected Pod, status counters freeze: the Warmup
// reports Running/Active=0/Succeeded=0 even though all of its work is done, and
// without globalTimeoutSeconds it stays that way indefinitely.
func TestVerifyF2CountersAndCompletionFrozenDuringWait(t *testing.T) {
	ctx := context.Background()
	warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{Name: "v478-f2", Namespace: "default", UID: "uid-v478-f2"},
		Spec:       verify478Spec("v478-f2-rbg"),
	}
	r := newWarmupReconciler(warmup, verify478RBG("v478-f2-rbg"),
		verify478TargetPod("v478-f2-rbg", "v478-f2-worker-0", "node-1"))

	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(warmup)}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	pods := &corev1.PodList{}
	if err := r.List(ctx, pods, client.MatchingLabels{LabelWarmupName: warmup.Name}); err != nil {
		t.Fatalf("list warmup pods: %v", err)
	}
	if len(pods.Items) != 1 {
		t.Fatalf("expected 1 warmup Pod, got %d", len(pods.Items))
	}
	succeeded := pods.Items[0].DeepCopy()
	succeeded.Status.Phase = corev1.PodSucceeded
	if err := r.Status().Update(ctx, succeeded); err != nil {
		t.Fatalf("mark warmup Pod succeeded: %v", err)
	}

	// A scale-out Pod appears in the selected role and is not scheduled yet.
	if err := r.Create(ctx, verify478TargetPod("v478-f2-rbg", "v478-f2-worker-1", "")); err != nil {
		t.Fatalf("create scale-out target pod: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(warmup)}); err != nil {
			t.Fatalf("reconcile during wait: %v", err)
		}
	}
	after := verify478Get(t, r, warmup.Name)
	if after.Status.Phase == workloadsv1alpha2.WarmupJobPhaseCompleted {
		// Waiting for the new Pod is intended per the PR; completing is also acceptable.
		t.Logf("warmup completed despite pending scale-out Pod")
	}
	// Either way, the status must not lie about the finished work: the only warmup
	// Pod is Succeeded, so Active must be 0 and Succeeded must be 1. While the
	// wait path returns before updateStatus, the counters stay frozen at whatever
	// the last pre-wait reconcile wrote (here Active=1, Succeeded=0) forever.
	if after.Status.Active != 0 || after.Status.Succeeded != 1 {
		t.Fatalf("F2: while waiting, status counters do not reflect the succeeded warmup Pod: "+
			"Active=%d (want 0), Succeeded=%d (want 1), phase=%q, desired=%d, conditions=%#v",
			after.Status.Active, after.Status.Succeeded, after.Status.Phase,
			after.Status.Desired, after.Status.Conditions)
	}
}

// ---------------------------------------------------------------------------
// Round 2 (head 60b1b7cb "make target readiness terminal-safe and deterministic")
// ---------------------------------------------------------------------------

// N1 (round 3, contract — supersedes the round-2 polarity) — a Warmup whose warmup
// Pod is still ACTIVE when the target RoleBasedGroup disappears must NOT complete:
// Completed is terminal (TTL-only afterwards) and would misreport the in-flight job
// and expose the still-running Pod to owner-ref GC. It stays Running with Active
// accounting intact until the Pod itself reaches a terminal state, and then
// completes with a truthful reason and a preserved startTime (the round-2 F4a/F7
// contract). RED at the previous head 60b1b7cb (false Completed/NoNodesMatched
// with Active=1); GREEN at b0915b21.
func TestVerifyN1TargetDeletedMidRunWithActivePodStaysRunning(t *testing.T) {
	ctx := context.Background()
	warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{Name: "v478-n1", Namespace: "default", UID: "uid-v478-n1"},
		Spec:       verify478Spec("v478-n1-rbg"),
	}
	r := newWarmupReconciler(warmup, verify478RBG("v478-n1-rbg"),
		verify478TargetPod("v478-n1-rbg", "v478-n1-worker-0", "node-1"))

	// Creates the warmup Pod (still active) and records startTime.
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(warmup)}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	pods := &corev1.PodList{}
	if err := r.List(ctx, pods, client.MatchingLabels{LabelWarmupName: warmup.Name}); err != nil {
		t.Fatalf("list warmup pods: %v", err)
	}
	if len(pods.Items) != 1 {
		t.Fatalf("expected 1 active warmup Pod, got %d", len(pods.Items))
	}
	started := verify478Get(t, r, warmup.Name)
	if started.Status.StartTime == nil {
		t.Fatalf("startTime must be recorded once a Pod exists")
	}
	startTime := *started.Status.StartTime

	// Target RBG and its Pods vanish mid-run (teardown / namespace cleanup).
	// The warmup Pod itself stays (it is still running).
	target := &corev1.PodList{}
	if err := r.List(ctx, target, client.MatchingLabels{constants.GroupNameLabelKey: "v478-n1-rbg"}); err != nil {
		t.Fatalf("list target pods: %v", err)
	}
	for i := range target.Items {
		if err := r.Delete(ctx, &target.Items[i]); err != nil {
			t.Fatalf("delete target pod: %v", err)
		}
	}
	rbg := &workloadsv1alpha2.RoleBasedGroup{}
	if err := r.Get(ctx, types.NamespacedName{Name: "v478-n1-rbg", Namespace: "default"}, rbg); err != nil {
		t.Fatalf("get rbg: %v", err)
	}
	if err := r.Delete(ctx, rbg); err != nil {
		t.Fatalf("delete rbg: %v", err)
	}

	// Reconcile with the warmup Pod still active and the target gone.
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(warmup)}); err != nil {
		t.Fatalf("reconcile after target deletion: %v", err)
	}
	after := verify478Get(t, r, warmup.Name)
	if after.Status.Phase != workloadsv1alpha2.WarmupJobPhaseRunning {
		t.Fatalf("N1/F4a: with an ACTIVE warmup Pod and the target gone, the Warmup must stay Running, "+
			"got %q (conditions=%#v, active=%d)", after.Status.Phase, after.Status.Conditions, after.Status.Active)
	}
	if after.Status.Active != 1 {
		t.Fatalf("N1/F4a: Active must keep accounting the running Pod, got %d", after.Status.Active)
	}
	// Condition polarity is deliberately loose here so N1 stays a stable F4a/F7
	// regression test across the round-3 (False) and round-4 (Unknown) condition
	// evolution; the exact Unknown contract is pinned by TestVerifyR4Q2 below.
	if cond := apimeta.FindStatusCondition(after.Status.Conditions, "TargetReady"); cond == nil ||
		cond.Reason != "RoleBasedGroupNotFound" || cond.Status == metav1.ConditionTrue {
		t.Fatalf("N1: expected TargetReady!=True/RoleBasedGroupNotFound after a started target "+
			"disappears, got %#v", after.Status.Conditions)
	}

	// Once the Pod succeeds, the job completes with a truthful reason (not
	// NoNodesMatched), accurate counters, and the original startTime (F7).
	succeeded := pods.Items[0].DeepCopy()
	succeeded.Status.Phase = corev1.PodSucceeded
	if err := r.Status().Update(ctx, succeeded); err != nil {
		t.Fatalf("succeed warmup pod: %v", err)
	}
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(warmup)}); err != nil {
		t.Fatalf("reconcile after pod success: %v", err)
	}
	final := verify478Get(t, r, warmup.Name)
	if final.Status.Phase != workloadsv1alpha2.WarmupJobPhaseCompleted {
		t.Fatalf("N1: the Warmup must complete once its Pod is terminal, got %q (conditions=%#v)",
			final.Status.Phase, final.Status.Conditions)
	}
	if cond := apimeta.FindStatusCondition(final.Status.Conditions, "Complete"); cond == nil || cond.Reason != "WarmupCompleted" {
		t.Fatalf("N1/F7: completion reason must be WarmupCompleted for a job that warmed a node, got %#v",
			final.Status.Conditions)
	}
	if final.Status.Succeeded != 1 || final.Status.Active != 0 {
		t.Fatalf("N1: terminal counters must be accurate, got active=%d succeeded=%d",
			final.Status.Active, final.Status.Succeeded)
	}
	if final.Status.StartTime == nil || !final.Status.StartTime.Time.Equal(startTime.Time) {
		t.Fatalf("N1/F7: startTime must be preserved on mid-run-deletion completion, was %v now %v",
			startTime, final.Status.StartTime)
	}
}

// R4-Q2 (round 4, contract — the round-3 Q2 fix) — once a started Warmup's
// target disappears, TargetReady is recorded as Unknown/RoleBasedGroupNotFound
// (not False) both while the job keeps Running and on its terminal Completed
// state, so Complete=True is never paired with a claim that a deleted target is
// currently not-ready/ready. RED at b0915b21 (recorded False); GREEN at 92cb7ef.
func TestVerifyR4Q2DeletedTargetMidRunRecordsTargetReadyUnknown(t *testing.T) {
	ctx := context.Background()
	warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{Name: "v478-r4q2", Namespace: "default", UID: "uid-v478-r4q2"},
		Spec:       verify478Spec("v478-r4q2-rbg"),
	}
	r := newWarmupReconciler(warmup, verify478RBG("v478-r4q2-rbg"),
		verify478TargetPod("v478-r4q2-rbg", "v478-r4q2-worker-0", "node-1"))

	// Creates the warmup Pod (still active).
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(warmup)}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	pods := &corev1.PodList{}
	if err := r.List(ctx, pods, client.MatchingLabels{LabelWarmupName: warmup.Name}); err != nil {
		t.Fatalf("list warmup pods: %v", err)
	}
	if len(pods.Items) != 1 {
		t.Fatalf("expected 1 active warmup Pod, got %d", len(pods.Items))
	}

	// The target RBG and its Pods vanish mid-run; the warmup Pod keeps running.
	target := &corev1.PodList{}
	if err := r.List(ctx, target, client.MatchingLabels{constants.GroupNameLabelKey: "v478-r4q2-rbg"}); err != nil {
		t.Fatalf("list target pods: %v", err)
	}
	for i := range target.Items {
		if err := r.Delete(ctx, &target.Items[i]); err != nil {
			t.Fatalf("delete target pod: %v", err)
		}
	}
	rbg := &workloadsv1alpha2.RoleBasedGroup{}
	if err := r.Get(ctx, types.NamespacedName{Name: "v478-r4q2-rbg", Namespace: "default"}, rbg); err != nil {
		t.Fatalf("get rbg: %v", err)
	}
	if err := r.Delete(ctx, rbg); err != nil {
		t.Fatalf("delete rbg: %v", err)
	}

	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(warmup)}); err != nil {
		t.Fatalf("reconcile after target deletion: %v", err)
	}
	after := verify478Get(t, r, warmup.Name)
	if cond := apimeta.FindStatusCondition(after.Status.Conditions, "TargetReady"); cond == nil ||
		cond.Status != metav1.ConditionUnknown || cond.Reason != "RoleBasedGroupNotFound" {
		t.Fatalf("R4-Q2: while Running with the target gone, expected "+
			"TargetReady=Unknown/RoleBasedGroupNotFound, got %#v", after.Status.Conditions)
	}

	// The Pod succeeds; the terminal Completed job must keep the Unknown status.
	succeeded := pods.Items[0].DeepCopy()
	succeeded.Status.Phase = corev1.PodSucceeded
	if err := r.Status().Update(ctx, succeeded); err != nil {
		t.Fatalf("succeed warmup pod: %v", err)
	}
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(warmup)}); err != nil {
		t.Fatalf("reconcile after pod success: %v", err)
	}
	final := verify478Get(t, r, warmup.Name)
	if final.Status.Phase != workloadsv1alpha2.WarmupJobPhaseCompleted {
		t.Fatalf("R4-Q2: the Warmup must complete once its Pod is terminal, got %q (conditions=%#v)",
			final.Status.Phase, final.Status.Conditions)
	}
	if cond := apimeta.FindStatusCondition(final.Status.Conditions, "TargetReady"); cond == nil ||
		cond.Status != metav1.ConditionUnknown || cond.Reason != "RoleBasedGroupNotFound" {
		t.Fatalf("R4-Q2: a terminal Completed job must not pair Complete=True with "+
			"TargetReady=False; expected Unknown/RoleBasedGroupNotFound, got %#v", final.Status.Conditions)
	}
}

// R4-F9 (round 4, contract — the round-3 F9 fix) — a Warmup whose desired set
// collapsed mid-run applies the same maxFailedNodes tolerance as a non-empty
// desired set: a failed attempt within tolerance completes the job (with the
// failure counted) instead of failing it, and only failures beyond the threshold
// fail with MaxFailedNodesExceeded. RED at b0915b21 (within tolerance settled
// Failed/InvalidTarget); GREEN at 92cb7ef.
func TestVerifyR4CollapsedSettleHonorsMaxFailedNodes(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name        string
		policies    *workloadsv1alpha2.WarmupPolicies
		wantFailed  bool
		wantReason  string
		wantFailedN int32
	}{
		{
			name:        "no policy tolerates the failed attempt",
			policies:    nil,
			wantFailed:  false,
			wantReason:  "WarmupCompleted",
			wantFailedN: 1,
		},
		{
			name: "failure within maxFailedNodes completes",
			policies: &workloadsv1alpha2.WarmupPolicies{
				BackoffLimitPerNode: ptr.To(int32(2)),
				MaxFailedNodes:      ptr.To(int32(1)),
			},
			wantFailed:  false,
			wantReason:  "WarmupCompleted",
			wantFailedN: 1,
		},
		{
			name: "failure beyond maxFailedNodes fails",
			policies: &workloadsv1alpha2.WarmupPolicies{
				BackoffLimitPerNode: ptr.To(int32(2)),
				MaxFailedNodes:      ptr.To(int32(0)),
			},
			wantFailed:  true,
			wantReason:  "MaxFailedNodesExceeded",
			wantFailedN: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			started := metav1.NewTime(time.Now().Add(-5 * time.Minute))
			warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "v478-r4f9-" + strings.ToLower(strings.ReplaceAll(tc.name, " ", "-")),
					Namespace: "default",
					UID:       types.UID("uid-v478-r4f9-" + strings.ToLower(strings.ReplaceAll(tc.name, " ", "-"))),
				},
				Spec:   verify478Spec("v478-r4f9-rbg"),
				Status: workloadsv1alpha2.RoleBasedGroupWarmupStatus{Phase: workloadsv1alpha2.WarmupJobPhaseRunning, StartTime: &started},
			}
			warmup.Spec.Policies = tc.policies
			// The target RBG was deleted mid-run after node-1's attempt failed.
			failed := makeWarmupPod("v478-r4f9-pod", "default", warmup.Name, string(warmup.UID), "node-1", corev1.PodFailed)
			r := newWarmupReconciler(warmup, failed)

			if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(warmup)}); err != nil {
				t.Fatalf("reconcile: %v", err)
			}
			after := verify478Get(t, r, warmup.Name)
			condType, wantPhase := "Complete", workloadsv1alpha2.WarmupJobPhaseCompleted
			if tc.wantFailed {
				condType, wantPhase = "Failed", workloadsv1alpha2.WarmupJobPhaseFailed
			}
			if after.Status.Phase != wantPhase {
				t.Fatalf("R4-F9: expected phase %q, got %q (conditions=%#v)",
					wantPhase, after.Status.Phase, after.Status.Conditions)
			}
			if cond := apimeta.FindStatusCondition(after.Status.Conditions, condType); cond == nil || cond.Reason != tc.wantReason {
				t.Fatalf("R4-F9: expected %s/%s, got %#v", condType, tc.wantReason, after.Status.Conditions)
			}
			if after.Status.Failed != tc.wantFailedN {
				t.Fatalf("R4-F9: the unsucceeded attempt must be counted, expected failed=%d got %d",
					tc.wantFailedN, after.Status.Failed)
			}
		})
	}
}

// R4-Q4 (round 4, contract — the round-3 Q4 fix) — readiness deficits are
// computed per selected role, so surplus scheduled Pods in one role must not
// mask a missing/Pending Pod in another selected role. RED at b0915b21 (the
// aggregate expected-scheduled subtraction masked the worker's deficit);
// GREEN at 92cb7ef.
func TestVerifyR4CrossRoleSurplusDoesNotMaskPendingTargetPod(t *testing.T) {
	ctx := context.Background()
	warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{Name: "v478-r4q4", Namespace: "default", UID: "uid-v478-r4q4"},
		Spec: workloadsv1alpha2.RoleBasedGroupWarmupSpec{
			TargetRoleBasedGroup: &workloadsv1alpha2.TargetRoleBasedGroup{
				Name: "v478-r4q4-rbg",
				Roles: map[string]workloadsv1alpha2.WarmupActions{
					"router": {ImagePreload: &workloadsv1alpha2.ImagePreloadAction{Images: []string{"busybox:1.36"}}},
					"worker": {ImagePreload: &workloadsv1alpha2.ImagePreloadAction{Images: []string{"busybox:1.36"}}},
				},
			},
		},
	}
	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "v478-r4q4-rbg", Namespace: "default"},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{
			Roles: []workloadsv1alpha2.RoleSpec{
				{Name: "router", Replicas: ptr.To(int32(1))},
				{Name: "worker", Replicas: ptr.To(int32(1))},
			},
		},
	}
	targetPod := func(name, role, node string) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: "default",
				Labels: map[string]string{
					constants.GroupNameLabelKey: "v478-r4q4-rbg",
					constants.RoleNameLabelKey:  role,
				},
			},
			Spec: corev1.PodSpec{NodeName: node},
		}
	}
	// Rollout surge: two live scheduled router Pods (replicas=1, one extra) while
	// the worker Pod is still Pending (no node assignment).
	r := newWarmupReconciler(warmup, rbg,
		targetPod("v478-r4q4-router-0", "router", "node-1"),
		targetPod("v478-r4q4-router-1", "router", "node-2"),
		targetPod("v478-r4q4-worker-0", "worker", ""))

	result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(warmup)})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	after := verify478Get(t, r, warmup.Name)
	if after.Status.Phase == workloadsv1alpha2.WarmupJobPhaseCompleted {
		t.Fatalf("R4-Q4: a Pending worker Pod must not be masked by surplus router Pods "+
			"(phase=%q, conditions=%#v)", after.Status.Phase, after.Status.Conditions)
	}
	if cond := apimeta.FindStatusCondition(after.Status.Conditions, "TargetReady"); cond == nil ||
		cond.Status != metav1.ConditionFalse || cond.Reason != "TargetPodsNotScheduled" {
		t.Fatalf("R4-Q4: expected TargetReady=False/TargetPodsNotScheduled, got %#v", after.Status.Conditions)
	}
	if result.RequeueAfter == 0 {
		t.Fatalf("R4-Q4: the Warmup must requeue while a selected role is not ready")
	}
	pods := &corev1.PodList{}
	if err := r.List(ctx, pods, client.MatchingLabels{LabelWarmupName: warmup.Name}); err != nil {
		t.Fatalf("list warmup pods: %v", err)
	}
	if len(pods.Items) != 0 {
		t.Fatalf("R4-Q4: no warmup Pod may start while a selected role is not ready, got %d", len(pods.Items))
	}
}

// N4 (round 3, contract — the round-2 F4b repro) — when every selected target Pod
// is momentarily unscheduled mid-run (eviction, node drain, recreate gap), the
// Warmup must keep waiting instead of completing as NoNodesMatched while its own
// Pod is still active, and the replacement node must be warmed once the target
// Pod reschedules. RED at the previous head 60b1b7cb; GREEN at b0915b21.
func TestVerifyN4AllTargetPodsUnscheduledMidRunWaitsAndWarmsReplacement(t *testing.T) {
	ctx := context.Background()
	warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{Name: "v478-n4", Namespace: "default", UID: "uid-v478-n4"},
		Spec:       verify478Spec("v478-n4-rbg"),
	}
	r := newWarmupReconciler(warmup, verify478RBG("v478-n4-rbg"),
		verify478TargetPod("v478-n4-rbg", "v478-n4-worker-0", "node-1"))

	// Creates the warmup Pod on node-1 (still active).
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(warmup)}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	pods := &corev1.PodList{}
	if err := r.List(ctx, pods, client.MatchingLabels{LabelWarmupName: warmup.Name}); err != nil {
		t.Fatalf("list warmup pods: %v", err)
	}
	if len(pods.Items) != 1 {
		t.Fatalf("expected 1 active warmup Pod, got %d", len(pods.Items))
	}

	// Eviction / drain / recreate gap: the target Pod loses its node assignment.
	gap := verify478TargetPod("v478-n4-rbg", "v478-n4-worker-0", "node-1")
	gap.Spec.NodeName = ""
	if err := r.Update(ctx, gap); err != nil {
		t.Fatalf("unschedule target pod: %v", err)
	}

	result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(warmup)})
	if err != nil {
		t.Fatalf("reconcile during gap: %v", err)
	}
	after := verify478Get(t, r, warmup.Name)
	if after.Status.Phase != workloadsv1alpha2.WarmupJobPhaseRunning {
		t.Fatalf("N4/F4b: a mid-run scheduling gap must not complete the Warmup, got %q "+
			"(conditions=%#v, active=%d)", after.Status.Phase, after.Status.Conditions, after.Status.Active)
	}
	if after.Status.Active != 1 {
		t.Fatalf("N4/F4b: Active must keep accounting the running Pod, got %d", after.Status.Active)
	}
	if cond := apimeta.FindStatusCondition(after.Status.Conditions, "TargetReady"); cond == nil ||
		cond.Status != metav1.ConditionFalse || cond.Reason != "TargetPodsNotScheduled" {
		t.Fatalf("N4: expected TargetReady=False/TargetPodsNotScheduled, got %#v", after.Status.Conditions)
	}
	if result.RequeueAfter == 0 {
		t.Fatalf("N4/F4b: the Warmup must requeue to observe the replacement target Pod")
	}

	// The replacement target Pod lands on a new node; it must be warmed.
	replacement := verify478TargetPod("v478-n4-rbg", "v478-n4-worker-1", "node-2")
	if err := r.Create(ctx, replacement); err != nil {
		t.Fatalf("create replacement target pod: %v", err)
	}
	stale := verify478TargetPod("v478-n4-rbg", "v478-n4-worker-0", "")
	if err := r.Delete(ctx, stale); err != nil {
		t.Fatalf("delete pending target pod: %v", err)
	}
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(warmup)}); err != nil {
		t.Fatalf("reconcile after replacement scheduled: %v", err)
	}
	replacementPods := &corev1.PodList{}
	if err := r.List(ctx, replacementPods, client.MatchingLabels{
		LabelWarmupName: warmup.Name, LabelNodeName: "node-2"}); err != nil {
		t.Fatalf("list replacement warmup pods: %v", err)
	}
	if len(replacementPods.Items) != 1 {
		t.Fatalf("N4/F4b: the replacement node must be warmed, got %d warmup Pods on node-2",
			len(replacementPods.Items))
	}
}

// N5 (round 3, contract — the round-2 F5 repro) — an active warmup Pod on a node
// that left the desired set keeps the job non-terminal, so the creation-anchored
// global timeout must still fire: the job fails and the orphan Pod is deleted,
// instead of being wedged in Running forever with no requeue.
// RED at the previous head 60b1b7cb; GREEN at b0915b21.
func TestVerifyN5OrphanActivePodOnDepartedNodeFailsOnGlobalTimeout(t *testing.T) {
	ctx := context.Background()
	warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "v478-n5",
			Namespace:         "default",
			UID:               "uid-v478-n5",
			CreationTimestamp: metav1.NewTime(time.Now().Add(-10 * time.Minute)),
		},
		Spec: verify478Spec("v478-n5-rbg"),
	}
	warmup.Spec.Policies = &workloadsv1alpha2.WarmupPolicies{GlobalTimeoutSeconds: ptr.To(int64(60))}

	// Desired is {node-2} (the target Pod migrated there); node-2 is terminal and
	// node-1's warmup Pod is stuck active (e.g. ImagePullBackOff).
	succeeded := makeWarmupPod("v478-n5-pod-ok", "default", warmup.Name, string(warmup.UID), "node-2", corev1.PodSucceeded)
	stuck := makeWarmupPod("v478-n5-pod-stuck", "default", warmup.Name, string(warmup.UID), "node-1", corev1.PodRunning)
	r := newWarmupReconciler(warmup, verify478RBG("v478-n5-rbg"),
		verify478TargetPod("v478-n5-rbg", "v478-n5-worker-0", "node-2"),
		succeeded, stuck)

	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(warmup)}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	after := verify478Get(t, r, warmup.Name)
	if after.Status.Phase != workloadsv1alpha2.WarmupJobPhaseFailed {
		t.Fatalf("N5/F5: an active Pod on a departed node must not defeat the expired global timeout; "+
			"expected Failed/GlobalTimeoutExceeded, got %q (conditions=%#v, active=%d)",
			after.Status.Phase, after.Status.Conditions, after.Status.Active)
	}
	if cond := apimeta.FindStatusCondition(after.Status.Conditions, "Failed"); cond == nil ||
		cond.Reason != "GlobalTimeoutExceeded" {
		t.Fatalf("N5/F5: expected Failed/GlobalTimeoutExceeded, got %#v", after.Status.Conditions)
	}
	err := r.Get(ctx, types.NamespacedName{Name: stuck.Name, Namespace: "default"}, &corev1.Pod{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("N5/F5: the orphan active Pod must be deleted on global timeout, got err=%v", err)
	}
}

// N2 (round 2, contract) — the global timeout is anchored at the Warmup's
// creation: once the budget has lapsed, the Warmup fails terminally even if the
// target became ready in the meantime, and no warmup Pod is created after the
// deadline.
func TestVerifyN2CreationAnchoredTimeoutFailsEvenWithReadyTarget(t *testing.T) {
	ctx := context.Background()
	warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "v478-n2",
			Namespace:         "default",
			UID:               "uid-v478-n2",
			CreationTimestamp: metav1.NewTime(time.Now().Add(-10 * time.Minute)),
		},
		Spec: verify478Spec("v478-n2-rbg"),
	}
	warmup.Spec.Policies = &workloadsv1alpha2.WarmupPolicies{GlobalTimeoutSeconds: ptr.To(int64(60))}
	// The target exists and is fully ready.
	r := newWarmupReconciler(warmup, verify478RBG("v478-n2-rbg"),
		verify478TargetPod("v478-n2-rbg", "v478-n2-worker-0", "node-1"))

	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(warmup)}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	after := verify478Get(t, r, warmup.Name)
	if after.Status.Phase != workloadsv1alpha2.WarmupJobPhaseFailed {
		t.Fatalf("N2: an expired creation-anchored timeout must fail the Warmup even with a ready "+
			"target, got %q (conditions=%#v)", after.Status.Phase, after.Status.Conditions)
	}
	if cond := apimeta.FindStatusCondition(after.Status.Conditions, "Failed"); cond == nil ||
		cond.Reason != "GlobalTimeoutExceeded" {
		t.Fatalf("N2: expected Failed/GlobalTimeoutExceeded, got %#v", after.Status.Conditions)
	}
	pods := &corev1.PodList{}
	if err := r.List(ctx, pods, client.MatchingLabels{LabelWarmupName: warmup.Name}); err != nil {
		t.Fatalf("list warmup pods: %v", err)
	}
	if len(pods.Items) != 0 {
		t.Fatalf("N2: no warmup Pod must be created after the deadline, got %d", len(pods.Items))
	}
}

// N3 (round 2, canary — RED while finding F6 is present) — warmupReachedTerminalState
// counts succeeded/failed nodes that are no longer desired toward
// len(terminalNodes) >= len(desiredNodes). After the target's node set shrinks on
// one node and grows on another (rolling-update churn), a succeeded warmup Pod on
// the now-undesired node masks a desired node with no terminal warmup Pod, so the
// expired global timeout is skipped and a Pod is created after the deadline.
func TestVerifyN3ExpiredTimeoutNotBypassedByStaleSucceededNode(t *testing.T) {
	ctx := context.Background()
	warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "v478-n3",
			Namespace:         "default",
			UID:               "uid-v478-n3",
			CreationTimestamp: metav1.NewTime(time.Now()), // young while the work runs
		},
		Spec: verify478Spec("v478-n3-rbg"),
	}
	warmup.Spec.Policies = &workloadsv1alpha2.WarmupPolicies{GlobalTimeoutSeconds: ptr.To(int64(60))}
	// Two scheduled target pods: nodes A and C.
	r := newWarmupReconciler(warmup, verify478RBG("v478-n3-rbg"),
		verify478TargetPod("v478-n3-rbg", "v478-n3-worker-0", "node-a"),
		verify478TargetPod("v478-n3-rbg", "v478-n3-worker-1", "node-c"))

	// Creates warmup pods for node-a and node-c (parallelism unset => all at once).
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(warmup)}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	pods := &corev1.PodList{}
	if err := r.List(ctx, pods, client.MatchingLabels{LabelWarmupName: warmup.Name}); err != nil {
		t.Fatalf("list warmup pods: %v", err)
	}
	if len(pods.Items) != 2 {
		t.Fatalf("expected 2 warmup Pods (node-a, node-c), got %d", len(pods.Items))
	}
	// Both succeed while the target still has pods on node-a and node-c.
	for i := range pods.Items {
		succeeded := pods.Items[i].DeepCopy()
		succeeded.Status.Phase = corev1.PodSucceeded
		if err := r.Status().Update(ctx, succeeded); err != nil {
			t.Fatalf("mark warmup Pod succeeded: %v", err)
		}
	}

	// Target churn: the pod on node-c is replaced by one on node-b (rolling update).
	// The succeeded warmup Pod on node-c STAYS (it is the stale terminal pod that
	// must not mask the un-warmed node-b).
	stale := verify478TargetPod("v478-n3-rbg", "v478-n3-worker-1", "node-c")
	if err := r.Delete(ctx, stale); err != nil {
		t.Fatalf("delete target pod on node-c: %v", err)
	}
	if err := r.Create(ctx, verify478TargetPod("v478-n3-rbg", "v478-n3-worker-2", "node-b")); err != nil {
		t.Fatalf("create target pod on node-b: %v", err)
	}

	// Age the Warmup past its 60s global deadline (the fake client stores the
	// metadata update, unlike a real API server — this is harness-only time travel).
	aged := verify478Get(t, r, warmup.Name)
	aged.CreationTimestamp = metav1.NewTime(time.Now().Add(-10 * time.Minute))
	if err := r.Update(ctx, aged); err != nil {
		t.Fatalf("age warmup past deadline: %v", err)
	}

	// The creation-anchored timeout (60s) expired long ago. Desired is now
	// {node-a, node-b}; node-b has no terminal warmup Pod, so the job has NOT
	// reached a terminal state and the deadline must be enforced.
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(warmup)}); err != nil {
		t.Fatalf("reconcile after churn: %v", err)
	}
	after := verify478Get(t, r, warmup.Name)
	if after.Status.Phase != workloadsv1alpha2.WarmupJobPhaseFailed {
		t.Fatalf("N3/F6: the expired global timeout must not be bypassed by a succeeded Pod on a "+
			"no-longer-desired node; expected Failed/GlobalTimeoutExceeded, got %q (conditions=%#v, desired=%d, succeeded=%d)",
			after.Status.Phase, after.Status.Conditions, after.Status.Desired, after.Status.Succeeded)
	}
}

// R4-F10 (round 4, contract — NEW finding, empirically confirmed) — a mid-run
// target collapse must not erase the per-node customized-action diagnostics
// while the Warmup is still Running (and on its within-tolerance settle).
// evaluateCustomizedActionResults/preserveCustomizedActionResults scope
// everything to the CURRENT desiredNodes map, so once the target disappears the
// recorded customizedActionResults and the CustomizedAction conditions are
// dropped even though the warmup Pods (and their per-container outcomes) are
// still live. Base preserves the diagnostics (the terminal failure path keeps
// them, pinned by the repo's own
// TestFailWarmupJobPreservesCustomizedActionDiagnosticsWithoutDesiredNodes);
// the collapsed-settle path introduced by this PR erases them.
// RED at b0915b21 and at 92cb7ef; GREEN at the merge base.
func TestVerifyR4F10CollapseKeepsCustomizedActionDiagnostics(t *testing.T) {
	ctx := context.Background()
	warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "v478-r4f10",
			Namespace: "default",
			UID:       "uid-v478-r4f10",
		},
		Spec: workloadsv1alpha2.RoleBasedGroupWarmupSpec{
			TargetRoleBasedGroup: &workloadsv1alpha2.TargetRoleBasedGroup{
				Name: "v478-r4f10-rbg",
				Roles: map[string]workloadsv1alpha2.WarmupActions{
					"worker": {CustomizedAction: &workloadsv1alpha2.CustomizedAction{
						Containers: []corev1.Container{{Name: "node-check", Image: "busybox"}},
					}},
				},
			},
		},
	}
	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "v478-r4f10-rbg", Namespace: "default"},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{
			Roles: []workloadsv1alpha2.RoleSpec{{Name: "worker", Replicas: ptr.To(int32(1))}},
		},
	}
	targetPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "v478-r4f10-worker-0",
			Namespace: "default",
			Labels: map[string]string{
				constants.GroupNameLabelKey: "v478-r4f10-rbg",
				constants.RoleNameLabelKey:  "worker",
			},
		},
		Spec: corev1.PodSpec{NodeName: "node-1"},
	}
	r := newWarmupReconciler(warmup, rbg, targetPod)

	// Start: the warmup Pod is created on node-1 with a running container.
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(warmup)}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	pods := &corev1.PodList{}
	if err := r.List(ctx, pods, client.MatchingLabels{LabelWarmupName: warmup.Name}); err != nil {
		t.Fatalf("list warmup pods: %v", err)
	}
	if len(pods.Items) != 1 {
		t.Fatalf("expected 1 warmup Pod, got %d", len(pods.Items))
	}
	wp := pods.Items[0].DeepCopy()
	wp.Status.Phase = corev1.PodRunning
	wp.Status.ContainerStatuses = []corev1.ContainerStatus{{
		Name:  "custom-0",
		State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
	}}
	if err := r.Status().Update(ctx, wp); err != nil {
		t.Fatalf("update warmup pod status: %v", err)
	}
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(warmup)}); err != nil {
		t.Fatalf("reconcile after container start: %v", err)
	}
	mid := verify478Get(t, r, warmup.Name)
	if len(mid.Status.CustomizedActionResults) == 0 {
		t.Fatalf("R4-F10 precondition: expected customizedActionResults recorded while running, got %#v",
			mid.Status.CustomizedActionResults)
	}

	// The target RBG and its Pod vanish mid-run; the warmup Pod keeps running.
	if err := r.Delete(ctx, targetPod); err != nil {
		t.Fatalf("delete target pod: %v", err)
	}
	if err := r.Delete(ctx, rbg); err != nil {
		t.Fatalf("delete rbg: %v", err)
	}
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(warmup)}); err != nil {
		t.Fatalf("reconcile after collapse: %v", err)
	}
	collapsed := verify478Get(t, r, warmup.Name)
	if len(collapsed.Status.CustomizedActionResults) == 0 {
		t.Fatalf("R4-F10: the collapse must not erase customizedActionResults while the job is still "+
			"Running (phase=%q active=%d conditions=%#v)", collapsed.Status.Phase, collapsed.Status.Active,
			collapsed.Status.Conditions)
	}
}
