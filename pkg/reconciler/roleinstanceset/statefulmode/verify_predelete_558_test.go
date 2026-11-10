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

// Reviewer A (Claude) verification harness for sgl-project/rbg PR #558
// ("fix(stateful): honor pre-delete lifecycle hooks").
//
// These tests drive the real updateStatefulInstanceSet loop (Phase A-D) with a
// fake-client-backed lifecycleControl, so the actual lifecycle patches hit a
// client that behaves like the API server (including resourceVersion conflict
// enforcement) while instance create/delete calls are recorded. They are
// additive only: production code on this branch is identical to the PR head.
//
// Polarity (see docs/verification/stateful-predelete-558/verify-manifest.json):
//   - CONTRACT tests assert intended behavior; red on the PR head = the
//     reproduction of a review finding.
//   - BUG-CANARY tests assert the currently observed (wrong) behavior; they are
//     green on the PR head and must be inverted once the gap is fixed.

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

const verify558HookLabel = "example.com/drain-protection"

type verify558Harness struct {
	set     *workloadsv1alpha2.RoleInstanceSet
	current *apps.ControllerRevision
	control *defaultStatefulInstanceSetControl
	objects *fakeInstanceObjectManager
	client  client.Client
}

// newVerify558Harness builds a 2-replica set (ordinals 0,1) at the current
// revision with a PreDelete hook. Both instances carry the blocking hook
// label. markNotReady controls the hook's MarkNotReady; gate decides whether
// the instances declare the RoleInstanceCustomReady readiness gate (required
// for MarkNotReady to have any RoleInstance-level effect).
func newVerify558Harness(t *testing.T, name string, policy constants.PodManagementPolicyType, markNotReady, gate bool) *verify558Harness {
	t.Helper()
	resetInstanceUnhealthySince()
	t.Cleanup(resetInstanceUnhealthySince)

	set := newRevisionTestSet("nginx:1.0")
	set.Name, set.UID = name, types.UID(name)
	set.Generation = 1
	set.Spec.Replicas = ptr.To[int32](2)
	set.Spec.PodManagementPolicy = policy
	set.Spec.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}}
	set.Spec.UpdateStrategy.Type = workloadsv1alpha2.RecreatePodUpdateStrategyType
	set.Spec.Lifecycle = &workloadsv1alpha2.RoleInstanceSetLifecycle{
		PreDelete: &workloadsv1alpha2.RoleInstanceSetLifecycleHook{
			LabelsHandler: map[string]string{verify558HookLabel: "hold"},
			MarkNotReady:  markNotReady,
		},
	}

	current, err := newRevision(set, 1, ptr.To[int32](0))
	require.NoError(t, err)
	set.Status.CurrentRevision, set.Status.UpdateRevision = current.Name, current.Name

	instances := []*workloadsv1alpha2.RoleInstance{
		buildInst(name, 0, current.Name, true, true),
		buildInst(name, 1, current.Name, true, true),
	}
	for _, inst := range instances {
		inst.Labels[verify558HookLabel] = "hold"
		inst.ResourceVersion = "1"
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
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(&workloadsv1alpha2.RoleInstance{}).
		Build()

	objects := &fakeInstanceObjectManager{}
	recorder := record.NewFakeRecorder(64)
	control := &defaultStatefulInstanceSetControl{
		instanceControl:  NewStatefulInstanceControlFromManager(objects, recorder),
		inplaceControl:   &fakeInplaceControl{},
		statusUpdater:    &fakeStatefulStatusUpdater{},
		recorder:         recorder,
		lifecycleControl: instancelifecycle.New(c),
	}
	key := getInstanceSetKey(set)
	durationStore.Pop(key)
	t.Cleanup(func() {
		durationStore.Pop(key)
		updateExpectations.DeleteExpectations(key)
	})
	return &verify558Harness{set: set, current: current, control: control, objects: objects, client: c}
}

// reconcile runs one full updateStatefulInstanceSet pass over the current
// fake-client state, mirroring what the real controller does per sync.
// Ordinal 2 is listed once the regret scenario creates a slot for it.
// The first announcement pass with MarkNotReady may legitimately return a
// conflict error (self-conflicting status write + optimistic-lock patch, see
// the lifecycle package test); tolerate at most that.
func (h *verify558Harness) reconcile(t *testing.T, allowConflict bool) {
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
	h.objects.created, h.objects.deleted = nil, nil
	status, err := h.control.updateStatefulInstanceSet(context.Background(), h.set, h.current, h.current, 0,
		instances, []*apps.ControllerRevision{h.current})
	if err != nil && allowConflict && apierrors.IsConflict(err) {
		return
	}
	require.NoError(t, err)
	h.set.Status = *status.DeepCopy()
}

func (h *verify558Harness) instance(t *testing.T, ordinal int) *workloadsv1alpha2.RoleInstance {
	t.Helper()
	inst := &workloadsv1alpha2.RoleInstance{}
	key := types.NamespacedName{Namespace: "default", Name: fmt.Sprintf("%s-%d", h.set.Name, ordinal)}
	require.NoError(t, h.client.Get(context.Background(), key, inst))
	return inst
}

func (h *verify558Harness) setReplicas(t *testing.T, replicas int32) {
	t.Helper()
	h.set.Spec.Replicas = ptr.To(replicas)
}

