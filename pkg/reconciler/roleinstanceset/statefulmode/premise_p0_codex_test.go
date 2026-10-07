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

// Verification harness for the review of PR #484 (fix: recover stateful
// rollout stalls), reviewer Codex. These are CONTRACT tests: they assert the
// intended correct behavior claimed by the PR and by issue #464.
//
// Polarity: contract. Expected RED on the base branch (9b22f4a8, the stall is
// the premise being confirmed) and GREEN on the PR head (the fix works).
// The file deliberately uses only symbols that exist on the base branch so it
// compiles against both.

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

func codexControl(objects *fakeInstanceObjectManager) *defaultStatefulInstanceSetControl {
	recorder := record.NewFakeRecorder(64)
	return &defaultStatefulInstanceSetControl{
		instanceControl: NewStatefulInstanceControlFromManager(objects, recorder),
		inplaceControl:  &fakeInplaceControl{},
		recorder:        recorder,
	}
}

// codexABArevisions builds the A -> B -> A' revision chain where A' has the
// same NAME as A (rollback to identical template) but a higher Revision.
func codexABArevisions(t *testing.T, set *workloadsv1alpha2.RoleInstanceSet) (a, b, a2 *apps.ControllerRevision) {
	t.Helper()
	var err error
	set.Spec.RoleInstanceTemplate.Components[0].Template.Spec.Containers[0].Image = "nginx:1.0"
	if a, err = newRevision(set, 1, ptr.To[int32](0)); err != nil {
		t.Fatal(err)
	}
	set.Spec.RoleInstanceTemplate.Components[0].Template.Spec.Containers[0].Image = "nginx:2.0"
	if b, err = newRevision(set, 2, ptr.To[int32](0)); err != nil {
		t.Fatal(err)
	}
	set.Spec.RoleInstanceTemplate.Components[0].Template.Spec.Containers[0].Image = "nginx:1.0"
	if a2, err = newRevision(set, 3, ptr.To[int32](0)); err != nil {
		t.Fatal(err)
	}
	if a.Name != a2.Name || a.Name == b.Name {
		t.Fatalf("revision chain = %q, %q, %q; want A, B, A (same name for A and A')", a.Name, b.Name, a2.Name)
	}
	return a, b, a2
}

// P0a — issue #464 component: OrderedReady stops at its readiness gate before
// reaching the rollout retry path.
//
// Contract: with an OrderedReady rollout whose first update target is
// unhealthy, the controller must (1) schedule a retry at the end of the
// stable-unhealthy window instead of depending on an unrelated event, and
// (2) once the window has expired, replace that stably-unhealthy target
// (cleanup-unhealthy semantics) without touching later ordinals.
func TestP0CodexOrderedReadyStalledTargetResumesAfterWindow(t *testing.T) {
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
		buildInst(set.Name, 0, currentRev.Name, false, true), // unhealthy update target
		buildInst(set.Name, 1, currentRev.Name, true, true),
	}
	key := getInstanceSetKey(set)
	durationStore.Pop(key)
	t.Cleanup(func() { durationStore.Pop(key) })

	objects := &fakeInstanceObjectManager{}
	control := codexControl(objects)
	reconcile := func() time.Duration {
		t.Helper()
		if _, err := control.updateStatefulInstanceSet(context.Background(), set, currentRev, updateRev, 0,
			instances, []*apps.ControllerRevision{currentRev, updateRev}); err != nil {
			t.Fatal(err)
		}
		return durationStore.Pop(key)
	}

	// Phase 1: window not expired yet — a retry must be scheduled so the
	// rollout resumes even if no further instance event ever arrives.
	instanceUnhealthySince.Store(getInstanceHealthKey(set, instances[0]), time.Now())
	wait := reconcile()
	if wait <= 0 {
		t.Fatalf("STALL: no requeue scheduled for OrderedReady rollout blocked on unhealthy target; " +
			"rollout only resumes on an unrelated event (issue #464)")
	}
	if wait > stableUnhealthyDuration {
		t.Fatalf("requeue = %v, want within the stable-unhealthy window %v", wait, stableUnhealthyDuration)
	}
	if len(objects.deleted) != 0 {
		t.Fatalf("deleted before the health window expired: %v", objects.deleted)
	}

	// Phase 2: window expired — the stably-unhealthy target must be replaced
	// (free cleanup) and later ordinals must remain untouched.
	instanceUnhealthySince.Store(getInstanceHealthKey(set, instances[0]), time.Now().Add(-2*stableUnhealthyDuration))
	if wait := reconcile(); wait != 0 {
		t.Fatalf("expired health window scheduled another retry: %v", wait)
	}
	deleted := sets.New(objects.deleted...)
	if !deleted.Has(instances[0].Name) {
		t.Fatalf("STALL: OrderedReady rollout did not replace stably-unhealthy target %s: deleted %v",
			instances[0].Name, objects.deleted)
	}
	if deleted.Has(instances[1].Name) {
		t.Fatalf("OrderedReady cleanup touched a later ordinal: deleted %v", objects.deleted)
	}
}

