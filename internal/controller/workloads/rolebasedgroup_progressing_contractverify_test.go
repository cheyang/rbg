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

package workloads

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"k8s.io/apimachinery/pkg/runtime"

	workloadconstants "sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// TestRBGAggregatesRISInvalidSpecAsConfigurationBlocker is a KEP-502 contract
// test (reviewer verification for PR #572, claim F2).
//
// Contract: a child RoleInstanceSet whose reconciliation is blocked by an
// invalid committed-ordinals spec reports Progressing=True(reason=InvalidSpec)
// on itself (written by validateCommittedMembership). The RBG aggregation must
// surface that fact to consumers as a configuration-family blocker — the
// KEP-502 decision table says "needs attention" is Progressing=True with a
// blocker-family reason, and the configuration family ranks FIRST in the
// documented precedence. Reporting it as an activity reason
// (RoleInstanceSetUpdated) tells consumers the workload is benignly converging.
//
// Polarity: contract test. FAILS on the code under review (reproduces the
// defect); PASSES once risReasonToRBG maps InvalidSpec (or the RBG computes the
// equivalent configuration blocker from its own facts).
func TestRBGAggregatesRISInvalidSpecAsConfigurationBlocker(t *testing.T) {
	rbg := newProgressingRBG()
	// One role blocked by an invalid committed-ordinals spec; the other healthy.
	roleStatuses := readyRoleStatuses()
	observations := map[string]risObservation{
		"prefill": observation(workloadconstants.ProgressingReasonConverged, corev1.ConditionFalse, "hash"),
		"decode":  observation(workloadconstants.ProgressingReasonInvalidSpec, corev1.ConditionTrue, "hash"),
	}
	cond := computeRBGProgressingCondition(rbg, roleStatuses, observations, expectedRevisions(), false, nil)

	if cond.Status != metav1.ConditionTrue {
		t.Fatalf("status = %q, want True (the group is blocked)", cond.Status)
	}
	if !isRBGBlockerFamily(cond.Reason) {
		t.Fatalf("reason = %q is not a blocker-family reason: a child RoleInstanceSet stuck on an invalid spec is a needs-attention blocker, not an activity. KEP-502 ranks the configuration family first; the consumer decision table treats a non-blocker True reason as \"still progressing, no attention needed\"", cond.Reason)
	}
}

// TestMixedBackendRBGCleansUpProgressing is a KEP-502 contract test (reviewer
// verification for PR #572, claim F3).
//
// Contract: "When an RBG no longer uses RIS for all roles, clean up
// controller-owned new-model output while preserving existing status and
// external conditions" (KEP-502 upgrade strategy). The controller-owned
// Progressing condition is new-model output: once the RBG becomes mixed-backend
// it is never recomputed, so a stale condition left in status misleads
// consumers indefinitely.
//
// Polarity: contract test. FAILS on the code under review; PASSES once the
// status path removes the controller-owned Progressing condition for
// mixed-backend RBGs.
func TestMixedBackendRBGCleansUpProgressing(t *testing.T) {
	ctx := context.Background()
	replicas := int32(2)
	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "mixed-rbg", Namespace: "default", Generation: 2},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{Roles: []workloadsv1alpha2.RoleSpec{
			// One role still on RoleInstanceSet...
			{Name: "prefill", Replicas: &replicas},
			// ...and one role moved to a legacy backend, making the RBG mixed.
			{Name: "decode", Replicas: &replicas, Annotations: map[string]string{
				workloadconstants.RoleWorkloadTypeAnnotationKey: workloadconstants.LeaderWorkerSetWorkloadType,
			}},
		}},
		Status: workloadsv1alpha2.RoleBasedGroupStatus{
			ObservedGeneration: 2,
			// Stale controller-owned new-model output from when all roles used RIS.
			Conditions: []metav1.Condition{{
				Type:               string(workloadsv1alpha2.RoleBasedGroupProgressing),
				Status:             metav1.ConditionFalse,
				Reason:             workloadconstants.ProgressingReasonConverged,
				Message:            "converged at generation 1",
				LastTransitionTime: metav1.Now(),
				ObservedGeneration: 1,
			}},
			RoleStatuses: []workloadsv1alpha2.RoleStatus{
				{Name: "prefill", Replicas: 2, ReadyReplicas: 2, UpdatedReplicas: 2},
				{Name: "decode", Replicas: 2, ReadyReplicas: 2, UpdatedReplicas: 2},
			},
		},
	}
	observed := []workloadsv1alpha2.RoleStatus{
		{Name: "prefill", Replicas: 2, ReadyReplicas: 2, UpdatedReplicas: 2},
		{Name: "decode", Replicas: 2, ReadyReplicas: 2, UpdatedReplicas: 2},
	}
	scheme := runtime.NewScheme()
	if err := workloadsv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(rbg).Build()
	r := &RoleBasedGroupReconciler{client: c, recorder: record.NewFakeRecorder(16)}
	if err := r.updateRBGStatus(ctx, rbg, observed, nil); err != nil {
		t.Fatal(err)
	}
	cond := findRBGProgressing(rbg.Status.Conditions)
	if cond != nil {
		t.Fatalf("stale controller-owned Progressing condition (reason=%q, observedGeneration=%d) survived the transition to a mixed-backend RBG: updateRBGStatus neither recomputes nor removes it, so consumers read a frozen verdict forever", cond.Reason, cond.ObservedGeneration)
	}
}

func findRBGProgressing(conditions []metav1.Condition) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == string(workloadsv1alpha2.RoleBasedGroupProgressing) {
			return &conditions[i]
		}
	}
	return nil
}

