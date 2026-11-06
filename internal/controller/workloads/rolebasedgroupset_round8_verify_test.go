package workloads

// Round 8 (head 09aaa4d7) contracts for the PR's new fix commits.
//
//  - 94981f04 "treat empty role collections as semantically equal": the carried
//    blocker C4358788576 contracts TestVerifyPR474_EmptyRoleAnnotationsMapNoWedge
//    (envtest, real apiserver) and TestVerifyPR474_RolesEqual_EmptyMapEqualsOmitted
//    (unit primitive) were RED at 39732268 and are GREEN at head — they remain
//    the runnable repro and are NOT duplicated here.
//  - 90858736 "cover availability shortfalls when sizing surge": new contract
//    below (C4431272984, the round-6/7 Copilot flag the author now fixes).
//  - 3981d38a "wake sets when terminating orphans disappear": new contract
//    below (C4445152303); the end-to-end refill is covered by the author's
//    envtest DescribeTable in test/envtest/testcase/rbgs_rolling.

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func round8Request(obj client.Object) ctrl.Request {
	return ctrl.Request{NamespacedName: client.ObjectKeyFromObject(obj)}
}

// TestVerifyPR474_Surge_DeficitBelowFloorCovered is the C4431272984 contract:
// when the base already sits BELOW the availability floor, surge sizing must
// cover the deficit in addition to the serving outdated batch, instead of
// sizing only from the declared budget. Recreate, replicas=4, partition=3,
// maxUnavailable=1 (floor=3), maxSurge=2; ordinals 0 and 1 (both below the
// partition) are down, ordinal 3 is the only serving outdated base.
// Pre-90858736 sizing: needed = min(2, servingOutdated-headroom) = 1.
// Post-90858736 sizing: needed = min(2, servingOutdated+floor-readyBase) = 2.
// Polarity: contract — RED on 39732268, GREEN on 09aaa4d7.
func TestVerifyPR474_Surge_DeficitBelowFloorCovered(t *testing.T) {
	strategy := &workloadsv1alpha2.GroupSetRolloutStrategy{
		Type:           workloadsv1alpha2.RecreateStrategyType,
		Partition:      ptrToIntStr(intstr.FromInt32(3)),
		MaxUnavailable: ptrToIntStr(intstr.FromInt32(1)),
		MaxSurge:       ptrToIntStr(intstr.FromInt32(2)),
	}
	set := verifyRollingSet("surge-deficit", "v2", 4, strategy)
	children := verifyChildren(t, set, "v1", 4)
	r, c := verifyReconciler(t, append([]client.Object{set}, children...)...)

	// Two held-back base ordinals fail: readyBase=2, floor=3 → deficit=1.
	setChildStatus(t, c, children[0].(*workloadsv1alpha2.RoleBasedGroup), workloadsv1alpha2.RoleBasedGroupStatus{})
	setChildStatus(t, c, children[1].(*workloadsv1alpha2.RoleBasedGroup), workloadsv1alpha2.RoleBasedGroupStatus{})

	_, err := r.Reconcile(context.Background(), round8Request(set))
	require.NoError(t, err)

	got := verifyChildImages(c, set)
	assert.Equal(t, "v2", got["surge-deficit-4"], "first surge covers the serving outdated group")
	assert.Equal(t, "v2", got["surge-deficit-5"], "second surge covers the deficit below the floor (C4431272984)")
	// Safety: the set is below the availability floor; no base child is removed.
	for i := 0; i < 4; i++ {
		assert.Contains(t, got, fmt.Sprintf("surge-deficit-%d", i), "base ordinal %d must not be deleted below the floor", i)
	}

	// Once everything is ready again, the in-scope ordinal must roll.
	markAllChildrenReady(t, c, set)
	_, err = r.Reconcile(context.Background(), round8Request(set))
	require.NoError(t, err)
	got = verifyChildImages(c, set)
	assert.NotContains(t, got, "surge-deficit-3", "the warmed surge must let the in-scope ordinal roll")
}

// TestVerifyPR474_OrphanWakeWatchMapping is the C4445152303 contract: an
// ownerless RBG that still carries the set-name label must map to a reconcile
// request for its set, so the set is woken when a terminating (or adopted then
// GC-orphaned) child finally disappears and the ordinal can be refilled
// without polling or unrelated writes. Controller-owned children and children
// without the label must not enqueue anything.
// Polarity: contract — RED on 39732268 (no such watch existed), GREEN on head.
func TestVerifyPR474_OrphanWakeWatchMapping(t *testing.T) {
	orphan := &workloadsv1alpha2.RoleBasedGroup{ObjectMeta: metav1.ObjectMeta{
		Name: "demo-1", Namespace: "default",
		Labels: map[string]string{constants.GroupSetNameLabelKey: "demo"},
	}}
	assert.Equal(t, []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: "default", Name: "demo"}}},
		groupSetForOrphanRBG(context.Background(), orphan), "an ownerless labeled child must wake its set")

	now := metav1.Now()
	terminating := orphan.DeepCopy()
	terminating.DeletionTimestamp = &now
	assert.Equal(t, []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: "default", Name: "demo"}}},
		groupSetForOrphanRBG(context.Background(), terminating), "a terminating orphan must wake its set while it is being deleted")

	owned := orphan.DeepCopy()
	owned.OwnerReferences = []metav1.OwnerReference{{
		APIVersion: workloadsv1alpha2.GroupVersion.String(), Kind: "RoleBasedGroupSet",
		Name: "demo", UID: "set-uid", Controller: ptr.To(true),
	}}
	assert.Empty(t, groupSetForOrphanRBG(context.Background(), owned), "controller-owned children already wake the set through Owns()")

	unlabeled := orphan.DeepCopy()
	unlabeled.Labels = nil
	assert.Empty(t, groupSetForOrphanRBG(context.Background(), unlabeled), "a child without the set label must not enqueue anything")
}