// P0b — early A -> B -> A rollback with identical CurrentRevision and
// UpdateRevision names.
//
// Contract: base instances still at the intermediate revision B must be
// detected as stale and rolled back to A even though the two revision names
// match. On the base branch inRollout=false, so the stale instances are
// never touched: the rollback is silently skipped.
func TestP0CodexEarlyRollbackSameNameRevisionResumes(t *testing.T) {
	resetInstanceUnhealthySince()
	t.Cleanup(resetInstanceUnhealthySince)

	set := newRevisionTestSet("nginx:1.0")
	set.Spec.Replicas = ptr.To[int32](2)
	set.Spec.PodManagementPolicy = constants.ParallelPodManagement
	set.Spec.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{"app": set.Name}}
	set.Spec.UpdateStrategy.MaxSurge = ptr.To(intstrutil.FromInt32(0))
	set.Spec.UpdateStrategy.MaxUnavailable = ptr.To(intstrutil.FromInt32(2))

	revA, revB, revA2 := codexABArevisions(t, set)
	set.Status.CurrentRevision = revA.Name
	set.Status.UpdateRevision = revB.Name

	instances := []*workloadsv1alpha2.RoleInstance{
		buildInst(set.Name, 0, revB.Name, true, true),
		buildInst(set.Name, 1, revB.Name, true, true),
	}
	key := getInstanceSetKey(set)
	durationStore.Pop(key)
	t.Cleanup(func() { durationStore.Pop(key) })

	objects := &fakeInstanceObjectManager{}
	control := codexControl(objects)
	if _, err := control.updateStatefulInstanceSet(context.Background(), set, revA, revA2, 0,
		instances, []*apps.ControllerRevision{revA, revB, revA2}); err != nil {
		t.Fatal(err)
	}

	deleted := sets.New(objects.deleted...)
	for _, inst := range instances {
		if !deleted.Has(inst.Name) {
			t.Fatalf("STALL: early A->B->A rollback skipped stale instance %s (rev %s, want %s): deleted %v",
				inst.Name, revB.Name, revA2.Name, objects.deleted)
		}
	}
}

// P0c — Parallel rollout: an unhealthy non-target (already at updateRev)
// consumes the maxUnavailable budget and blocks the remaining healthy target.
//
// Contract: the controller schedules a requeue for the end of the unhealthy
// instance's window so progress does not depend on an unrelated event. The
// retry must NOT relax the budget: the healthy target stays blocked while the
// non-target is down.
func TestP0CodexParallelBlockedTargetSchedulesRetry(t *testing.T) {
	resetInstanceUnhealthySince()
	t.Cleanup(resetInstanceUnhealthySince)

	set := newRevisionTestSet("nginx:1.0")
	set.Spec.Replicas = ptr.To[int32](2)
	set.Spec.PodManagementPolicy = constants.ParallelPodManagement
	set.Spec.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{"app": set.Name}}
	set.Spec.UpdateStrategy.MaxSurge = ptr.To(intstrutil.FromInt32(0))
	set.Spec.UpdateStrategy.MaxUnavailable = ptr.To(intstrutil.FromInt32(1))

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
		buildInst(set.Name, 0, updateRev.Name, false, true), // unhealthy non-target, consumes budget
		buildInst(set.Name, 1, currentRev.Name, true, true), // healthy target, blocked by budget
	}
	key := getInstanceSetKey(set)
	durationStore.Pop(key)
	t.Cleanup(func() { durationStore.Pop(key) })

	objects := &fakeInstanceObjectManager{}
	control := codexControl(objects)
	instanceUnhealthySince.Store(getInstanceHealthKey(set, instances[0]), time.Now())

	if _, err := control.updateStatefulInstanceSet(context.Background(), set, currentRev, updateRev, 0,
		instances, []*apps.ControllerRevision{currentRev, updateRev}); err != nil {
		t.Fatal(err)
	}
	if len(objects.deleted) != 0 {
		t.Fatalf("budget violated: healthy target deleted while non-target down: %v", objects.deleted)
	}
	wait := durationStore.Pop(key)
	if wait <= 0 {
		t.Fatalf("STALL: budget blocked by unhealthy non-target but no requeue scheduled; " +
			"rollout waits for an unrelated event (issue #464)")
	}
	if wait > stableUnhealthyDuration {
		t.Fatalf("requeue = %v, want within the stable-unhealthy window %v", wait, stableUnhealthyDuration)
	}
}

