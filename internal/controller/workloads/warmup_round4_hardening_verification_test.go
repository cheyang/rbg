package workloads

// Round-4 (head 92cb7ef1) CONTRACT tests for the two hardening changes in
// 3bd283a5 / 92cb7ef1:
//   Q4 -> per-role target readiness deficits (countTargetPodsNotReady)
//   Q2 -> TargetReady=Unknown (not False) once a started job's target is gone
// Both are red at b0915b21 and green at 92cb7ef1 (results/unit-round4-*.txt).

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// Q4 contract: surplus scheduled Pods in one selected role must not mask a
// missing/unscheduled Pod in another selected role. Fixture (Codex): router
// expects 1 Pod and has 2 scheduled (rollout surge), worker expects 1 and has
// 1 Pending (no node). Aggregate arithmetic (expected 2 - scheduled 2 = 0)
// passes the gate at b0915b21; per-role accounting must see worker's deficit.
// POLARITY: CONTRACT.
func TestVerify_CrossRoleSurplusDoesNotMaskPendingPod(t *testing.T) {
	warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{
			Name: "q4-cross-role", Namespace: "default", UID: "uid-q4-cross-role",
			CreationTimestamp: metav1.NewTime(time.Now().Add(-1 * time.Minute)),
		},
		Spec: workloadsv1alpha2.RoleBasedGroupWarmupSpec{
			TargetRoleBasedGroup: &workloadsv1alpha2.TargetRoleBasedGroup{
				Name: "q4-rbg",
				Roles: map[string]workloadsv1alpha2.WarmupActions{
					"router": {ImagePreload: &workloadsv1alpha2.ImagePreloadAction{Images: []string{"busybox:1.36"}}},
					"worker": {ImagePreload: &workloadsv1alpha2.ImagePreloadAction{Images: []string{"busybox:1.36"}}},
				},
			},
		},
	}
	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "q4-rbg", Namespace: "default"},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{
			Roles: []workloadsv1alpha2.RoleSpec{
				{Name: "router", Replicas: ptr.To(int32(1))},
				{Name: "worker", Replicas: ptr.To(int32(1))},
			},
		},
	}
	r := newWarmupReconciler(
		warmup, rbg,
		verifyTargetPod("q4-router-0", "q4-rbg", "router", "node-1"),
		verifyTargetPod("q4-router-1", "q4-rbg", "router", "node-2"), // surplus
		verifyTargetPod("q4-worker-0", "q4-rbg", "worker", ""),       // pending
	)

	res := reconcileOnce(t, r, warmup)
	updated := getWarmup(t, r, warmup)

	if updated.Status.Phase == workloadsv1alpha2.WarmupJobPhaseCompleted {
		t.Fatalf("a Pending worker Pod must not be masked by surplus router Pods: phase=Completed (conditions=%#v)",
			updated.Status.Conditions)
	}
	cond := apimeta.FindStatusCondition(updated.Status.Conditions, ConditionTargetReady)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != "TargetPodsNotScheduled" {
		t.Fatalf("expected TargetReady=False/TargetPodsNotScheduled, got %#v", updated.Status.Conditions)
	}
	if res.RequeueAfter != TargetWaitRequeuePeriod {
		t.Fatalf("expected the target-wait requeue %v, got %#v", TargetWaitRequeuePeriod, res)
	}
	pods := &corev1.PodList{}
	if err := r.List(vctx(), pods, client.InNamespace("default"),
		client.MatchingLabels{LabelWarmupName: warmup.Name}); err != nil {
		t.Fatalf("list warmup pods: %v", err)
	}
	if len(pods.Items) != 0 {
		t.Fatalf("no warmup Pod may start while a selected role is not ready, got %d", len(pods.Items))
	}
}

// Q2 contract: a started job whose target disappears settles terminally with
// TargetReady=Unknown/RoleBasedGroupNotFound, not the contradictory
// Complete=True + TargetReady=False pairing of b0915b21. POLARITY: CONTRACT.
func TestVerify_TerminalSettleAfterTargetDeletionRecordsTargetReadyUnknown(t *testing.T) {
	warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{
			Name: "q2-unknown", Namespace: "default", UID: types.UID("uid-q2-unknown"),
			CreationTimestamp: metav1.NewTime(time.Now().Add(-3 * time.Minute)),
		},
		Spec: verifySpec("q2-unknown-rbg"),
	}
	warmup.Status.Phase = workloadsv1alpha2.WarmupJobPhaseRunning
	started := metav1.NewTime(time.Now().Add(-3 * time.Minute))
	warmup.Status.StartTime = &started

	r := newWarmupReconciler(
		warmup,
		makeWarmupPod("q2-unknown-a", "default", "q2-unknown", "uid-q2-unknown", "node-a", corev1.PodSucceeded),
	)

	reconcileOnce(t, r, warmup)
	updated := getWarmup(t, r, warmup)

	if updated.Status.Phase != workloadsv1alpha2.WarmupJobPhaseCompleted {
		t.Fatalf("expected Completed after all attempted nodes succeeded, got %q (conditions=%#v)",
			updated.Status.Phase, updated.Status.Conditions)
	}
	cond := apimeta.FindStatusCondition(updated.Status.Conditions, ConditionComplete)
	if cond == nil || cond.Reason != "WarmupCompleted" {
		t.Fatalf("expected Complete/WarmupCompleted, got %#v", updated.Status.Conditions)
	}
	tr := apimeta.FindStatusCondition(updated.Status.Conditions, ConditionTargetReady)
	if tr == nil || tr.Status != metav1.ConditionUnknown || tr.Reason != "RoleBasedGroupNotFound" {
		t.Fatalf("expected TargetReady=Unknown/RoleBasedGroupNotFound on the terminal settle, got %#v",
			updated.Status.Conditions)
	}
	if updated.Status.StartTime == nil || !updated.Status.StartTime.Time.Equal(started.Time) {
		t.Fatalf("startTime must be preserved, was %v now %v", started, updated.Status.StartTime)
	}
}
