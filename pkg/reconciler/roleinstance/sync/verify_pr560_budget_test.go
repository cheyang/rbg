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

package sync

// Independent verification harness for PR #560 (maxGroupRestarts budget).
// These tests were written by the reviewer against the PR's documented
// contracts, without reusing the author's fixtures. Production code is
// untouched; this file is additive.

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// verifyBudgetControl builds a realControl backed by a fake client whose
// apiReader is the same (always-fresh) fake client, mirroring production where
// apiReader bypasses the informer cache.
func verifyBudgetControl(t *testing.T, limit *int32, podPhases ...v1.PodPhase) (*realControl, *workloadsv1alpha2.RoleInstance, []*v1.Pod) {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, v1.AddToScheme(scheme))
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))
	instance := &workloadsv1alpha2.RoleInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "vbudget-0", Namespace: "default", UID: types.UID("vbudget-uid"), Generation: 1},
		Spec: workloadsv1alpha2.RoleInstanceSpec{
			RestartPolicyConfig: &workloadsv1alpha2.RestartPolicyConfig{
				Type:             workloadsv1alpha2.RecreateRoleInstanceOnPodRestartFromCreation,
				MaxGroupRestarts: limit,
			},
			Components: []workloadsv1alpha2.RoleInstanceComponent{{
				Name: "worker", Size: ptr.To[int32](int32(len(podPhases))),
				Template: v1.PodTemplateSpec{Spec: v1.PodSpec{Containers: []v1.Container{{Name: "worker", Image: "img:v1"}}}},
			}},
		},
		Status: workloadsv1alpha2.RoleInstanceStatus{ObservedGeneration: 1},
	}
	objects := []client.Object{instance}
	pods := make([]*v1.Pod, 0, len(podPhases))
	for i, phase := range podPhases {
		p := &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("vbudget-0-worker-%d", i),
				Namespace: "default",
				UID:       types.UID(fmt.Sprintf("vpod-%d", i)),
				Labels: map[string]string{
					constants.RoleInstanceNameLabelKey: instance.Name,
					constants.ComponentNameLabelKey:    "worker",
					constants.ComponentIDLabelKey:      fmt.Sprint(i),
				},
				OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(instance, workloadsv1alpha2.SchemeGroupVersion.WithKind("RoleInstance"))},
			},
			Status: v1.PodStatus{Phase: phase},
		}
		pods = append(pods, p)
		objects = append(objects, p)
	}
	cli := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(instance, &v1.Pod{}).WithObjects(objects...).Build()
	c := &realControl{Client: cli, apiReader: cli, recorder: record.NewFakeRecorder(100)}
	t.Cleanup(func() { c.ClearRestarting(instance) })
	return c, instance, pods
}

func verifyLive(t *testing.T, c *realControl, instance *workloadsv1alpha2.RoleInstance) *workloadsv1alpha2.RoleInstance {
	t.Helper()
	live := &workloadsv1alpha2.RoleInstance{}
	require.NoError(t, c.apiReader.Get(context.Background(), client.ObjectKeyFromObject(instance), live))
	return live
}

// verifyMarkGroupRecovered simulates the group having been rebuilt and become
// Ready again after an accepted restart: Restarting cleared, Ready set, pods
// Running. This is the Ready-separated loop the budget is meant to count.
func verifyMarkGroupRecovered(t *testing.T, c *realControl, instance *workloadsv1alpha2.RoleInstance, pods []*v1.Pod) {
	t.Helper()
	ctx := context.Background()
	c.ClearRestarting(instance)
	live := verifyLive(t, c, instance)
	live.Status.Conditions = []workloadsv1alpha2.RoleInstanceCondition{
		{Type: workloadsv1alpha2.RoleInstanceReady, Status: v1.ConditionTrue, LastTransitionTime: metav1.Now()},
	}
	require.NoError(t, c.Status().Update(ctx, live))
	for _, p := range pods {
		cur := &v1.Pod{}
		if err := c.Get(ctx, client.ObjectKeyFromObject(p), cur); err != nil {
			continue
		}
		cur.Status.Phase = v1.PodRunning
		require.NoError(t, c.Status().Update(ctx, cur))
	}
}