// ---------------------------------------------------------------------------
// F1: no PreparingDelete -> Normal recovery ("regret") path in stateful mode.
//
// Scenario: scale 2 -> 1 with a matching PreDelete hook (instance-1 blocked in
// PreparingDelete), then scale back 1 -> 2 before the hook is released. The
// stateless implementation reverts PreparingDelete -> Normal in
// managePreparingDelete(); the stateful one has no such path.
// ---------------------------------------------------------------------------

// CONTRACT: after regret, an in-range instance must not stay labeled
// PreparingDelete (mirrors statelessmode sync.managePreparingDelete, which
// reverts when the lifecycle does not MarkNotReady).
func TestVerify558RegretRevertsPreparingDeleteState(t *testing.T) {
	h := newVerify558Harness(t, "regret-state", constants.ParallelPodManagement, false, false)

	// Scale down 2 -> 1: instance-1 is condemned, hook blocks it.
	h.setReplicas(t, 1)
	h.reconcile(t, false)
	require.Equal(t, constants.RoleInstanceLifecycleStatePreparingDelete,
		instancelifecycle.GetRoleInstanceLifecycleState(h.instance(t, 1)),
		"precondition: blocked scale-down must enter PreparingDelete")
	require.Empty(t, h.objects.deleted, "precondition: hook blocks the delete")

	// Regret: scale back 1 -> 2 while the hook still holds.
	h.setReplicas(t, 2)
	h.reconcile(t, false)
	h.reconcile(t, false)

	got := h.instance(t, 1)
	require.Equal(t, constants.RoleInstanceLifecycleStateNormal,
		instancelifecycle.GetRoleInstanceLifecycleState(got),
		"an instance that is back in range must not be left in PreparingDelete")
}

// BUG-CANARY: with MarkNotReady=true, a regretted instance stays NotReady
// forever (no revert path; the preDeleteHook readiness key is never removed).
// Green on the PR head; must be inverted when the recovery path lands.
func TestVerify558RegretLeavesInstanceUnavailableCanary(t *testing.T) {
	h := newVerify558Harness(t, "regret-unavailable", constants.ParallelPodManagement, true, true)

	h.setReplicas(t, 1)
	// F2: the first announcement self-conflicts (status write bumps the
	// resourceVersion the optimistic-lock label patch is preconditioned on),
	// so a second reconcile is needed for the label to land.
	h.reconcile(t, true)
	h.reconcile(t, true)
	require.Equal(t, constants.RoleInstanceLifecycleStatePreparingDelete,
		instancelifecycle.GetRoleInstanceLifecycleState(h.instance(t, 1)))

	// Regret and release the hook label: nothing restores readiness.
	h.setReplicas(t, 2)
	got := h.instance(t, 1)
	before := got.DeepCopy()
	delete(got.Labels, verify558HookLabel)
	require.NoError(t, h.client.Update(context.Background(), got))
	h.reconcile(t, true)
	h.reconcile(t, true)

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
func TestVerify558RegretOrderedReadyFreezesScaleUpCanary(t *testing.T) {
	h := newVerify558Harness(t, "regret-ordered", constants.OrderedReadyPodManagement, true, true)

	h.setReplicas(t, 1)
	// F2: the first announcement self-conflicts (status write bumps the
	// resourceVersion the optimistic-lock label patch is preconditioned on),
	// so a second reconcile is needed for the label to land.
	h.reconcile(t, true)
	h.reconcile(t, true)
	require.Equal(t, constants.RoleInstanceLifecycleStatePreparingDelete,
		instancelifecycle.GetRoleInstanceLifecycleState(h.instance(t, 1)))

	// Regret and extend: 1 -> 3. Ordinal 2 would need to be created, but the
	// unhealthy (stuck NotReady) ordinal 1 blocks OrderedReady processing.
	h.setReplicas(t, 3)
	h.reconcile(t, true)
	h.reconcile(t, true)

	require.NotContains(t, h.objects.created, h.set.Name+"-2",
		"canary: stuck PreparingDelete ordinal blocks OrderedReady scale-up above it")
}

// ---------------------------------------------------------------------------
// F4 (independent check of the "OrderedReady stall after hook release" claim):
// with MarkNotReady=true and the readiness gate present, the blocked instance
// is Ready=False. The processCondemned health gate must still let the released
// instance be deleted (it is the firstUnhealthy instance, so the gate passes).
// CONTRACT: releasing the hook must resume the actual deletion.
// ---------------------------------------------------------------------------
func TestVerify558OrderedReadyMarkNotReadyReleaseResumes(t *testing.T) {
	h := newVerify558Harness(t, "release-ordered", constants.OrderedReadyPodManagement, true, true)

	h.setReplicas(t, 1)
	// F2: the first announcement self-conflicts (status write bumps the
	// resourceVersion the optimistic-lock label patch is preconditioned on),
	// so a second reconcile is needed for the label to land.
	h.reconcile(t, true)
	h.reconcile(t, true)
	require.Equal(t, constants.RoleInstanceLifecycleStatePreparingDelete,
		instancelifecycle.GetRoleInstanceLifecycleState(h.instance(t, 1)))
	require.Empty(t, h.objects.deleted)

	// Release the hook label; the instance is still NotReady (the readiness
	// key is not removed on release).
	got := h.instance(t, 1)
	delete(got.Labels, verify558HookLabel)
	require.NoError(t, h.client.Update(context.Background(), got))

	h.reconcile(t, true)
	require.Contains(t, h.objects.deleted, h.set.Name+"-1",
		"hook release must resume the deletion even while the instance is NotReady")
}
