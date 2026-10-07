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

package statefulmode

// Reviewer-A (Claude) verification harness for PR #484
// (https://github.com/sgl-project/rbg/pull/484).
//
// This file is additive-only (no production code touched). It encodes the
// PR's premise (P0) and the review findings as contract tests that are
// expected to be:
//
//   - RED on the PR base (merge-base 9b22f4a) for P0/G1..G4 — that redness IS
//     the premise reproduction (the stall exists today, without the patch).
//   - GREEN on the PR head for P0/G1..G4 — the patch fixes them.
//   - RED on the PR head for F1 — the cancelled-rollout signal regression
//     (cheyang's P2, independently reproduced here). GREEN on the base for F1
//     shows it is a regression introduced by the PR, not pre-existing.
//
// The file deliberately references only symbols that exist on BOTH the base
// branch and the PR head, so the same file can be grafted onto either ref.

import (
	"context"
	"testing"
	"time"

	apps "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	intstrutil "k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// newClaudeControl returns a control wired to recording fakes, matching the
// convention of the other unit tests in this package.
func newClaudeControl(t *testing.T) (*defaultStatefulInstanceSetControl, *fakeInstanceObjectManager) {
	t.Helper()
	objects := &fakeInstanceObjectManager{}
	recorder := record.NewFakeRecorder(64)
	control := &defaultStatefulInstanceSetControl{
		instanceControl: NewStatefulInstanceControlFromManager(objects, recorder),
		inplaceControl:  &fakeInplaceControl{},
		recorder:        recorder,
	}
	return control, objects
}

// seedUnhealthy backdates the unhealthy-since entry for every unhealthy
// instance by `age`, so the stable-unhealthy gate can be exercised without
// sleeping.
func seedUnhealthy(set *workloadsv1alpha2.RoleInstanceSet, instances []*workloadsv1alpha2.RoleInstance, age time.Duration) {
	for _, inst := range instances {
		if inst != nil && !isHealthy(inst) {
			instanceUnhealthySince.Store(getInstanceHealthKey(set, inst), time.Now().Add(-age))
		}
	}
}

// ---------------------------------------------------------------------------
// P0 — the PR's premise: an unhealthy NON-target instance that consumes the
// availability budget gets no requeue, so the rollout stalls until an
// unrelated event triggers reconciliation (issue #464, gap remaining after
// #470).
// ---------------------------------------------------------------------------

// TestClaudeP0UnhealthyNonTargetGetsRequeue is the premise reproduction.
// Contract: while an unhealthy non-target (already at updateRev) consumes the
// budget and blocks the (healthy) update target, the controller must schedule
// a timed requeue. On the base branch no requeue is pushed (stall).
func TestClaudeP0UnhealthyNonTargetGetsRequeue(t *testing.T) {
	resetInstanceUnhealthySince()
	t.Cleanup(resetInstanceUnhealthySince)

	set := buildSet("s", 2, ptr.To(intstrutil.FromInt32(0)), ptr.To(intstrutil.FromInt32(1)))
	key := getInstanceSetKey(set)
	durationStore.Pop(key)
	t.Cleanup(func() { durationStore.Pop(key) })

	control, objects := newClaudeControl(t)
	replicas := []*workloadsv1alpha2.RoleInstance{
		buildInst("s", 0, testOldRev, true, true),     // healthy update target
		buildInst("s", 1, testUpdateRev, false, true), // unhealthy non-target at updateRev
	}
	seedUnhealthy(set, replicas, 6*time.Second)

	topo := topology{
		startOrdinal: 0, endOrdinal: 2, surgeStart: 2,
		replicas: 2, partition: 0, maxUnavailable: 1, maxSurge: 0, inRollout: true,
	}
	if _, err := control.progressUpdate(set, &workloadsv1alpha2.RoleInstanceSetStatus{},
		newStatefulRevision(testOldRev, 1), newStatefulRevision(testUpdateRev, 2),
		nil, replicas, replicas, 0, topo); err != nil {
		t.Fatal(err)
	}

	wait := durationStore.Pop(key)
	if wait <= 0 || wait > 4*time.Second {
		t.Fatalf("P0: no timed requeue scheduled for the budget-exhausting unhealthy non-target: wait = %v, want (0, 4s]", wait)
	}
	if len(objects.deleted) != 0 {
		t.Fatalf("P0: unhealthy non-target must still consume the budget (not be free-deleted): deleted %v", objects.deleted)
	}
}

// ---------------------------------------------------------------------------
// G1 — OrderedReady: rollout must resume past its readiness gate once the
// first unhealthy update target is stably unhealthy.
// ---------------------------------------------------------------------------

// TestClaudeG1OrderedReadyResumesStablyUnhealthyTarget: with OrderedReady,
// the monotonic loop stops before Phase C at the first unhealthy replica; the
// rollout must still be retried (timed requeue before the window expires) and
// must resume (delete/replace only that ordinal) once the window expires.
func TestClaudeG1OrderedReadyResumesStablyUnhealthyTarget(t *testing.T) {
	resetInstanceUnhealthySince()
	t.Cleanup(resetInstanceUnhealthySince)

	set := newRevisionTestSet("nginx:1.0")
	set.Spec.Replicas = ptr.To[int32](2)
	set.Spec.PodManagementPolicy = constants.OrderedReadyPodManagement
	set.Spec.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{"app": set.Name}}
	set.Spec.UpdateStrategy.MaxSurge = ptr.To(intstrutil.FromInt32(0))
	set.Spec.UpdateStrategy.MaxUnavailable = ptr.To(intstrutil.FromInt32(0))

	currentRev, err := newRevision(set, 1, ptr.To[int32](0))
	if err != nil {
		t.Fatal(err)
	}
	set.Spec.RoleInstanceTemplate.Components[0].Template.Spec.Containers[0].Image = "nginx:2.0"
	updateRev, err := newRevision(set, 2, ptr.To[int32](0))
	if err != nil {
		t.Fatal(err)
	}
	set.Status.CurrentRevision = currentRev.Name
	set.Status.UpdateRevision = updateRev.Name

	instances := []*workloadsv1alpha2.RoleInstance{
		buildInst(set.Name, 0, currentRev.Name, false, true),
		buildInst(set.Name, 1, currentRev.Name, true, true),
	}
	key := getInstanceSetKey(set)
	durationStore.Pop(key)
	t.Cleanup(func() { durationStore.Pop(key) })
	control, objects := newClaudeControl(t)

	reconcile := func() time.Duration {
		t.Helper()
		_, err := control.updateStatefulInstanceSet(context.Background(), set, currentRev, updateRev, 0,
			instances, []*apps.ControllerRevision{currentRev, updateRev})
		if err != nil {
			t.Fatal(err)
		}
		return durationStore.Pop(key)
	}

	// Phase 1: window not yet expired — a retry must be scheduled and nothing
	// deleted (the instance is still budget-protected / gated).
	seedUnhealthy(set, instances, 6*time.Second)
	if wait := reconcile(); wait <= 0 || wait > 4*time.Second {
		t.Fatalf("G1 phase 1: no retry scheduled for the OrderedReady unhealthy gate: wait = %v, want (0, 4s]", wait)
	}
	if len(objects.deleted) != 0 {
		t.Fatalf("G1 phase 1: deleted before the health window expired: %v", objects.deleted)
	}

	// Phase 2: window expired — the rollout must resume, and only at that
	// ordinal.
	seedUnhealthy(set, instances, 2*stableUnhealthyDuration)
	reconcile()
	deleted := sets.New(objects.deleted...)
	if !deleted.Has(instances[0].Name) {
		t.Fatalf("G1 phase 2: OrderedReady rollout did not resume at the stably-unhealthy target: deleted %v", objects.deleted)
	}
	if deleted.Has(instances[1].Name) {
		t.Fatalf("G1 phase 2: OrderedReady cleanup touched a later ordinal: deleted %v", objects.deleted)
	}
}

