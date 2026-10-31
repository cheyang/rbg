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

// Reviewer harness for PR #474 — round 6 delta (4a9043c -> af9dd84b).
//
// New contracts for the four round-6 commits:
//   - a06fa803 "size surge from live availability rather than the declared
//     budget" (#519): an unready held-back ordinal must not starve the surge.
//   - 13cbcab4: rolling-path orphan child adoption, idle sets do not poll,
//     canonical revision JSON (GC strategic-merge patches stay valid).
//   - 6649fe36 "complete metadata-only rollouts after workload application":
//     finalizeRolloutStatus clears the barrier with no request outstanding.
//   - af9dd84b "reclaim orphaned predecessor revision history": controller-less
//     label-matching ControllerRevisions are adopted and pruning is bounded.
//
// All tests are polarity=contract: they FAIL on code that regresses the
// behavior the round-6 commits introduced.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	utiljson "k8s.io/apimachinery/pkg/util/json"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/pkg/reconciler"
)

func ptrToPodTemplate(image string) *corev1.PodTemplateSpec {
	return &corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: image}}}}
}

// rawVerifyTemplate serializes a template the way a ControllerRevision would.
func rawVerifyTemplate(t *testing.T, image string) []byte {
	t.Helper()
	data, err := canonicalGroupSetTemplateJSON(verifyRollingSet("raw", image, 1, nil).Spec.GroupTemplate.DeepCopy())
	require.NoError(t, err)
	return data
}

// setChildStatus rewrites a child's status through the status subresource.
func setChildStatus(t *testing.T, c client.Client, child *workloadsv1alpha2.RoleBasedGroup, status workloadsv1alpha2.RoleBasedGroupStatus) {
	t.Helper()
	latest := &workloadsv1alpha2.RoleBasedGroup{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(child), latest))
	latest.Status = status
	require.NoError(t, c.Status().Update(context.Background(), latest))
}

func markAllChildrenReady(t *testing.T, c client.Client, set *workloadsv1alpha2.RoleBasedGroupSet) {
	t.Helper()
	list := &workloadsv1alpha2.RoleBasedGroupList{}
	require.NoError(t, c.List(context.Background(), list, client.InNamespace(set.Namespace),
		client.MatchingLabels{constants.GroupSetNameLabelKey: set.Name}))
	for i := range list.Items {
		child := list.Items[i].DeepCopy()
		verifyReady(child)
		setChildStatus(t, c, child, child.Status)
	}
}

// TestVerifyPR474_Surge_UnreadyHeldBackOrdinalStillWarmsSurge is the #519
// contract. Recreate, replicas=4, partition=3, maxUnavailable=1, maxSurge=1.
// Ordinal 0 (below the partition, untouched by the rollout) is unready. The
// old sizing (outdatedBase - maxUnavailable) yielded zero surge here, so the
// in-scope update stalled until ordinal 0 recovered by itself; the live-budget
// sizing must create the surge group the configuration allows.
func TestVerifyPR474_Surge_UnreadyHeldBackOrdinalStillWarmsSurge(t *testing.T) {
	strategy := &workloadsv1alpha2.GroupSetRolloutStrategy{
		Type:           workloadsv1alpha2.RecreateStrategyType,
		Partition:      ptrToIntStr(intstr.FromInt32(3)),
		MaxUnavailable: ptrToIntStr(intstr.FromInt32(1)),
		MaxSurge:       ptrToIntStr(intstr.FromInt32(1)),
	}
	set := verifyRollingSet("surge-live", "v2", 4, strategy)
	children := verifyChildren(t, set, "v1", 4)
	r, c := verifyReconciler(t, append([]client.Object{set}, children...)...)

	// The held-back base ordinal is down: its failure has already spent the
	// unavailability budget, so declared-budget surge math yields zero.
	setChildStatus(t, c, children[0].(*workloadsv1alpha2.RoleBasedGroup), workloadsv1alpha2.RoleBasedGroupStatus{})

	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(set)})
	require.NoError(t, err)

	got := verifyChildImages(c, set)
	assert.Contains(t, got, "surge-live-4", "an unready held-back ordinal must still free its surge capacity (#519)")
	assert.Equal(t, "v2", got["surge-live-4"], "the surge group is created at the update revision")
	// Safety: while the set sits at the availability floor, no base child is removed.
	for i := 0; i < 4; i++ {
		assert.Contains(t, got, fmt.Sprintf("surge-live-%d", i), "base ordinal %d must not be deleted at the floor", i)
	}

	// The stalled rollout must make progress once the surge serves.
	markAllChildrenReady(t, c, set)
	_, err = r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(set)})
	require.NoError(t, err)
	got = verifyChildImages(c, set)
	assert.NotContains(t, got, "surge-live-3", "the ready surge must let the in-scope ordinal roll")
}