// P0d — the early-rollback surge bridge (no controller restart).
//
// Contract: during A -> B -> A with maxSurge=1/maxUnavailable=0, once the
// stale B base disappears and its A replacement is not yet Ready, the
// existing surge instance must remain in range (it is the only thing keeping
// availability). On the base branch inRollout=false collapses endOrdinal to
// replicas, so Phase B condemns the protecting surge while the stale base is
// still present — the worst possible ordering.
func TestP0CodexEarlyRollbackKeepsSurgeAcrossReplacementGap(t *testing.T) {
	resetInstanceUnhealthySince()
	t.Cleanup(resetInstanceUnhealthySince)

	set := newRevisionTestSet("nginx:1.0")
	set.Spec.Replicas = ptr.To[int32](1)
	set.Spec.PodManagementPolicy = constants.ParallelPodManagement
	set.Spec.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{"app": set.Name}}
	set.Spec.UpdateStrategy.MaxSurge = ptr.To(intstrutil.FromInt32(1))
	set.Spec.UpdateStrategy.MaxUnavailable = ptr.To(intstrutil.FromInt32(0))

	revA, revB, revA2 := codexABArevisions(t, set)
	set.Status.CurrentRevision = revA.Name
	set.Status.UpdateRevision = revB.Name

	key := getInstanceSetKey(set)
	durationStore.Pop(key)
	t.Cleanup(func() { durationStore.Pop(key) })

	objects := &fakeInstanceObjectManager{}
	control := codexControl(objects)
	revisions := []*apps.ControllerRevision{revA, revB, revA2}
	reconcile := func(instances ...*workloadsv1alpha2.RoleInstance) {
		t.Helper()
		objects.created = nil
		objects.deleted = nil
		if _, err := control.updateStatefulInstanceSet(context.Background(), set, revA, revA2, 0,
			instances, revisions); err != nil {
			t.Fatal(err)
		}
	}

	base := buildInst(set.Name, 0, revB.Name, true, true)
	surge := buildInst(set.Name, 1, revA2.Name, true, true)

	// Step 1: stale B base + ready A surge. The stale base must be replaced;
	// the surge (the only available replica afterwards) must be retained.
	reconcile(base, surge)
	if !sets.New(objects.deleted...).Has(base.Name) {
		t.Fatalf("STALL: stale base not replaced despite ready surge: deleted %v", objects.deleted)
	}
	if sets.New(objects.deleted...).Has(surge.Name) {
		t.Fatalf("AVAILABILITY: protecting surge condemned while stale base still present: %v", objects.deleted)
	}

	// Step 2: stale base terminating, replacement not yet created -> surge retained.
	withTerminating(base)
	reconcile(base, surge)
	if len(objects.deleted) != 0 {
		t.Fatalf("surge condemned while stale base still terminating: %v", objects.deleted)
	}

	// Step 3 (the gap): stale base gone, replacement at A not Ready -> surge
	// must stay in range; it is the only available replica.
	replacement := buildInst(set.Name, 0, revA2.Name, false, true)
	reconcile(replacement, surge)
	if len(objects.deleted) != 0 {
		t.Fatalf("AVAILABILITY: surge condemned while replacement not ready (maxUnavailable=0 violated): %v",
			objects.deleted)
	}

	// Step 4: replacement ready -> surge falls out of range and is condemned.
	replacement.Status.Conditions[0].Status = "True"
	replacement.Status.ObservedGeneration = replacement.Generation
	reconcile(replacement, surge)
	if !sets.New(objects.deleted...).Has(surge.Name) {
		t.Fatalf("completed rollback did not condemn surge: deleted %v", objects.deleted)
	}
}