// ---------------------------------------------------------------------------
// G2 — early A -> B -> A rollback with matching revision names must resume.
// ---------------------------------------------------------------------------

// TestClaudeG2EarlyRollbackSameNameResumes: after A -> B -> A, when the
// current and update revision names match again while live instances are
// still at B, the controller must detect the stale base and replace it.
func TestClaudeG2EarlyRollbackSameNameResumes(t *testing.T) {
	resetInstanceUnhealthySince()
	t.Cleanup(resetInstanceUnhealthySince)

	set := newRevisionTestSet("nginx:1.0")
	set.Spec.Replicas = ptr.To[int32](2)
	set.Spec.PodManagementPolicy = constants.ParallelPodManagement
	set.Spec.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{"app": set.Name}}
	set.Spec.UpdateStrategy.MaxSurge = ptr.To(intstrutil.FromInt32(0))
	set.Spec.UpdateStrategy.MaxUnavailable = ptr.To(intstrutil.FromInt32(2))

	currentRev, err := newRevision(set, 1, ptr.To[int32](0))
	if err != nil {
		t.Fatal(err)
	}
	set.Spec.RoleInstanceTemplate.Components[0].Template.Spec.Containers[0].Image = "nginx:2.0"
	rolledForwardRev, err := newRevision(set, 2, ptr.To[int32](0))
	if err != nil {
		t.Fatal(err)
	}
	set.Spec.RoleInstanceTemplate.Components[0].Template.Spec.Containers[0].Image = "nginx:1.0"
	updateRev, err := newRevision(set, 3, ptr.To[int32](0))
	if err != nil {
		t.Fatal(err)
	}
	if currentRev.Name != updateRev.Name || currentRev.Name == rolledForwardRev.Name {
		t.Fatalf("G2: revisions = %q, %q, %q; want A, B, A", currentRev.Name, rolledForwardRev.Name, updateRev.Name)
	}
	set.Status.CurrentRevision = currentRev.Name
	set.Status.UpdateRevision = rolledForwardRev.Name

	instances := []*workloadsv1alpha2.RoleInstance{
		buildInst(set.Name, 0, rolledForwardRev.Name, false, true),
		buildInst(set.Name, 1, rolledForwardRev.Name, false, true),
	}
	control, objects := newClaudeControl(t)
	key := getInstanceSetKey(set)
	durationStore.Pop(key)
	t.Cleanup(func() { durationStore.Pop(key) })

	seedUnhealthy(set, instances, 2*stableUnhealthyDuration)
	if _, err := control.updateStatefulInstanceSet(context.Background(), set, currentRev, updateRev, 0,
		instances, []*apps.ControllerRevision{currentRev, rolledForwardRev, updateRev}); err != nil {
		t.Fatal(err)
	}
	deleted := sets.New(objects.deleted...)
	for _, inst := range instances {
		if !deleted.Has(inst.Name) {
			t.Fatalf("G2: early rollback did not replace stale instance %s: deleted %v", inst.Name, objects.deleted)
		}
	}
}

