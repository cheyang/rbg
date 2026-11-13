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

// Reviewer harness for PR #573 (verify/restart-wave-guard-claude).
//
// These tests encode reviewer-derived claims about the restart wave guard that
// the PR's own tests do not cover:
//
//   - H1 (contract): the guard still absorbs the cascading MemberTerminating
//     trigger it was originally built for — a pre-restart Pod observed
//     Terminating (desired member) must not start a new wave.
//   - H2 (canary, residual limitation): a pre-restart Pod stuck Terminating
//     (finalizer / dead node) holds the guard armed, so a failed replacement
//     cannot start a second recreation. This documents the #563 symptom
//     persisting when pre-restart Pod deletion never completes.
//   - H3 (canary, acknowledged limitation): the persisted-wave fallback
//     (controller restart) treats a Pod created in the same second as
//     LastRestartTime as a replacement, releasing the guard early.
//   - H4 (contract): after controller restart mid-wave (marker lost), with the
//     persisted Restarting condition and every Pod created after the restart,
//     a failed Pod is allowed to start a new wave.

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/utils/ptr"
)

func harnessWaveInstance(name string, lastRestart time.Time) *workloadsv1alpha2.RoleInstance {
	return &workloadsv1alpha2.RoleInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Namespace:  "default",
			Generation: 1,
		},
		Spec: workloadsv1alpha2.RoleInstanceSpec{
			RestartPolicyConfig: &workloadsv1alpha2.RestartPolicyConfig{
				Type: workloadsv1alpha2.RecreateRoleInstanceOnPodRestart,
			},
			Components: []workloadsv1alpha2.RoleInstanceComponent{
				{Name: "comp", Size: ptr.To[int32](2)},
			},
		},
		Status: workloadsv1alpha2.RoleInstanceStatus{
			ObservedGeneration: 1,
			LastReadyGeneration: 1,
			CurrentRevision:     "rev-1",
			UpdateRevision:      "rev-1",
			LastRestartTime:     &metav1.Time{Time: lastRestart},
			RestartCount:        1,
			// The rebuilt generation has not become Ready — the #563 shape.
			Conditions: []workloadsv1alpha2.RoleInstanceCondition{
				{Type: workloadsv1alpha2.RoleInstanceReady, Status: corev1.ConditionFalse},
				{Type: workloadsv1alpha2.RoleInstanceRestarting, Status: corev1.ConditionTrue, Reason: "RestartPolicyTriggered"},
			},
		},
	}
}

// H1: the cascading MemberTerminating trigger produced by the wave's own
// deletion is absorbed while the pre-restart Pod is still observed.
func TestHarnessWaveGuardAbsorbsMemberTerminatingTrigger(t *testing.T) {
	now := time.Now()
	instance := harnessWaveInstance("h1-instance", now.Add(-time.Minute))
	ctrl := &realControl{}
	t.Cleanup(func() { restartingCache.Delete(instanceKey(instance)) })

	terminating := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "h1-instance-comp-0",
			UID:               "h1-old-uid",
			CreationTimestamp: metav1.NewTime(now.Add(-10 * time.Minute)),
			DeletionTimestamp: ptr.To(metav1.NewTime(now.Add(-5 * time.Second))),
		},
	}

	rememberRestartWave(instance, []*corev1.Pod{terminating})

	// The terminating desired member alone is a recreation trigger…
	assert.True(t, shouldRecreateInstance(instance, []*corev1.Pod{terminating}, nil),
		"a desired member observed Terminating must be a trigger")
	// …but the wave guard must absorb it.
	assert.False(t, ctrl.shouldRecreateInstanceGuarded(context.Background(), instance, []*corev1.Pod{terminating}, nil, nil),
		"H1: the wave guard must absorb the cascading MemberTerminating trigger while the pre-restart Pod is terminating")
}

