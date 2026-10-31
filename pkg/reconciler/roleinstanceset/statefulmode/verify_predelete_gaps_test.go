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

// Reviewer verification harness for PR #535 ("honor PreDelete hooks for
// Stateful instance deletion").
//
// These tests drive the real updateStatefulInstanceSet loop (Phase A-D) with a
// fake-client-backed lifecycleControl, so the actual lifecycle patches hit a
// client that behaves like the API server while instance create/delete calls
// are recorded. They are additive only: production code on this branch is
// identical to the PR head.
//
// Polarity (see docs/verification/stateful-predelete-claude/verify-manifest.json):
//   - CONTRACT tests assert intended behavior; red on the PR head = the
//     reproduction of a review finding.
//   - BUG-CANARY tests assert the currently observed (wrong or deferred)
//     behavior; they are green on the PR head and must be inverted once the
//     corresponding gap is fixed.

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	instancelifecycle "sigs.k8s.io/rbgs/pkg/inplace/instance/lifecycle"
)

const verifyHookLabel = "example.com/drain-protection"

type verifyHarness struct {
	set     *workloadsv1alpha2.RoleInstanceSet
	current *apps.ControllerRevision
	control *defaultStatefulInstanceSetControl
	objects *fakeInstanceObjectManager
	client  client.Client
}

// newVerifyHarness builds a 2-replica set (ordinals 0,1) whose only difference
// from a plain set is the PreDelete hook. Both instances carry the blocking
// hook label. markNotReady controls the hook's MarkNotReady; gate decides
// whether the instances declare the RoleInstanceCustomReady readiness gate
// (required for MarkNotReady to have any RoleInstance-level effect today).
func newVerifyHarness(t *testing.T, name string, policy constants.PodManagementPolicyType, markNotReady, gate bool) *verifyHarness {
	t.Helper()
	resetInstanceUnhealthySince()
	t.Cleanup(resetInstanceUnhealthySince)

	set := newRevisionTestSet("nginx:1.0")
	set.Name, set.UID = name, types.UID(name)
	set.Generation = 1
	set.Spec.Replicas = ptr.To[int32](2)
	set.Spec.PodManagementPolicy = policy
	set.Spec.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}}
	set.Spec.Lifecycle = &workloadsv1alpha2.RoleInstanceSetLifecycle{
		PreDelete: &workloadsv1alpha2.RoleInstanceSetLifecycleHook{
			LabelsHandler: map[string]string{verifyHookLabel: "hold"},
			MarkNotReady:  markNotReady,
		},
	}

	current := newStatefulRevision(name+"-current", 1)

	instances := []*workloadsv1alpha2.RoleInstance{
		buildInst(name, 0, current.Name, true, true),
		buildInst(name, 1, current.Name, true, true),
	}
	for _, inst := range instances {
		inst.Labels[verifyHookLabel] = "hold"
		if gate {
			inst.Spec.ReadinessGates = []workloadsv1alpha2.RoleInstanceReadinessGate{
				{ConditionType: workloadsv1alpha2.RoleInstanceCustomReady},
			}
			inst.Status.Conditions = append(inst.Status.Conditions, workloadsv1alpha2.RoleInstanceCondition{
				Type:   workloadsv1alpha2.RoleInstanceCustomReady,
				Status: corev1.ConditionTrue,
			})
		}
	}

	scheme := runtime.NewScheme()
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))
	objs := make([]client.Object, 0, len(instances))
	for _, inst := range instances {
		objs = append(objs, inst)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).WithStatusSubresource(objs...).Build()

	objects := &fakeInstanceObjectManager{}
	recorder := record.NewFakeRecorder(64)
	control := &defaultStatefulInstanceSetControl{
		instanceControl:  NewStatefulInstanceControlFromManager(objects, recorder),
		inplaceControl:   &fakeInplaceControl{},
		statusUpdater:    &fakeStatefulStatusUpdater{},
		recorder:         recorder,
		lifecycleControl: instancelifecycle.New(c),
	}
	return &verifyHarness{set: set, current: current, control: control, objects: objects, client: c}
}

