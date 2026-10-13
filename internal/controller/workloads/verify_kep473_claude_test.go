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

// Reviewer harness for PR #492 (KEP-473), round 2. Maps to finding F3 in
// docs/verification/kep473-topology/verify-manifest.json.
package workloads

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	constants "sigs.k8s.io/rbgs/api/workloads/constants"
	v1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/pkg/scheduler/common"
)

// TestVerifyF3AdmissionAllowedRoleRemovalFreezesLiveRBG is a CONTRACT test for
// finding F3 (round 2, head dab87262). It closes the full chain:
//
//  1. A live RBG combines a whole-group gang with a role-level topology constraint,
//     and its contract is recorded on first acceptance (annotation set, no error).
//  2. The operator removes a role that never carried a topology constraint —
//     ValidateRoleTopologyImmutability explicitly allows this.
//  3. The next resolve+ensure cycle must keep the RBG manageable. On the head it
//     instead reports TopologyPolicyLate, which gates role reconciliation.
//
// On the head this FAILS (red = reproduction) at step 3.
func TestVerifyF3AdmissionAllowedRoleRemovalFreezesLiveRBG(t *testing.T) {
	rbg := &v1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "rbg", Namespace: "default", UID: "rbg-uid"},
		Spec: v1alpha2.RoleBasedGroupSpec{Roles: []v1alpha2.RoleSpec{
			{Name: "prefill", Replicas: ptr.To(int32(2))},
			{Name: "decode", Replicas: ptr.To(int32(2)), InstanceTopologyConstraint: &v1alpha2.TopologyConstraint{
				Pack: &v1alpha2.TopologyPackConstraint{Required: ptr.To("rack")},
			}},
			{Name: "router", Replicas: ptr.To(int32(1))},
		}},
	}

	scheme := runtime.NewScheme()
	if err := v1alpha2.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(rbg).Build()
	r := &RoleBasedGroupReconciler{client: c}
	ctx := context.Background()

	// 1. First acceptance: whole-group gang + role-level topology on decode.
	gang := &common.GangStrategy{} // empty Roles = legacy whole-group gang
	before, err := common.ResolvePlacementPlan(ctx, c, rbg, gang)
	if err != nil {
		t.Fatal(err)
	}
	if !before.HasTopology() {
		t.Fatal("expected a topology-bearing plan")
	}
	if err := r.ensureTopologyContract(ctx, rbg, before); err != nil {
		t.Fatalf("first acceptance must record the contract, got %v", err)
	}
	if rbg.Annotations[constants.RBGTopologyContractAnnotation] == "" {
		t.Fatalf("expected the contract annotation to be recorded, got %#v", rbg.Annotations)
	}

	// 2. Operator removes the router role; admission allows it.
	rbgAfter := rbg.DeepCopy()
	rbgAfter.Spec.Roles = rbgAfter.Spec.Roles[:2]
	if err := v1alpha2.ValidateRoleTopologyImmutability(rbg, rbgAfter); err != nil {
		t.Fatalf("admission must allow removing a role without topology constraints: %v", err)
	}

	// 3. The post-removal reconcile must not freeze the RBG.
	after, err := common.ResolvePlacementPlan(ctx, c, rbgAfter, gang)
	if err != nil {
		t.Fatal(err)
	}
	err = r.ensureTopologyContract(ctx, rbgAfter, after)
	if common.IsTopologyTranslationError(err) {
		if reason, ok := common.TopologyTranslationReason(err); ok && reason == common.TopologyReasonTopologyPolicyLate {
			t.Errorf("F3 REPRODUCED: removing a non-topology role (allowed by admission) "+
				"freezes the live RBG: %v", err)
		}
	}
}