// ---------------------------------------------------------------------------
// G3 — OrderedReady must recycle an in-range stale-revision surge slot.
// ---------------------------------------------------------------------------

// TestClaudeG3OrderedReadyRecyclesStaleSurge: an unhealthy stale-revision
// surge instance inside the current rollout's surge range blocks the
// OrderedReady monotonic loop before Phase C; it must still be recycled.
func TestClaudeG3OrderedReadyRecyclesStaleSurge(t *testing.T) {
	resetInstanceUnhealthySince()
	t.Cleanup(resetInstanceUnhealthySince)

	set := newRevisionTestSet("nginx:1.0")
	set.Spec.Replicas = ptr.To[int32](2)
	set.Spec.PodManagementPolicy = constants.OrderedReadyPodManagement
	set.Spec.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{"app": set.Name}}
	set.Spec.UpdateStrategy.MaxSurge = ptr.To(intstrutil.FromInt32(1))
	set.Spec.UpdateStrategy.MaxUnavailable = ptr.To(intstrutil.FromInt32(0))

	currentRev, err := newRevision(set, 1, ptr.To[int32](0))
	if err != nil {
		t.Fatal(err)
	}
	set.Spec.RoleInstanceTemplate.Components[0].Template.Spec.Containers[0].Image = "nginx:2.0"
	midRev, err := newRevision(set, 2, ptr.To[int32](0))
	if err != nil {
		t.Fatal(err)
	}
	set.Spec.RoleInstanceTemplate.Components[0].Template.Spec.Containers[0].Image = "nginx:3.0"
	updateRev, err := newRevision(set, 3, ptr.To[int32](0))
	if err != nil {
		t.Fatal(err)
	}
	set.Status.CurrentRevision = currentRev.Name
	set.Status.UpdateRevision = midRev.Name

	instances := []*workloadsv1alpha2.RoleInstance{
		buildInst(set.Name, 0, currentRev.Name, true, true),
		buildInst(set.Name, 1, currentRev.Name, true, true),
		buildInst(set.Name, 2, midRev.Name, false, true),
	}
	seedUnhealthy(set, instances, 2*stableUnhealthyDuration)

	control, objects := newClaudeControl(t)
	if _, err := control.updateStatefulInstanceSet(context.Background(), set, currentRev, updateRev, 0,
		instances, []*apps.ControllerRevision{currentRev, midRev, updateRev}); err != nil {
		t.Fatal(err)
	}
	deleted := sets.New(objects.deleted...)
	if !deleted.Has(instances[2].Name) {
		t.Fatalf("G3: OrderedReady did not recycle the stale unhealthy surge: deleted %v", objects.deleted)
	}
	if deleted.Has(instances[0].Name) || deleted.Has(instances[1].Name) {
		t.Fatalf("G3: stale-surge cleanup touched base instances: deleted %v", objects.deleted)
	}
}