// TestVerifyBudgetExactAccountingContract is a CONTRACT test for the PR's
// central promise: exactly `limit` accepted rebuilds per template revision,
// then suppression + termination + retention + Degraded, and explicit recovery
// restores a fresh budget. Expected to PASS on a correct implementation.
func TestVerifyBudgetExactAccountingContract(t *testing.T) {
	ctx := context.Background()
	c, instance, pods := verifyBudgetControl(t, ptr.To[int32](1), v1.PodFailed, v1.PodRunning)

	// --- Fault 1: accepted, consumes the single attempt. ---
	livePods, accepted, err := c.acceptBudgetRestart(ctx, instance)
	require.NoError(t, err)
	require.True(t, accepted, "first fault within budget must be accepted")
	require.Len(t, livePods, 2, "acceptance returns the live complete group for the drain")
	live := verifyLive(t, c, instance)
	require.EqualValues(t, 1, live.Status.GroupRestartCount)
	require.False(t, live.Status.RestartBudgetExhausted)
	require.NotEmpty(t, live.Status.GroupRestartRevision)
	require.True(t, isInstanceRestarting(live), "accepted restart must persist Restarting before the drain")

	// Suppressed duplicate while the drain is in flight consumes nothing.
	_, accepted, err = c.acceptBudgetRestart(ctx, instance)
	require.NoError(t, err)
	require.False(t, accepted)
	require.EqualValues(t, 1, verifyLive(t, c, instance).Status.GroupRestartCount)

	// --- Group recovers (Ready-separated loop). ---
	verifyMarkGroupRecovered(t, c, instance, pods)

	// --- Fault 2: budget empty -> exhaustion, termination, retention. ---
	failed := &v1.Pod{}
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(pods[0]), failed))
	failed.Status.Phase = v1.PodFailed
	require.NoError(t, c.Status().Update(ctx, failed))

	blocked, err := c.reconcileRestartBudget(ctx, verifyLive(t, c, instance))
	require.NoError(t, err)
	require.True(t, blocked, "exhaustion must block the scale path")
	live = verifyLive(t, c, instance)
	require.True(t, live.Status.RestartBudgetExhausted)
	require.EqualValues(t, 1, live.Status.GroupRestartCount, "exhaustion itself consumes no attempt")
	require.Contains(t, live.Finalizers, constants.GroupRestartBudgetCleanupFinalizer)

	// Degraded / Ready conditions enforced on the persisted object.
	var degraded, ready *workloadsv1alpha2.RoleInstanceCondition
	for i := range live.Status.Conditions {
		switch live.Status.Conditions[i].Type {
		case workloadsv1alpha2.RoleInstanceDegraded:
			degraded = &live.Status.Conditions[i]
		case workloadsv1alpha2.RoleInstanceReady:
			ready = &live.Status.Conditions[i]
		}
	}
	require.NotNil(t, degraded, "exhaustion must report Degraded")
	require.Equal(t, v1.ConditionTrue, degraded.Status)
	require.Equal(t, GroupRestartBudgetExceeded, degraded.Reason)
	require.NotNil(t, ready, "exhaustion must force Ready=False")
	require.Equal(t, v1.ConditionFalse, ready.Status)

	// Pods are being terminated AND retained by the cleanup finalizer.
	for _, p := range pods {
		got := &v1.Pod{}
		require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(p), got))
		require.NotNil(t, got.DeletionTimestamp, "exhausted group must be terminated: %s", p.Name)
		require.Contains(t, got.Finalizers, constants.GroupRestartBudgetCleanupFinalizer, "retained for inspection: %s", p.Name)
	}

	// --- Non-recovery edits must not unfreeze. ---
	for _, mutate := range []string{"raise-limit", "drop-policy", "mark-ready"} {
		live = verifyLive(t, c, instance)
		switch mutate {
		case "raise-limit":
			live.Spec.RestartPolicyConfig.MaxGroupRestarts = ptr.To[int32](99)
		case "drop-policy":
			live.Spec.RestartPolicyConfig.Type = workloadsv1alpha2.RestartPolicyNone
		case "mark-ready":
			live.Status.Conditions = []workloadsv1alpha2.RoleInstanceCondition{{Type: workloadsv1alpha2.RoleInstanceReady, Status: v1.ConditionTrue}}
		}
		if mutate == "mark-ready" {
			require.NoError(t, c.Status().Update(ctx, live))
		} else {
			require.NoError(t, c.Client.Update(ctx, live))
			// Generation changed; keep the passed object in sync like the informer would.
			live = verifyLive(t, c, instance)
		}
		blocked, err = c.reconcileRestartBudget(ctx, live)
		require.NoError(t, err)
		require.True(t, blocked, "%s must not resume recovery", mutate)
		require.True(t, verifyLive(t, c, instance).Status.RestartBudgetExhausted, mutate)
	}

	// Restore a valid spec (the edits above were negative controls).
	live = verifyLive(t, c, instance)
	live.Spec.RestartPolicyConfig.Type = workloadsv1alpha2.RecreateRoleInstanceOnPodRestartFromCreation
	live.Spec.RestartPolicyConfig.MaxGroupRestarts = ptr.To[int32](1)
	require.NoError(t, c.Client.Update(ctx, live))

	// --- Explicit recovery: annotate -> release retained pods, fresh budget. ---
	live = verifyLive(t, c, instance)
	live.Annotations = map[string]string{constants.RoleInstanceRecoverAnnotationKey: "true"}
	require.NoError(t, c.Client.Update(ctx, live))
	blocked, err = c.reconcileRestartBudget(ctx, verifyLive(t, c, instance))
	require.NoError(t, err)
	require.True(t, blocked, "recovery takes a cleanup pass before creation")
	live = verifyLive(t, c, instance)
	require.False(t, live.Status.RestartBudgetExhausted)
	require.Zero(t, live.Status.GroupRestartCount)
	require.Empty(t, live.Annotations[constants.RoleInstanceRecoverAnnotationKey], "recovery annotation must be consumed")
	for _, p := range pods {
		err := c.Get(ctx, client.ObjectKeyFromObject(p), &v1.Pod{})
		require.True(t, apierrors.IsNotFound(err), "released terminating pod must be gone: %s", p.Name)
	}
	// Next pass is no longer blocked.
	blocked, err = c.reconcileRestartBudget(ctx, live)
	require.NoError(t, err)
	require.False(t, blocked)
}