// H2 (canary): a pre-restart Pod that never finishes deleting keeps the guard
// armed, so a failed replacement of a different ordinal cannot recreate.
func TestHarnessStuckTerminatingPreRestartPodHoldsGuard(t *testing.T) {
	now := time.Now()
	instance := harnessWaveInstance("h2-instance", now.Add(-time.Minute))
	ctrl := &realControl{}
	t.Cleanup(func() { restartingCache.Delete(instanceKey(instance)) })

	stuck := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "h2-instance-comp-0",
			UID:               "h2-old-uid",
			CreationTimestamp: metav1.NewTime(now.Add(-10 * time.Minute)),
			DeletionTimestamp: ptr.To(metav1.NewTime(now.Add(-9 * time.Minute))),
		},
	}
	failedReplacement := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "h2-instance-comp-1",
			UID:               "h2-new-uid",
			CreationTimestamp: metav1.NewTime(now.Add(-30 * time.Second)),
		},
		Status: corev1.PodStatus{Phase: corev1.PodFailed},
	}

	rememberRestartWave(instance, []*corev1.Pod{stuck, {ObjectMeta: metav1.ObjectMeta{
		Name: "h2-instance-comp-1", UID: "h2-old-uid-2", CreationTimestamp: metav1.NewTime(now.Add(-10 * time.Minute))}}})

	// The failed replacement alone would trigger…
	assert.True(t, shouldRecreateInstance(instance, []*corev1.Pod{failedReplacement}, nil),
		"a Failed replacement Pod must be a trigger")
	// …but with the stuck pre-restart Pod still present, the guard blocks it:
	// #563's symptom persists when pre-restart deletion never completes.
	blocked := ctrl.shouldRecreateInstanceGuarded(context.Background(), instance,
		[]*corev1.Pod{stuck, failedReplacement}, nil, nil)
	assert.True(t, blocked == false,
		"H2 canary: the guard currently blocks a failed replacement while a pre-restart Pod is stuck Terminating")
}

// H3 (canary): the persisted-wave fallback counts a Pod created in the same
// second as LastRestartTime as a replacement, releasing the guard early.
func TestHarnessSameSecondPodCountsAsReplacement(t *testing.T) {
	restartAt := time.Now().Add(-time.Minute)
	instance := harnessWaveInstance("h3-instance", restartAt)

	sameSecondPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "h3-instance-comp-0",
			UID:               "h3-old-uid",
			CreationTimestamp: metav1.Time{Time: restartAt}, // same second, terminating
			DeletionTimestamp: ptr.To(metav1.NewTime(time.Now())),
		},
	}
	assert.False(t, hasPreRestartPods(instance, []*corev1.Pod{sameSecondPod}),
		"H3 canary: a same-second pre-restart Pod is (mis)classified as a replacement by the fallback heuristic")

	beforePod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "h3-instance-comp-1",
			UID:               "h3-old-uid-2",
			CreationTimestamp: metav1.NewTime(restartAt.Add(-time.Second)),
		},
	}
	assert.True(t, hasPreRestartPods(instance, []*corev1.Pod{beforePod}),
		"a Pod created strictly before the restart is pre-restart")
}

// H4: after controller restart mid-wave, the persisted Restarting condition
// with every Pod created after the restart no longer blocks a new wave.
func TestHarnessControllerRestartFallbackReleasesWhenOldPodsGone(t *testing.T) {
	restartAt := time.Now().Add(-time.Minute)
	instance := harnessWaveInstance("h4-instance", restartAt)

	fresh := instance.DeepCopy()

	replacement := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "h4-instance-comp-0",
			UID:               "h4-new-uid",
			CreationTimestamp: metav1.NewTime(restartAt.Add(30 * time.Second)),
		},
		Status: corev1.PodStatus{Phase: corev1.PodFailed},
	}

	// No in-memory marker (controller restarted); fresh API copy holds the
	// persisted Restarting=True.
	assert.True(t, isInstanceRestarting(fresh))
	recreate := (&realControl{}).shouldRecreateInstanceGuarded(context.Background(), instance, []*corev1.Pod{replacement}, nil, fresh)
	assert.True(t, recreate,
		"H4: a persisted wave whose pre-restart Pods are gone must not block a new recreation")
}

// H5: the marker-less fallback still blocks while a pre-restart Pod exists,
// reading the persisted condition through the API reader.
func TestHarnessControllerRestartFallbackBlocksWhilePreRestartPodTerminating(t *testing.T) {
	restartAt := time.Now().Add(-time.Minute)
	instance := harnessWaveInstance("h5-instance", restartAt)

	testScheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(testScheme))
	require.NoError(t, workloadsv1alpha2.AddToScheme(testScheme))
	c := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(instance).Build()
	ctrl := &realControl{apiReader: c}

	oldTerminating := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "h5-instance-comp-0",
			UID:               "h5-old-uid",
			CreationTimestamp: metav1.NewTime(restartAt.Add(-time.Minute)),
			DeletionTimestamp: ptr.To(metav1.NewTime(time.Now())),
		},
	}
	assert.True(t, ctrl.isAlreadyRestarting(context.Background(), instance, []*corev1.Pod{oldTerminating}, nil),
		"H5: the persisted wave blocks while a pre-restart Pod (created before LastRestartTime) is still terminating")
}
