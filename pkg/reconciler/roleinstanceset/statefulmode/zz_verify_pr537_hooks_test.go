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

// Verification harness for sgl-project/rbg PR #537 (Reviewer B / Codex).
// Additive only: no production code is modified by this file.
//
// Polarity:
//   - TestVerifyPR537P0... : CONTRACT. Fails on the PR base (hook bypassed =>
//     premise reproduced), passes on the PR head (hook honored).
//     The file intentionally uses only symbols whose signatures are identical
//     on base and head so the SAME test can run against both.
//   - TestVerifyPR537F1... : CONTRACT. Fails on the PR head (repro of the
//     stranding finding), passes once a rescue path exists.
//   - TestVerifyPR537F2... : CANARY. Documents the current (broken) wiring of
//     ResourceVersionExpectations; flips to red once the wiring is fixed, at
//     which point it must be inverted/promoted.

package statefulmode

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	api "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	instanceinplace "sigs.k8s.io/rbgs/pkg/inplace/instance/inplaceupdate"
	instancelifecycle "sigs.k8s.io/rbgs/pkg/inplace/instance/lifecycle"
	instanceutils "sigs.k8s.io/rbgs/pkg/reconciler/roleinstance/utils"
	"sigs.k8s.io/rbgs/pkg/utils/revisionadapter"
)

const verifyHookLabel = "verify.example.com/inplace-hold"

// verifyPR537Control builds a control with a fake client holding the given
// instance, mirroring the PR's own harness construction (helpers shared with
// the base tree).
func verifyPR537Control(t *testing.T, set *api.RoleInstanceSet, instance *api.RoleInstance) (*defaultStatefulInstanceSetControl, client.Client, *fakeInstanceObjectManager) {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(instance).WithStatusSubresource(instance).Build()
	objects := &fakeInstanceObjectManager{}
	control := &defaultStatefulInstanceSetControl{
		instanceControl:  NewStatefulInstanceControlFromManager(objects, record.NewFakeRecorder(32)),
		inplaceControl:   instanceinplace.New(c, revisionadapter.NewDefaultImpl()),
		lifecycleControl: instancelifecycle.New(c),
	}
	t.Cleanup(func() {
		updateExpectations.DeleteExpectations(getInstanceSetKey(set))
		durationStore.Pop(getInstanceSetKey(set))
		instanceutils.ResourceVersionExpectations.Delete(instance)
		resetInstanceUnhealthySince()
	})
	return control, c, objects
}

// verifyPR537Fixture returns a hooked InPlaceIfPossible set, its old and
// update revisions, and a hooked ord-0 instance at the old revision.
func verifyPR537Fixture(t *testing.T) (set *api.RoleInstanceSet, oldRev, updateRev *apps.ControllerRevision, instance *api.RoleInstance) {
	t.Helper()
	set = newRevisionTestSet("nginx:1.0")
	set.Name = "verify-hook"
	set.Spec.Replicas = ptr.To[int32](1)
	set.Spec.PodManagementPolicy = constants.ParallelPodManagement
	set.Spec.UpdateStrategy.Type = api.InPlaceIfPossibleUpdateStrategyType
	set.Spec.UpdateStrategy.MaxUnavailable = ptr.To(intstr.FromInt32(1))
	set.Spec.Lifecycle = &api.RoleInstanceSetLifecycle{
		InPlaceUpdate: &api.RoleInstanceSetLifecycleHook{
			LabelsHandler: map[string]string{verifyHookLabel: "hold"},
			MarkNotReady:  true,
		},
	}
	old, err := newRevision(set, 1, ptr.To[int32](0))
	require.NoError(t, err)
	instance = buildInst(set.Name, 0, old.Name, true, true)
	instance.Spec = *set.Spec.RoleInstanceTemplate.RoleInstanceSpec.DeepCopy()
	instance.Spec.ReadinessGates = []api.RoleInstanceReadinessGate{
		{ConditionType: api.RoleInstanceInPlaceUpdateReady},
		{ConditionType: api.RoleInstanceCustomReady},
	}
	instance.Generation = 1
	instance.Status.ObservedGeneration = 1
	instance.Status.ComponentStatuses = []api.RoleInstanceComponentStatus{{Name: "worker", UpdatedReplicas: 1}}
	instance.Status.Conditions = append(instance.Status.Conditions,
		api.RoleInstanceCondition{Type: api.RoleInstanceInPlaceUpdateReady, Status: corev1.ConditionTrue})
	instance.Labels[verifyHookLabel] = "hold"
	set.Spec.RoleInstanceTemplate.Components[0].Template.Spec.Containers[0].Image = "nginx:2.0"
	update, err := newRevision(set, 2, ptr.To[int32](0))
	require.NoError(t, err)
	set.Status.CurrentRevision, set.Status.UpdateRevision = old.Name, update.Name
	return set, old, update, instance
}

// P0 (premise, runs against BOTH base and head): a hooked Stateful instance
// selected for an in-place-capable revision update must be held in
// PreparingUpdate — its spec must not be replaced and it must not be deleted.
// On the base tree the hook is never consulted, so the update proceeds and
// this test FAILS (premise reproduced); on the PR head it passes.
func TestVerifyPR537P0_HookedInstanceMustWaitPreparingUpdate(t *testing.T) {
	set, oldRev, updateRev, instance := verifyPR537Fixture(t)
	control, c, objects := verifyPR537Control(t, set, instance)

	_, err := control.applyTargetUpdate(context.Background(), set, &api.RoleInstanceSetStatus{},
		instance, oldRev, updateRev, []*apps.ControllerRevision{oldRev, updateRev}, false, false)
	require.NoError(t, err)

	got := &api.RoleInstance{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(instance), got))
	require.Equal(t, string(constants.RoleInstanceLifecycleStatePreparingUpdate),
		got.Labels[constants.RoleInstanceSetLifecycleStateKey],
		"hooked instance must be held in PreparingUpdate before any in-place update")
	require.Equal(t, "nginx:1.0", got.Spec.Components[0].Template.Spec.Containers[0].Image,
		"hooked instance spec must not be replaced while the hook holds")
	require.Empty(t, objects.deleted, "hook waiting must never fall back to deletion")
}