// TestVerifyBudgetRevisionIdentityContract pins the revision-hash semantics:
// size-only edits keep the revision (budget survives resizes), pod-template
// edits start a fresh budget. CONTRACT: expected to PASS on a correct impl.
func TestVerifyBudgetRevisionIdentityContract(t *testing.T) {
	c, instance, _ := verifyBudgetControl(t, ptr.To[int32](1), v1.PodRunning)
	_ = c
	rev := groupRestartRevision(instance)
	require.NotEmpty(t, rev)

	// Size-only change: same revision.
	resized := instance.DeepCopy()
	resized.Spec.Components[0].Size = ptr.To[int32](5)
	require.Equal(t, rev, groupRestartRevision(resized), "scale-only edits must not reset a revision's budget")

	// Pod-template change: new revision.
	retemplated := instance.DeepCopy()
	retemplated.Spec.Components[0].Template.Spec.Containers[0].Image = "img:v2"
	require.NotEqual(t, rev, groupRestartRevision(retemplated), "pod-template change must start a fresh budget")
}

// TestVerifyFailedDrainBurnsBudgetAttempt is a CANARY for finding F3: when the
// pod drain after an accepted restart fails partway (transient API error), the
// attempt stays consumed AND the Restarting gate prevents any retry of the
// group drain — the group heals only via per-pod replacement. Expected to PASS
// (assert current behavior) on the reviewed head; FLIP to fail when the
// accounting/drain retry is fixed, then invert.
func TestVerifyFailedDrainBurnsBudgetAttempt(t *testing.T) {
	ctx := context.Background()
	c, instance, pods := verifyBudgetControl(t, ptr.To[int32](1), v1.PodFailed, v1.PodRunning)

	// Accept the restart (count=1, Restarting persisted) — the pre-drain commit.
	livePods, accepted, err := c.acceptBudgetRestart(ctx, instance)
	require.NoError(t, err)
	require.True(t, accepted)
	require.Len(t, livePods, 2)

	// The drain fails on one member (transient API error), like a partial
	// deletePods failure in Scale.
	failDelete := interceptor.Funcs{
		Delete: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			if obj.GetName() == pods[1].Name {
				return errors.New("injected transient delete failure")
			}
			return cl.Delete(ctx, obj, opts...)
		},
	}
	c.Client = interceptor.NewClient(c.Client.(client.WithWatch), failDelete)
	_, delErr := c.deletePods(ctx, instance, livePods)
	require.Error(t, delErr, "fixture: drain must fail")

	// Canary assertions — current observed behavior:
	live := verifyLive(t, c, instance)
	require.EqualValues(t, 1, live.Status.GroupRestartCount, "the burned attempt stays consumed")
	require.True(t, isInstanceRestarting(live), "Restarting stays latched")

	// A subsequent reconcile can neither re-accept (Restarting gate) nor exhaust
	// (isInstanceRestarting blocks the exhaustion path), so the failed member is
	// never group-drained through the restart policy again.
	_, accepted, err = c.acceptBudgetRestart(ctx, instance)
	require.NoError(t, err)
	require.False(t, accepted, "Restarting gate blocks re-acceptance of the drained group")
	blocked, err := c.reconcileRestartBudget(ctx, verifyLive(t, c, instance))
	require.NoError(t, err)
	require.False(t, blocked, "exhaustion path also skips while Restarting — the burned attempt is neither refunded nor retried")
	require.False(t, verifyLive(t, c, instance).Status.RestartBudgetExhausted)
}

