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

// Verification harness, round 2 (head 60b1b7cb), for the review of PR #478
// (reviewer B / codex). Additive only: no production code is modified.
//
// These tests cover mid-run churn of the warmup's target while warmup Pods are
// still active. All three are CONTRACT tests: they assert the intended
// behavior and FAIL on the head under review when the finding reproduces.
// They also run against the previous head (9f61d4f7) and the merge base
// (35e5d029) for regression attribution.

package workloads

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// ---------------------------------------------------------------------------
// F4a (contract): the target RoleBasedGroup disappearing mid-run must not turn
// a Warmup with in-flight Pods into Completed/NoNodesMatched. On head
// 60b1b7cb the "target gone after Pods exist" path continues with
// targetRBG=nil and targetPods=nil, so getDesiredNodesToWarmup derives an
// empty desired set and updateStatus hits its desired==0 branch: the job is
// reported Completed with reason NoNodesMatched ("No nodes matched the target
// configuration") while status.Active still counts the running Pod. The
// completion is terminal, so the Pod is never accounted afterwards; with
// ttlSecondsAfterFinished set, the CR (and the running Pod via owner refs) is
// deleted while work is in flight.
// ---------------------------------------------------------------------------
func TestVerify_TargetDeletedMidRunWithActivePodsMustNotComplete(t *testing.T) {
	warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{Name: "f4-gone", Namespace: "default", UID: "uid-f4-gone"},
		Spec:       verifySpec("f4-gone-rbg"),
	}
	// The warmup Pod on node-1 is still pulling images; the target RBG is gone.
	r := newWarmupReconciler(warmup,
		makeWarmupPod("f4-gone-pod", "default", "f4-gone", "uid-f4-gone", "node-1", corev1.PodRunning))

	reconcileOnce(t, r, warmup)
	updated := getWarmup(t, r, warmup)
	if updated.Status.Phase == workloadsv1alpha2.WarmupJobPhaseCompleted {
		cond := apimeta.FindStatusCondition(updated.Status.Conditions, "Complete")
		t.Fatalf("F4a reproduced: in-flight warmup reported Completed (reason=%q message=%q, counters Active=%d Succeeded=%d) while its Pod is still running",
			condReason(cond), condMessage(cond), updated.Status.Active, updated.Status.Succeeded)
	}
}

// ---------------------------------------------------------------------------
// F4b (contract): the target still exists, but every selected-role Pod is
// momentarily unscheduled (eviction / node drain / Recreate-style rollout
// gap). Once any warmup Pod exists, the head-60b1b7cb readiness gate no
// longer blocks, desiredNodes collapses to empty (unscheduled Pods contribute
// no node), and updateStatus completes the job as NoNodesMatched - while a
// warmup Pod is still running, and before the replacement target Pod lands on
// a (cold) node that will then never be warmed, because a terminal Warmup is
// never reconciled again. The previous head held the gate regardless of
// existing Pods and simply waited.
// ---------------------------------------------------------------------------
func TestVerify_AllTargetPodsUnscheduledMidRunMustNotComplete(t *testing.T) {
	warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{Name: "f4-drain", Namespace: "default", UID: "uid-f4-drain"},
		Spec:       verifySpec("f4-drain-rbg"),
	}
	r := newWarmupReconciler(warmup,
		verifyRBGWithRole("f4-drain-rbg", "worker", 1),
		// The target Pod was evicted; its replacement is not scheduled yet.
		verifyTargetPod("f4-drain-worker-0", "f4-drain-rbg", "worker", ""),
		// The warmup Pod on node-1 is still pulling images.
		makeWarmupPod("f4-drain-pod", "default", "f4-drain", "uid-f4-drain", "node-1", corev1.PodRunning))

	reconcileOnce(t, r, warmup)
	updated := getWarmup(t, r, warmup)
	if updated.Status.Phase == workloadsv1alpha2.WarmupJobPhaseCompleted {
		cond := apimeta.FindStatusCondition(updated.Status.Conditions, "Complete")
		t.Fatalf("F4b reproduced: mid-run drain of all selected target Pods reported Completed (reason=%q message=%q, counters Active=%d Succeeded=%d)",
			condReason(cond), condMessage(cond), updated.Status.Active, updated.Status.Succeeded)
	}
}

