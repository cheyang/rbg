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

// Verification harness for the review of PR #478 (reviewer B / codex).
// Additive only: no production code is modified by this file.
//
// Polarity legend (see docs/verification/warmup-target-waits/README.md):
//   - contract: asserts the intended correct behavior; FAILS on the code under
//     review when the finding reproduces.
//   - canary: asserts the currently observed (questionable) behavior; PASSES on
//     the code under review and must be inverted once the behavior is fixed.

package workloads

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func vctx() context.Context {
	return ctrl.LoggerInto(context.Background(), zap.New(zap.UseDevMode(true)))
}

// verifySpec targets one role of a RoleBasedGroup with a trivial image preload.
func verifySpec(rbgName string) workloadsv1alpha2.RoleBasedGroupWarmupSpec {
	return workloadsv1alpha2.RoleBasedGroupWarmupSpec{
		TargetRoleBasedGroup: &workloadsv1alpha2.TargetRoleBasedGroup{
			Name: rbgName,
			Roles: map[string]workloadsv1alpha2.WarmupActions{
				"worker": {ImagePreload: &workloadsv1alpha2.ImagePreloadAction{Images: []string{"busybox:1.36"}}},
			},
		},
	}
}

func verifyRBG(name string) *workloadsv1alpha2.RoleBasedGroup {
	return &workloadsv1alpha2.RoleBasedGroup{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"}}
}

func verifyTargetPod(name, rbg, role, node string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: "default",
			Labels: map[string]string{constants.GroupNameLabelKey: rbg, constants.RoleNameLabelKey: role},
		},
		Spec: corev1.PodSpec{NodeName: node},
	}
}

func reconcileOnce(t *testing.T, r *RoleBasedGroupWarmupReconciler, warmup *workloadsv1alpha2.RoleBasedGroupWarmup) ctrl.Result {
	t.Helper()
	res, err := r.Reconcile(vctx(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(warmup)})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	return res
}

func getWarmup(t *testing.T, r *RoleBasedGroupWarmupReconciler, warmup *workloadsv1alpha2.RoleBasedGroupWarmup) *workloadsv1alpha2.RoleBasedGroupWarmup {
	t.Helper()
	updated := &workloadsv1alpha2.RoleBasedGroupWarmup{}
	if err := r.Get(vctx(), client.ObjectKeyFromObject(warmup), updated); err != nil {
		t.Fatalf("get warmup: %v", err)
	}
	return updated
}

// ---------------------------------------------------------------------------
// F1a (contract): a Warmup whose desired-node work has fully reached a
// terminal state must be able to complete even if a *new* selected-role target
// Pod is currently pending (e.g. target scale-up or a recreated Pod after a
// node failure). On the code under review the pending-Pod gate runs before the
// completion accounting in updateStatus, so completion is unreachable while
// any selected-role Pod is unscheduled - the job stalls in Running forever
// when no globalTimeoutSeconds is set.
// ---------------------------------------------------------------------------
func TestVerify_MidWarmupPendingPodBlocksCompletion(t *testing.T) {
	newDoneWarmup := func(t *testing.T, name string, withPendingPod bool) *RoleBasedGroupWarmupReconciler {
		warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", UID: types.UID("uid-" + name)},
			Spec:       verifySpec(name + "-rbg"),
		}
		r := newWarmupReconciler(warmup, verifyRBG(name+"-rbg"),
			verifyTargetPod(name+"-worker-0", name+"-rbg", "worker", "node-1"))

		// Reconcile 1: create the warmup Pod for node-1.
		reconcileOnce(t, r, warmup)

		// The warmup Pod succeeds.
		pods := &corev1.PodList{}
		if err := r.List(vctx(), pods, client.MatchingLabels{LabelWarmupName: name}); err != nil {
			t.Fatalf("list warmup pods: %v", err)
		}
		if len(pods.Items) != 1 {
			t.Fatalf("expected 1 warmup pod, got %d", len(pods.Items))
		}
		done := &pods.Items[0]
		done.Status.Phase = corev1.PodSucceeded
		if err := r.Status().Update(vctx(), done); err != nil {
			t.Fatalf("mark warmup pod succeeded: %v", err)
		}

		if withPendingPod {
			// A new selected-role Pod appears unscheduled (scale-up / recreation).
			if err := r.Create(vctx(), verifyTargetPod(name+"-worker-1", name+"-rbg", "worker", "")); err != nil {
				t.Fatalf("create pending target pod: %v", err)
			}
		}
		return r
	}

	t.Run("control: completes when no pending pod interferes", func(t *testing.T) {
		warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
			ObjectMeta: metav1.ObjectMeta{Name: "f1-control", Namespace: "default"},
		}
		r := newDoneWarmup(t, "f1-control", false)
		reconcileOnce(t, r, warmup)
		updated := getWarmup(t, r, warmup)
		if updated.Status.Phase != workloadsv1alpha2.WarmupJobPhaseCompleted {
			t.Fatalf("control: expected Completed, got %q (conditions=%#v)", updated.Status.Phase, updated.Status.Conditions)
		}
	})

	t.Run("contract: all work done must still complete with a pending selected pod", func(t *testing.T) {
		warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
			ObjectMeta: metav1.ObjectMeta{Name: "f1-stall", Namespace: "default"},
		}
		r := newDoneWarmup(t, "f1-stall", true)
		// Reconcile repeatedly: completion must not depend on how often we requeue.
		for i := 0; i < 3; i++ {
			reconcileOnce(t, r, warmup)
		}
		updated := getWarmup(t, r, warmup)
		if updated.Status.Phase != workloadsv1alpha2.WarmupJobPhaseCompleted {
			t.Fatalf("F1 reproduced: warmup with all desired nodes succeeded cannot complete while a selected target pod is pending; phase=%q conditions=%#v",
				updated.Status.Phase, updated.Status.Conditions)
		}
	})
}