// TestVerifyPR474_Rolling_AdoptsOrphanChildrenBeforeBudgetedUpdates: a
// cascade=orphan deletion leaves label-matching children without an owner;
// the rolling path must adopt them (same UID) before any budgeted write, and
// must not take over a child controlled by a different parent UID.
func TestVerifyPR474_Rolling_AdoptsOrphanChildrenBeforeBudgetedUpdates(t *testing.T) {
	strategy := &workloadsv1alpha2.GroupSetRolloutStrategy{
		Type:           workloadsv1alpha2.RecreateStrategyType,
		MaxUnavailable: ptrToIntStr(intstr.FromInt32(1)),
	}
	set := verifyRollingSet("adoptkids", "v1", 3, strategy)
	owned := verifyChildren(t, set, "v1", 2)

	// An orphan left by a --cascade=orphan deletion of the predecessor set:
	// label-matching, no controller reference, ready.
	orphan := newRBGForSet(set, 2)
	orphan.Spec.Roles[0].Pattern.StandalonePattern.TemplateSource.Template = ptrToPodTemplate("v1")
	orphan.UID = types.UID("orphan-child-uid")
	verifyReady(orphan)

	// A foreign child: same label, but controlled by a different parent UID.
	foreign := newRBGForSet(set, 3)
	foreign.Spec.Roles[0].Pattern.StandalonePattern.TemplateSource.Template = ptrToPodTemplate("v1")
	foreign.UID = types.UID("foreign-child-uid")
	foreign.OwnerReferences = []metav1.OwnerReference{{
		APIVersion: workloadsv1alpha2.GroupVersion.String(), Kind: "RoleBasedGroupSet",
		Name: "other-set", UID: types.UID("other-set-uid"), Controller: ptr.To(true),
	}}
	verifyReady(foreign)

	r, c := verifyReconciler(t, append([]client.Object{set}, append(owned, orphan, foreign)...)...)
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(set)})
	require.NoError(t, err)

	adopted := &workloadsv1alpha2.RoleBasedGroup{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(orphan), adopted),
		"the orphan child must be kept (UID retention)")
	assert.Equal(t, types.UID("orphan-child-uid"), adopted.UID)
	assert.True(t, metav1.IsControlledBy(adopted, set), "the orphan must be adopted by this set")

	kept := &workloadsv1alpha2.RoleBasedGroup{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(foreign), kept))
	assert.False(t, metav1.IsControlledBy(kept, set), "a child controlled by another parent must not be taken over")
	require.NotNil(t, metav1.GetControllerOfNoCopy(kept))
	assert.Equal(t, types.UID("other-set-uid"), metav1.GetControllerOfNoCopy(kept).UID)
}

