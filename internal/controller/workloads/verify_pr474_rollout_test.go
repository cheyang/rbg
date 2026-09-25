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

// Verification harness for the review of PR #474 (RoleBasedGroupSet rolling update).
// Additive only — no production code is touched by this file. Helpers
// (rollingTestSet, rollingTestChild, newRollingTestReconciler, ...) come from the
// PR's own rolebasedgroupset_controller_test.go in the same package.
//
// Polarities:
//   - TestVerifyPR474_LegacySpellingReplicasOnlyScalesInPlace is a CONTRACT test
//     (re-review of round-1 blocker "onlyReplicasChanged skips normalization"):
//     it must PASS on a correct implementation.
//   - The F5/F6/F7 probes are CANARIES: they assert the behavior the PR head
//     actually exhibits today, so they PASS now and FLIP TO RED once the
//     behavior is fixed. A green canary is the bug, not the fix.

package workloads

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"k8s.io/client-go/tools/record"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// countingDeleteClient wraps the fake client and records every deleted name.
func countingDeleteClient(scheme *runtime.Scheme, objs ...runtime.Object) (client.WithWatch, *[]string) {
	deleted := &[]string{}
	base := fake.NewClientBuilder().WithScheme(scheme).
		WithRuntimeObjects(objs...).
		WithStatusSubresource(&workloadsv1alpha2.RoleBasedGroupSet{}).Build()
	c := interceptor.NewClient(base, interceptor.Funcs{
		Delete: func(
			ctx context.Context, inner client.WithWatch, obj client.Object, opts ...client.DeleteOption,
		) error {
			*deleted = append(*deleted, obj.GetName())
			return inner.Delete(ctx, obj, opts...)
		},
	})
	return c, deleted
}

// F4 re-check (contract): a child carrying the legacy "Recreate" spelling of the
// role-level update strategy, differing from the (normalized) template only in
// replicas, must be scaled in place — never routed to recreate. This is the
// round-1 blocker re-tested at the current head.
func TestVerifyPR474_LegacySpellingReplicasOnlyScalesInPlace(t *testing.T) {
	scheme := rbgsTestScheme(t)

	templateRoles := []workloadsv1alpha2.RoleSpec{{
		Name:     "worker",
		Replicas: ptr.To(int32(4)),
		RolloutStrategy: &workloadsv1alpha2.RolloutStrategy{
			RollingUpdate: &workloadsv1alpha2.RollingUpdate{Type: workloadsv1alpha2.RecreatePodUpdateStrategyType},
		},
	}}
	childRoles := []workloadsv1alpha2.RoleSpec{{
		Name:     "worker",
		Replicas: ptr.To(int32(1)),
		RolloutStrategy: &workloadsv1alpha2.RolloutStrategy{
			// Legacy v1alpha1 spelling kept by an object stored before normalization.
			RollingUpdate: &workloadsv1alpha2.RollingUpdate{Type: workloadsv1alpha2.LegacyRecreateUpdateStrategyType},
		},
	}}

	set := rollingTestSet("s", 1, templateRoles, &workloadsv1alpha2.GroupSetRolloutStrategy{})
	c, deleted := countingDeleteClient(scheme, set, rollingTestChild("s", 0, childRoles, true))
	r := &RoleBasedGroupSetReconciler{client: c, scheme: scheme, recorder: record.NewFakeRecorder(100)}

	reconcileSet(t, r, "s")

	assert.Empty(t, *deleted,
		"a replicas-only diff on a legacy-spelling child must not recreate the group")
	child := &workloadsv1alpha2.RoleBasedGroup{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "s-0"}, child))
	assert.Equal(t, ptr.To(int32(4)), child.Spec.Roles[0].Replicas,
		"the child must have been scaled in place to the template replicas")
}

