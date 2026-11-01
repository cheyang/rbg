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

// Reviewer A (Claude) verification harness for PR sgl-project/rbg#537.
//
// These tests are additive review artifacts; production code is untouched.
// Each test is labeled with the claim it verifies (P0 = premise, F# = finding).
// Polarity:
//   - P0 is a CONTRACT test: on the PR head it passes (hook blocks the update);
//     on the merge-base 4a0765f it fails (update proceeds, bug reproduced).
//   - F1/F3/F4/F5 are CANARY tests documenting current head behavior; they flip
//     if the behavior changes and must then be re-examined.
//   - F2 pins the stateful "wait for ALL post-update handlers" semantics; the
//     stateless differential half lives in
//     pkg/reconciler/roleinstanceset/statelessmode/sync/update_claude_verify_test.go.

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
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	api "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	instanceinplace "sigs.k8s.io/rbgs/pkg/inplace/instance/inplaceupdate"
	instancelifecycle "sigs.k8s.io/rbgs/pkg/inplace/instance/lifecycle"
	instanceutils "sigs.k8s.io/rbgs/pkg/reconciler/roleinstance/utils"
	"sigs.k8s.io/rbgs/pkg/utils/revisionadapter"
)

const claudeHookLabel = "claude.example.com/inplace-protection"
const claudeHookFinalizer = "claude.example.com/inplace-protection"

type claudeHookHarness struct {
	set         *api.RoleInstanceSet
	old, update *apps.ControllerRevision
	control     *defaultStatefulInstanceSetControl
	client      client.Client
	objects     *fakeInstanceObjectManager
	key         client.ObjectKey
}

// newClaudeHookHarness builds a single-replica set at old revision with a
// pending image update, an instance that matches the hook handlers, and a
// control wired to a fake client. mutate mutates the set template before the
// update revision is cut (e.g. to make the diff non-in-place-able).
func newClaudeHookHarness(t *testing.T, hook *api.RoleInstanceSetLifecycleHook, intercept interceptor.Funcs, mutate func(*api.RoleInstanceSet)) *claudeHookHarness {
	t.Helper()
	set := newRevisionTestSet("nginx:1.0")
	set.Name = "claude-inplace-hook"
	set.Spec.Replicas = ptr.To[int32](1)
	set.Spec.PodManagementPolicy = constants.ParallelPodManagement
	set.Spec.UpdateStrategy.Type = api.InPlaceIfPossibleUpdateStrategyType
	set.Spec.UpdateStrategy.MaxUnavailable = ptr.To(intstr.FromInt32(1))
	if hook != nil {
		set.Spec.Lifecycle = &api.RoleInstanceSetLifecycle{InPlaceUpdate: hook}
	}
	old, err := newRevision(set, 1, ptr.To[int32](0))
	require.NoError(t, err)
	instance := buildInst(set.Name, 0, old.Name, true, true)
	instance.Spec = *set.Spec.RoleInstanceTemplate.RoleInstanceSpec.DeepCopy()
	instance.Spec.ReadinessGates = []api.RoleInstanceReadinessGate{{ConditionType: api.RoleInstanceInPlaceUpdateReady}}
	instance.Generation = 1
	instance.Status.ObservedGeneration = 1
	instance.Status.ComponentStatuses = []api.RoleInstanceComponentStatus{{Name: "worker", UpdatedReplicas: 1}}
	instance.Status.Conditions = append(instance.Status.Conditions, api.RoleInstanceCondition{Type: api.RoleInstanceInPlaceUpdateReady, Status: corev1.ConditionTrue})
	if hook != nil {
		for k, v := range hook.LabelsHandler {
			instance.Labels[k] = v
		}
		instance.Finalizers = append(instance.Finalizers, hook.FinalizersHandler...)
	}
	set.Spec.RoleInstanceTemplate.Components[0].Template.Spec.Containers[0].Image = "nginx:2.0"
	if mutate != nil {
		mutate(set)
	}
	update, err := newRevision(set, 2, ptr.To[int32](0))
	require.NoError(t, err)
	set.Status.CurrentRevision, set.Status.UpdateRevision = old.Name, update.Name
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(instance).WithStatusSubresource(instance).WithInterceptorFuncs(intercept).Build()
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
	return &claudeHookHarness{set: set, old: old, update: update, control: control, client: c, objects: objects, key: client.ObjectKeyFromObject(instance)}
}

