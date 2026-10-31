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

// Verification harness for sgl-project/rbg PR #533 ("fix: update updatedReplicas status").
// ADDITIVE ONLY: this file is part of the review harness (branch
// verify/pr-533-updatedreplicas-status-codex), not of the PR under review.
//
// Polarity: all tests here are CONTRACT tests. They assert the intended correct
// behavior, so they FAIL on the base branch (7894133d, premise P0) and PASS on
// the PR head (4ab4a5ee).

package workloads

import (
	"context"
	"encoding/json"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// pr533Fixture builds an RBG whose stored status is fully settled (Ready condition
// True, matching observedGeneration) except that prefill's updatedReplicas is stale.
func pr533Fixture(staleUpdated int32) *workloadsv1alpha2.RoleBasedGroup {
	return &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "verify-rbg", Namespace: "default", Generation: 1},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{Roles: []workloadsv1alpha2.RoleSpec{
			{Name: "prefill", Replicas: ptr.To(int32(2))},
		}},
		Status: workloadsv1alpha2.RoleBasedGroupStatus{
			ObservedGeneration: 1,
			Conditions: []metav1.Condition{{
				Type:               string(workloadsv1alpha2.RoleBasedGroupReady),
				Status:             metav1.ConditionTrue,
				ObservedGeneration: 1,
				LastTransitionTime: metav1.Now(),
				Reason:             "AllRolesReady",
				Message:            "All roles are ready",
			}},
			RoleStatuses: []workloadsv1alpha2.RoleStatus{
				{Name: "prefill", Replicas: 2, ReadyReplicas: 2, UpdatedReplicas: staleUpdated},
			},
		},
	}
}

type pr533PatchRecorder struct {
	patches          int
	updatedReplicas  []int32 // updatedReplicas values seen in each SSA status payload
	sawExplicitField bool
}

func pr533Client(t *testing.T, rec *pr533PatchRecorder) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := workloadsv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
		SubResourcePatch: func(_ context.Context, _ client.Client, subresource string, obj client.Object, patch client.Patch, _ ...client.SubResourcePatchOption) error {
			if subresource != "status" {
				t.Fatalf("unexpected subresource patch: %s", subresource)
			}
			if patch.Type() != types.ApplyPatchType {
				t.Fatalf("expected SSA apply patch, got %s", patch.Type())
			}
			rec.patches++
			data, err := patch.Data(obj)
			if err != nil {
				return err
			}
			var payload struct {
				Status struct {
					RoleStatuses []struct {
						UpdatedReplicas *int32 `json:"updatedReplicas"`
					} `json:"roleStatuses"`
				} `json:"status"`
			}
			if err := json.Unmarshal(data, &payload); err != nil {
				t.Fatalf("unmarshal SSA payload: %v", err)
			}
			for _, rs := range payload.Status.RoleStatuses {
				if rs.UpdatedReplicas == nil {
					t.Error("SSA status payload omitted updatedReplicas (zero value would not clear a stale field)")
					continue
				}
				rec.sawExplicitField = true
				rec.updatedReplicas = append(rec.updatedReplicas, *rs.UpdatedReplicas)
			}
			return nil
		},
	}).Build()
}

// Contract: an UpdatedReplicas-only drift must be merged into rbg.Status.RoleStatuses
// and flushed with exactly one SSA status patch; a second no-change call must not patch.
//
// P0 premise probe: on base (7894133d) the stored status keeps the stale value and
// no patch is issued, so this test FAILS there.
func TestVerifyPR533UpdatedReplicasOnlyChangePropagates(t *testing.T) {
	ctx := context.Background()
	rbg := pr533Fixture(1) // stored updatedReplicas = 1 (stale)
	observed := []workloadsv1alpha2.RoleStatus{
		{Name: "prefill", Replicas: 2, ReadyReplicas: 2, UpdatedReplicas: 2},
	}
	rec := &pr533PatchRecorder{}
	r := &RoleBasedGroupReconciler{client: pr533Client(t, rec)}

	if err := r.updateRBGStatus(ctx, rbg, observed); err != nil {
		t.Fatal(err)
	}
	if got := rbg.Status.RoleStatuses[0].UpdatedReplicas; got != 2 {
		t.Errorf("in-memory updatedReplicas = %d, want 2 (stale value retained)", got)
	}
	if rec.patches != 1 {
		t.Fatalf("status patches after drift = %d, want 1", rec.patches)
	}
	if len(rec.updatedReplicas) != 1 || rec.updatedReplicas[0] != 2 {
		t.Errorf("SSA payload updatedReplicas = %v, want [2]", rec.updatedReplicas)
	}

	// Idempotence: same observation again must not produce another API write.
	if err := r.updateRBGStatus(ctx, rbg, observed); err != nil {
		t.Fatal(err)
	}
	if rec.patches != 1 {
		t.Errorf("status patches after stable second call = %d, want still 1", rec.patches)
	}
}

// Contract: an UpdatedReplicas RESET to zero (new revision / scale-to-zero) must be
// written explicitly. This is the case most likely to silently regress because
// json omitempty would drop the zero.
func TestVerifyPR533UpdatedReplicasResetToZeroPropagates(t *testing.T) {
	ctx := context.Background()
	rbg := pr533Fixture(2) // stored updatedReplicas = 2 (stale)
	observed := []workloadsv1alpha2.RoleStatus{
		{Name: "prefill", Replicas: 2, ReadyReplicas: 2, UpdatedReplicas: 0},
	}
	rec := &pr533PatchRecorder{}
	r := &RoleBasedGroupReconciler{client: pr533Client(t, rec)}

	if err := r.updateRBGStatus(ctx, rbg, observed); err != nil {
		t.Fatal(err)
	}
	if got := rbg.Status.RoleStatuses[0].UpdatedReplicas; got != 0 {
		t.Errorf("in-memory updatedReplicas = %d, want 0 (stale value retained)", got)
	}
	if rec.patches != 1 {
		t.Fatalf("status patches after reset = %d, want 1", rec.patches)
	}
	if len(rec.updatedReplicas) != 1 || rec.updatedReplicas[0] != 0 {
		t.Errorf("SSA payload updatedReplicas = %v, want explicit [0]", rec.updatedReplicas)
	}
}

// Contract (regression guard for pre-existing behavior): a ReadyReplicas change must
// still propagate and patch.
func TestVerifyPR533ReadyReplicasChangeStillPropagates(t *testing.T) {
	ctx := context.Background()
	rbg := pr533Fixture(2)
	observed := []workloadsv1alpha2.RoleStatus{
		{Name: "prefill", Replicas: 2, ReadyReplicas: 1, UpdatedReplicas: 2},
	}
	rec := &pr533PatchRecorder{}
	r := &RoleBasedGroupReconciler{client: pr533Client(t, rec)}

	if err := r.updateRBGStatus(ctx, rbg, observed); err != nil {
		t.Fatal(err)
	}
	if got := rbg.Status.RoleStatuses[0].ReadyReplicas; got != 1 {
		t.Errorf("in-memory readyReplicas = %d, want 1", got)
	}
	if rec.patches != 1 {
		t.Errorf("status patches = %d, want 1", rec.patches)
	}
}