// F5 (canary): maxUnavailable=-1 (accepted by the webhook today) with maxSurge=1
// stalls the rollout: the delete budget is -1 + readySurge = 0, so no serving
// group may ever be deleted and the rollout never starts. Green today == bug
// present; when the webhook rejects negative budgets this canary's setup becomes
// unreachable and the test should be replaced by the admission contract test.
func TestVerifyPR474_NegativeMaxUnavailableStallsRollout(t *testing.T) {
	scheme := rbgsTestScheme(t)

	oldRoles := []workloadsv1alpha2.RoleSpec{{Name: "worker", Replicas: ptr.To(int32(1))}}
	newRoles := []workloadsv1alpha2.RoleSpec{{Name: "worker", Replicas: ptr.To(int32(1)), MinReadySeconds: 10}}

	set := rollingTestSet("s", 2, newRoles, &workloadsv1alpha2.GroupSetRolloutStrategy{
		MaxUnavailable: ptr.To(intstr.FromInt32(-1)),
		MaxSurge:       ptr.To(intstr.FromInt32(1)),
	})
	surge := rollingTestChild("s", 2, newRoles, true) // ready surge group, template current

	c, deleted := countingDeleteClient(
		scheme, set,
		rollingTestChild("s", 0, oldRoles, true),
		rollingTestChild("s", 1, oldRoles, true),
		surge,
	)
	r := &RoleBasedGroupSetReconciler{client: c, scheme: scheme, recorder: record.NewFakeRecorder(100)}

	reconcileSet(t, r, "s")

	// Current (broken) behavior: nothing is deleted although a ready surge group
	// exists and both base groups are outdated — the rollout is stalled.
	assert.Empty(t, *deleted,
		"canary: budget = maxUnavailable(-1) + readySurge(1) = 0, so no group may be recreated")
}

// F6 (canary): with partition>0, a held-back group that stops serving keeps
// rolloutComplete() false (it requires EVERY base group serving), so surge
// capacity is never reclaimed — while the status Rolling condition reports
// RolloutComplete because the held-back ordinal counts in neither current nor
// updatedReady. Green today == the two completion definitions disagree.
func TestVerifyPR474_HeldBackBrokenGroupPinsSurgeWhileStatusComplete(t *testing.T) {
	scheme := rbgsTestScheme(t)

	oldRoles := []workloadsv1alpha2.RoleSpec{{Name: "worker", Replicas: ptr.To(int32(1))}}
	newRoles := []workloadsv1alpha2.RoleSpec{{Name: "worker", Replicas: ptr.To(int32(1)), MinReadySeconds: 10}}

	set := rollingTestSet("s", 2, newRoles, &workloadsv1alpha2.GroupSetRolloutStrategy{
		Partition:      ptr.To(intstr.FromInt32(1)),
		MaxUnavailable: ptr.To(intstr.FromInt32(1)),
		MaxSurge:       ptr.To(intstr.FromInt32(1)),
	})
	heldBackBroken := rollingTestChild("s", 0, oldRoles, false) // below partition, not serving
	rolled := rollingTestChild("s", 1, newRoles, true)          // in scope, on the new template
	surge := rollingTestChild("s", 2, newRoles, true)

	c, _ := countingDeleteClient(scheme, set, heldBackBroken, rolled, surge)
	r := &RoleBasedGroupSetReconciler{client: c, scheme: scheme, recorder: record.NewFakeRecorder(100)}

	reconcileSet(t, r, "s")

	// Canary half 1: the surge group is still there — rolloutComplete() counts the
	// broken held-back group as not serving, so surgeDone never becomes true.
	surgeAfter := &workloadsv1alpha2.RoleBasedGroup{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "s-2"}, surgeAfter),
		"canary: surge is pinned forever by a broken held-back group")

	// Canary half 2: the status condition reports the rollout as complete anyway.
	updated := &workloadsv1alpha2.RoleBasedGroupSet{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "s"}, updated))
	rolling := meta.FindStatusCondition(updated.Status.Conditions, string(workloadsv1alpha2.RoleBasedGroupSetRolling))
	require.NotNil(t, rolling)
	assert.Equal(t, metav1.ConditionFalse, rolling.Status,
		"canary: status reports RolloutComplete while surge is pinned")
	assert.Equal(t, "RolloutComplete", rolling.Reason)
}