// TestVerifyResizeMustNotResetBudget is a CONTRACT test for the PR's documented
// guarantee: "Scale-only changes (size) deliberately do not [reset the budget],
// so resizing cannot be used to bypass exhaustion."
//
// groupRestartRevision nils component.Size, but the RoleInstanceSet reconciler
// injects the size into the component's pod-template labels
// (constants.ComponentSizeLabelKey, roleinstanceset_reconciler.go:367/449/466),
// and a resize rolls the RI template, updating that label on the RI spec. The
// label survives in the hash input, so a pure resize changes the revision hash
// and resets the budget — the exact bypass the doc promises to prevent.
//
// Expected: PASS when the hash ignores size-derived template labels.
// On the reviewed head: FAILS (budget is reset by a resize).
func TestVerifyResizeMustNotResetBudget(t *testing.T) {
	ctx := context.Background()
	c, instance, _ := verifyBudgetControl(t, ptr.To[int32](0), v1.PodFailed, v1.PodRunning)
	// Mirror what the RIS reconciler writes into the RI spec's component
	// template: the size label (roleinstanceset_reconciler.go injects it).
	resizeLabel := func(inst *workloadsv1alpha2.RoleInstance, size string) {
		labels := inst.Spec.Components[0].Template.Labels
		if labels == nil {
			labels = map[string]string{}
		}
		labels[constants.ComponentSizeLabelKey] = size
		inst.Spec.Components[0].Template.Labels = labels
	}
	live := verifyLive(t, c, instance)
	resizeLabel(live, "2")
	require.NoError(t, c.Client.Update(ctx, live))

	// Exhaust the zero budget on the first fault.
	blocked, err := c.reconcileRestartBudget(ctx, verifyLive(t, c, instance))
	require.NoError(t, err)
	require.True(t, blocked)
	live = verifyLive(t, c, instance)
	require.True(t, live.Status.RestartBudgetExhausted)
	storedRevision := live.Status.GroupRestartRevision
	require.NotEmpty(t, storedRevision)

	// Sanity: a bare Size change (no template label change) must keep the
	// revision — this is the case the implementation explicitly handles.
	resized := live.DeepCopy()
	resized.Spec.Components[0].Size = ptr.To[int32](3)
	require.Equal(t, storedRevision, groupRestartRevision(resized), "Size field is excluded from the hash")

	// The real resize as performed through the RIS template: Size AND the
	// component-size template label change together (that is what the
	// reconciler writes; the label is part of the pod template).
	resizedForReal := live.DeepCopy()
	resizedForReal.Spec.Components[0].Size = ptr.To[int32](3)
	resizeLabel(resizedForReal, "3")
	require.Equal(t, storedRevision, groupRestartRevision(resizedForReal),
		"CONTRACT VIOLATION: an RIS-propagated resize must not change the budget revision — otherwise resizing bypasses exhaustion")

	// End-to-end at the reconcile level: applying the resized spec must not
	// clear the exhaustion marker or release the retained pods.
	require.NoError(t, c.Client.Update(ctx, resizedForReal))
	blocked, err = c.reconcileRestartBudget(ctx, verifyLive(t, c, instance))
	require.NoError(t, err)
	liveAfter := verifyLive(t, c, instance)
	require.True(t, blocked, "a resize of an exhausted instance must stay blocked")
	require.True(t, liveAfter.Status.RestartBudgetExhausted,
		"CONTRACT VIOLATION: resize silently un-exhausted the instance (budget bypassed)")
}

