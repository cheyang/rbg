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

// Verification harness for PR #559 review (Reviewer B / Codex).
// Additive only: no production code is modified by this file.
//
// Findings covered (see docs/verification/pr-559-recreate-from-creation/README.md):
//   P0  premise: on the LEGACY policy, member deletion is not a trigger and the
//       Ready gate permanently disables the policy once NotReady (runs against
//       base and head; legacy behavior must be identical on both).
//   F1  RecreateRoleInstanceOnPodRestartFromCreation can fire only ONCE before
//       first Ready: the isAlreadyRestarting guard (in-memory cache + persisted
//       Restarting condition) is cleared only when the instance becomes Ready,
//       so a second pre-Ready failure is silently suppressed.
//   F3  checkRestartBackoff slow path no longer counts Ignore-annotated Failed
//       pods (legacy-policy behavior delta vs base).
//   F4  canary: a Succeeded pod being deleted currently triggers whole-instance
//       recreation under FromCreation although the KEP lists Succeeded as a
//       non-goal.

package sync

import (
	"context"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/stretchr/testify/require"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func verify559Instance(policy workloadsv1alpha2.RestartPolicyType) *workloadsv1alpha2.RoleInstance {
	return &workloadsv1alpha2.RoleInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "verify559-worker-0",
			Namespace:  "default",
			Generation: 1,
		},
		Spec: workloadsv1alpha2.RoleInstanceSpec{
			RestartPolicyConfig: &workloadsv1alpha2.RestartPolicyConfig{Type: policy},
			Components: []workloadsv1alpha2.RoleInstanceComponent{
				{Name: "worker", Size: ptr.To[int32](1)},
			},
		},
		Status: workloadsv1alpha2.RoleInstanceStatus{
			ObservedGeneration: 1,
			// No Ready condition: the instance has never become Ready.
		},
	}
}

func verify559DesiredPod() *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "verify559-worker-0-worker-0",
			Namespace: "default",
			Labels: map[string]string{
				constants.ComponentNameLabelKey: "worker",
				constants.ComponentIDLabelKey:   "0",
			},
		},
		Status: v1.PodStatus{Phase: v1.PodRunning},
	}
}

func verify559CrashedPod() *v1.Pod {
	p := verify559DesiredPod()
	p.Status.ContainerStatuses = []v1.ContainerStatus{{Name: "main", RestartCount: 3}}
	return p
}

// P0a (premise, contract): the LEGACY policy must NOT treat a deleted
// (terminating) member pod as a trigger. True on base (premise holds) and must
// remain true on head ("legacy policy unchanged").
func TestVerify559_Premise_LegacyIgnoresMemberDeletion(t *testing.T) {
	instance := verify559Instance(workloadsv1alpha2.RecreateRoleInstanceOnPodRestart)
	// Legacy policy still requires the instance to have been Ready.
	instance.Status.Conditions = []workloadsv1alpha2.RoleInstanceCondition{
		{Type: workloadsv1alpha2.RoleInstanceReady, Status: v1.ConditionTrue},
	}
	pod := verify559DesiredPod()
	now := metav1.Now()
	pod.DeletionTimestamp = &now // member deleted (node failure / eviction / kubectl delete)
	require.False(t, shouldRecreateInstance(instance, []*v1.Pod{pod}, nil),
		"legacy policy must not treat member deletion as a restart trigger")
}

// P0b (premise, contract): under the LEGACY policy, once the instance is
// NotReady and no recent restart-policy rebuild exists, even a Failed pod does
// not trigger recreation — the gate is permanently closed. True on base
// (premise holds) and must remain true on head for the legacy policy.
func TestVerify559_Premise_LegacyReadyGatePermanentlyClosed(t *testing.T) {
	instance := verify559Instance(workloadsv1alpha2.RecreateRoleInstanceOnPodRestart)
	// no Ready condition, no LastRestartTime
	pod := verify559DesiredPod()
	pod.Status.Phase = v1.PodFailed
	require.False(t, shouldRecreateInstance(instance, []*v1.Pod{pod}, nil),
		"legacy policy with Ready=False and no recent restart must not recreate")
}

// Control (contract): the new FromCreation policy DOES fire on a first
// pre-Ready failure when no restart cycle is in progress.
func TestVerify559_FromCreation_FirstPreReadyFailureTriggers(t *testing.T) {
	ctrl := &realControl{}
	instance := verify559Instance(workloadsv1alpha2.RecreateRoleInstanceOnPodRestartFromCreation)
	require.True(t, ctrl.shouldRecreateInstanceGuarded(context.Background(), instance,
		[]*v1.Pod{verify559CrashedPod()}, nil),
		"first pre-Ready failure must trigger recreation under FromCreation")
}

// F1 (contract): after the first recreation has fired, the Restarting state
// (in-memory cache) is cleared ONLY when the instance becomes Ready. An
// instance that fails again BEFORE ever becoming Ready — the exact scenario
// the new policy exists for — must trigger another recreation (paced by
// backoff), not be silently swallowed.
//
// Polarity: contract — FAILS on the PR head (guard suppresses), must PASS
// after a fix.
func TestVerify559_FromCreation_SecondPreReadyFailure_InMemoryGuard(t *testing.T) {
	ctrl := &realControl{}
	instance := verify559Instance(workloadsv1alpha2.RecreateRoleInstanceOnPodRestartFromCreation)
	// Simulate the state after recreation #1 fired in this controller process:
	// Restarting condition set + in-memory cache populated + tracking updated.
	setRestartingCondition(instance)
	updateRestartTracking(instance)
	t.Cleanup(func() { restartingCache.Delete(instanceKey(instance)) })

	// Recreated pods crash again while the instance is still pre-Ready.
	got := ctrl.shouldRecreateInstanceGuarded(context.Background(), instance,
		[]*v1.Pod{verify559CrashedPod()}, nil)
	require.True(t, got,
		"second pre-Ready failure must trigger another recreation; isAlreadyRestarting pins the policy after a single use")
}