// reconcile runs one full updateStatefulInstanceSet pass over the current
// fake-client state, mirroring what the real controller does per sync.
// Ordinal 2 is only listed once the regret scenario creates a slot for it.
func (h *verifyHarness) reconcile(t *testing.T) {
	t.Helper()
	instances := make([]*workloadsv1alpha2.RoleInstance, 0, 3)
	for _, key := range []types.NamespacedName{
		{Namespace: "default", Name: h.set.Name + "-0"},
		{Namespace: "default", Name: h.set.Name + "-1"},
		{Namespace: "default", Name: h.set.Name + "-2"},
	} {
		inst := &workloadsv1alpha2.RoleInstance{}
		if err := h.client.Get(context.Background(), key, inst); err != nil {
			continue
		}
		instances = append(instances, inst)
	}
	status, err := h.control.updateStatefulInstanceSet(context.Background(), h.set, h.current, h.current, 0,
		instances, []*apps.ControllerRevision{h.current})
	require.NoError(t, err)
	h.set.Status = *status.DeepCopy()
}

func (h *verifyHarness) instance(t *testing.T, ordinal int) *workloadsv1alpha2.RoleInstance {
	t.Helper()
	inst := &workloadsv1alpha2.RoleInstance{}
	key := types.NamespacedName{Namespace: "default", Name: fmt.Sprintf("%s-%d", h.set.Name, ordinal)}
	require.NoError(t, h.client.Get(context.Background(), key, inst))
	return inst
}

func (h *verifyHarness) setReplicas(t *testing.T, replicas int32) {
	t.Helper()
	h.set.Spec.Replicas = ptr.To(replicas)
}

// ---------------------------------------------------------------------------
// P1 (F1 in the review): scale-down regret leaves PreparingDelete behind.
//
// Scenario: scale 2 -> 1 with a matching PreDelete hook (instance-1 blocked in
// PreparingDelete), then scale back 1 -> 2 before the hook is released.
// The stateless implementation reverts PreparingDelete -> Normal in
// managePreparingDelete(); the stateful one has no such path.
// ---------------------------------------------------------------------------

// CONTRACT: after regret, an in-range instance must not stay labeled
// PreparingDelete (mirrors statelessmode sync.managePreparingDelete, which
// reverts when the lifecycle does not MarkNotReady).
func TestVerifyRegretRevertsPreparingDeleteState(t *testing.T) {
	h := newVerifyHarness(t, "regret-state", constants.ParallelPodManagement, false, false)

	// Scale down 2 -> 1: instance-1 is condemned, hook blocks it.
	h.setReplicas(t, 1)
	h.reconcile(t)
	require.Equal(t, constants.RoleInstanceLifecycleStatePreparingDelete,
		instancelifecycle.GetRoleInstanceLifecycleState(h.instance(t, 1)),
		"precondition: blocked scale-down must enter PreparingDelete")
	require.Empty(t, h.objects.deleted, "precondition: hook blocks the delete")

	// Regret: scale back 1 -> 2 while the hook still holds.
	h.setReplicas(t, 2)
	h.reconcile(t)
	h.reconcile(t)

	got := h.instance(t, 1)
	require.Equal(t, constants.RoleInstanceLifecycleStateNormal,
		instancelifecycle.GetRoleInstanceLifecycleState(got),
		"an instance that is back in range must not be left in PreparingDelete")
}

// BUG-CANARY: with MarkNotReady=true, a regretted instance stays NotReady
// forever (no revert path, and removing the hook label no longer triggers the
// lifecycle code). Green on the PR head; must be inverted when the recovery
// path lands.
func TestVerifyRegretLeavesInstanceUnavailableCanary(t *testing.T) {
	h := newVerifyHarness(t, "regret-unavailable", constants.ParallelPodManagement, true, true)

	h.setReplicas(t, 1)
	h.reconcile(t)
	require.Equal(t, constants.RoleInstanceLifecycleStatePreparingDelete,
		instancelifecycle.GetRoleInstanceLifecycleState(h.instance(t, 1)))

	// Regret and release the hook label: nothing restores readiness.
	h.setReplicas(t, 2)
	got := h.instance(t, 1)
	before := got.DeepCopy()
	delete(got.Labels, verifyHookLabel)
	require.NoError(t, h.client.Update(context.Background(), got))
	h.reconcile(t)
	h.reconcile(t)

	got = h.instance(t, 1)
	require.Equal(t, constants.RoleInstanceLifecycleStatePreparingDelete,
		instancelifecycle.GetRoleInstanceLifecycleState(got),
		"canary: stateful mode has no PreparingDelete -> Normal recovery")
	for _, cond := range got.Status.Conditions {
		if cond.Type == workloadsv1alpha2.RoleInstanceReady {
			require.Equal(t, corev1.ConditionFalse, cond.Status,
				"canary: the regretted instance is stuck NotReady")
		}
	}
	require.Equal(t, int32(1), h.set.Status.ReadyReplicas,
		"canary: ReadyReplicas permanently undercounts by one")
	require.Empty(t, before.DeletionTimestamp)
}

