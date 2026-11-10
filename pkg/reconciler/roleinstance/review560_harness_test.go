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

// Reviewer harness for PR #560 (maxGroupRestarts) — findings F1/F3.
// Independent of the PR's own test fixtures so it survives PR test edits.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	synccontrol "sigs.k8s.io/rbgs/pkg/reconciler/roleinstance/sync"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func r560StatusScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, v1.AddToScheme(scheme))
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))
	return scheme
}

func r560ExhaustedInstance() *workloadsv1alpha2.RoleInstance {
	return &workloadsv1alpha2.RoleInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "r560-stale", Namespace: "default", UID: "r560-stale-uid", Generation: 1},
		Spec: workloadsv1alpha2.RoleInstanceSpec{
			RestartPolicyConfig: &workloadsv1alpha2.RestartPolicyConfig{
				Type:             workloadsv1alpha2.RecreateRoleInstanceOnPodRestart,
				MaxGroupRestarts: ptr.To[int32](2),
			},
			Components: []workloadsv1alpha2.RoleInstanceComponent{{Name: "worker", Size: ptr.To[int32](1)}},
		},
		Status: workloadsv1alpha2.RoleInstanceStatus{
			ObservedGeneration:   1,
			GroupRestartCount:    2,
			GroupRestartRevision: "template-hash",
			RestartBudgetExhausted: true,
			Conditions: []workloadsv1alpha2.RoleInstanceCondition{
				{Type: workloadsv1alpha2.RoleInstanceCustomReady, Status: v1.ConditionFalse, Reason: "LiveExternal"},
			},
		},
	}
}

// TestR560F1StaleWriteMustNotClobberLiveExternalCondition is a CONTRACT test
// for finding F1 (it reproduces the PR's own failing
// TestStaleStatusPreservesLiveRestartBudget, independently): a stale status
// write may not roll back the LIVE externally-written condition
// (RoleInstanceCustomReady) of an exhausted instance. The PR body claims
// "stale writes cannot roll back live budget/conditions"; at the PR head this
// contract is violated for external conditions — the stale write wins.
func TestR560F1StaleWriteMustNotClobberLiveExternalCondition(t *testing.T) {
	ctx := context.Background()
	instance := r560ExhaustedInstance()
	synccontrol.EnforceRestartBudgetConditions(instance)
	c := fake.NewClientBuilder().WithScheme(r560StatusScheme(t)).WithStatusSubresource(instance).WithObjects(instance).Build()
	updater := &realStatusUpdater{Client: c, apiReader: c}

	// A STALE in-memory view (informer lag) that still believes the external
	// condition was True, plus a computed status carrying that stale value.
	stale := instance.DeepCopy()
	staleView := &workloadsv1alpha2.RoleInstanceStatus{ObservedGeneration: 1, Conditions: []workloadsv1alpha2.RoleInstanceCondition{
		{Type: workloadsv1alpha2.RoleInstanceReady, Status: v1.ConditionTrue},
		{Type: workloadsv1alpha2.RoleInstanceCustomReady, Status: v1.ConditionTrue, Reason: "StaleExternal"},
	}}
	require.NoError(t, updater.updateStatus(ctx, stale, staleView))

	live := &workloadsv1alpha2.RoleInstance{}
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(instance), live))
	byType := map[workloadsv1alpha2.RoleInstanceConditionType]workloadsv1alpha2.RoleInstanceCondition{}
	for _, cond := range live.Status.Conditions {
		require.NotContains(t, byType, cond.Type, "duplicate condition type: %s", cond.Type)
		byType[cond.Type] = cond
	}
	// The budget state itself is preserved (this part of the PR works):
	require.True(t, live.Status.RestartBudgetExhausted)
	require.EqualValues(t, 2, live.Status.GroupRestartCount)
	require.Equal(t, v1.ConditionFalse, byType[workloadsv1alpha2.RoleInstanceReady].Status)
	require.Equal(t, synccontrol.GroupRestartBudgetExceeded, byType[workloadsv1alpha2.RoleInstanceDegraded].Reason)
	// F1: the live external write must win over the stale view:
	require.Equal(t, "LiveExternal", byType[workloadsv1alpha2.RoleInstanceCustomReady].Reason,
		"F1: a stale status write rolled back the live externally-written CustomReady condition")
}

// TestR560F3StatusWriteSilentlyDroppedWhenGenerationAdvances is a BUG-CANARY
// for finding F3: when the live RoleInstance's generation advances between the
// informer read and the status write (a concurrent spec edit), updateStatus
// silently drops the ENTIRE status write — including restart-tracking
// increments (RestartCount/LastRestartTime) and the Restarting condition that
// guard non-budgeted recreate policies against duplicate group recreations.
// At the PR head this test PASSES (the drop happens). If the guard is narrowed
// so restart tracking survives, this canary must be inverted.
func TestR560F3StatusWriteSilentlyDroppedWhenGenerationAdvances(t *testing.T) {
	ctx := context.Background()
	instance := r560ExhaustedInstance()
	instance.Spec.RestartPolicyConfig = nil // plain non-budgeted recreate instance
	instance.Spec.RestartPolicy = workloadsv1alpha2.RecreateRoleInstanceOnPodRestart
	instance.Status = workloadsv1alpha2.RoleInstanceStatus{ObservedGeneration: 1, RestartCount: 3}
	c := fake.NewClientBuilder().WithScheme(r560StatusScheme(t)).WithStatusSubresource(instance).WithObjects(instance).Build()
	updater := &realStatusUpdater{Client: c, apiReader: c}

	// Simulate a concurrent spec edit: the live object's generation advances
	// while the reconciler still holds the old in-memory instance.
	liveBumped := instance.DeepCopy()
	liveBumped.Generation = 2
	require.NoError(t, c.Update(ctx, liveBumped))

	// The reconciler accepted a restart this cycle: restart tracking bumped and
	// the Restarting condition set, as syncInstance would propagate.
	newStatus := &workloadsv1alpha2.RoleInstanceStatus{
		ObservedGeneration: 1,
		RestartCount:       4,
		LastRestartTime:    &metav1.Time{Time: metav1.Now().Time},
		Conditions: []workloadsv1alpha2.RoleInstanceCondition{
			{Type: workloadsv1alpha2.RoleInstanceRestarting, Status: v1.ConditionTrue, Reason: "RestartPolicyTriggered"},
		},
	}
	require.NoError(t, updater.updateStatus(ctx, instance, newStatus))

	after := &workloadsv1alpha2.RoleInstance{}
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(instance), after))
	require.Equal(t, int32(3), after.Status.RestartCount,
		"F3 canary: at head the whole status write is dropped, so RestartCount is not persisted")
	foundRestarting := false
	for _, cond := range after.Status.Conditions {
		if cond.Type == workloadsv1alpha2.RoleInstanceRestarting && cond.Status == v1.ConditionTrue {
			foundRestarting = true
		}
	}
	require.False(t, foundRestarting,
		"F3 canary: at head the Restarting condition write is dropped together with the rest of the status")
}