// ---------------------------------------------------------------------------
// F1b (canary): with globalTimeoutSeconds set, the same mid-warmup pending Pod
// eventually turns the fully-warmed job into a terminal
// Failed/GlobalTimeoutExceeded ("timed out waiting for N selected target
// Pod(s)"), even though 1/1 desired nodes succeeded. Passes on the code under
// review; must flip once the gate no longer blocks terminal accounting.
// ---------------------------------------------------------------------------
func TestVerify_MidWarmupPendingPodFailsCompletedWorkOnTimeout(t *testing.T) {
	warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{Name: "f1-timeout", Namespace: "default", UID: "uid-f1-timeout"},
		Spec:       verifySpec("f1-timeout-rbg"),
	}
	warmup.Spec.Policies = &workloadsv1alpha2.WarmupPolicies{GlobalTimeoutSeconds: ptr.To(int64(60))}
	r := newWarmupReconciler(warmup, verifyRBG("f1-timeout-rbg"),
		verifyTargetPod("f1-timeout-worker-0", "f1-timeout-rbg", "worker", "node-1"))

	reconcileOnce(t, r, warmup) // creates the warmup Pod, sets startTime

	pods := &corev1.PodList{}
	if err := r.List(vctx(), pods, client.MatchingLabels{LabelWarmupName: "f1-timeout"}); err != nil {
		t.Fatalf("list warmup pods: %v", err)
	}
	if len(pods.Items) != 1 {
		t.Fatalf("expected 1 warmup pod, got %d", len(pods.Items))
	}
	done := &pods.Items[0]
	done.Status.Phase = corev1.PodSucceeded
	if err := r.Status().Update(vctx(), done); err != nil {
		t.Fatalf("mark warmup pod succeeded: %v", err)
	}
	if err := r.Create(vctx(), verifyTargetPod("f1-timeout-worker-1", "f1-timeout-rbg", "worker", "")); err != nil {
		t.Fatalf("create pending target pod: %v", err)
	}

	// Let the global timeout lapse while the gate blocks completion.
	cur := getWarmup(t, r, warmup)
	if cur.Status.StartTime == nil {
		t.Fatalf("expected startTime to be set once a Pod exists")
	}
	cur.Status.StartTime = &metav1.Time{Time: time.Now().Add(-2 * time.Minute)}
	if err := r.Status().Update(vctx(), cur); err != nil {
		t.Fatalf("backdate startTime: %v", err)
	}

	reconcileOnce(t, r, warmup)
	updated := getWarmup(t, r, warmup)
	if updated.Status.Phase != workloadsv1alpha2.WarmupJobPhaseFailed {
		t.Fatalf("canary: expected Failed/GlobalTimeoutExceeded for the blocked job, got %q (%#v)",
			updated.Status.Phase, updated.Status.Conditions)
	}
	cond := apimeta.FindStatusCondition(updated.Status.Conditions, "Failed")
	if cond == nil || cond.Reason != "GlobalTimeoutExceeded" {
		t.Fatalf("canary: expected Failed/GlobalTimeoutExceeded, got %#v", updated.Status.Conditions)
	}
	// The failure must come from the target-wait gate, not the generic run-phase
	// timeout: that is what makes this a canary for F1 specifically.
	if !strings.Contains(cond.Message, "timed out waiting for") {
		t.Fatalf("canary flipped: failure no longer comes from the target-wait gate: %q", cond.Message)
	}
	if updated.Status.Succeeded != 1 {
		t.Fatalf("canary: expected the succeeded node to be preserved (1), got %d", updated.Status.Succeeded)
	}
	t.Logf("canary observed: 1/1 nodes succeeded yet job is Failed: %q", cond.Message)
}