func (h *claudeHookHarness) get(t *testing.T) *api.RoleInstance {
	t.Helper()
	got := &api.RoleInstance{}
	require.NoError(t, h.client.Get(context.Background(), h.key, got))
	return got
}

func (h *claudeHookHarness) apply(t *testing.T) bool {
	t.Helper()
	instance := h.get(t)
	before := instance.DeepCopy()
	changed, err := h.control.applyTargetUpdate(context.Background(), h.set, &api.RoleInstanceSetStatus{}, instance,
		h.old, h.update, []*apps.ControllerRevision{h.old, h.update}, false, false)
	require.NoError(t, err)
	require.Equal(t, before, instance, "informer snapshots must remain immutable")
	return changed
}

// TestClaudeP0StatefulInPlaceHookMustBlockSpecUpdate is the premise check for
// PR #537 (issue #534 Task 3): a matching InPlaceUpdate handler must prevent
// the actual spec update on the Stateful path.
//
// CONTRACT test. Expected on head: PASS (instance waits in PreparingUpdate,
// image unchanged, not deleted). Expected on base 4a0765f: FAIL (the base
// stateful path performs the in-place update immediately, bypassing the hook —
// the bug the PR claims to fix).
func TestClaudeP0StatefulInPlaceHookMustBlockSpecUpdate(t *testing.T) {
	hook := &api.RoleInstanceSetLifecycleHook{LabelsHandler: map[string]string{claudeHookLabel: "hold"}}
	h := newClaudeHookHarness(t, hook, interceptor.Funcs{}, nil)
	for range 2 {
		require.False(t, h.apply(t), "waiting must not count as a revision transition")
		got := h.get(t)
		require.Equal(t, constants.RoleInstanceLifecycleStatePreparingUpdate, instancelifecycle.GetRoleInstanceLifecycleState(got),
			"P0: blocked instance must wait in PreparingUpdate")
		require.Equal(t, h.old.Name, getInstanceRevision(got))
		require.Equal(t, "nginx:1.0", got.Spec.Components[0].Template.Spec.Containers[0].Image,
			"P0: hook must prevent the spec update")
		require.Empty(t, h.objects.deleted, "P0: hook waiting must not fall back to deletion")
	}
}

// TestClaudeF1LifecycleExpectationRegisteredOnPodKeyedStore documents that the
// PR registers ResourceVersionExpectations entries under the RoleInstance UID
// on the per-instance controller's store (pkg/reconciler/roleinstance/utils),
// while every consumer of that store Observe/IsSatisfied/Delete only Pod
// objects (instance_reconciler.go:125, instance_update.go:95,
// instance_status.go:523, instance_event_handler.go:93). The entry is
// therefore never observed and never deleted by production code: the intended
// staleness guard is inert, and the entry leaks for the process lifetime.
// (PatchRoleInstance returns the pre-patch object, so the registered version
// is also stale — the entry would self-clear if anything ever presented the
// instance again, but nothing does.)
//
// CANARY test (documents current head behavior; flips if the Expect calls are
// removed or redirected to an observed store).
func TestClaudeF1LifecycleExpectationRegisteredOnPodKeyedStore(t *testing.T) {
	hook := &api.RoleInstanceSetLifecycleHook{LabelsHandler: map[string]string{claudeHookLabel: "hold"}}
	h := newClaudeHookHarness(t, hook, interceptor.Funcs{}, nil)
	require.False(t, h.apply(t))
	got := h.get(t)

	// Probe with a low resource version: the entry survives (not newer), which
	// proves an expectation WAS registered under the RoleInstance UID.
	probe := &api.RoleInstance{ObjectMeta: metav1.ObjectMeta{UID: got.UID, ResourceVersion: "1"}}
	satisfied, _ := instanceutils.ResourceVersionExpectations.IsSatisfied(probe)
	require.False(t, satisfied, "F1: an expectation entry should exist under the RoleInstance UID")

	// Every real consumer only ever presents Pod objects, which cannot match a
	// RoleInstance UID: the entry cannot be observed or deleted by any
	// production code path.
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{UID: types.UID("unrelated-pod-uid"), ResourceVersion: "999999"}}
	instanceutils.ResourceVersionExpectations.Observe(pod)
	instanceutils.ResourceVersionExpectations.Delete(pod)
	satisfied, _ = instanceutils.ResourceVersionExpectations.IsSatisfied(probe)
	require.False(t, satisfied, "F1: no Pod-based Observe/Delete can ever touch the RoleInstance-UID entry")
}

