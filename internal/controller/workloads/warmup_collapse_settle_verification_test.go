package workloads

// Round-4 (head 92cb7ef1) CONTRACT tests for the collapsed-desired-set settle
// path (failCollapsedDesiredSet / updateEmptyDesiredPhase). Round 3 pinned the
// old semantics with a CANARY (any unsucceeded node failed the job without
// consulting maxFailedNodes). At 92cb7ef1 the canary flipped: the settle now
// honors maxFailedNodes, so this file promotes the contract form.
//
// Differential: at b0915b21 every leg here is RED (Failed/InvalidTarget or
// Failed/TargetNoLongerSelectsNodes regardless of tolerance); at 92cb7ef1 all
// legs are GREEN. See results/unit-round4-*.txt.

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// F9 contract: the collapsed-desired-set settle honors maxFailedNodes exactly
// like the non-empty-desired-set path. Fixture (Codex, distinct from the PR's
// TestReconcile_CollapsedDesiredSetHonorsMaxFailedNodes): node-a Succeeded,
// node-b Failed with backoffLimitPerNode=0 (one attempt, final), the settle
// reconcile runs with an empty desired set (target deleted, or target scaled
// to zero). POLARITY: CONTRACT.
func TestVerify_CollapsedSettleHonorsMaxFailedNodesTolerance(t *testing.T) {
	tests := []struct {
		name string
		// nil policies -> maxFailedNodes unset (KEP: any number tolerated)
		policies *workloadsv1alpha2.WarmupPolicies
		// targetAlive=true keeps a replicas=0 target RBG (the "no longer
		// selects any node" variant); false exercises target.deleted.
		targetAlive bool
		wantPhase   workloadsv1alpha2.WarmupJobPhase
		wantReason  string // on the Complete or Failed condition
		wantFailed  int32
	}{
		{
			name:        "within tolerance (maxFailedNodes=1) and target deleted completes",
			policies:    &workloadsv1alpha2.WarmupPolicies{BackoffLimitPerNode: ptr.To(int32(0)), MaxFailedNodes: ptr.To(int32(1))},
			targetAlive: false,
			wantPhase:   workloadsv1alpha2.WarmupJobPhaseCompleted,
			wantReason:  "WarmupCompleted",
			wantFailed:  1,
		},
		{
			name:        "within tolerance and target scaled to zero completes",
			policies:    &workloadsv1alpha2.WarmupPolicies{BackoffLimitPerNode: ptr.To(int32(0)), MaxFailedNodes: ptr.To(int32(1))},
			targetAlive: true,
			wantPhase:   workloadsv1alpha2.WarmupJobPhaseCompleted,
			wantReason:  "WarmupCompleted",
			wantFailed:  1,
		},
		{
			name:        "unset maxFailedNodes tolerates the failed attempt",
			policies:    nil,
			targetAlive: false,
			wantPhase:   workloadsv1alpha2.WarmupJobPhaseCompleted,
			wantReason:  "WarmupCompleted",
			wantFailed:  1,
		},
		{
			name:        "beyond tolerance (maxFailedNodes=0) fails MaxFailedNodesExceeded",
			policies:    &workloadsv1alpha2.WarmupPolicies{BackoffLimitPerNode: ptr.To(int32(0)), MaxFailedNodes: ptr.To(int32(0))},
			targetAlive: false,
			wantPhase:   workloadsv1alpha2.WarmupJobPhaseFailed,
			wantReason:  "MaxFailedNodesExceeded",
			wantFailed:  1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "f9-contract", Namespace: "default", UID: "uid-f9-contract",
					CreationTimestamp: metav1.NewTime(time.Now().Add(-2 * time.Minute)),
				},
				Spec: verifySpec("f9-contract-rbg"),
			}
			warmup.Spec.Policies = tc.policies
			warmup.Status.Phase = workloadsv1alpha2.WarmupJobPhaseRunning
			started := metav1.NewTime(time.Now().Add(-2 * time.Minute).Truncate(time.Second))
			warmup.Status.StartTime = &started

			objs := []runtime.Object{
				warmup,
				makeWarmupPod("f9-contract-a", "default", "f9-contract", "uid-f9-contract", "node-a", corev1.PodSucceeded),
				makeWarmupPod("f9-contract-b", "default", "f9-contract", "uid-f9-contract", "node-b", corev1.PodFailed),
			}
			if tc.targetAlive {
				objs = append(objs, verifyRBGWithRole("f9-contract-rbg", "worker", 0))
			}
			r := newWarmupReconciler(objs...)

			reconcileOnce(t, r, warmup)
			updated := getWarmup(t, r, warmup)

			if updated.Status.Phase != tc.wantPhase {
				t.Fatalf("phase: want %q, got %q (conditions=%#v)", tc.wantPhase, updated.Status.Phase, updated.Status.Conditions)
			}
			condType := ConditionComplete
			if tc.wantPhase == workloadsv1alpha2.WarmupJobPhaseFailed {
				condType = ConditionFailed
			}
			cond := apimeta.FindStatusCondition(updated.Status.Conditions, condType)
			if cond == nil || cond.Reason != tc.wantReason {
				t.Fatalf("condition %s: want reason %q, got %#v", condType, tc.wantReason, updated.Status.Conditions)
			}
			if updated.Status.Failed != tc.wantFailed {
				t.Fatalf("failed counter: want %d, got %d", tc.wantFailed, updated.Status.Failed)
			}
			if updated.Status.Succeeded != 1 {
				t.Fatalf("succeeded counter: want 1, got %d", updated.Status.Succeeded)
			}
			// A within-tolerance settle must still preserve the start time (F7).
			if tc.wantPhase == workloadsv1alpha2.WarmupJobPhaseCompleted &&
				(updated.Status.StartTime == nil || !updated.Status.StartTime.Time.Equal(started.Time)) {
				t.Fatalf("startTime must be preserved, was %v now %v", started, updated.Status.StartTime)
			}
			// Deleted target: the terminal outcome must not be paired with
			// TargetReady=False (Q2) on either the complete or the fail path.
			if !tc.targetAlive {
				tr := apimeta.FindStatusCondition(updated.Status.Conditions, ConditionTargetReady)
				if tr == nil || tr.Status != metav1.ConditionUnknown || tr.Reason != "RoleBasedGroupNotFound" {
					t.Fatalf("expected TargetReady=Unknown/RoleBasedGroupNotFound, got %#v", updated.Status.Conditions)
				}
			}
		})
	}
}
