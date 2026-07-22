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

// Package pr407 is an ADDITIVE verification harness for
// https://github.com/sgl-project/rbg/pull/407 ("test(e2e): stabilize flaky
// v1alpha2 specs on slow CI clusters").
//
// PR #407 touches only e2e test files; it changes no production code. There is
// no bug to disprove — these tests instead PROVE the two design claims the PR's
// review conclusion rests on, against real production code:
//
//	V1 (scaler math, PRIMARY) — backs coordinated_policy.go MaxSkew 50%->33%.
//	    With two roles scaling 1->3 under OrderReady progression, the real
//	    CoordinationScaler advances one replica per batch (target 1->2->3) at
//	    maxSkew=33%, but jumps 1->3 in a single batch at maxSkew=50%. The single
//	    batch jump is exactly what let the ready-replicas skew reach 2 and made
//	    the e2e assertion (skew <= 1) flaky; 33% bounds it to 1 by design.
//
//	V2 (retry-safety) — backs scaling_adapter.go updateRbgSpecV2Retry. Re-Get'ing
//	    into the reused object inside the Eventually retry loop resets the Roles
//	    slice, so re-applying the append mutation after a 409 Conflict does NOT
//	    double-append. Proven through the real controller-runtime client Get path
//	    (fake client), with a control that shows the broken pattern DOES double.
//
// Both are contract-style proofs: they must PASS at the PR head. They also guard
// the underlying contracts against future regression. Production code untouched.
package pr407

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	cs "sigs.k8s.io/rbgs/pkg/coordination/coordinationscaling"
)

// targetsFor runs the real CoordinationScaler for a two-role coordination under
// OrderReady progression and returns the per-role target replicas for one batch.
func targetsFor(t *testing.T, maxSkew string, states map[string]cs.RoleScalingState) map[string]int32 {
	t.Helper()
	roles := make([]string, 0, len(states))
	for r := range states {
		roles = append(roles, r)
	}
	rule := &workloadsv1alpha2.CoordinatedPolicyRule{
		Roles: roles,
		Strategy: workloadsv1alpha2.CoordinatedPolicyStrategy{
			Scaling: &workloadsv1alpha2.ScalingCoordinationStrategy{
				MaxSkew:     ptr.To(intstr.FromString(maxSkew)),
				Progression: workloadsv1alpha2.OrderReadyProgression,
			},
		},
	}
	scaler, err := cs.NewCoordinationScalerFromPolicy(rule)
	if err != nil {
		t.Fatalf("NewCoordinationScalerFromPolicy(%s): %v", maxSkew, err)
	}
	got, err := scaler.CalculateTargetReplicas(states)
	if err != nil {
		t.Fatalf("CalculateTargetReplicas(%s): %v", maxSkew, err)
	}
	return got
}

func twoRoles(current, ready int32) map[string]cs.RoleScalingState {
	mk := func(name string) cs.RoleScalingState {
		return cs.RoleScalingState{
			RoleName:          name,
			DesiredReplicas:   3,
			CurrentReplicas:   current,
			ScheduledReplicas: current,
			ReadyReplicas:     ready,
		}
	}
	return map[string]cs.RoleScalingState{"role-a": mk("role-a"), "role-b": mk("role-b")}
}

// V1 (PRIMARY, contract): maxSkew bounds the per-batch step. This is the exact
// scenario in coordinated_policy.go's "scaling coordination with OrderReady
// progression constrains scaling skew" spec: both roles 1 -> 3.
func TestPR407_MaxSkewBoundsBatchStep(t *testing.T) {
	// --- Batch 1: both roles at current=1, ready=1 (gate open), desired=3 ---
	// 50% => maxAllowedProgress = 1/3 + 0.50 = 0.833; ceil(0.833*3) = 3.
	// The scaler jumps 1 -> 3 in ONE batch, so one role can reach ready=3 while
	// the other is still ready=1 => ready-skew 2 => the e2e "skew <= 1" check
	// fails nondeterministically. This is the flakiness the PR removes.
	if got := targetsFor(t, "50%", twoRoles(1, 1)); got["role-a"] != 3 || got["role-b"] != 3 {
		t.Fatalf("maxSkew=50%% batch-1: got %v, want role-a=3 role-b=3 (single-batch jump 1->3)", got)
	}

	// 33% => maxAllowedProgress = 1/3 + 0.33 = 0.663; ceil(0.663*3) = 2.
	// The scaler advances only 1 -> 2 this batch.
	if got := targetsFor(t, "33%", twoRoles(1, 1)); got["role-a"] != 2 || got["role-b"] != 2 {
		t.Fatalf("maxSkew=33%% batch-1: got %v, want role-a=2 role-b=2 (step 1->2)", got)
	}

	// --- Batch 2 (33%): both at current=2, ready=2 (OrderReady gate open) ---
	// maxAllowedProgress = 2/3 + 0.33 = 0.997; ceil(0.997*3) = 3. Advances 2 -> 3.
	if got := targetsFor(t, "33%", twoRoles(2, 2)); got["role-a"] != 3 || got["role-b"] != 3 {
		t.Fatalf("maxSkew=33%% batch-2: got %v, want role-a=3 role-b=3 (step 2->3)", got)
	}
}