// TestClaudeF2PartialRestoreKeepsStatefulHandshakeOpen pins the stateful
// completion semantics chosen by the PR: an Updating instance whose hook has
// only PART of its handlers restored stays in Updated until all are restored.
// The stateless reference implementation (sync/update.go:144-150) uses
// !IsRoleInstanceHooked instead of !IsRoleInstanceAllHooked and would complete
// (go Normal) in this exact state — see the differential test in
// statelessmode/sync/update_claude_verify_test.go.
//
// CONTRACT test for the stateful semantics asserted by the PR body
// ("Require all post-update handlers to be restored before completing").
func TestClaudeF2PartialRestoreKeepsStatefulHandshakeOpen(t *testing.T) {
	hook := &api.RoleInstanceSetLifecycleHook{
		LabelsHandler:     map[string]string{claudeHookLabel: "hold"},
		FinalizersHandler: []string{claudeHookFinalizer},
	}
	h := newClaudeHookHarness(t, hook, interceptor.Funcs{}, nil)
	got := h.get(t)
	// Partial restore: the label handler is back, the finalizer handler is not.
	got.Finalizers = nil
	instancelifecycle.SetRoleInstanceLifecycle(constants.RoleInstanceLifecycleStateUpdating)(got)
	require.NoError(t, h.client.Update(context.Background(), got))

	// Update completed; partial restore moves Updating -> Updated (wait).
	modified, _, err := h.control.refreshInstanceState(h.set, h.get(t), h.update.Name)
	require.NoError(t, err)
	require.True(t, modified)
	require.Equal(t, constants.RoleInstanceLifecycleStateUpdated, instancelifecycle.GetRoleInstanceLifecycleState(h.get(t)))

	// Still partial on the next refresh: must NOT complete to Normal.
	modified, _, err = h.control.refreshInstanceState(h.set, h.get(t), h.update.Name)
	require.NoError(t, err)
	require.Equal(t, constants.RoleInstanceLifecycleStateUpdated, instancelifecycle.GetRoleInstanceLifecycleState(h.get(t)),
		"F2: a partially restored hook must keep the stateful handshake open")
	require.False(t, modified)
}

// TestClaudeF3UnexpectedLifecycleStateErrorsOnUpdatePath documents that the
// new state machine returns a hard error when an update target carries a
// lifecycle state outside {Normal, "", PreparingUpdate, Updating, Updated} —
// e.g. PreparingDelete, which instances can carry after a
// stateless->stateful pattern switch (or once issue #534 Task 1 lands). The
// error propagates out of the reconcile instead of skipping the target the
// way stateless sync/update.go:94 does for PreparingDelete.
//
// CANARY test (documents current head behavior; on base the same call
// succeeds and performs the in-place update).
func TestClaudeF3UnexpectedLifecycleStateErrorsOnUpdatePath(t *testing.T) {
	hook := &api.RoleInstanceSetLifecycleHook{LabelsHandler: map[string]string{claudeHookLabel: "hold"}}
	h := newClaudeHookHarness(t, hook, interceptor.Funcs{}, nil)
	got := h.get(t)
	got.Labels[constants.RoleInstanceSetLifecycleStateKey] = string(constants.RoleInstanceLifecycleStatePreparingDelete)
	require.NoError(t, h.client.Update(context.Background(), got))

	instance := h.get(t)
	_, err := h.control.applyTargetUpdate(context.Background(), h.set, &api.RoleInstanceSetStatus{}, instance,
		h.old, h.update, []*apps.ControllerRevision{h.old, h.update}, false, false)
	require.ErrorContains(t, err, "not allowed to in-place update",
		"F3: PreparingDelete targets error out instead of being skipped")
	require.Empty(t, h.objects.deleted)
}

