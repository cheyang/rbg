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

package roleinstance

// Verification harness for PR #536 (sgl-project/rbg#534 Task 2 premise + F1).
// Polarity: CONTRACT tests — they assert the intended behavior.
// P0 tests are expected RED on base 4a0765f7 and GREEN on the PR head.
// F1 is expected RED on the PR head (confirmed residual gap).

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	api "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// P0c: a declared custom readiness gate whose condition is not True must force
// the RoleInstance Ready condition to False.
func TestVerifyP0ReadyHonorsCustomGate(t *testing.T) {
	instance := newReadinessTestInstance()
	instance.Spec.ReadinessGates = append(instance.Spec.ReadinessGates,
		api.RoleInstanceReadinessGate{ConditionType: api.RoleInstanceCustomReady})
	instance.Status.Conditions = append(instance.Status.Conditions,
		api.RoleInstanceCondition{Type: api.RoleInstanceCustomReady, Status: corev1.ConditionFalse})
	ready := getInstanceReadyCondition(instance,
		&api.RoleInstanceCondition{Type: api.RoleInstanceAllPodsReady, Status: corev1.ConditionTrue},
		&api.RoleInstanceCondition{Type: api.RoleInstanceInPlaceUpdateReady, Status: corev1.ConditionTrue})
	require.Equal(t, corev1.ConditionFalse, ready.Status,
		"P0c: Ready must be False while a declared custom gate is not satisfied")
}

// P0d: a lifecycle-blocked instance must propagate NotReady to its component
// Pods through the InstancePodReady gate (here with ReadyPolicy=None so only
// the custom gate can drive the outcome).
func TestVerifyP0PodsHonorCustomGate(t *testing.T) {
	instance := newReadinessTestInstance()
	instance.Spec.ReadyPolicy = api.RoleInstanceReadyPolicyTypeNone
	instance.Spec.ReadinessGates = append(instance.Spec.ReadinessGates,
		api.RoleInstanceReadinessGate{ConditionType: api.RoleInstanceCustomReady})
	instance.Status.Conditions = append(instance.Status.Conditions,
		api.RoleInstanceCondition{Type: api.RoleInstanceCustomReady, Status: corev1.ConditionFalse})
	pod := newReadinessTestPod(instance, "router")
	pod.Spec.ReadinessGates = []corev1.PodReadinessGate{{ConditionType: constants.InstancePodReadyConditionType}}
	pod.Status.Conditions = append(pod.Status.Conditions,
		corev1.PodCondition{Type: constants.InstancePodReadyConditionType, Status: corev1.ConditionTrue})
	c := fake.NewClientBuilder().WithScheme(readinessTestScheme(t)).WithObjects(instance, pod).WithStatusSubresource(instance, pod).Build()
	updater := newStatusUpdater(c)
	require.NoError(t, updater.UpdateInstanceStatus(context.Background(), instance, &api.RoleInstanceStatus{}, []*corev1.Pod{pod}))
	got := &corev1.Pod{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(pod), got))
	for _, condition := range got.Status.Conditions {
		if condition.Type == constants.InstancePodReadyConditionType {
			require.Equal(t, corev1.ConditionFalse, condition.Status,
				"P0d: the Pod gate must be False while the custom gate blocks the instance")
			return
		}
	}
	t.Fatal("P0d: InstancePodReady condition missing on Pod")
}

// lifecycleBlock holds the live API state of a blocked RoleInstance.
func lifecycleBlock() (api.RoleInstanceReadinessGate, api.RoleInstanceCondition) {
	return api.RoleInstanceReadinessGate{ConditionType: api.RoleInstanceCustomReady},
		api.RoleInstanceCondition{Type: api.RoleInstanceCustomReady, Status: corev1.ConditionFalse,
			Message: `[{"userAgent":"Lifecycle","key":"preDeleteHook"}]`}
}

func blockedPod(instance *api.RoleInstance) *corev1.Pod {
	pod := newReadinessTestPod(instance, "router")
	pod.Spec.ReadinessGates = []corev1.PodReadinessGate{{ConditionType: constants.InstancePodReadyConditionType}}
	pod.Status.Conditions = append(pod.Status.Conditions,
		corev1.PodCondition{Type: constants.InstancePodReadyConditionType, Status: corev1.ConditionFalse,
			Message: `[{"userAgent":"Lifecycle","key":"InstanceReady"}]`})
	return pod
}