// TestVerifyNeverReadyCrashloopNeverExhausts is a CANARY for finding F4:
// with a pre-Ready crash-looping group (the FromCreation target scenario), the
// first fault is accepted and latches Restarting=True; the latch is cleared
// only when the group becomes Ready, so every later fault is suppressed by the
// restart guard AND the exhaustion path skips latching instances. The budget
// circuit breaker therefore never fires and the instance never reports
// Degraded — it is silently per-pod-replaced forever.
// CANARY: asserts current behavior; must FLIP when the latch/exhaustion
// interaction is fixed.
func TestVerifyNeverReadyCrashloopNeverExhausts(t *testing.T) {
	ctx := context.Background()
	c, instance, _ := verifyBudgetControl(t, ptr.To[int32](1), v1.PodFailed, v1.PodRunning)

	// Fault 1 pre-Ready (no Ready condition ever set): accepted for FromCreation.
	_, accepted, err := c.acceptBudgetRestart(ctx, instance)
	require.NoError(t, err)
	require.True(t, accepted, "FromCreation accepts a pre-Ready fault")
	require.EqualValues(t, 1, verifyLive(t, c, instance).Status.GroupRestartCount)

	// Faults 2..N: group never becomes Ready (pods keep failing). Restarting
	// stays latched from the accepted restart.
	for round := 2; round <= 5; round++ {
		live := verifyLive(t, c, instance)
		require.True(t, isInstanceRestarting(live), "Restarting latch persists while the group never becomes Ready")

		// Restart guard suppresses a new group rebuild.
		_, accepted, err = c.acceptBudgetRestart(ctx, live)
		require.NoError(t, err)
		require.False(t, accepted, "round %d: rebuild suppressed by Restarting latch", round)

		// And the budget circuit breaker cannot fire either: the exhaustion
		// path skips latching instances, so no Degraded signal is ever raised.
		blocked, err := c.reconcileRestartBudget(ctx, live)
		require.NoError(t, err)
		require.False(t, blocked, "round %d: budget never blocks while Restarting", round)
		live = verifyLive(t, c, instance)
		require.False(t, live.Status.RestartBudgetExhausted, "round %d: never-Ready crashloop never exhausts the budget", round)
		require.NotContains(t, condTypes(live), workloadsv1alpha2.RoleInstanceDegraded,
			"round %d: no Degraded signal reaches the operator", round)
	}
}

