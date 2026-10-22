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

// Reviewer-B (Codex) independent verification harness for PR #508
// (verify/stateful-rollback-codex). All tests are CONTRACT tests: they assert
// the intended correct behavior, so they must PASS on the PR head. Production
// code is untouched by this file.

import (
	"context"
	"errors"
	"testing"

	apps "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

const codexRevA = "codex-rev-a"

// codexRollbackScene is the post-restart observation from issue #482:
// replicas=1, maxSurge=1, maxUnavailable=0, current==update==A. The stale B
// base is already gone; the replacement base (ord 0) exists at A but is not
// Ready; the existing surge (ord 1) is Ready at A.
func codexRollbackScene(replacementReady bool) (*workloadsv1alpha2.RoleInstanceSet, []*workloadsv1alpha2.RoleInstance) {
	set := buildSet("codex-rollback", 1,
		ptr.To(intstr.FromInt32(1)), ptr.To(intstr.FromInt32(0)))
	set.UID = "uid-codex-rollback"
	replacement := buildInst(set.Name, 0, codexRevA, replacementReady, true)
	surge := buildInst(set.Name, 1, codexRevA, true, true)
	return set, []*workloadsv1alpha2.RoleInstance{replacement, surge}
}

// C1: a controller that knows ONLY persisted API state (fresh process, no
// in-memory signal) must retain the surge while the recorded replacement is
// not yet Ready. This is the core fix for issue #482.
func TestCodexRestartKeepsSurgeFromPersistedState(t *testing.T) {
	set, instances := codexRollbackScene(false)
	set.Status.RolloutReplacementState = &workloadsv1alpha2.RolloutReplacementState{
		UpdateRevision: codexRevA, Replicas: 1, Partition: 0,
	}
	if !rolloutReplacementStateActive(set, instances, 0, 1, codexRevA) {
		t.Fatal("persisted replacement state must be active while the replacement is unready")
	}
	topo, err := computeTopologyForReconcile(set, instances, codexRevA, codexRevA)
	if err != nil {
		t.Fatalf("computeTopologyForReconcile: %v", err)
	}
	if topo.endOrdinal != 2 || topo.activeSurge != 1 || !topo.inRollout {
		t.Fatalf("surge not retained after restart: endOrdinal=%d activeSurge=%d inRollout=%v, want 2/1/true",
			topo.endOrdinal, topo.activeSurge, topo.inRollout)
	}
	if inherited := inheritedRolloutReplacementState(set, instances, topo, codexRevA); inherited == nil {
		t.Fatal("active persisted state must be carried into the next status write")
	}
}

// C2: once every base in the recorded scope is Ready at the target revision
// with an observed generation, protection ends and the surge is released.
func TestCodexSurgeReleasedAfterReplacementConverges(t *testing.T) {
	set, instances := codexRollbackScene(true)
	set.Status.RolloutReplacementState = &workloadsv1alpha2.RolloutReplacementState{
		UpdateRevision: codexRevA, Replicas: 1, Partition: 0,
	}
	if rolloutReplacementStateActive(set, instances, 0, 1, codexRevA) {
		t.Fatal("state must be inactive once the replacement is Ready at the target revision")
	}
	topo, err := computeTopologyForReconcile(set, instances, codexRevA, codexRevA)
	if err != nil {
		t.Fatalf("computeTopologyForReconcile: %v", err)
	}
	if topo.endOrdinal != 1 || topo.activeSurge != 0 {
		t.Fatalf("surge not released after convergence: endOrdinal=%d activeSurge=%d, want 1/0",
			topo.endOrdinal, topo.activeSurge)
	}
	if inherited := inheritedRolloutReplacementState(set, instances, topo, codexRevA); inherited != nil {
		t.Fatal("converged replacement state must be dropped from the next status write")
	}
}

// C2b: a Ready-but-terminating replacement must NOT count as converged.
func TestCodexTerminatingReplacementKeepsProtection(t *testing.T) {
	set, instances := codexRollbackScene(true)
	set.Status.RolloutReplacementState = &workloadsv1alpha2.RolloutReplacementState{
		UpdateRevision: codexRevA, Replicas: 1, Partition: 0,
	}
	withTerminating(instances[0])
	if !rolloutReplacementStateActive(set, instances, 0, 1, codexRevA) {
		t.Fatal("a terminating replacement must keep protection active")
	}
}

// C3a: the replacement intent is persisted BEFORE the stale base is deleted.
// C3b: when the intent write fails, the target instance is left untouched.
func TestCodexPersistIntentPrecedesDelete(t *testing.T) {
	var order []string
	set, instances := codexRollbackScene(true)
	set.Spec.UpdateStrategy.Type = workloadsv1alpha2.RecreatePodUpdateStrategyType
	stale := buildInst(set.Name, 0, "codex-rev-b", true, true) // stale base at B
	_ = instances
	objects := &fakeInstanceObjectManager{}
	updater := &fakeStatefulStatusUpdater{}
	control := &defaultStatefulInstanceSetControl{
		instanceControl: NewStatefulInstanceControlFromManager(objects, record.NewFakeRecorder(16)),
		inplaceControl:  &fakeInplaceControl{},
		statusUpdater:   updater,
		recorder:        record.NewFakeRecorder(16),
	}
	status := &workloadsv1alpha2.RoleInstanceSetStatus{}
	objects.beforeDelete = func(*workloadsv1alpha2.RoleInstance) error {
		if set.Status.RolloutReplacementState == nil {
			order = append(order, "delete-before-persist")
		} else {
			order = append(order, "delete-after-persist")
		}
		return nil
	}
	revs := []*apps.ControllerRevision{
		{ObjectMeta: metav1.ObjectMeta{Name: codexRevA}},
		{ObjectMeta: metav1.ObjectMeta{Name: "codex-rev-b"}},
	}
	_, err := control.applyTargetUpdate(context.Background(), set, status, stale,
		revs[0], revs[0], revs, false, false)
	if err != nil {
		t.Fatalf("applyTargetUpdate: %v", err)
	}
	if len(objects.deleted) != 1 || objects.deleted[0] != stale.Name {
		t.Fatalf("deleted = %v, want [%s]", objects.deleted, stale.Name)
	}
	if len(order) != 1 || order[0] != "delete-after-persist" {
		t.Fatalf("persist/delete order = %v, want [delete-after-persist]", order)
	}
	if status.RolloutReplacementState == nil ||
		status.RolloutReplacementState.UpdateRevision != codexRevA ||
		status.RolloutReplacementState.Partition != 0 || status.RolloutReplacementState.Replicas != 1 {
		t.Fatalf("recorded state = %+v, want {rev-a replicas=1 partition=0}", status.RolloutReplacementState)
	}

	// C3b: persist failure must leave the target untouched.
	set2, _ := codexRollbackScene(true)
	set2.Spec.UpdateStrategy.Type = workloadsv1alpha2.RecreatePodUpdateStrategyType
	stale2 := buildInst(set2.Name, 0, "codex-rev-b", true, true)
	objects2 := &fakeInstanceObjectManager{}
	updater2 := &fakeStatefulStatusUpdater{persistErr: errors.New("codex persist failure")}
	control2 := &defaultStatefulInstanceSetControl{
		instanceControl: NewStatefulInstanceControlFromManager(objects2, record.NewFakeRecorder(16)),
		inplaceControl:  &fakeInplaceControl{},
		statusUpdater:   updater2,
		recorder:        record.NewFakeRecorder(16),
	}
	if _, err := control2.applyTargetUpdate(context.Background(), set2,
		&workloadsv1alpha2.RoleInstanceSetStatus{}, stale2, revs[0], revs[0], revs, false, false); err == nil {
		t.Fatal("persist failure must propagate an error")
	}
	if len(objects2.deleted) != 0 {
		t.Fatalf("failed persist still deleted %v; the target must be left untouched", objects2.deleted)
	}
}

// C4: a target-revision change invalidates the recorded scope.
func TestCodexRevisionChangeInvalidatesState(t *testing.T) {
	set, instances := codexRollbackScene(false)
	set.Status.RolloutReplacementState = &workloadsv1alpha2.RolloutReplacementState{
		UpdateRevision: codexRevA, Replicas: 1, Partition: 0,
	}
	if rolloutReplacementStateActive(set, instances, 0, 1, "codex-rev-c") {
		t.Fatal("state recorded for rev-a must not protect a rollout targeting rev-c")
	}
}

// C5: replicas/partition changes restrict completion to the overlap with the
// current update range; an empty overlap ends protection. Instances outside
// the overlap must not hold surge hostage.
func TestCodexScopeOverlapSemantics(t *testing.T) {
	// Recorded scope [0,3); scale down to replicas=1 -> overlap [0,1).
	set, _ := codexRollbackScene(false)
	set.Status.RolloutReplacementState = &workloadsv1alpha2.RolloutReplacementState{
		UpdateRevision: codexRevA, Replicas: 3, Partition: 0,
	}
	// Ordinal 0 unready -> protection held even though ordinals 1,2 vanished.
	if !rolloutReplacementStateActive(set, []*workloadsv1alpha2.RoleInstance{buildInst(set.Name, 0, codexRevA, false, true)}, 0, 1, codexRevA) {
		t.Fatal("in-overlap unready replacement must hold protection after scale-down")
	}
	// Ordinal 0 ready; ordinals 1,2 (outside overlap) missing -> released.
	if rolloutReplacementStateActive(set, []*workloadsv1alpha2.RoleInstance{buildInst(set.Name, 0, codexRevA, true, true)}, 0, 1, codexRevA) {
		t.Fatal("converged overlap must release protection even though the original scope was wider")
	}
	// Empty overlap: recorded [2,3) but the update range is now [0,1).
	set.Status.RolloutReplacementState = &workloadsv1alpha2.RolloutReplacementState{
		UpdateRevision: codexRevA, Replicas: 3, Partition: 2,
	}
	if rolloutReplacementStateActive(set, nil, 0, 1, codexRevA) {
		t.Fatal("an empty overlap between recorded scope and update range must end protection")
	}
}

// C6: a stale base already terminating (deleted outside the controller) is
// recorded before pause/readiness gates can hide its disappearance; and no
// state is ever written when maxSurge=0.
func TestCodexTerminatingStaleBaseIsRecorded(t *testing.T) {
	set := buildSet("codex-terminating", 2,
		ptr.To(intstr.FromInt32(1)), ptr.To(intstr.FromInt32(0)))
	set.UID = "uid-codex-terminating"
	updater := &fakeStatefulStatusUpdater{}
	control := &defaultStatefulInstanceSetControl{
		statusUpdater: updater,
		recorder:      record.NewFakeRecorder(16),
	}
	staleTerminating := withTerminating(buildInst(set.Name, 1, "codex-rev-b", true, true))
	status := &workloadsv1alpha2.RoleInstanceSetStatus{}
	topo := topology{partition: 0, replicas: 2, maxSurge: 1}
	if err := control.persistTerminatingRolloutReplacement(context.Background(), set, status,
		[]*workloadsv1alpha2.RoleInstance{staleTerminating}, topo, codexRevA, codexRevA); err != nil {
		t.Fatalf("persistTerminatingRolloutReplacement: %v", err)
	}
	if updater.persistCalls != 1 || status.RolloutReplacementState == nil {
		t.Fatalf("terminating stale base not recorded: calls=%d state=%+v", updater.persistCalls, status.RolloutReplacementState)
	}

	// maxSurge=0 -> no write, no stall, even with a stale terminating base.
	setNoSurge := buildSet("codex-no-surge", 2, ptr.To(intstr.FromInt32(0)), ptr.To(intstr.FromInt32(1)))
	updaterNoSurge := &fakeStatefulStatusUpdater{}
	controlNoSurge := &defaultStatefulInstanceSetControl{
		statusUpdater: updaterNoSurge,
		recorder:      record.NewFakeRecorder(16),
	}
	statusNoSurge := &workloadsv1alpha2.RoleInstanceSetStatus{}
	topoNoSurge := topology{partition: 0, replicas: 2, maxSurge: 0}
	if err := controlNoSurge.persistTerminatingRolloutReplacement(context.Background(), setNoSurge, statusNoSurge,
		[]*workloadsv1alpha2.RoleInstance{staleTerminating}, topoNoSurge, codexRevA, codexRevA); err != nil {
		t.Fatalf("maxSurge=0 must not fail: %v", err)
	}
	if updaterNoSurge.persistCalls != 0 || statusNoSurge.RolloutReplacementState != nil {
		t.Fatalf("maxSurge=0 wrote state: calls=%d state=%+v", updaterNoSurge.persistCalls, statusNoSurge.RolloutReplacementState)
	}
}

// C7: the resource-version gate blocks reconciles until the set informer
// observes the replacement-state write, and a recreated set (new UID) is not
// blocked by the old set's expectation.
func TestCodexExpectationGateSemantics(t *testing.T) {
	set, _ := codexRollbackScene(false)
	set.ResourceVersion = "100"
	rolloutStatusExpectations.Delete(set) // clean slate
	// No expectation -> satisfied.
	if ok, _ := rolloutStatusExpectations.IsSatisfied(set); !ok {
		t.Fatal("no expectation must mean satisfied")
	}
	// Register an expectation at RV=101 (as a successful write would).
	written := set.DeepCopy()
	written.ResourceVersion = "101"
	rolloutStatusExpectations.Expect(written)
	if ok, _ := rolloutStatusExpectations.IsSatisfied(set); ok {
		t.Fatal("stale cache (RV 100 < 101) must block the reconcile")
	}
	// Cache observes the write.
	if ok, _ := rolloutStatusExpectations.IsSatisfied(written); !ok {
		t.Fatal("cache at the written RV must satisfy the gate")
	}
	// A recreated set with a new UID is never blocked by the old UID's expectation.
	rolloutStatusExpectations.Expect(written)
	recreated := set.DeepCopy()
	recreated.UID = "uid-recreated"
	if ok, _ := rolloutStatusExpectations.IsSatisfied(recreated); !ok {
		t.Fatal("recreated set (new UID) must not inherit the old expectation")
	}
	rolloutStatusExpectations.Delete(written)
}