// F1b (contract): the suppression survives a controller restart, because the
// persisted Restarting=True condition is likewise cleared only on Ready.
func TestVerify559_FromCreation_SecondPreReadyFailure_PersistedCondition(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))

	instance := verify559Instance(workloadsv1alpha2.RecreateRoleInstanceOnPodRestartFromCreation)
	instance.Status.Conditions = []workloadsv1alpha2.RoleInstanceCondition{{
		Type:               workloadsv1alpha2.RoleInstanceRestarting,
		Status:             v1.ConditionTrue,
		LastTransitionTime: metav1.Now(),
		Reason:             "RestartPolicyTriggered",
	}}
	updateRestartTracking(instance)

	fresh := instance.DeepCopy()
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(fresh).Build()
	ctrl := &realControl{apiReader: fakeClient}
	// In-memory cache empty, as after a controller restart.
	restartingCache.Delete(instanceKey(instance))
	t.Cleanup(func() { restartingCache.Delete(instanceKey(instance)) })

	got := ctrl.shouldRecreateInstanceGuarded(context.Background(), instance,
		[]*v1.Pod{verify559CrashedPod()}, nil)
	require.True(t, got,
		"persisted Restarting=True (cleared only on Ready) suppresses the second pre-Ready recreation even after a controller restart")
}

// F1c (canary): documents the CURRENT broken interplay — with Restarting=True
// stuck, checkRestartBackoff returns 0 (no pacing) while the guard blocks the
// recreation: the net effect is silent inaction, not a backoff-paced retry.
// Asserts the buggy behavior; must FLIP when the fix lands (then invert).
func TestVerify559_FromCreation_StuckRestartingSkipsBackoff(t *testing.T) {
	ctrl := &realControl{}
	instance := verify559Instance(workloadsv1alpha2.RecreateRoleInstanceOnPodRestartFromCreation)
	setRestartingCondition(instance)
	updateRestartTracking(instance)
	t.Cleanup(func() { restartingCache.Delete(instanceKey(instance)) })

	// Instance carries default backoff (30s/600s); a second failure right after
	// recreation #1 should normally be paced by backoff (requeue > 0). Because
	// Restarting is stuck True the backoff is skipped entirely.
	requeue := ctrl.checkRestartBackoff(instance, nil, []*v1.Pod{verify559CrashedPod()}, nil)
	require.Equal(t, time.Duration(0), requeue,
		"CANARY: stuck Restarting short-circuits backoff; flip when fixed")
	// And the recreation itself is suppressed.
	require.False(t, ctrl.shouldRecreateInstanceGuarded(context.Background(), instance,
		[]*v1.Pod{verify559CrashedPod()}, nil),
		"CANARY: guard suppresses the second pre-Ready recreation; flip when fixed")
}

// F3 (contract-for-new-behavior): on the PR head, an Ignore-annotated Failed
// pod no longer holds the legacy backoff slow path open. On base the same
// input returns a positive requeue delay (the Ignore pod counted as
// hasFailedPods). Documents the unannounced legacy-policy behavior delta.
func TestVerify559_LegacyBackoff_IgnorePodNoLongerHoldsBackoff(t *testing.T) {
	ctrl := &realControl{}
	instance := verify559Instance(workloadsv1alpha2.RecreateRoleInstanceOnPodRestart)
	instance.Status.Conditions = []workloadsv1alpha2.RoleInstanceCondition{
		{Type: workloadsv1alpha2.RoleInstanceReady, Status: v1.ConditionTrue},
	}
	now := metav1.Now()
	instance.Status.RestartCount = 1
	instance.Status.LastRestartTime = &now

	pod := verify559DesiredPod()
	pod.Status.Phase = v1.PodFailed
	pod.Annotations = map[string]string{
		constants.RestartTriggerPolicyAnnotationKey: constants.RestartTriggerPolicyIgnore,
	}
	requeue := ctrl.checkRestartBackoff(instance, nil, nil, []*v1.Pod{pod})
	require.Equal(t, time.Duration(0), requeue,
		"Ignore-annotated Failed pod must not hold restart backoff open (new behavior on head)")
}

// F4 (canary): a Succeeded pod that is being deleted currently triggers
// whole-instance recreation under FromCreation, although the KEP lists
// Succeeded pods as a non-goal ("normal completion"). Asserts the CURRENT
// behavior; must FLIP to false when the trigger excludes terminal pods.
func TestVerify559_FromCreation_SucceededPodDeletion_Canary(t *testing.T) {
	instance := verify559Instance(workloadsv1alpha2.RecreateRoleInstanceOnPodRestartFromCreation)
	pod := verify559DesiredPod()
	pod.Status.Phase = v1.PodSucceeded
	now := metav1.Now()
	pod.DeletionTimestamp = &now // completed member being cleaned up
	require.True(t, shouldRecreateInstance(instance, []*v1.Pod{pod}, nil),
		"CANARY: Succeeded+terminating pod currently triggers recreation; flip this test when terminal pods are excluded")
}