// TestVerifyRecoveryDuringDrainRetriggers is a CANARY for finding F5: when
// recovery is requested while an old member is still terminating (finalizer
// released but object not yet gone — always true for a kubelet-grace-period
// window, and permanently true after a failed drain), the cleared budget
// immediately re-triggers on the still-terminating desired member: a zero
// budget re-exhausts before the fresh group ever starts.
// CANARY: asserts current behavior; must FLIP when recovery waits for the old
// group to be fully gone.
func TestVerifyRecoveryDuringDrainRetriggers(t *testing.T) {
	ctx := context.Background()
	c, instance, pods := verifyBudgetControl(t, ptr.To[int32](0), v1.PodFailed, v1.PodRunning)

	// Exhaust the zero budget.
	blocked, err := c.reconcileRestartBudget(ctx, instance)
	require.NoError(t, err)
	require.True(t, blocked)
	live := verifyLive(t, c, instance)
	require.True(t, live.Status.RestartBudgetExhausted)

	// Simulate a drain that never completed for pod[1]: it is still Running
	// (its delete failed earlier), while pod[0] is mid-termination. Rebuild the
	// API state accordingly: un-delete pod[1] by recreating it (the failed
	// delete left it untouched in practice).
	terminating := &v1.Pod{}
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(pods[0]), terminating))
	require.NotNil(t, terminating.DeletionTimestamp, "fixture: pod[0] retained mid-termination")

	// Recover while pod[0] is still terminating.
	live.Annotations = map[string]string{constants.RoleInstanceRecoverAnnotationKey: "true"}
	require.NoError(t, c.Client.Update(ctx, live))
	blocked, err = c.reconcileRestartBudget(ctx, verifyLive(t, c, instance))
	require.NoError(t, err)
	require.True(t, blocked, "recovery cleanup pass")
	live = verifyLive(t, c, instance)
	require.False(t, live.Status.RestartBudgetExhausted, "recovery clears exhaustion")

	// pod[0]'s finalizer was released; the fake client collects it (real
	// clusters keep it for the kubelet grace window). Re-seed the still
	// terminating member to model that window.
	stillTerminating := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pods[0].Name,
			Namespace: "default",
			UID:       types.UID("vpod-0-old"),
			Labels: map[string]string{
				constants.RoleInstanceNameLabelKey: instance.Name,
				constants.ComponentNameLabelKey:    "worker",
				constants.ComponentIDLabelKey:      "0",
			},
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(instance, workloadsv1alpha2.SchemeGroupVersion.WithKind("RoleInstance"))},
			Finalizers:      []string{"example.com/hold"},
		},
		Status: v1.PodStatus{Phase: v1.PodRunning},
	}
	require.NoError(t, c.Create(ctx, stillTerminating))
	require.NoError(t, c.Delete(ctx, stillTerminating)) // DeletionTimestamp set, object retained by the foreign finalizer

	// The cleared zero budget re-exhausts on the old member before any fresh
	// group pod exists.
	blocked, err = c.reconcileRestartBudget(ctx, verifyLive(t, c, instance))
	require.NoError(t, err)
	require.True(t, blocked)
	require.True(t, verifyLive(t, c, instance).Status.RestartBudgetExhausted,
		"CANARY: a still-terminating old member re-exhausts a zero budget immediately after recovery")
}

func condTypes(instance *workloadsv1alpha2.RoleInstance) []workloadsv1alpha2.RoleInstanceConditionType {
	out := make([]workloadsv1alpha2.RoleInstanceConditionType, 0, len(instance.Status.Conditions))
	for _, cond := range instance.Status.Conditions {
		out = append(out, cond.Type)
	}
	return out
}
