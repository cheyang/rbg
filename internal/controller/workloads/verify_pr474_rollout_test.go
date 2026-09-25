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
