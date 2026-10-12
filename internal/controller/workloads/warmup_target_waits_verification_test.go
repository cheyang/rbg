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

// verifyRBGWithRole declares a role with an explicit replica count so the
// expected-Pod readiness gate (head 60b1b7cb) engages: expected =
// ComputeSubGroupSize(role) * replicas.
func verifyRBGWithRole(name, role string, replicas int32) *workloadsv1alpha2.RoleBasedGroup {
	return &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{
			Roles: []workloadsv1alpha2.RoleSpec{{Name: role, Replicas: ptr.To(replicas)}},
		},
	}
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
		r := newWarmupReconciler(warmup, verifyRBGWithRole(name+"-rbg", "worker", 1),
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
			// Scale-up: the target now expects 2 Pods; the new one is unscheduled.
			rbg := &workloadsv1alpha2.RoleBasedGroup{}
			if err := r.Get(vctx(), client.ObjectKey{Name: name + "-rbg", Namespace: "default"}, rbg); err != nil {
				t.Fatalf("get target rbg: %v", err)
			}
			rbg.Spec.Roles[0].Replicas = ptr.To(int32(2))
			if err := r.Update(vctx(), rbg); err != nil {
				t.Fatalf("scale target rbg: %v", err)
			}
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
// F1b (contract; INVERTED after head 60b1b7cb fixed the finding): a fully
// warmed job whose creation-anchored global timeout has already lapsed must
// COMPLETE, not fail, when a new selected-role target Pod is pending. On the
// originally reviewed head (9f61d4f7) this scenario ended in
// Failed/GlobalTimeoutExceeded with status.succeeded=1; the fixed code lets
// terminal accounting win.
// ---------------------------------------------------------------------------
func TestVerify_CompletedWorkSurvivesExpiredGlobalTimeout(t *testing.T) {
	warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{
			Name: "f1-timeout", Namespace: "default", UID: "uid-f1-timeout",
			// Creation-anchored budget (60s) is long past.
			CreationTimestamp: metav1.NewTime(time.Now().Add(-10 * time.Minute)),
		},
		Spec: verifySpec("f1-timeout-rbg"),
	}
	warmup.Spec.Policies = &workloadsv1alpha2.WarmupPolicies{GlobalTimeoutSeconds: ptr.To(int64(60))}
	r := newWarmupReconciler(warmup,
		verifyRBGWithRole("f1-timeout-rbg", "worker", 2),
		// node-1 was warmed and its warmup Pod succeeded; the scale-up Pod is pending.
		makeWarmupPod("f1-timeout-pod", "default", "f1-timeout", "uid-f1-timeout", "node-1", corev1.PodSucceeded),
		verifyTargetPod("f1-timeout-worker-0", "f1-timeout-rbg", "worker", "node-1"),
		verifyTargetPod("f1-timeout-worker-1", "f1-timeout-rbg", "worker", ""),
	)

	reconcileOnce(t, r, warmup)
	updated := getWarmup(t, r, warmup)
	if updated.Status.Phase == workloadsv1alpha2.WarmupJobPhaseFailed {
		t.Fatalf("regression: fully warmed job failed despite completed work: %#v", updated.Status.Conditions)
	}
	if updated.Status.Phase != workloadsv1alpha2.WarmupJobPhaseCompleted {
		t.Fatalf("fully warmed job must complete even past the deadline, got %q (%#v)",
			updated.Status.Phase, updated.Status.Conditions)
	}
	if updated.Status.Succeeded != 1 {
		t.Fatalf("expected the succeeded node to be preserved (1), got %d", updated.Status.Succeeded)
	}
}

// ---------------------------------------------------------------------------
// F2 (contract; INVERTED after head 60b1b7cb fixed the finding): the global
// timeout is a single budget anchored at the Warmup's creation time, covering
// both the target wait and the Pod run phase. A Warmup created 120s ago with
// globalTimeoutSeconds=60 must fail during the wait phase even if
// status.startTime says a Pod was created only 30s ago: the wait clock no
// longer restarts at first-Pod creation. On the originally reviewed head
// (9f61d4f7) this job kept waiting (additive budgets).
// ---------------------------------------------------------------------------
func TestVerify_GlobalTimeoutIsSingleCreationAnchoredBudget(t *testing.T) {
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

	r := newWarmupReconciler(warmup, verifyRBGWithRole("f2-rbg", "worker", 1),
		verifyTargetPod("f2-worker-0", "f2-rbg", "worker", ""))

	reconcileOnce(t, r, warmup)
	updated := getWarmup(t, r, warmup)
	if updated.Status.Phase != workloadsv1alpha2.WarmupJobPhaseFailed {
		t.Fatalf("creation-anchored budget (120s of 60s) is exhausted; job must fail instead of waiting on the startTime clock, got %q (%#v)",
			updated.Status.Phase, updated.Status.Conditions)
	}
	cond := apimeta.FindStatusCondition(updated.Status.Conditions, "Failed")
	if cond == nil || cond.Reason != "GlobalTimeoutExceeded" {
		t.Fatalf("expected Failed/GlobalTimeoutExceeded, got %#v", updated.Status.Conditions)
	}
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
	r := newWarmupReconciler(warmup, verifyRBGWithRole("f3-rbg", "worker", 1), verifyTargetPod("f3-worker-0", "f3-rbg", "worker", ""))

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
