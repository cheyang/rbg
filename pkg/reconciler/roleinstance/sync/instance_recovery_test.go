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

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func testControl() *realControl {
	return &realControl{recorder: record.NewFakeRecorder(100)}
}

// readyRecreateInstance builds a stable, previously-Ready RoleInstance using the
// RecreateRoleInstanceOnPodRestart policy, ready to enter recovery.
func readyRecreateInstance(name string, cfg *workloadsv1alpha2.RestartPolicyConfig) *workloadsv1alpha2.RoleInstance {
	return &workloadsv1alpha2.RoleInstance{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", Generation: 1},
		Spec: workloadsv1alpha2.RoleInstanceSpec{
			RestartPolicy:       workloadsv1alpha2.RecreateRoleInstanceOnPodRestart,
			RestartPolicyConfig: cfg,
			Components:          []workloadsv1alpha2.RoleInstanceComponent{{Name: "worker", Size: ptr.To[int32](2)}},
		},
		Status: workloadsv1alpha2.RoleInstanceStatus{
			ObservedGeneration: 1,
			CurrentRevision:    "rev-1",
			UpdateRevision:     "rev-1",
			Conditions: []workloadsv1alpha2.RoleInstanceCondition{
				{Type: workloadsv1alpha2.RoleInstanceReady, Status: corev1.ConditionTrue},
			},
		},
	}
}

func crashedPod(name string, restarts int32) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       corev1.PodSpec{NodeName: "node-a"},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: "main", RestartCount: restarts, LastTerminationState: corev1.ContainerState{
					Terminated: &corev1.ContainerStateTerminated{ExitCode: 1, Reason: "Error", FinishedAt: metav1.Now()},
				}},
			},
		},
	}
}

func healthyPod(name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       corev1.PodSpec{NodeName: "node-a"},
		Status: corev1.PodStatus{
			Phase:             corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{Name: "main", RestartCount: 0}},
		},
	}
}

func getCond(inst *workloadsv1alpha2.RoleInstance, t workloadsv1alpha2.RoleInstanceConditionType) *workloadsv1alpha2.RoleInstanceCondition {
	for i := range inst.Status.Conditions {
		if inst.Status.Conditions[i].Type == t {
			return &inst.Status.Conditions[i]
		}
	}
	return nil
}

func TestRecovery_EnterHold_PreservesPodAndRequeues(t *testing.T) {
	inst := readyRecreateInstance("enter-hold", nil) // default delay 15s
	defer restartingCache.Delete(instanceKey(inst))
	pods := []*corev1.Pod{crashedPod("enter-hold-worker-0", 1), healthyPod("enter-hold-worker-1")}

	diff := testControl().reconcileRestartPolicy(inst, pods, nil)

	assert.NotNil(t, diff)
	assert.Equal(t, 0, diff.toDeleteNum, "crashed pod must be preserved during the diagnosis window")
	assert.Greater(t, diff.requeueAfter, time.Duration(0), "should requeue to rebuild after the hold")
	assert.LessOrEqual(t, diff.requeueAfter, 15*time.Second)
	assert.Equal(t, int32(0), inst.Status.ConsecutiveRestarts, "no rebuild executed yet")

	cond := getCond(inst, workloadsv1alpha2.RoleInstanceRestarting)
	assert.NotNil(t, cond)
	assert.Equal(t, corev1.ConditionTrue, cond.Status)

	assert.NotNil(t, inst.Status.LastCrashInfo)
	assert.Len(t, inst.Status.LastCrashInfo.Pods, 1)
	assert.True(t, inst.Status.LastCrashInfo.Pods[0].LikelyRootCause)
	assert.Equal(t, "enter-hold-worker-0", inst.Status.LastCrashInfo.Pods[0].PodName)
	assert.Equal(t, int32(1), inst.Status.LastCrashInfo.Pods[0].ExitCode)
}

func TestRecovery_HoldNotElapsed_DoesNotRebuild(t *testing.T) {
	inst := readyRecreateInstance("hold-wait", &workloadsv1alpha2.RestartPolicyConfig{RebuildDelaySeconds: ptr.To[int32](600)})
	// Already restarting, detected 5s ago, delay 600s → still holding.
	inst.Status.Conditions = append(inst.Status.Conditions, workloadsv1alpha2.RoleInstanceCondition{
		Type: workloadsv1alpha2.RoleInstanceRestarting, Status: corev1.ConditionTrue,
		LastTransitionTime: metav1.NewTime(time.Now().Add(-5 * time.Second)),
	})
	pods := []*corev1.Pod{crashedPod("hold-wait-worker-0", 1)}

	diff := testControl().reconcileRestartPolicy(inst, pods, nil)

	assert.NotNil(t, diff)
	assert.Equal(t, 0, diff.toDeleteNum)
	assert.Greater(t, diff.requeueAfter, time.Duration(0))
	assert.Equal(t, int32(0), inst.Status.ConsecutiveRestarts)
}

