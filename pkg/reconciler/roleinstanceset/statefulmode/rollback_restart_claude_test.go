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

// Reviewer A (Claude) verification harness for PR #508 / issue #482.
// These tests are the reviewer's independent repro of the PR's core claims;
// they are additive and leave production code untouched.
//
// Polarity: all tests below are CONTRACT tests — they assert the intended
// post-fix behavior and FAIL on code where the persisted replacement state
// does not protect surge across a simulated controller restart.

import (
	"context"
	"testing"

	apps "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// claudeRollbackSet builds the issue-#482 shape: replicas=1, maxSurge=1,
// maxUnavailable=0, so any premature surge removal violates availability.
func claudeRollbackSet(name string, replicas int32) *workloadsv1alpha2.RoleInstanceSet {
	set := buildSet(name, replicas,
		ptr.To(intstr.FromInt32(1)), ptr.To(intstr.FromInt32(0)))
	set.Status.CurrentRevision = testUpdateRev
	set.Status.UpdateRevision = testUpdateRev
	return set
}

// TestClaudeRestartSurgeProtectedByPersistedState reproduces the controller's
// post-restart view (issue #482 steps 7-11): the stale base was already
// deleted, its replacement is not Ready, and the only availability is the
// existing surge. With the replacement intent persisted in status, the fresh
// controller (empty process-local state) must keep the surge in range.
func TestClaudeRestartSurgeProtectedByPersistedState(t *testing.T) {
	set := claudeRollbackSet("claude-restart", 1)
	set.Status.RolloutReplacementState = &workloadsv1alpha2.RolloutReplacementState{
		UpdateRevision: testUpdateRev,
		Replicas:       1,
		Partition:      0,
	}
	// Replacement base (ordinal 0) is gone; surge at ordinal 1 is the only
	// ready instance. No in-memory state exists (fresh process).
	instances := []*workloadsv1alpha2.RoleInstance{
		buildInst(set.Name, 1, testUpdateRev, true, true),
	}

	if !rolloutReplacementStateActive(set, instances, 0, 1, testUpdateRev) {
		t.Fatal("persisted replacement state must stay active while the replacement base is absent")
	}
	topo, err := computeTopologyForReconcile(set, instances, testUpdateRev, testUpdateRev)
	if err != nil {
		t.Fatal(err)
	}
	if !topo.inRollout {
		t.Fatalf("topology must treat the persisted replacement as pending rollout work: %+v", topo)
	}
	if topo.endOrdinal < 2 || topo.activeSurge < 1 {
		t.Fatalf("surge at ordinal 1 must stay inside the managed range (endOrdinal>=2, activeSurge>=1): %+v", topo)
	}
}

// TestClaudeReadyReplacementReleasesSurge verifies the other direction: once
// every base instance in the recorded scope is Ready at the target revision
// with its generation observed, the state is cleared and surplus surge falls
// out of range (no unbounded surge leak).
func TestClaudeReadyReplacementReleasesSurge(t *testing.T) {
	set := claudeRollbackSet("claude-release", 1)
	set.Status.RolloutReplacementState = &workloadsv1alpha2.RolloutReplacementState{
		UpdateRevision: testUpdateRev,
		Replicas:       1,
		Partition:      0,
	}
	replacement := buildInst(set.Name, 0, testUpdateRev, true, true)
	replacement.Generation = 2
	replacement.Status.ObservedGeneration = 2
	surge := buildInst(set.Name, 1, testUpdateRev, true, true)
	instances := []*workloadsv1alpha2.RoleInstance{replacement, surge}

	if rolloutReplacementStateActive(set, instances, 0, 1, testUpdateRev) {
		t.Fatal("state must be inactive once the replacement base is Ready with observed generation")
	}
	topo, err := computeTopologyForReconcile(set, instances, testUpdateRev, testUpdateRev)
	if err != nil {
		t.Fatal(err)
	}
	if topo.inRollout {
		t.Fatalf("topology must consider the rollout complete: %+v", topo)
	}
	if topo.endOrdinal != 1 || topo.activeSurge != 0 {
		t.Fatalf("surplus surge must be condemned after completion: %+v", topo)
	}
}

// TestClaudeRevisionChangeInvalidatesState: a superseded target revision ends
// protection so a stale persisted scope cannot wedge a later rollout.
func TestClaudeRevisionChangeInvalidatesState(t *testing.T) {
	set := claudeRollbackSet("claude-superseded", 1)
	set.Status.RolloutReplacementState = &workloadsv1alpha2.RolloutReplacementState{
		UpdateRevision: testOldRev,
		Replicas:       1,
		Partition:      0,
	}
	instances := []*workloadsv1alpha2.RoleInstance{
		buildInst(set.Name, 0, testOldRev, false, true),
	}
	if rolloutReplacementStateActive(set, instances, 0, 1, testUpdateRev) {
		t.Fatal("a state recorded for a different target revision must not stay active")
	}
}

// TestClaudeScaleDownOverlapRetainsFormerBaseAsSurge: after lowering replicas
// inside a recorded scope, protection is restricted to the overlap; a healthy
// former base instance outside the new replica count serves as retained surge
// until the remaining replacement converges.
func TestClaudeScaleDownOverlapRetainsFormerBaseAsSurge(t *testing.T) {
	set := claudeRollbackSet("claude-scaledown", 2)
	set.Spec.Replicas = ptr.To[int32](2)
	set.Status.RolloutReplacementState = &workloadsv1alpha2.RolloutReplacementState{
		UpdateRevision: testUpdateRev,
		Replicas:       3,
		Partition:      0,
	}
	instances := []*workloadsv1alpha2.RoleInstance{
		buildInst(set.Name, 0, testUpdateRev, true, true),
		buildInst(set.Name, 1, testUpdateRev, false, true), // replacement still unready
		buildInst(set.Name, 2, testUpdateRev, true, true),  // former base, now beyond replicas
	}

	if !rolloutReplacementStateActive(set, instances, 0, 2, testUpdateRev) {
		t.Fatal("overlap [0,2) still contains an unready replacement; protection must hold")
	}
	topo, err := computeTopologyForReconcile(set, instances, testUpdateRev, testUpdateRev)
	if err != nil {
		t.Fatal(err)
	}
	if topo.endOrdinal != 3 {
		t.Fatalf("ordinal 2 must be retained as surge while ordinal 1 is unready: %+v", topo)
	}

	instances[1].Status.Conditions[0].Status = "True"
	if rolloutReplacementStateActive(set, instances, 0, 2, testUpdateRev) {
		t.Fatal("protection must end once the remaining replacement in the overlap is ready")
	}
	topo, err = computeTopologyForReconcile(set, instances, testUpdateRev, testUpdateRev)
	if err != nil {
		t.Fatal(err)
	}
	if topo.endOrdinal != 2 {
		t.Fatalf("former base must be released after convergence: %+v", topo)
	}
}

// TestClaudePersistIntentPrecedesBaseDeletion runs the real control path for
// an early rollback (A -> B -> A) and asserts the replacement intent is
// persisted BEFORE the stale base instance is deleted — the ordering that
// makes the protection restart-safe.
func TestClaudePersistIntentPrecedesBaseDeletion(t *testing.T) {
	resetInstanceUnhealthySince()
	t.Cleanup(resetInstanceUnhealthySince)

	set := newRevisionTestSet("nginx:1.0")
	set.Name, set.UID = "claude-order", types.UID("claude-order")
	set.Spec.Replicas = ptr.To[int32](1)
	set.Spec.PodManagementPolicy = constants.ParallelPodManagement
	set.Spec.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{"app": set.Name}}
	set.Spec.UpdateStrategy.Type = workloadsv1alpha2.RecreatePodUpdateStrategyType
	set.Spec.UpdateStrategy.MaxSurge = ptr.To(intstr.FromInt32(1))
	set.Spec.UpdateStrategy.MaxUnavailable = ptr.To(intstr.FromInt32(0))
	current, err := newRevision(set, 1, ptr.To[int32](0))
	if err != nil {
		t.Fatal(err)
	}
	set.Spec.RoleInstanceTemplate.Components[0].Template.Spec.Containers[0].Image = "nginx:2.0"
	intermediate, err := newRevision(set, 2, ptr.To[int32](0))
	if err != nil {
		t.Fatal(err)
	}
	set.Spec.RoleInstanceTemplate.Components[0].Template.Spec.Containers[0].Image = "nginx:1.0"
	set.Status.CurrentRevision, set.Status.UpdateRevision = current.Name, current.Name

	// The stale base is Ready at B (interrupted rollout) while both revisions
	// of the set resolve to A; the surge at ordinal 1 is Ready at A.
	staleBase := buildInst(set.Name, 0, intermediate.Name, true, true)
	staleBase.Labels["app"] = set.Name
	surge := buildInst(set.Name, 1, current.Name, true, true)
	surge.Labels["app"] = set.Name

	updater := &fakeStatefulStatusUpdater{}
	objects := &fakeInstanceObjectManager{}
	persistedBeforeDelete := false
	observedDelete := false
	objects.beforeDelete = func(inst *workloadsv1alpha2.RoleInstance) error {
		if inst.Name == staleBase.Name {
			observedDelete = true
			persistedBeforeDelete = updater.persistCalls > 0
		}
		return nil
	}
	recorder := record.NewFakeRecorder(64)
	control := &defaultStatefulInstanceSetControl{
		instanceControl: NewStatefulInstanceControlFromManager(objects, recorder),
		inplaceControl:  &fakeInplaceControl{},
		statusUpdater:   updater,
		recorder:        recorder,
	}
	key := getInstanceSetKey(set)
	durationStore.Pop(key)
	t.Cleanup(func() { durationStore.Pop(key) })

	_, err = control.updateStatefulInstanceSet(context.Background(), set, current, current, 0,
		[]*workloadsv1alpha2.RoleInstance{staleBase, surge},
		[]*apps.ControllerRevision{current, intermediate})
	if err != nil {
		t.Fatalf("updateStatefulInstanceSet() error = %v", err)
	}

	if updater.persistCalls == 0 {
		t.Fatal("the control must persist replacement intent when replacing a stale base during early rollback")
	}
	if !observedDelete {
		t.Fatalf("expected the stale base %s to be deleted, deleted=%v", staleBase.Name, objects.deleted)
	}
	if !persistedBeforeDelete {
		t.Fatalf("replacement intent must be persisted before deleting the stale base (persistCalls at delete time = 0)")
	}
	state := set.Status.RolloutReplacementState
	if state == nil || state.UpdateRevision != current.Name || state.Partition != 0 || state.Replicas != 1 {
		t.Fatalf("persisted scope = %+v, want {UpdateRevision: %s, Partition: 0, Replicas: 1}", state, current.Name)
	}
}