// OrderedReady makes the stuck instance also freeze scale-up above its ordinal.
// BUG-CANARY (documents impact of the same gap in OrderedReady mode).
func TestVerifyRegretOrderedReadyFreezesScaleUpCanary(t *testing.T) {
	h := newVerifyHarness(t, "regret-ordered", constants.OrderedReadyPodManagement, true, true)

	h.setReplicas(t, 1)
	h.reconcile(t)
	require.Equal(t, constants.RoleInstanceLifecycleStatePreparingDelete,
		instancelifecycle.GetRoleInstanceLifecycleState(h.instance(t, 1)))

	// Regret and extend: 1 -> 3. Ordinal 2 would need to be created, but the
	// unhealthy (stuck NotReady) ordinal 1 blocks OrderedReady processing.
	h.setReplicas(t, 3)
	h.reconcile(t)
	h.reconcile(t)

	require.NotContains(t, h.objects.created, h.set.Name+"-2",
		"canary: stuck PreparingDelete ordinal blocks OrderedReady scale-up above it")
}

// ---------------------------------------------------------------------------
// P2 (F2 in the review): processCondemned now stops the OrderedReady loop on
// an already-terminating condemned instance (deleteInstance's isTerminating
// short-circuit returns actualDeleting=false, so the loop exits). Base
// behavior deleted the lower ordinals too. This canary records the new
// semantics; run the same file on the base commit for the differential.
// ---------------------------------------------------------------------------

func TestVerifyOrderedReadyTerminatingCondemnedBlocksLowerCanary(t *testing.T) {
	set := buildSet("terminating-condemned", 1, nil, nil)
	high := withTerminating(buildInst(set.Name, 2, testOldRev, true, true))
	// The fake client refuses terminating objects without a finalizer; a
	// lingering finalizer is also the realistic reason a condemned instance
	// stays in Terminating across reconciles.
	high.Finalizers = []string{"example.com/lingering"}
	condemned := []*workloadsv1alpha2.RoleInstance{
		high,
		buildInst(set.Name, 1, testOldRev, true, true),
	}
	control, c := newPreDeleteTestControl(t, condemned...)
	_, err := runForAll(condemned, func(i int) (bool, error) {
		return control.processCondemned(context.Background(), set, nil, true, condemned, i)
	}, true)
	require.NoError(t, err)
	require.Empty(t, c.deletes, "canary: head blocks the lower ordinal while a condemned instance is terminating")
}

// ---------------------------------------------------------------------------
// P3 (F3 in the review): MarkNotReady=true is a silent no-op unless the
// instance template declares the RoleInstanceCustomReady readiness gate
// (deferred Task 2 of sgl-project/rbg#534). BUG-CANARY.
// ---------------------------------------------------------------------------

func TestVerifyMarkNotReadyWithoutGateIsNoOpCanary(t *testing.T) {
	set := buildSet("gateless", 1, nil, nil)
	set.Spec.Lifecycle = &workloadsv1alpha2.RoleInstanceSetLifecycle{
		PreDelete: &workloadsv1alpha2.RoleInstanceSetLifecycleHook{
			LabelsHandler: map[string]string{verifyHookLabel: "hold"},
			MarkNotReady:  true,
		},
	}
	instance := buildInst(set.Name, 0, testOldRev, true, true)
	instance.Labels[verifyHookLabel] = "hold"
	control, c := newPreDeleteTestControl(t, instance)

	_, deleting, err := control.deleteInstance(set, instance)
	require.NoError(t, err)
	require.False(t, deleting)

	got := getPreDeleteTestInstance(t, c, instance)
	for _, cond := range got.Status.Conditions {
		if cond.Type == workloadsv1alpha2.RoleInstanceReady {
			require.Equal(t, corev1.ConditionTrue, cond.Status,
				"canary: markPodNotReady=true does not mark a gate-less RoleInstance NotReady (Task 2 gap)")
		}
	}
}