func TestRecovery_HoldElapsed_ExecutesRebuild(t *testing.T) {
	inst := readyRecreateInstance("hold-done", &workloadsv1alpha2.RestartPolicyConfig{RebuildDelaySeconds: ptr.To[int32](15)})
	// Detected 20s ago, delay 15s, not rebuilt yet → execute rebuild.
	inst.Status.Conditions = append(inst.Status.Conditions, workloadsv1alpha2.RoleInstanceCondition{
		Type: workloadsv1alpha2.RoleInstanceRestarting, Status: corev1.ConditionTrue,
		LastTransitionTime: metav1.NewTime(time.Now().Add(-20 * time.Second)),
	})
	pods := []*corev1.Pod{crashedPod("hold-done-worker-0", 1), healthyPod("hold-done-worker-1")}

	diff := testControl().reconcileRestartPolicy(inst, pods, nil)

	assert.NotNil(t, diff)
	assert.Equal(t, 2, diff.toDeleteNum, "whole group is rebuilt")
	assert.Len(t, diff.toDeletePod, 2)
	assert.Equal(t, int32(1), inst.Status.ConsecutiveRestarts)
	assert.NotNil(t, inst.Status.LastRestartTime)
}

func TestRecovery_LoopBreaker_Trips(t *testing.T) {
	inst := readyRecreateInstance("give-up", &workloadsv1alpha2.RestartPolicyConfig{MaxConsecutiveRebuilds: ptr.To[int32](3)})
	inst.Status.ConsecutiveRestarts = 3 // reached the limit
	defer restartingCache.Delete(instanceKey(inst))
	pods := []*corev1.Pod{crashedPod("give-up-worker-0", 5)}

	diff := testControl().reconcileRestartPolicy(inst, pods, nil)

	assert.NotNil(t, diff)
	assert.Equal(t, 0, diff.toDeleteNum, "loop breaker stops rebuilding")
	assert.Equal(t, time.Duration(0), diff.requeueAfter)

	exhausted := getCond(inst, workloadsv1alpha2.RoleInstanceRestartBackoffExhausted)
	assert.NotNil(t, exhausted)
	assert.Equal(t, corev1.ConditionTrue, exhausted.Status)
}

func TestRecovery_LoopBreaker_Unlimited(t *testing.T) {
	inst := readyRecreateInstance("unlimited", &workloadsv1alpha2.RestartPolicyConfig{MaxConsecutiveRebuilds: ptr.To[int32](0)})
	inst.Status.ConsecutiveRestarts = 999
	defer restartingCache.Delete(instanceKey(inst))
	pods := []*corev1.Pod{crashedPod("unlimited-worker-0", 5)}

	diff := testControl().reconcileRestartPolicy(inst, pods, nil)

	// Should enter hold (not trip), because max=0 means unlimited.
	assert.NotNil(t, diff)
	assert.Nil(t, getCond(inst, workloadsv1alpha2.RoleInstanceRestartBackoffExhausted))
	assert.NotNil(t, getCond(inst, workloadsv1alpha2.RoleInstanceRestarting))
}

func TestRecovery_BackoffExhausted_ShortCircuitsUntilSpecChange(t *testing.T) {
	inst := readyRecreateInstance("stuck", nil)
	inst.Status.ConsecutiveRestarts = 10
	inst.Status.Conditions = append(inst.Status.Conditions, workloadsv1alpha2.RoleInstanceCondition{
		Type: workloadsv1alpha2.RoleInstanceRestartBackoffExhausted, Status: corev1.ConditionTrue,
	})
	pods := []*corev1.Pod{crashedPod("stuck-worker-0", 5)}

	// Same generation → do nothing.
	diff := testControl().reconcileRestartPolicy(inst, pods, nil)
	assert.NotNil(t, diff)
	assert.Equal(t, 0, diff.toDeleteNum)

	// Operator changes the spec → resume (clears state, falls through).
	inst.Generation = 2 // Status.ObservedGeneration stays 1
	diff = testControl().reconcileRestartPolicy(inst, pods, nil)
	assert.Nil(t, diff, "should fall through to normal reconciliation after a spec change")
	assert.Equal(t, int32(0), inst.Status.ConsecutiveRestarts)
	assert.Equal(t, corev1.ConditionFalse, getCond(inst, workloadsv1alpha2.RoleInstanceRestartBackoffExhausted).Status)
}