// F1: the stale-calculation guard lives only in updateStatus, which runs only
// when the RoleInstance status itself needs a write. When the informer snapshot
// is stale (pre-block) but no status write is needed, updatePodsLifeCycle runs
// unguarded and CLEARS the Pod block even though the live object is blocked.
// This test simulates that interleaving: stale snapshot (no gate) + live object
// blocked + Pod carrying the Lifecycle/InstanceReady NotReady key.
// Contract: the Pod block must survive a reconcile calculated from a stale snapshot.
func TestVerifyF1StaleSnapshotClearsPodBlock(t *testing.T) {
	// Live object in the API: gate declared and condition False (blocked).
	gate, condition := lifecycleBlock()
	live := newReadinessTestInstance()
	live.Spec.ReadyPolicy = api.RoleInstanceReadyPolicyTypeNone
	live.Spec.ReadinessGates = append(live.Spec.ReadinessGates, gate)
	live.Status.Conditions = append(live.Status.Conditions, condition)

	// Stale informer snapshot: pre-block (no custom gate, no custom condition).
	stale := newReadinessTestInstance()
	stale.Spec.ReadyPolicy = api.RoleInstanceReadyPolicyTypeNone
	pod := blockedPod(stale)
	pods := []*corev1.Pod{pod}

	c := fake.NewClientBuilder().WithScheme(readinessTestScheme(t)).WithObjects(live, pod).WithStatusSubresource(live, pod).Build()
	updater := newStatusUpdater(c).(*realStatusUpdater)
	// Derive a self-consistent status for the stale snapshot so that
	// inconsistentStatus()==false and updateStatus (and its guard) is skipped.
	baseline := &api.RoleInstanceStatus{}
	updater.calculateStatus(stale, baseline, nil, pods)
	stale.Status = *baseline.DeepCopy()

	require.NoError(t, updater.UpdateInstanceStatus(context.Background(), stale, &api.RoleInstanceStatus{}, pods))

	got := &corev1.Pod{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(pod), got))
	for _, cnd := range got.Status.Conditions {
		if cnd.Type == constants.InstancePodReadyConditionType {
			require.Equal(t, corev1.ConditionFalse, cnd.Status,
				"F1: a reconcile from a stale snapshot must not clear the Pod block while the live instance is blocked")
			require.Contains(t, cnd.Message, "Lifecycle")
			return
		}
	}
	t.Fatal("F1: InstancePodReady condition missing on Pod")
}

// F1-control: when the reconcile DOES need a status write, the stale guard in
// updateStatus rejects the calculation and updatePodsLifeCycle is skipped, so
// the Pod block survives. This pins the boundary of the guard.
func TestVerifyF1StaleSnapshotWithStatusWriteIsRejected(t *testing.T) {
	gate, condition := lifecycleBlock()
	live := newReadinessTestInstance()
	live.Spec.ReadyPolicy = api.RoleInstanceReadyPolicyTypeNone
	live.Spec.ReadinessGates = append(live.Spec.ReadinessGates, gate)
	live.Status.Conditions = append(live.Status.Conditions, condition)

	stale := newReadinessTestInstance()
	stale.Spec.ReadyPolicy = api.RoleInstanceReadyPolicyTypeNone
	pod := blockedPod(stale)

	c := fake.NewClientBuilder().WithScheme(readinessTestScheme(t)).WithObjects(live, pod).WithStatusSubresource(live, pod).Build()
	updater := newStatusUpdater(c)
	// Force inconsistentStatus()==true via ObservedGeneration so updateStatus runs.
	err := updater.UpdateInstanceStatus(context.Background(), stale, &api.RoleInstanceStatus{ObservedGeneration: 1}, []*corev1.Pod{pod})
	require.Error(t, err, "F1-control: the stale calculation must be rejected when a status write is attempted")

	got := &corev1.Pod{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(pod), got))
	for _, cnd := range got.Status.Conditions {
		if cnd.Type == constants.InstancePodReadyConditionType {
			require.Equal(t, corev1.ConditionFalse, cnd.Status,
				"F1-control: pod block preserved when the status write is rejected")
			return
		}
	}
	t.Fatal("F1-control: InstancePodReady condition missing on Pod")
}
