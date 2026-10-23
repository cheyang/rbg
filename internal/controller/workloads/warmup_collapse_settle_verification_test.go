package workloads

// Round-3 (head b0915b21) probe for the new collapsed-desired-set settle path
// (failCollapsedDesiredSet / updateEmptyDesiredPhase). POLARITY: CANARY — it
// pins the CURRENT behavior because the correct semantics are under discussion
// in review finding F9. If the author changes the settle path to honor
// maxFailedNodes tolerance, this canary flips red and must be inverted into a
// contract test.

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// F9 canary: the collapsed-set settle ignores maxFailedNodes tolerance.
// Fixture: node-a succeeded, node-b failed permanently (backoffLimit=0) but
// within maxFailedNodes=1; the target RBG is deleted before the reconcile that
// would observe the settled pods. Had the target survived, the same pod state
// completes (terminalCount >= desired, failed <= maxFailedNodes). After the
// deletion the job ends Failed/InvalidTarget instead — the settle path fires
// before and independently of the tolerance check.
//
// At the round-2 head 60b1b7cb this fixture completed as
// Completed/NoNodesMatched (the F4 false-completion family); at b0915b21 it
// fails. Neither honors the tolerance policy.
func TestVerify_CollapsedSettleBypassesMaxFailedNodesTolerance(t *testing.T) {
	warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{
			Name: "f9-tolerance", Namespace: "default", UID: "uid-f9-tolerance",
			CreationTimestamp: metav1.NewTime(time.Now().Add(-2 * time.Minute)),
		},
		Spec: verifySpec("f9-tolerance-rbg"),
	}
	warmup.Spec.Policies = &workloadsv1alpha2.WarmupPolicies{
		BackoffLimitPerNode: ptr.To(int32(0)),
		MaxFailedNodes:      ptr.To(int32(1)),
	}
	warmup.Status.Phase = workloadsv1alpha2.WarmupJobPhaseRunning
	warmup.Status.StartTime = &metav1.Time{Time: time.Now().Add(-2 * time.Minute)}

	// No target RBG object -> the target.deleted path.
	r := newWarmupReconciler(warmup,
		makeWarmupPod("f9-tolerance-a", "default", "f9-tolerance", "uid-f9-tolerance", "node-a", corev1.PodSucceeded),
		makeWarmupPod("f9-tolerance-b", "default", "f9-tolerance", "uid-f9-tolerance", "node-b", corev1.PodFailed))

	reconcileOnce(t, r, warmup)
	updated := getWarmup(t, r, warmup)

	// CANARY assertion: current (b0915b21) behavior is Failed/InvalidTarget
	// even though the single failed node was within maxFailedNodes=1.
	if updated.Status.Phase != workloadsv1alpha2.WarmupJobPhaseFailed {
		t.Fatalf("canary flipped: collapsed settle now honors tolerance or changed semantics; phase=%q conditions=%#v — invert this canary into a contract test",
			updated.Status.Phase, updated.Status.Conditions)
	}
	cond := apimeta.FindStatusCondition(updated.Status.Conditions, "Failed")
	if cond == nil || cond.Reason != "InvalidTarget" {
		t.Fatalf("canary flipped: expected Failed/InvalidTarget, got %#v", updated.Status.Conditions)
	}
	t.Logf("F9 pinned: within-tolerance job settled as %s/%s after target deletion (maxFailedNodes=1, failed=1)",
		updated.Status.Phase, cond.Reason)
}