// ---------------------------------------------------------------------------
// F2 (canary): the wait-phase timeout clock and the run-phase timeout clock
// are different. While no Pod exists the wait is bounded from the Warmup's
// creation time; once the first Pod exists, status.startTime (set at Pod
// creation) takes over. Net effect: a Warmup can outlive its
// globalTimeoutSeconds by up to a factor of two measured from creation.
// Passes on the code under review; flips if the wait budget becomes
// creation-anchored for the whole job lifetime.
// ---------------------------------------------------------------------------
func TestVerify_WaitAndRunTimeoutBudgetsAreAdditive(t *testing.T) {
	warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{
			Name: "f2-budget", Namespace: "default", UID: "uid-f2-budget",
			// Created 120s ago, but the first (hypothetical) Pod was created 30s ago.
			CreationTimestamp: metav1.NewTime(time.Now().Add(-120 * time.Second)),
		},
		Spec: verifySpec("f2-rbg"),
	}
	warmup.Spec.Policies = &workloadsv1alpha2.WarmupPolicies{GlobalTimeoutSeconds: ptr.To(int64(60))}
	warmup.Status.StartTime = &metav1.Time{Time: time.Now().Add(-30 * time.Second)}

	r := newWarmupReconciler(warmup, verifyRBG("f2-rbg"), verifyTargetPod("f2-worker-0", "f2-rbg", "worker", ""))

	reconcileOnce(t, r, warmup)
	updated := getWarmup(t, r, warmup)
	if updated.Status.Phase == workloadsv1alpha2.WarmupJobPhaseFailed {
		t.Fatalf("canary flipped: job failed although startTime-anchored budget (30s of 60s) remains: %#v", updated.Status.Conditions)
	}
	cond := apimeta.FindStatusCondition(updated.Status.Conditions, ConditionTargetReady)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != "TargetPodsNotScheduled" {
		t.Fatalf("canary: expected TargetReady=False/TargetPodsNotScheduled, got %#v", updated.Status.Conditions)
	}
	t.Logf("canary observed: creation-anchored elapsed=120s already exceeds globalTimeoutSeconds=60, " +
		"yet the job keeps waiting because the wait clock uses startTime (30s ago) once a Pod existed")
}

// ---------------------------------------------------------------------------
// F3 (contract, passes on the code under review): coverage the PR itself
// lacks - the GlobalTimeoutExceeded branch for still-unscheduled target Pods.
// A Warmup whose wait outlives its creation-anchored global timeout fails
// terminally instead of waiting forever.
// ---------------------------------------------------------------------------
func TestVerify_PendingPodsFailAfterGlobalTimeout(t *testing.T) {
	warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{
			Name: "f3-timeout", Namespace: "default", UID: "uid-f3-timeout",
			CreationTimestamp: metav1.NewTime(time.Now().Add(-10 * time.Minute)),
		},
		Spec: verifySpec("f3-rbg"),
	}
	warmup.Spec.Policies = &workloadsv1alpha2.WarmupPolicies{GlobalTimeoutSeconds: ptr.To(int64(60))}
	r := newWarmupReconciler(warmup, verifyRBG("f3-rbg"), verifyTargetPod("f3-worker-0", "f3-rbg", "worker", ""))

	res := reconcileOnce(t, r, warmup)
	if res.RequeueAfter != 0 {
		t.Fatalf("expired wait must not requeue, got %#v", res)
	}
	updated := getWarmup(t, r, warmup)
	if updated.Status.Phase != workloadsv1alpha2.WarmupJobPhaseFailed {
		t.Fatalf("expected Failed after global timeout with pending target pods, got %q", updated.Status.Phase)
	}
	cond := apimeta.FindStatusCondition(updated.Status.Conditions, "Failed")
	if cond == nil || cond.Reason != "GlobalTimeoutExceeded" {
		t.Fatalf("expected Failed/GlobalTimeoutExceeded, got %#v", updated.Status.Conditions)
	}
}