func TestRecovery_ResetAfterSustainedReady(t *testing.T) {
	inst := readyRecreateInstance("reset", &workloadsv1alpha2.RestartPolicyConfig{RebuildDelaySeconds: ptr.To[int32](15)})
	inst.Status.ConsecutiveRestarts = 4
	old := metav1.NewTime(time.Now().Add(-5 * time.Minute)) // well past the stability window
	inst.Status.LastRestartTime = &old
	pods := []*corev1.Pod{healthyPod("reset-worker-0"), healthyPod("reset-worker-1")}

	diff := testControl().reconcileRestartPolicy(inst, pods, nil)

	assert.Nil(t, diff)
	assert.Equal(t, int32(0), inst.Status.ConsecutiveRestarts, "counter resets after sustained Ready")
}

func TestRecovery_NoResetWithinStabilityWindow(t *testing.T) {
	inst := readyRecreateInstance("no-reset", &workloadsv1alpha2.RestartPolicyConfig{RebuildDelaySeconds: ptr.To[int32](15)})
	inst.Status.ConsecutiveRestarts = 4
	recent := metav1.NewTime(time.Now().Add(-3 * time.Second)) // within the 60s window
	inst.Status.LastRestartTime = &recent
	pods := []*corev1.Pod{healthyPod("no-reset-worker-0")}

	diff := testControl().reconcileRestartPolicy(inst, pods, nil)

	assert.Nil(t, diff)
	assert.Equal(t, int32(4), inst.Status.ConsecutiveRestarts, "counter must not reset inside the stability window")
}

func TestRecovery_PostRebuildWait_NoCrashersFallsThrough(t *testing.T) {
	inst := readyRecreateInstance("post-rebuild", &workloadsv1alpha2.RestartPolicyConfig{RebuildDelaySeconds: ptr.To[int32](15)})
	// Restarting True, rebuild already executed this cycle (LastRestartTime after detection).
	det := time.Now().Add(-30 * time.Second)
	inst.Status.Conditions = append(inst.Status.Conditions, workloadsv1alpha2.RoleInstanceCondition{
		Type: workloadsv1alpha2.RoleInstanceRestarting, Status: corev1.ConditionTrue,
		LastTransitionTime: metav1.NewTime(det),
	})
	rebuilt := metav1.NewTime(det.Add(2 * time.Second))
	inst.Status.LastRestartTime = &rebuilt
	pods := []*corev1.Pod{healthyPod("post-rebuild-worker-0")} // fresh pods recovering

	diff := testControl().reconcileRestartPolicy(inst, pods, nil)

	assert.Nil(t, diff, "post-rebuild with no crashers should fall through to normal scaling")
}

func TestRecovery_NonRecreatePolicy_NoOp(t *testing.T) {
	inst := readyRecreateInstance("none", nil)
	inst.Spec.RestartPolicy = workloadsv1alpha2.RestartPolicyNone
	pods := []*corev1.Pod{crashedPod("none-worker-0", 3)}

	diff := testControl().reconcileRestartPolicy(inst, pods, nil)
	assert.Nil(t, diff)
}

func TestBuildCrashInfo_OrdersByFinishTime(t *testing.T) {
	early := metav1.NewTime(time.Now().Add(-2 * time.Minute))
	late := metav1.NewTime(time.Now().Add(-1 * time.Minute))
	podLate := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "late"},
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{
			{Name: "main", RestartCount: 1, LastTerminationState: corev1.ContainerState{
				Terminated: &corev1.ContainerStateTerminated{ExitCode: 2, Reason: "Error", FinishedAt: late}}}}},
	}
	podEarly := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "early"},
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{
			{Name: "main", RestartCount: 1, LastTerminationState: corev1.ContainerState{
				Terminated: &corev1.ContainerStateTerminated{ExitCode: 1, Reason: "OOMKilled", FinishedAt: early}}}}},
	}

	info := buildCrashInfo([]*corev1.Pod{podLate, podEarly})
	assert.Len(t, info.Pods, 2)
	assert.Equal(t, "early", info.Pods[0].PodName, "earliest crasher first")
	assert.True(t, info.Pods[0].LikelyRootCause)
	assert.False(t, info.Pods[1].LikelyRootCause)
}
