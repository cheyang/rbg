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

// Verification harness for PR #474 round 6 (reviewer additive tests; production code
// untouched). Covers the round-6 delta (13cbcab4..af9dd84b):
//
//   R6SURGE   - surge is sized from live availability (#519): a failed group below the
//               partition spends maxUnavailable, yet the free maxSurge slot is still used;
//               the in-scope group is not taken down before its surge is ready.
//               Differential: RED on the round-5 head 4a9043c (stall, no surge).
//   R6ADOPT   - the rolling path adopts label-matching orphan children (cascade=orphan
//               leftovers) instead of colliding on their ordinals; a child still
//               controlled by a foreign UID is refused; a terminating orphan is left
//               alone. Differential: RED on 4a9043c (orphans hidden, create collides).
//   R6REVADOPT- orphaned predecessor ControllerRevisions are adopted and pruned down to
//               the history limit; child-referenced and status-pinned revisions are
//               protected; a replaced parent refuses adoption. Differential: RED on
//               4a9043c (orphan history never reclaimed).
//   R6FINALIZE- a requestless, labels-only role update completes in the SAME reconcile
//               that applies it (finalizeRolloutStatus after workload application):
//               barrier cleared, no ack stamped, no requeue requested. 13cbcab4 removed
//               the RollingUpdateInProgress 2s poll that used to clear this barrier on
//               the NEXT pass, so the post-application check is what stands between the
//               head and a permanent wedge for standalone RBGs.
//               Differential: RED on 13cbcab4 (poll removed, finalize not yet added);
//               on 4a9043c the same assert fails on the first pass but the 2s poll would
//               clear it a pass later (recorded in the round-6 evidence log).
//
// Polarity: all tests here are CONTRACT tests - they assert the intended behavior and
// must FAIL on code without the fix and PASS on the fixed head.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/pkg/reconciler"
	wrappersv2 "sigs.k8s.io/rbgs/test/wrappers/v1alpha2"
)

// ---- R6SURGE (#519) -------------------------------------------------------

// TestVerify_R6_SurgeSizedFromLiveAvailability_Contract pins the live-availability
// surge sizing: with replicas=4, partition=3, maxUnavailable=1, maxSurge=1 and the
// held-back ordinal 0 down, the one free surge slot must still be used to roll the
// in-scope ordinal 3 - and ordinal 3 must stay up until the surge is ready.
func TestVerify_R6_SurgeSizedFromLiveAvailability_Contract(t *testing.T) {
	set := rollingVerifySet("r6-surge", 4, intPtrStrategy(1, 1, 3))
	d := newRollingDrive(t, set)
	d.waitSettled(4, 1, 20)
	require.Len(t, d.listChildren(), 4)

	// The held-back group fails and stays down: maxUnavailable is now spent.
	d.notReady["r6-surge-0"] = true
	d.reconcileOnce(4, 1) // let the sim apply the failure
	require.Equal(t, 3, countReadyNonTerminating(d.listChildren()), "held-back group is down")

	d.triggerTemplateChange("image:v2")

	surgeSeen := false
	baseThreeReplacedEarly := false
	for i := 0; i < 40; i++ {
		before := d.worldFingerprint()
		res := d.reconcileOnce(4, 1)
		children := d.listChildren()
		// The serving floor: failed-0 is already down, so no second base group may go
		// down until the surge carries it.
		assert.GreaterOrEqualf(t, countReadyNonTerminating(children), 3,
			"pass %d: serving base groups dropped below replicas-maxUnavailable", d.step)
		surge := findChild(children, "r6-surge-4")
		if surge != nil {
			surgeSeen = true
			if !groupSetChildReady(surge) {
				if c := findChild(children, "r6-surge-3"); c != nil &&
					c.DeletionTimestamp.IsZero() && childImage(c) == "image:v1" {
					// correct: in-scope group waits for its surge
				} else if c == nil || childImage(c) == "image:v2" {
					baseThreeReplacedEarly = true
				}
			}
		}
		if res.RequeueAfter == 0 && !res.Requeue && before == d.worldFingerprint() {
			break
		}
	}
	assert.True(t, surgeSeen, "a failed held-back group must not stall a surge-enabled rollout (#519)")
	assert.False(t, baseThreeReplacedEarly, "the in-scope group must not be replaced before its surge is ready")

	children := d.listChildren()
	c3 := findChild(children, "r6-surge-3")
	require.NotNil(t, c3, "ordinal 3 must be rebuilt after its surge became ready")
	assert.Equal(t, "image:v2", childImage(c3), "in-scope ordinal 3 must roll once surge covers it")
	assert.Nil(t, findChild(children, "r6-surge-4"), "surge is reclaimed once the partition batch completes")
	rolling := rollingCondition(t, d.getSet())
	assert.Equal(t, "PartitionComplete", rolling.Reason,
		"the in-scope batch completes even though the held-back failure keeps full RolloutComplete open")
}