// F7 (canary): while the rollout is paused, a replicas-only diff is applied in
// place — documented and intended — but the same update also syncs the template
// labels/annotations, so template metadata propagates even though "paused
// freezes template propagation". Green today == the inconsistency exists.
func TestVerifyPR474_PausedScaleOnlyPropagatesTemplateMetadata(t *testing.T) {
	scheme := rbgsTestScheme(t)

	newRoles := []workloadsv1alpha2.RoleSpec{{Name: "worker", Replicas: ptr.To(int32(3))}}
	oldRoles := []workloadsv1alpha2.RoleSpec{{Name: "worker", Replicas: ptr.To(int32(1))}}

	set := rollingTestSet("s", 1, newRoles, &workloadsv1alpha2.GroupSetRolloutStrategy{Paused: true})
	set.Spec.GroupTemplate.Labels = map[string]string{"team": "new"}

	child := rollingTestChild("s", 0, oldRoles, true)
	child.Labels["team"] = "old"

	c, _ := countingDeleteClient(scheme, set, child)
	r := &RoleBasedGroupSetReconciler{client: c, scheme: scheme, recorder: record.NewFakeRecorder(100)}

	reconcileSet(t, r, "s")

	after := &workloadsv1alpha2.RoleBasedGroup{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "s-0"}, after))
	assert.Equal(t, ptr.To(int32(3)), after.Spec.Roles[0].Replicas,
		"scaling still happens while paused (documented)")
	assert.Equal(t, "new", after.Labels["team"],
		"canary: template metadata propagated even though the rollout is paused")
}

// --- Round-2 findings (raised by a second reviewer, independently verified here) ---

// F9/P1-1 (contract): surge reclaimed in the same reconcile must not still count
// in the delete budget. reconcileRolling reclaims the excess surge group first,
// but recreateOutdatedGroups computes readySurge from the snapshot taken at the
// start of the reconcile, in which the just-deleted surge still reads as serving.
//
// Setup: replicas=3, all base outdated and serving, one ready surge group, and
// maxSurge shrunk 1 -> 0 in the same update that changed the template. Correct:
// the surge is reclaimed AND at most maxUnavailable(1) serving base group is
// deleted, keeping >= 2 serving. On the PR head the phantom surge widens the
// budget to 2, so TWO serving base groups are deleted, leaving 1 serving.
func TestVerifyPR474_ReclaimedSurgeDoesNotWidenBudget(t *testing.T) {
	scheme := rbgsTestScheme(t)

	oldRoles := []workloadsv1alpha2.RoleSpec{{Name: "worker", Replicas: ptr.To(int32(1))}}
	newRoles := []workloadsv1alpha2.RoleSpec{{Name: "worker", Replicas: ptr.To(int32(1)), MinReadySeconds: 10}}

	// maxSurge is 0 now; the surge group at ordinal 3 is a leftover from maxSurge=1.
	set := rollingTestSet("s", 3, newRoles, &workloadsv1alpha2.GroupSetRolloutStrategy{
		MaxUnavailable: ptr.To(intstr.FromInt32(1)),
		MaxSurge:       ptr.To(intstr.FromInt32(0)),
	})
	surge := rollingTestChild("s", 3, oldRoles, true)

	c, deleted := countingDeleteClient(scheme, set,
		rollingTestChild("s", 0, oldRoles, true),
		rollingTestChild("s", 1, oldRoles, true),
		rollingTestChild("s", 2, oldRoles, true),
		surge,
	)
	r := &RoleBasedGroupSetReconciler{client: c, scheme: scheme, recorder: record.NewFakeRecorder(100)}

	reconcileSet(t, r, "s")

	baseDeleted := 0
	for _, name := range *deleted {
		if name != "s-3" {
			baseDeleted++
		}
	}
	assert.Contains(t, *deleted, "s-3", "the excess surge group is reclaimed")
	assert.LessOrEqual(t, baseDeleted, 1,
		"maxUnavailable=1 allows deleting at most one serving base group; the reclaimed surge must not widen the budget")
}

