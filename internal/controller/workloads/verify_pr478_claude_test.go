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

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
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

func verify478RBG(name string) *workloadsv1alpha2.RoleBasedGroup {
	return &workloadsv1alpha2.RoleBasedGroup{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"}}
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
	r := newWarmupReconciler(warmup, verify478RBG("v478-c4-rbg"), routerPending)

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