func findChild(children []workloadsv1alpha2.RoleBasedGroup, name string) *workloadsv1alpha2.RoleBasedGroup {
	for i := range children {
		if children[i].Name == name {
			return &children[i]
		}
	}
	return nil
}

// ---- R6ADOPT ---------------------------------------------------------------

// TestVerify_R6_RollingPathAdoptsOrphans_Contract: children orphaned by a
// cascade=orphan set deletion keep the set label; the rolling path must adopt them
// (preserving UID) and still fill the ordinals above them, exactly like the legacy
// path. On the round-5 head the orphans were filtered out of the child list, so the
// create for an occupied ordinal collided and aborted the fill.
func TestVerify_R6_RollingPathAdoptsOrphans_Contract(t *testing.T) {
	ctx := context.Background()
	template := revisionTestTemplate(nil)
	set := revisionTestRollingSet(3, &groupSetRevision{template: template})
	objects := []client.Object{set}
	for index := 0; index < 2; index++ {
		orphan := revisionTestReadyChild(set, index, &groupSetRevision{template: template})
		orphan.OwnerReferences = nil
		orphan.UID = types.UID(fmt.Sprintf("orphan-uid-%d", index))
		objects = append(objects, orphan)
	}
	r, _ := revisionTestRollingReconciler(t, objects...)
	require.NoError(t, r.client.Get(ctx, client.ObjectKeyFromObject(set), set))

	result, err := r.reconcileRollingGroupSet(ctx, client.ObjectKeyFromObject(set))
	require.NoError(t, err)
	assert.Positive(t, result.RequeueAfter, "adoption asks for a follow-up pass")

	for index := 0; index < 2; index++ {
		child := revisionTestGetChild(t, r, set, index)
		assert.True(t, metav1.IsControlledBy(child, set), "orphan ordinal %d must be adopted", index)
		assert.Equal(t, types.UID(fmt.Sprintf("orphan-uid-%d", index)), child.UID,
			"adoption must not recreate the orphan")
	}
	assert.True(t, metav1.IsControlledBy(revisionTestGetChild(t, r, set, 2), set),
		"the ordinal above the orphans must still be created")
}

// TestVerify_R6_RollingPathRefusesForeignChild_Contract: a child still controlled by a
// predecessor UID is not adopted and not silently managed; the collision surfaces an
// error instead of stealing the child.
func TestVerify_R6_RollingPathRefusesForeignChild_Contract(t *testing.T) {
	ctx := context.Background()
	template := revisionTestTemplate(nil)
	set := revisionTestRollingSet(1, &groupSetRevision{template: template})
	foreign := revisionTestReadyChild(set, 0, &groupSetRevision{template: template})
	predecessor := set.DeepCopy()
	predecessor.UID = "deleted-set-uid"
	foreign.OwnerReferences = []metav1.OwnerReference{
		*metav1.NewControllerRef(predecessor, workloadsv1alpha2.GroupVersion.WithKind("RoleBasedGroupSet")),
	}
	r, _ := revisionTestRollingReconciler(t, set, foreign)
	require.NoError(t, r.client.Get(ctx, client.ObjectKeyFromObject(set), set))

	_, err := r.reconcileRollingGroupSet(ctx, client.ObjectKeyFromObject(set))
	require.Error(t, err, "a create collision with a foreign-owned child must surface, not adopt")
	assert.Contains(t, err.Error(), "not owned by")
	after := revisionTestGetChild(t, r, set, 0)
	assert.False(t, metav1.IsControlledBy(after, set), "the foreign child must not be adopted")
}

