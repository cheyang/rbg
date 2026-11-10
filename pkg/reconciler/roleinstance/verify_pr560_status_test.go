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

// F1 verification for PR #560: the PR's own doc/test claim is that after the
// updateStatus rework "stale writes cannot roll back live budget/conditions".
// The budget fields are protected (copied from the live object), and Degraded
// is re-derived from live state — but the OTHER externally-owned conditions
// (RoleInstanceCustomReady, RoleInstanceInPlaceUpdateReady) are classified as
// controller-owned, so a stale informer snapshot still rolls back a newer live
// value written by the readiness/inplace controllers.
//
// This is the minimal production-level reproduction behind the PR's own
// failing CI test TestStaleStatusPreservesLiveRestartBudget (which encodes the
// same expectation at instance_status_test.go:268).
//
// Polarity: CONTRACT — asserts the PR's own stated invariant. FAILS on the
// reviewed head; must PASS once the merge logic is fixed (or the claim/test
// is corrected).

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func TestVerifyStaleStatusCannotRollbackLiveExternalCondition(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))

	// Live object: the readiness controller has already published
	// CustomReady=False/LiveExternal on the API server.
	live := &workloadsv1alpha2.RoleInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "verify-stale", Namespace: "default", UID: types.UID("verify-stale-uid"), Generation: 1},
		Spec: workloadsv1alpha2.RoleInstanceSpec{
			Components: []workloadsv1alpha2.RoleInstanceComponent{{Name: "worker"}},
		},
		Status: workloadsv1alpha2.RoleInstanceStatus{
			ObservedGeneration: 1,
			Conditions: []workloadsv1alpha2.RoleInstanceCondition{
				{Type: workloadsv1alpha2.RoleInstanceCustomReady, Status: v1.ConditionFalse, Reason: "LiveExternal", LastTransitionTime: metav1.Now()},
			},
		},
	}
	cli := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(live).WithObjects(live).Build()
	updater := &realStatusUpdater{Client: cli, apiReader: cli}

	// Stale informer snapshot (same UID/generation, older status): still shows
	// the previous CustomReady=True/StaleExternal.
	stale := live.DeepCopy()
	stale.Status.Conditions = []workloadsv1alpha2.RoleInstanceCondition{
		{Type: workloadsv1alpha2.RoleInstanceCustomReady, Status: v1.ConditionTrue, Reason: "StaleExternal", LastTransitionTime: metav1.Now()},
	}

	// newStatus computed from the STALE snapshot, as setInstanceConditions does:
	// externally-owned conditions are copied from the cached instance.
	newStatus := &workloadsv1alpha2.RoleInstanceStatus{
		ObservedGeneration: 1,
		Conditions: []workloadsv1alpha2.RoleInstanceCondition{
			{Type: workloadsv1alpha2.RoleInstanceReady, Status: v1.ConditionTrue},
			{Type: workloadsv1alpha2.RoleInstanceCustomReady, Status: v1.ConditionTrue, Reason: "StaleExternal"},
		},
	}

	require.NoError(t, updater.updateStatus(ctx, stale, newStatus))

	got := &workloadsv1alpha2.RoleInstance{}
	require.NoError(t, cli.Get(ctx, client.ObjectKeyFromObject(live), got))
	for _, cond := range got.Status.Conditions {
		if cond.Type == workloadsv1alpha2.RoleInstanceCustomReady {
			require.Equal(t, "LiveExternal", cond.Reason,
				"a stale status write must not roll back a live externally-owned condition")
			require.Equal(t, v1.ConditionFalse, cond.Status)
			return
		}
	}
	t.Fatal("CustomReady condition missing after status write")
}