// F10/P1-2 (contract): a retained surge group must follow the current template.
// classifyRolloutChildren only examines base groups and warmUpSurgeCapacity only
// fills missing ordinals, so a surge group created from a superseded template is
// kept forever. With maxUnavailable=0 and the surge never becoming ready (bad
// template), the delete budget stays 0 and the rollout wedges permanently even
// after the template is corrected.
//
// Setup: template is C; two base groups are ready on A; the surge group sits on
// B (superseded) and never became ready. Correct: the stale surge is replaced
// with one built from C. On the PR head nothing touches it and no base group
// can roll (budget = 0 + 0).
func TestVerifyPR474_SurgeGroupFollowsTemplateChanges(t *testing.T) {
	scheme := rbgsTestScheme(t)

	rolesA := []workloadsv1alpha2.RoleSpec{{Name: "worker", Replicas: ptr.To(int32(1))}}
	rolesB := []workloadsv1alpha2.RoleSpec{{Name: "worker", Replicas: ptr.To(int32(1)), MinReadySeconds: 10}}
	rolesC := []workloadsv1alpha2.RoleSpec{{Name: "worker", Replicas: ptr.To(int32(1)), MinReadySeconds: 20}}

	set := rollingTestSet("s", 2, rolesC, &workloadsv1alpha2.GroupSetRolloutStrategy{
		MaxUnavailable: ptr.To(intstr.FromInt32(0)),
		MaxSurge:       ptr.To(intstr.FromInt32(1)),
	})
	staleSurge := rollingTestChild("s", 2, rolesB, false) // created from B, never ready

	c, deleted := countingDeleteClient(scheme, set,
		rollingTestChild("s", 0, rolesA, true),
		rollingTestChild("s", 1, rolesA, true),
		staleSurge,
	)
	r := &RoleBasedGroupSetReconciler{client: c, scheme: scheme, recorder: record.NewFakeRecorder(100)}

	reconcileSet(t, r, "s")
	reconcileSet(t, r, "s")

	// The stale surge must be replaced so it is rebuilt from the current template C.
	assert.Contains(t, *deleted, "s-2",
		"the superseded surge group must be recreated so it picks up template C")
	surge := &workloadsv1alpha2.RoleBasedGroup{}
	err := c.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "s-2"}, surge)
	if err == nil {
		assert.True(t, r.rolesEqual(surge.Spec.Roles, rolesC),
			"the surge group must carry the current template after the correction")
	}
}

// F11/P1-3 (contract): a budget-blocked serving candidate must not stop the
// budget-free repair of a lower-ordinal group that is already not serving. The
// PR documents that the budget gates serving groups only ("holding it back would
// slow a rollback to a healthy template for no gain"), but the `break` on a
// budget-blocked serving group skips every lower ordinal, including broken ones.
//
// Setup: template is C; s-2 serving on B (outdated, budget-blocked); s-1 stuck
// unready on B (outdated, free to replace); s-0 serving on B. With
// maxUnavailable=1 the unavailable s-1 already fills the budget, so s-2 breaks
// the loop and s-1 is never repaired: the rollback to C wedges permanently.
// Correct: skip budget-blocked serving candidates, keep scanning lower ordinals.
func TestVerifyPR474_BudgetBlockedServingGroupDoesNotStopBrokenRepair(t *testing.T) {
	scheme := rbgsTestScheme(t)

	rolesB := []workloadsv1alpha2.RoleSpec{{Name: "worker", Replicas: ptr.To(int32(1)), MinReadySeconds: 10}}
	rolesC := []workloadsv1alpha2.RoleSpec{{Name: "worker", Replicas: ptr.To(int32(1)), MinReadySeconds: 20}}

	set := rollingTestSet("s", 3, rolesC, &workloadsv1alpha2.GroupSetRolloutStrategy{
		MaxUnavailable: ptr.To(intstr.FromInt32(1)),
	})
	c, deleted := countingDeleteClient(scheme, set,
		rollingTestChild("s", 0, rolesB, true),
		rollingTestChild("s", 1, rolesB, false), // stuck unready on the bad template
		rollingTestChild("s", 2, rolesB, true),
	)
	r := &RoleBasedGroupSetReconciler{client: c, scheme: scheme, recorder: record.NewFakeRecorder(100)}

	reconcileSet(t, r, "s")

	assert.Contains(t, *deleted, "s-1",
		"the not-serving s-1 consumes no budget and must be replaced even while serving s-2 is budget-blocked")
	assert.NotContains(t, *deleted, "s-2",
		"the serving s-2 is budget-blocked and must wait")
	assert.NotContains(t, *deleted, "s-0",
		"s-0 is behind the budget-blocked s-2 in this pass")
}