// TestVerify_R6_TerminatingOrphanDoesNotBlockAdoption_Contract: an orphan that is
// already terminating must not be adopted (its deletion owns the ordinal), and it must
// not block adopting the live orphan next to it.
func TestVerify_R6_TerminatingOrphanDoesNotBlockAdoption_Contract(t *testing.T) {
	ctx := context.Background()
	template := revisionTestTemplate(nil)
	set := revisionTestRollingSet(2, &groupSetRevision{template: template})
	live := revisionTestReadyChild(set, 1, &groupSetRevision{template: template})
	live.OwnerReferences = nil
	dying := revisionTestReadyChild(set, 0, &groupSetRevision{template: template})
	dying.OwnerReferences = nil
	dying.Finalizers = []string{"verify/finalizer"}
	objects := []client.Object{set, live, dying}
	r, _ := revisionTestRollingReconciler(t, objects...)
	require.NoError(t, r.client.Get(ctx, client.ObjectKeyFromObject(set), set))
	// Mark ordinal 0 terminating through the API so DeletionTimestamp is real.
	require.NoError(t, r.client.Delete(ctx, dying))

	_, err := r.reconcileRollingGroupSet(ctx, client.ObjectKeyFromObject(set))
	require.NoError(t, err)
	after := revisionTestGetChild(t, r, set, 1)
	assert.True(t, metav1.IsControlledBy(after, set), "the live orphan must be adopted")
	stuck := revisionTestGetChild(t, r, set, 0)
	assert.False(t, metav1.IsControlledBy(stuck, set),
		"the terminating orphan must not be adopted - its deletion still owns the ordinal")
	assert.False(t, stuck.DeletionTimestamp.IsZero(), "the terminating orphan must still be terminating")
}

// ---- R6REVADOPT ------------------------------------------------------------

// TestVerify_R6_OrphanRevisionHistoryReclaimed_Contract: ControllerRevisions orphaned
// by a deleted predecessor set (label match, no controller ref) are adopted so the
// history can be bounded; referenced/current revisions are protected; unused history
// is pruned to the retention limit, oldest first.
func TestVerify_R6_OrphanRevisionHistoryReclaimed_Contract(t *testing.T) {
	ctx := context.Background()
	template := revisionTestTemplate(nil)
	set := revisionTestRollingSet(1, &groupSetRevision{template: template})
	objects := []client.Object{set}
	child := revisionTestReadyChild(set, 0, &groupSetRevision{template: template})
	child.Labels[constants.GroupSetRevisionLabelKey] = "rbgs-live-referenced"
	objects = append(objects, child)

	mkRevision := func(name string, age time.Duration) *appsv1.ControllerRevision {
		return &appsv1.ControllerRevision{
			ObjectMeta: metav1.ObjectMeta{
				Name:              name,
				Namespace:         set.Namespace,
				Labels:            map[string]string{constants.GroupSetNameLabelKey: set.Name},
				CreationTimestamp: metav1.NewTime(time.Now().Add(-age)),
			},
			// Orphaned by the deleted predecessor: no controller reference.
			Data: runtime.RawExtension{Raw: []byte(`{"spec":{}}`)},
		}
	}
	// Protected: the revision the live child references.
	objects = append(objects, mkRevision("rbgs-live-referenced", 8*time.Hour))
	// Seven unreferenced orphans, oldest first: the two oldest must be reclaimed
	// (limit 5 unused retained), the newest five survive.
	for i := 0; i < 7; i++ {
		objects = append(objects, mkRevision(fmt.Sprintf("rbgs-orphan-%d", i), time.Duration(7-i)*time.Hour))
	}
	r, _ := revisionTestRollingReconciler(t, objects...)
	require.NoError(t, r.client.Get(ctx, client.ObjectKeyFromObject(set), set))

	children := &workloadsv1alpha2.RoleBasedGroupList{}
	require.NoError(t, r.client.List(ctx, children, client.InNamespace(set.Namespace)))
	require.NoError(t, r.pruneGroupSetRevisions(ctx, set, children))

	revisions := &appsv1.ControllerRevisionList{}
	require.NoError(t, r.client.List(ctx, revisions, client.InNamespace(set.Namespace)))
	remaining := map[string]bool{}
	for _, rev := range revisions.Items {
		remaining[rev.Name] = true
		assert.True(t, metav1.IsControlledBy(&rev, set),
			"surviving revision %s must be adopted by the set", rev.Name)
	}
	assert.True(t, remaining["rbgs-live-referenced"], "a child-referenced revision is protected")
	assert.Equal(t, 1+5, len(remaining),
		"referenced revisions plus at most DefaultRevisionHistoryLimit unused ones survive")
	assert.False(t, remaining["rbgs-orphan-0"], "the oldest unused orphan is pruned first")
	assert.False(t, remaining["rbgs-orphan-1"], "the second-oldest unused orphan is pruned")
	assert.True(t, remaining["rbgs-orphan-6"], "the newest unused orphan is retained")
}