// V1 supporting proof: OrderReady actually gates the next batch. With maxSkew=33%
// both roles at current=2 but only ready=1, the scaler must NOT advance to 3
// (it holds current), so a lagging role can never let its partner overshoot the
// skew bound. Confirms the "combined with OrderReady gating" half of the comment.
func TestPR407_OrderReadyGatesNextBatch(t *testing.T) {
	got := targetsFor(t, "33%", twoRoles(2, 1)) // current=2, ready=1 -> gate closed
	if got["role-a"] != 2 || got["role-b"] != 2 {
		t.Fatalf("OrderReady gate: got %v, want role-a=2 role-b=2 (held until ready catches up)", got)
	}
}

// V2 (contract): updateRbgSpecV2Retry is retry-safe. Re-Get'ing into the reused
// object resets Spec.Roles, so re-applying the append after a simulated 409
// Conflict yields 3 roles, not 4. Exercised through the real controller-runtime
// client.Get path (the same call the e2e helper makes), plus a control showing
// the broken pattern (mutate twice without re-Get) DOES double-append.
func TestPR407_UpdateRetryReGetResetsSlice_NoDoubleAppend(t *testing.T) {
	scheme := runtime.NewScheme()
	utilruntime.Must(workloadsv1alpha2.AddToScheme(scheme))

	server := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "rbg", Namespace: "default"},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{
			Roles: []workloadsv1alpha2.RoleSpec{{Name: "role-0"}, {Name: "role-1"}},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(server).Build()
	ctx := context.Background()
	key := client.ObjectKeyFromObject(server)

	mutate := func(r *workloadsv1alpha2.RoleBasedGroup) {
		r.Spec.Roles = append(r.Spec.Roles, workloadsv1alpha2.RoleSpec{Name: "role-2"})
	}

	// The real helper declares newRbg ONCE and re-Get's into it each Eventually
	// iteration. Simulate two iterations: iteration 1 = conflict (Update fails),
	// iteration 2 = retry that succeeds.
	newRbg := &workloadsv1alpha2.RoleBasedGroup{}
	for i := range 2 {
		if err := cl.Get(ctx, key, newRbg); err != nil {
			t.Fatalf("Get iteration %d: %v", i, err)
		}
		mutate(newRbg)
	}
	if got := len(newRbg.Spec.Roles); got != 3 {
		t.Fatalf("retry-safe: after re-Get+mutate x2, got %d roles, want 3 (no double-append)", got)
	}
	if newRbg.Spec.Roles[2].Name != "role-2" || newRbg.Spec.Roles[0].Name != "role-0" {
		t.Fatalf("retry-safe: unexpected roles %v", newRbg.Spec.Roles)
	}

	// Harness-bites control: the SAME append applied twice WITHOUT a re-Get in
	// between DOES double (len=4). This proves the assertion above is meaningful —
	// it is the re-Get, not luck, that keeps the mutation idempotent.
	bad := &workloadsv1alpha2.RoleBasedGroup{}
	if err := cl.Get(ctx, key, bad); err != nil {
		t.Fatalf("control Get: %v", err)
	}
	mutate(bad)
	mutate(bad)
	if got := len(bad.Spec.Roles); got != 4 {
		t.Fatalf("control expectation broken: mutate x2 without re-Get gave %d roles, want 4", got)
	}
}