// TestVerifyPR474_Revisions_AdoptOrphanPredecessorHistory: label-matching
// controller-less ControllerRevisions (predecessor set UIDs) are adopted and
// then pruned down to DefaultRevisionHistoryLimit unused snapshots, while the
// update revision stays protected.
func TestVerifyPR474_Revisions_AdoptOrphanPredecessorHistory(t *testing.T) {
	strategy := &workloadsv1alpha2.GroupSetRolloutStrategy{
		Type:           workloadsv1alpha2.InPlaceUpdateStrategyType,
		MaxUnavailable: ptrToIntStr(intstr.FromInt32(1)),
	}
	set := verifyRollingSet("adoptrev", "v1", 1, strategy)
	children := verifyChildren(t, set, "v1", 1)
	r, c := verifyReconciler(t, append([]client.Object{set}, children...)...)
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(set)})
	require.NoError(t, err)

	fresh := &workloadsv1alpha2.RoleBasedGroupSet{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(set), fresh))
	require.NotEmpty(t, fresh.Status.UpdateRevision)

	// Seven orphaned revisions from predecessor incarnations of this set name.
	for i := 0; i < 7; i++ {
		orphan := &appsv1.ControllerRevision{
			ObjectMeta: metav1.ObjectMeta{
				Name:              fmt.Sprintf("rbgs-orphan-%d", i),
				Namespace:         set.Namespace,
				UID:               types.UID(fmt.Sprintf("orphan-rev-uid-%d", i)),
				Labels:            map[string]string{constants.GroupSetNameLabelKey: set.Name},
				CreationTimestamp: metav1.Time{Time: time.Date(2026, 1, i+1, 0, 0, 0, 0, time.UTC)},
			},
			Revision: int64(i + 1),
			Data:     runtime.RawExtension{Raw: rawVerifyTemplate(t, "v1")},
		}
		require.NoError(t, c.Create(context.Background(), orphan))
	}

	_, err = r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(set)})
	require.NoError(t, err)

	revisions := &appsv1.ControllerRevisionList{}
	require.NoError(t, c.List(context.Background(), revisions, client.InNamespace(set.Namespace)))
	adoptedCount, orphanRemain := 0, 0
	for i := range revisions.Items {
		rev := &revisions.Items[i]
		if rev.Name == fresh.Status.UpdateRevision {
			continue // the protected update revision
		}
		orphanRemain++
		if metav1.IsControlledBy(rev, set) {
			adoptedCount++
		} else {
			t.Errorf("revision %s was neither pruned nor adopted", rev.Name)
		}
	}
	assert.Equal(t, 5, orphanRemain,
		"exactly DefaultRevisionHistoryLimit unused adopted revisions must be retained")
	assert.Equal(t, 5, adoptedCount, "all retained orphan history must carry this set's controller reference")

	protected := &appsv1.ControllerRevision{}
	require.NoError(t, c.Get(context.Background(),
		client.ObjectKey{Namespace: set.Namespace, Name: fresh.Status.UpdateRevision}, protected))
	assert.True(t, metav1.IsControlledBy(protected, set), "the update revision stays owned and protected")
}

// TestVerifyPR474_Set_IdleReconcileDoesNotPoll: with nothing to change (a
// blocked, unchanged set with an unready child), the set controller must not
// return a periodic requeue — readiness changes arrive through child watches.
func TestVerifyPR474_Set_IdleReconcileDoesNotPoll(t *testing.T) {
	strategy := &workloadsv1alpha2.GroupSetRolloutStrategy{
		Type:           workloadsv1alpha2.InPlaceUpdateStrategyType,
		MaxUnavailable: ptrToIntStr(intstr.FromInt32(1)),
	}
	set := verifyRollingSet("idle", "v1", 3, strategy)
	children := verifyChildren(t, set, "v1", 3)

	r, c := verifyReconciler(t, append([]client.Object{set}, children...)...)
	setChildStatus(t, c, children[0].(*workloadsv1alpha2.RoleBasedGroup), workloadsv1alpha2.RoleBasedGroupStatus{})
	for pass := 0; pass < 2; pass++ {
		result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(set)})
		require.NoError(t, err)
		assert.Zero(t, result.RequeueAfter,
			"pass %d: an unchanged blocked set must not poll on a timer", pass)
	}
}

// TestVerifyPR474_Finalize_CompletesStandaloneWithoutRequest: the round-6
// finalizeRolloutStatus must clear a pre-application Rolling=True barrier
// even when no rollout request exists (standalone RBGs), once the target
// workloads are live-complete.
func TestVerifyPR474_Finalize_CompletesStandaloneWithoutRequest(t *testing.T) {
	ctx := context.Background()
	rbg, objects := rolloutTestFixture(t, "Deployment")

	scheme := rolloutTestScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(append(objects, rbg)...).
		WithStatusSubresource(&workloadsv1alpha2.RoleBasedGroup{}).Build()
	r := &RoleBasedGroupReconciler{client: c, apiReader: c, scheme: scheme, recorder: record.NewFakeRecorder(10),
		workloadReconciler: make(map[string]reconciler.WorkloadReconciler)}

	// A stale expected hash keeps the pre-application barrier True with no
	// request outstanding (the standalone-RBG shape).
	stale := map[string]string{"role": "stale"}
	_, err := r.constructAndUpdateRoleStatuses(ctx, rbg, stale)
	require.NoError(t, err)

	// The workloads are already at the target hash; finalize must complete.
	require.NoError(t, r.finalizeRolloutStatus(ctx, rbg, map[string]string{"role": "target"}))
	got := &workloadsv1alpha2.RoleBasedGroup{}
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(rbg), got))
	condition := apimeta.FindStatusCondition(got.Status.Conditions,
		string(workloadsv1alpha2.RoleBasedGroupRollingUpdateInProgress))
	require.NotNil(t, condition)
	assert.Equal(t, metav1.ConditionFalse, condition.Status,
		"a standalone RBG with complete workloads must not stay Rolling=True forever")
	assert.Empty(t, got.Status.ObservedRolloutRequest, "no request outstanding means no acknowledgement to write")
}