// TestVerify_R6_RevisionAdoptionRefusedWhenParentReplaced_Contract: if the in-memory
// set is stale (the live object carries a different UID), adopting predecessor history
// must fail loudly instead of attaching revisions to a dead parent.
func TestVerify_R6_RevisionAdoptionRefusedWhenParentReplaced_Contract(t *testing.T) {
	ctx := context.Background()
	template := revisionTestTemplate(nil)
	set := revisionTestRollingSet(1, &groupSetRevision{template: template})
	orphan := &appsv1.ControllerRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rbgs-orphan",
			Namespace: set.Namespace,
			Labels:    map[string]string{constants.GroupSetNameLabelKey: set.Name},
		},
		Data: runtime.RawExtension{Raw: []byte(`{"spec":{}}`)},
	}
	r, _ := revisionTestRollingReconciler(t, set, orphan)
	// Simulate the stale-cache race: the stored set is a different incarnation.
	stored := &workloadsv1alpha2.RoleBasedGroupSet{}
	require.NoError(t, r.client.Get(ctx, client.ObjectKeyFromObject(set), stored))
	require.NoError(t, r.client.Delete(ctx, stored))
	recreated := set.DeepCopy()
	recreated.UID = "new-incarnation-uid"
	recreated.ResourceVersion = ""
	require.NoError(t, r.client.Create(ctx, recreated))

	children := &workloadsv1alpha2.RoleBasedGroupList{}
	require.NoError(t, r.client.List(ctx, children, client.InNamespace(set.Namespace)))
	err := r.pruneGroupSetRevisions(ctx, set, children)
	require.Error(t, err, "adoption must be refused when the parent was replaced")
	assert.Contains(t, err.Error(), "parent was replaced")
	stored2 := &appsv1.ControllerRevision{}
	require.NoError(t, r.client.Get(ctx, client.ObjectKeyFromObject(orphan), stored2))
	assert.Nil(t, metav1.GetControllerOfNoCopy(stored2), "the orphan revision must not be adopted")
}

// ---- R6FINALIZE (requestless metadata-only completion) ---------------------

// completeAllWorkloads plays the downstream controllers for the current generation of
// the RBG's workloads: each RoleInstanceSet reports full status and gets one ready,
// up-to-date instance.
func completeRISWorkloads(t *testing.T, c client.Client, rbg *workloadsv1alpha2.RoleBasedGroup) {
	ctx := context.Background()
	risList := &workloadsv1alpha2.RoleInstanceSetList{}
	require.NoError(t, c.List(ctx, risList, client.InNamespace(rbg.Namespace)))
	for i := range risList.Items {
		ris := &risList.Items[i]
		if !metav1.IsControlledBy(ris, rbg) {
			continue
		}
		latest := &workloadsv1alpha2.RoleInstanceSet{}
		require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(ris), latest))
		replicas := ptr.Deref(latest.Spec.Replicas, 1)
		latest.Status.ObservedGeneration = latest.Generation
		latest.Status.Replicas = replicas
		latest.Status.ReadyReplicas = replicas
		latest.Status.UpdatedReplicas = replicas
		latest.Status.UpdatedReadyReplicas = replicas
		latest.Status.UpdateRevision = "verify-rev-1"
		require.NoError(t, c.Status().Update(ctx, latest))

		selector, err := metav1.LabelSelectorAsSelector(latest.Spec.Selector)
		require.NoError(t, err)
		instances := &workloadsv1alpha2.RoleInstanceList{}
		require.NoError(t, c.List(ctx, instances, client.InNamespace(rbg.Namespace),
			client.MatchingLabelsSelector{Selector: selector}))
		existing := 0
		for j := range instances.Items {
			if metav1.IsControlledBy(&instances.Items[j], latest) {
				existing++
			}
		}
		for k := existing; k < int(replicas); k++ {
			instance := &workloadsv1alpha2.RoleInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:       fmt.Sprintf("%s-verify-%d", latest.Name, k),
					Namespace:  latest.Namespace,
					Labels:     map[string]string{appsv1.ControllerRevisionHashLabelKey: "verify-rev-1"},
					Finalizers: []string{},
				},
				Spec: workloadsv1alpha2.RoleInstanceSpec{},
			}
			for key, value := range latest.Spec.Selector.MatchLabels {
				instance.Labels[key] = value
			}
			require.NoError(t, ctrl.SetControllerReference(latest, instance, c.Scheme()))
			require.NoError(t, c.Create(ctx, instance))
			instance.Status.ObservedGeneration = instance.Generation
			instance.Status.Conditions = []workloadsv1alpha2.RoleInstanceCondition{{
				Type:   workloadsv1alpha2.RoleInstanceReady,
				Status: corev1.ConditionTrue,
			}}
			require.NoError(t, c.Status().Update(ctx, instance))
		}
	}
}