// F1: an instance admitted to PreparingUpdate whose ordinal falls below a
// raised partition is no longer an update target. No remaining controller
// path (Phase B refresh, progressUpdate, OrderedReady cleanup) ever moves it
// out of PreparingUpdate, so the lifecycle state — and with markNotReady the
// held readiness key / NotReady condition — persists until partition is
// lowered again. This CONTRACT test expects an exit from PreparingUpdate and
// FAILS on the PR head (reproduction).
func TestVerifyPR537F1_PreparingUpdateStrandedWhenPartitionRaised(t *testing.T) {
	for _, released := range []bool{false, true} {
		name := "hook still held"
		if released {
			name = "hook released by owner"
		}
		t.Run(name, func(t *testing.T) {
			set, oldRev, updateRev, instance := verifyPR537Fixture(t)
			set.Spec.Replicas = ptr.To[int32](2)
			set.Spec.UpdateStrategy.Partition = ptr.To(intstr.FromInt32(2)) // pin ords {0,1}
			control, c, _ := verifyPR537Control(t, set, instance)

			// Admit ord-1 to PreparingUpdate (as progressUpdate would have done
			// while it was still a target), with the hook marking it NotReady.
			stranded := instance.DeepCopy()
			stranded.Name = set.Name + "-1"
			stranded.UID = types.UID("uid-verify-hook-1")
			stranded.ResourceVersion = ""
			instancelifecycle.SetRoleInstanceLifecycle(constants.RoleInstanceLifecycleStatePreparingUpdate)(stranded)
			for i := range stranded.Status.Conditions {
				if stranded.Status.Conditions[i].Type == api.RoleInstanceReady {
					stranded.Status.Conditions[i].Status = corev1.ConditionFalse // markNotReady effect
				}
			}
			require.NoError(t, c.Create(context.Background(), stranded))
			require.NoError(t, c.Status().Update(context.Background(), stranded))
			if released {
				delete(stranded.Labels, verifyHookLabel)
				require.NoError(t, c.Update(context.Background(), stranded))
			}

			replicas := []*api.RoleInstance{instance, stranded}
			topo, err := computeTopology(set, replicas, oldRev.Name, updateRev.Name, false)
			require.NoError(t, err)

			// 1. Phase B refresh must not abandon the admitted operation.
			_, _, err = control.refreshInstanceState(set, stranded, updateRev.Name)
			require.NoError(t, err)
			// 2. Phase C must still reach it (it cannot: partition excludes it).
			status := &api.RoleInstanceSetStatus{}
			_, err = control.progressUpdate(context.Background(), set, status,
				oldRev, updateRev, []*apps.ControllerRevision{oldRev, updateRev}, replicas, replicas, 0, topo)
			require.NoError(t, err)
			// 3. OrderedReady cleanup cannot reach it either.
			require.Nil(t, orderedReadyCleanupTarget(set, replicas, topo, updateRev.Name),
				"partition-pinned ordinal is excluded from the OrderedReady rescue path")
			// The stranded NotReady instance keeps occupying base availability.
			require.True(t, control.collectBaseUnavailable(set, replicas, topo, 0).Has(stranded.Name),
				"stranded instance drains the update budget of the whole set")

			got := &api.RoleInstance{}
			require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(stranded), got))
			require.NotEqual(t, string(constants.RoleInstanceLifecycleStatePreparingUpdate),
				got.Labels[constants.RoleInstanceSetLifecycleStateKey],
				"F1: an admitted PreparingUpdate instance pinned below partition must not be stranded")
		})
	}
}

// F2 (canary): the PR registers ResourceVersionExpectations entries keyed by
// RoleInstance UID into pkg/reconciler/roleinstance/utils' registry, whose
// only Observe/IsSatisfied/Delete callers pass *Pod* objects. Nothing ever
// observes a RoleInstance UID there: the expectation can never be satisfied
// through the real pipeline and the entry is never cleaned up. This test
// documents current behavior; when the wiring is fixed it flips and must be
// inverted.
func TestVerifyPR537F2_LifecycleExpectationsNeverObservedForInstances(t *testing.T) {
	set, oldRev, updateRev, instance := verifyPR537Fixture(t)
	control, _, _ := verifyPR537Control(t, set, instance)

	_, err := control.applyTargetUpdate(context.Background(), set, &api.RoleInstanceSetStatus{},
		instance, oldRev, updateRev, []*apps.ControllerRevision{oldRev, updateRev}, false, false)
	require.NoError(t, err)

	// The admission above Expect()ed the patched RoleInstance. The only The only
	// observer wired in production observes pods (instance_reconciler.go).
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: instance.Name + "-worker-0", Namespace: instance.Namespace,
		UID: "uid-verify-pod", ResourceVersion: "999999",
	}}
	instanceutils.ResourceVersionExpectations.Observe(pod)

	// A later cache snapshot of the instance still reports the expectation
	// unsatisfied, and the entry persists (nothing deletes it) — i.e. the
	// Expect call gates nothing and leaks one map entry per instance.
	satisfied, _ := instanceutils.ResourceVersionExpectations.IsSatisfied(
		&metav1.ObjectMeta{UID: instance.UID, ResourceVersion: instance.ResourceVersion})
	require.False(t, satisfied,
		"canary: instance-keyed expectation is never observed by the pod-keyed pipeline")
}