// TestVerifyPR474_CanonicalRevisionJSON_MatchesAPIServerReencode: revision
// data must be byte-stable under the API server's decode/re-encode path, so
// garbage collection's strategic-merge patches never trip the immutable-field
// check, and canonicalization must actually reorder struct-ordered output.
func TestVerifyPR474_CanonicalRevisionJSON_MatchesAPIServerReencode(t *testing.T) {
	template := &workloadsv1alpha2.RoleBasedGroupTemplateSpec{
		Labels:      map[string]string{"team": "a&b", "app": "rollout"},
		Annotations: map[string]string{"note": "html<escape>"},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{
			Roles: []workloadsv1alpha2.RoleSpec{{
				Name:     "worker",
				Replicas: ptr.To(int32(3)),
			}},
		},
	}
	canonical, err := canonicalGroupSetTemplateJSON(template)
	require.NoError(t, err)

	// The API server decodes into a generic map and re-encodes: sorted keys,
	// HTML escaping, integers kept integral.
	var generic interface{}
	require.NoError(t, utiljson.Unmarshal(canonical, &generic))
	reencoded, err := json.Marshal(generic)
	require.NoError(t, err)
	assert.True(t, bytes.Equal(canonical, reencoded),
		"revision data must be byte-identical after an API-server re-encode")

	plain, err := json.Marshal(template)
	require.NoError(t, err)
	assert.False(t, bytes.Equal(plain, canonical),
		"canonicalization must reorder struct-ordered output (sorted map keys)")
	assert.Contains(t, string(canonical), `"replicas":3`, "integers stay integral")
}

// TestVerifyPR474_EmptyRoleAnnotations_DroppedBySerialization refutes the
// open question C4358788576: rolesEqual's reflect.DeepEqual does treat an
// explicitly empty annotations map as different from an omitted one, but the
// omitempty serialization the API server applies means neither the stored set
// template nor a written child can retain an empty map — the divergent state
// is unreachable through the API, so there is no perpetual-outdated loop.
func TestVerifyPR474_EmptyRoleAnnotations_DroppedBySerialization(t *testing.T) {
	withEmpty := workloadsv1alpha2.RoleSpec{Name: "w", Annotations: map[string]string{}}
	data, err := json.Marshal(withEmpty)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "annotations", "omitempty drops an empty annotations map on write")

	var roundTripped workloadsv1alpha2.RoleSpec
	require.NoError(t, json.Unmarshal(data, &roundTripped))
	assert.Nil(t, roundTripped.Annotations, "a serialized role reads back with nil annotations")

	// Document the comparison asymmetry that motivates the question...
	assert.False(t, reflect.DeepEqual(
		[]workloadsv1alpha2.RoleSpec{{Name: "w"}},
		[]workloadsv1alpha2.RoleSpec{withEmpty}),
		"rolesEqual's DeepEqual does diverge on empty-map vs nil")
	// ...and why it cannot wedge: the normalized JSON comparison sees them equal,
	// and the persisted world only ever holds the nil form.
	left, errL := json.Marshal(workloadsv1alpha2.RoleSpec{Name: "w"})
	right, errR := json.Marshal(withEmpty)
	assert.NoError(t, errL)
	assert.NoError(t, errR)
	assert.True(t, bytes.Equal(left, right), "the normalized JSON forms are identical")
}