// ---------------------------------------------------------------------------
// F1 — cancelled early rollback leaves the in-memory signal active, which
// retains surge (here: blocks an ordinary scale-down) while a base instance
// is unhealthy. Reported by cheyang (P2); independently reproduced here with
// two trigger variants.
// ---------------------------------------------------------------------------

// TestClaudeF1CancelledRollbackBlocksScaleDown drives the set through a
// cancelled A -> B rollout, reverts to A, and then scales down. The desired
// end state is replicas=1: the out-of-range instance must be deleted. On the
// PR head the retained early-rollback signal keeps the set "in rollout",
// retains the second instance as surge, and the scale-down never happens
// while ordinal 0 stays unhealthy.
func TestClaudeF1CancelledRollbackBlocksScaleDown(t *testing.T) {
	for _, variant := range []string{"paused-cancel", "immediate-revert"} {
		t.Run(variant, func(t *testing.T) {
			resetInstanceUnhealthySince()
			t.Cleanup(resetInstanceUnhealthySince)

			set := newRevisionTestSet("nginx:1.0")
			set.Spec.Replicas = ptr.To[int32](2)
			set.Spec.PodManagementPolicy = constants.ParallelPodManagement
			set.Spec.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{"app": set.Name}}
			set.Spec.UpdateStrategy.MaxSurge = ptr.To(intstrutil.FromInt32(1))
			set.Spec.UpdateStrategy.MaxUnavailable = ptr.To(intstrutil.FromInt32(0))
			set.UID = "f1-set-uid"

			currentRev, err := newRevision(set, 1, ptr.To[int32](0))
			if err != nil {
				t.Fatal(err)
			}
			set.Spec.RoleInstanceTemplate.Components[0].Template.Spec.Containers[0].Image = "nginx:2.0"
			rolledForwardRev, err := newRevision(set, 2, ptr.To[int32](0))
			if err != nil {
				t.Fatal(err)
			}
			set.Spec.RoleInstanceTemplate.Components[0].Template.Spec.Containers[0].Image = "nginx:1.0"
			updateRev, err := newRevision(set, 3, ptr.To[int32](0))
			if err != nil {
				t.Fatal(err)
			}
			if currentRev.Name != updateRev.Name || currentRev.Name == rolledForwardRev.Name {
				t.Fatalf("F1: revisions = %q, %q, %q; want A, B, A", currentRev.Name, rolledForwardRev.Name, updateRev.Name)
			}
			set.Status.CurrentRevision = currentRev.Name
			set.Status.UpdateRevision = rolledForwardRev.Name

			instances := []*workloadsv1alpha2.RoleInstance{
				buildInst(set.Name, 0, currentRev.Name, false, true), // stays unhealthy throughout
				buildInst(set.Name, 1, currentRev.Name, true, true),
			}
			control, objects := newClaudeControl(t)
			key := getInstanceSetKey(set)
			durationStore.Pop(key)
			t.Cleanup(func() { durationStore.Pop(key) })

			// Phase 1: template moved to B. In the "paused-cancel" variant the
			// rollout is paused, so no update work can happen at all; in the
			// "immediate-revert" variant the rollout is live but the unhealthy
			// base exhausts the budget (maxUnavailable=0) before the window
			// expires (seeded 6s < 10s), so no instance is actually updated
			// either — ord0 must survive phase 1 as a non-stably-unhealthy,
			// budget-consuming instance on BOTH refs.
			if variant == "paused-cancel" {
				set.Spec.UpdateStrategy.Paused = true
			}
			seedUnhealthy(set, instances, 6*time.Second)
			if _, err := control.updateStatefulInstanceSet(context.Background(), set, currentRev, rolledForwardRev, 0,
				instances, []*apps.ControllerRevision{currentRev, rolledForwardRev}); err != nil {
				t.Fatal(err)
			}

			// Phase 2: revert to A (revision names match again), unpause, and
			// scale down to 1 replica.
			set.Spec.UpdateStrategy.Paused = false
			set.Spec.Replicas = ptr.To[int32](1)
			seedUnhealthy(set, instances, 2*stableUnhealthyDuration)
			if _, err := control.updateStatefulInstanceSet(context.Background(), set, currentRev, updateRev, 0,
				instances, []*apps.ControllerRevision{currentRev, rolledForwardRev, updateRev}); err != nil {
				t.Fatal(err)
			}

			// The set now wants 1 replica and every instance is already at the
			// desired revision A: the out-of-range instance must be deleted.
			deleted := sets.New(objects.deleted...)
			if !deleted.Has(instances[1].Name) {
				t.Fatalf("F1 (%s): scale-down blocked; instance %s retained (deleted: %v). "+
					"Desired replicas is 1 and no update work remains, but the set stays 'in rollout' "+
					"and keeps the extra instance as surge while %s is unhealthy.",
					variant, instances[1].Name, objects.deleted, instances[0].Name)
			}
			if deleted.Has(instances[0].Name) {
				t.Fatalf("F1 (%s): the base instance at the desired revision was deleted: %v", variant, objects.deleted)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// F2 — regression guard for the fix's own claim: unhealthy non-targets must
// still CONSUME the availability budget (the retry must not relax
// maxUnavailable). Green on both base and head.
// ---------------------------------------------------------------------------

func TestClaudeF2UnhealthyNonTargetStillConsumesBudget(t *testing.T) {
	resetInstanceUnhealthySince()
	t.Cleanup(resetInstanceUnhealthySince)

	set := buildSet("s", 2, ptr.To(intstrutil.FromInt32(0)), ptr.To(intstrutil.FromInt32(1)))
	key := getInstanceSetKey(set)
	durationStore.Pop(key)
	t.Cleanup(func() { durationStore.Pop(key) })

	control, objects := newClaudeControl(t)
	replicas := []*workloadsv1alpha2.RoleInstance{
		buildInst("s", 0, testOldRev, true, true),
		buildInst("s", 1, testUpdateRev, false, true),
	}
	seedUnhealthy(set, replicas, 2*stableUnhealthyDuration)

	topo := topology{
		startOrdinal: 0, endOrdinal: 2, surgeStart: 2,
		replicas: 2, partition: 0, maxUnavailable: 1, maxSurge: 0, inRollout: true,
	}
	if _, err := control.progressUpdate(set, &workloadsv1alpha2.RoleInstanceSetStatus{},
		newStatefulRevision(testOldRev, 1), newStatefulRevision(testUpdateRev, 2),
		nil, replicas, replicas, 0, topo); err != nil {
		t.Fatal(err)
	}
	if wait := durationStore.Pop(key); wait != 0 {
		t.Fatalf("F2: expired unhealthy window kept scheduling retries: %v", wait)
	}
	if len(objects.deleted) != 0 {
		t.Fatalf("F2: budget was relaxed — unhealthy non-target free-deleted: %v", objects.deleted)
	}
}