// TestClaudeF4MarkNotReadySilentNoopWithoutCustomReadyGate documents that with
// MarkNotReady=true and no RoleInstanceCustomReady readiness gate on the
// instance, entering PreparingUpdate silently skips the NotReady marking
// (readiness addNotReadyKey returns early when the gate is absent — issue
// #534 Task 2). The PR's markPodNotRead contract therefore depends on the
// separate Task 2 fix; hook waiting itself still works.
//
// CANARY test (documents current head behavior).
func TestClaudeF4MarkNotReadySilentNoopWithoutCustomReadyGate(t *testing.T) {
	hook := &api.RoleInstanceSetLifecycleHook{
		LabelsHandler: map[string]string{claudeHookLabel: "hold"},
		MarkNotReady:  true,
	}
	h := newClaudeHookHarness(t, hook, interceptor.Funcs{}, nil)
	require.False(t, h.apply(t))
	got := h.get(t)
	require.Equal(t, constants.RoleInstanceLifecycleStatePreparingUpdate, instancelifecycle.GetRoleInstanceLifecycleState(got))
	var customReady *api.RoleInstanceCondition
	readyStatus := corev1.ConditionUnknown
	for i := range got.Status.Conditions {
		c := &got.Status.Conditions[i]
		switch c.Type {
		case api.RoleInstanceCustomReady:
			customReady = c
		case api.RoleInstanceReady:
			readyStatus = c.Status
		}
	}
	require.Nil(t, customReady, "F4: no CustomReady condition is written when the gate is absent (Task 2 gap)")
	require.Equal(t, corev1.ConditionTrue, readyStatus, "F4: instance Ready is not flipped by MarkNotReady without the gate")
}

// TestClaudeF5HookDoesNotProtectRecreateFallback documents that an InPlaceUpdate
// handler does not protect a target whose revision diff is not in-place-able
// (component size change): CanUpdateInPlace returns false, the new code
// returns handled=false and the target is deleted outright — even while the
// hook still matches and the instance sits in PreparingUpdate. Deletion
// protection is PreDelete's job (issue #534 Task 1, not yet in main), so until
// Task 1 lands a hooked rollout silently deletes the drained instance when the
// template change falls back to recreate.
//
// CANARY test (documents current head behavior).
func TestClaudeF5HookDoesNotProtectRecreateFallback(t *testing.T) {
	hook := &api.RoleInstanceSetLifecycleHook{LabelsHandler: map[string]string{claudeHookLabel: "hold"}}
	// Make the pending update non-in-place-able: component size 1 -> 2.
	h := newClaudeHookHarness(t, hook, interceptor.Funcs{}, func(set *api.RoleInstanceSet) {
		set.Spec.RoleInstanceTemplate.Components[0].Size = ptr.To[int32](2)
	})
	// The update revision was cut with size 2, so CanUpdateInPlace is false
	// from the start and the instance never even reaches PreparingUpdate: it
	// is deleted outright despite the matching hook.
	_ = h.apply(t)
	require.NotEmpty(t, h.objects.deleted, "F5: a matching InPlaceUpdate hook does not block the recreate fallback deletion")
}

// TestClaudeF5PreparingUpdateThenNonInplaceableDiffDeletes: same as F5 but the
// instance was already admitted to PreparingUpdate by an in-place-able update
// (image-only), and only then does a newer revision make the diff
// non-in-place-able. The "waiting must never fall back to deletion" invariant
// in inPlaceUpdateInstance's doc comment does not hold for this transition:
// the waiting instance is deleted while its hook still matches.
//
// CANARY test (documents current head behavior).
func TestClaudeF5PreparingUpdateThenNonInplaceableDiffDeletes(t *testing.T) {
	hook := &api.RoleInstanceSetLifecycleHook{LabelsHandler: map[string]string{claudeHookLabel: "hold"}}
	h := newClaudeHookHarness(t, hook, interceptor.Funcs{}, nil)
	// Admitted to PreparingUpdate by the image-only diff.
	require.False(t, h.apply(t))
	require.Equal(t, constants.RoleInstanceLifecycleStatePreparingUpdate, instancelifecycle.GetRoleInstanceLifecycleState(h.get(t)))

	// Newer revision changes component size: v1 -> v3 is not in-place-able.
	h.set.Spec.RoleInstanceTemplate.Components[0].Size = ptr.To[int32](2)
	update3, err := newRevision(h.set, 3, ptr.To[int32](0))
	require.NoError(t, err)
	h.set.Status.UpdateRevision = update3.Name

	instance := h.get(t)
	_, err = h.control.applyTargetUpdate(context.Background(), h.set, &api.RoleInstanceSetStatus{}, instance,
		h.old, update3, []*apps.ControllerRevision{h.old, h.update, update3}, false, false)
	require.NoError(t, err)
	require.NotEmpty(t, h.objects.deleted,
		"F5: a PreparingUpdate instance with a still-matching hook is deleted when the diff becomes non-in-place-able")
}