// ---------------------------------------------------------------------------
// F5 (contract): the global timeout must still bound the job while ANY warmup
// Pod is active, including one on a node that is no longer desired (e.g. the
// target Pod moved off node-1 during a rolling update while the warmup Pod
// there is stuck in ImagePullBackOff). On head 60b1b7cb
// warmupReachedTerminalState only counts active Pods on *desired* nodes, so
// with desired={node-2} fully terminal the expired-timeout check is skipped,
// while updateStatus counts the orphan on node-1 as active and refuses to
// complete: the job is stuck in Running with no requeue (requeueForTimeout
// returns nothing once the deadline passed), the orphan Pod is never deleted,
// and the documented "all active Pods are deleted and the job is marked as
// Failed" contract is broken. Previous head (9f61d4f7) and base (35e5d029)
// enforced the timeout unconditionally.
// ---------------------------------------------------------------------------
func TestVerify_OrphanActivePodStillBoundedByGlobalTimeout(t *testing.T) {
	warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{
			Name: "f5-orphan", Namespace: "default", UID: "uid-f5-orphan",
			// Creation-anchored budget (60s) is long past.
			CreationTimestamp: metav1.NewTime(time.Now().Add(-10 * time.Minute)),
		},
		Spec: verifySpec("f5-orphan-rbg"),
	}
	warmup.Spec.Policies = &workloadsv1alpha2.WarmupPolicies{GlobalTimeoutSeconds: ptr.To(int64(60))}
	// Seed the run-phase anchor for the pre-60b1b7cb heads, whose timeout
	// clock starts at status.startTime.
	warmup.Status.StartTime = &metav1.Time{Time: time.Now().Add(-10 * time.Minute)}
	warmup.Status.Phase = workloadsv1alpha2.WarmupJobPhaseRunning

	r := newWarmupReconciler(warmup,
		verifyRBGWithRole("f5-orphan-rbg", "worker", 1),
		verifyTargetPod("f5-orphan-worker-0", "f5-orphan-rbg", "worker", "node-2"),
		// node-2 is warmed (desired and terminal)...
		makeWarmupPod("f5-orphan-done", "default", "f5-orphan", "uid-f5-orphan", "node-2", corev1.PodSucceeded),
		// ...while the warmup Pod on node-1 (no longer desired) is still running.
		makeWarmupPod("f5-orphan-stuck", "default", "f5-orphan", "uid-f5-orphan", "node-1", corev1.PodRunning))

	res := reconcileOnce(t, r, warmup)
	updated := getWarmup(t, r, warmup)
	if updated.Status.Phase != workloadsv1alpha2.WarmupJobPhaseFailed {
		t.Fatalf("F5 reproduced: expired global timeout did not fire while an orphan Pod is active; phase=%q active=%d conditions=%#v",
			updated.Status.Phase, updated.Status.Active, updated.Status.Conditions)
	}
	cond := apimeta.FindStatusCondition(updated.Status.Conditions, "Failed")
	if cond == nil || cond.Reason != "GlobalTimeoutExceeded" {
		t.Fatalf("expected Failed/GlobalTimeoutExceeded, got %#v", updated.Status.Conditions)
	}
	if res.RequeueAfter != 0 {
		t.Fatalf("a terminal failure must not requeue, got %#v", res)
	}
	// The documented timeout contract deletes active Pods.
	stuck := &corev1.Pod{}
	err := r.Get(vctx(), types.NamespacedName{Name: "f5-orphan-stuck", Namespace: "default"}, stuck)
	if err == nil {
		t.Fatalf("the active orphan Pod must be deleted on global timeout, but it still exists")
	}
}

func condReason(cond *metav1.Condition) string {
	if cond == nil {
		return "<none>"
	}
	return cond.Reason
}

func condMessage(cond *metav1.Condition) string {
	if cond == nil {
		return "<none>"
	}
	return cond.Message
}