// TestVerify_R6_RequestlessLabelsOnlyCompletesInSameReconcile_Contract drives the FULL
// RBG Reconcile (no set, no rollout-request token): after a labels-only role change -
// which the workload predicate filters, so no follow-up event will ever arrive - the
// reconcile that applies the change must itself clear the rollout barrier. On
// 13cbcab4 (poll removed, no post-application finalize) the barrier stays True with no
// requeue: a permanent wedge.
func TestVerify_R6_RequestlessLabelsOnlyCompletesInSameReconcile(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	rbg := wrappersv2.BuildBasicRoleBasedGroup("r6-labels", "default").Obj()
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(rbg).
		WithStatusSubresource(&workloadsv1alpha2.RoleBasedGroup{}, &workloadsv1alpha2.RoleInstanceSet{},
			&workloadsv1alpha2.RoleInstance{}).
		Build()
	r := &RoleBasedGroupReconciler{
		client:             c,
		apiReader:          c,
		scheme:             scheme,
		recorder:           record.NewFakeRecorder(20),
		workloadReconciler: make(map[string]reconciler.WorkloadReconciler),
		gangScheduler:      &stubGangScheduler{},
	}
	ctx := ctrl.LoggerInto(context.Background(), logr.Discard())
	request := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(rbg)}

	barrier := func() *metav1.Condition {
		current := &workloadsv1alpha2.RoleBasedGroup{}
		require.NoError(t, c.Get(ctx, request.NamespacedName, current))
		return apimeta.FindStatusCondition(current.Status.Conditions,
			string(workloadsv1alpha2.RoleBasedGroupRollingUpdateInProgress))
	}

	// Reconcile 1: workloads are created; nothing is ready yet.
	_, err := r.Reconcile(ctx, request)
	require.NoError(t, err)
	require.NotNil(t, barrier())
	require.Equal(t, metav1.ConditionTrue, barrier().Status, "workloads still rolling")

	// The downstream controllers complete the current generation.
	completeRISWorkloads(t, c, rbg)
	_, err = r.Reconcile(ctx, request)
	require.NoError(t, err)
	require.NotNil(t, barrier())
	require.Equal(t, metav1.ConditionFalse, barrier().Status, "fixture sanity: completed workloads clear the barrier")

	// A labels-only role change: no workload spec changes, so no workload update event
	// will ever fire (WorkloadPredicate). Nobody writes a rollout request for a
	// standalone RBG either.
	latest := &workloadsv1alpha2.RoleBasedGroup{}
	require.NoError(t, c.Get(ctx, request.NamespacedName, latest))
	latest.Spec.Roles[0].Labels = map[string]string{"tier": "b"}
	require.NoError(t, c.Update(ctx, latest))

	result, err := r.Reconcile(ctx, request)
	require.NoError(t, err)
	got := barrier()
	require.NotNil(t, got)
	assert.Equal(t, metav1.ConditionFalse, got.Status,
		"the reconcile that applies a labels-only change must itself complete the rollout")
	assert.Equal(t, "AllRolesUpdated", got.Reason)
	assert.Zero(t, result.RequeueAfter, "a completed requestless rollout must not keep polling")

	stored := &workloadsv1alpha2.RoleBasedGroup{}
	require.NoError(t, c.Get(ctx, request.NamespacedName, stored))
	assert.Empty(t, stored.Status.ObservedRolloutRequest, "no request existed, so no ack may be stamped")
	assert.Empty(t, stored.Annotations[constants.RolloutRequestAnnotationKey],
		"a standalone RBG must never gain a controller-owned request token")

	// And the labels actually reached the workload object.
	risList := &workloadsv1alpha2.RoleInstanceSetList{}
	require.NoError(t, c.List(ctx, risList, client.InNamespace("default")))
	require.NotEmpty(t, risList.Items)
	assert.Equal(t, "b", risList.Items[0].Labels["tier"], "the labels-only change must be applied to the workload")
}
