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
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

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
	cond := computeRBGProgressingCondition(rbg, roleStatuses, observations, expectedRevisions(), false, nil, nil)

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
	if err := r.updateRBGStatus(ctx, rbg, observed, nil, nil); err != nil {
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

// TestRBGBlockerFamilySurvivesRecomputeAndUnclassifiedRetry is a KEP-502 contract
// test (reviewer verification for PR #572 round 2, ledger F3).
//
// Contract (KEP-502 line 178): a confirmed current-generation blocker family
// survives unclassifiable follow-up errors until a fresh successful observation
// of that operation clears it. Round 1 reproduced the opposite: updateRBGStatus
// (step 4) recomputed the condition from child aggregation and erased the
// persisted WaitingForQuota family, so an unclassifiable step-8 role error was
// reported as UnclassifiedBlocker. Round 2 introduces the entry-blocker
// snapshot; this test verifies the full controller-level path on the new code:
//
//   - step 4 (updateRBGStatus with the entry snapshot) must not publish an
//     unconfirmed clearing while the own-operation blocker is still confirmed;
//   - step 8 (reportRBGProgressingBlocker with an unclassifiable error) must
//     retain the confirmed family and its base message, appending only the
//     latest error;
//   - a fresh successful retry (finalizeRBGProgressingCondition without the
//     entry snapshot) must clear the family.
//
// Polarity: contract test. PASSES on fixed code; FAILS if the retention is lost
// again (or never re-verified against the new entry-blocker design).
func TestRBGBlockerFamilySurvivesRecomputeAndUnclassifiedRetry(t *testing.T) {
	ctx := context.Background()
	replicas := int32(2)
	newRBG := func() *workloadsv1alpha2.RoleBasedGroup {
		return &workloadsv1alpha2.RoleBasedGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "inference", Namespace: "default", Generation: 6},
			Spec: workloadsv1alpha2.RoleBasedGroupSpec{Roles: []workloadsv1alpha2.RoleSpec{
				{Name: "prefill", Replicas: &replicas},
				{Name: "decode", Replicas: &replicas},
			}},
			Status: workloadsv1alpha2.RoleBasedGroupStatus{
				ObservedGeneration: 6,
				RoleStatuses: []workloadsv1alpha2.RoleStatus{
					{Name: "prefill", Replicas: 2, ReadyReplicas: 2, UpdatedReplicas: 2},
					{Name: "decode", Replicas: 2, ReadyReplicas: 2, UpdatedReplicas: 2},
				},
				// Confirmed current-generation own-operation blocker.
				Conditions: []metav1.Condition{{
					Type:               string(workloadsv1alpha2.RoleBasedGroupProgressing),
					Status:             metav1.ConditionTrue,
					Reason:             workloadconstants.ProgressingReasonWaitingForQuota,
					Message:            "quota exceeded on role update",
					LastTransitionTime: metav1.Now(),
					ObservedGeneration: 6,
				}},
			},
		}
	}
	rbg := newRBG()
	roleStatuses := []workloadsv1alpha2.RoleStatus{
		{Name: "prefill", Replicas: 2, ReadyReplicas: 2, UpdatedReplicas: 2},
		{Name: "decode", Replicas: 2, ReadyReplicas: 2, UpdatedReplicas: 2},
	}

	scheme := runtime.NewScheme()
	if err := workloadsv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(rbg).Build()
	// Register healthy, current-generation children so the aggregation sees
	// converged facts; without the entry-blocker carry these would publish a
	// clearing of the confirmed quota family.
	for i := range rbg.Spec.Roles {
		role := &rbg.Spec.Roles[i]
		set := &workloadsv1alpha2.RoleInstanceSet{
			ObjectMeta: metav1.ObjectMeta{Name: rbg.GetWorkloadName(role), Namespace: rbg.Namespace, Generation: 1},
			Status: workloadsv1alpha2.RoleInstanceSetStatus{
				Conditions: []workloadsv1alpha2.RoleInstanceSetCondition{{
					Type:               workloadsv1alpha2.RoleInstanceSetProgressing,
					Status:             corev1.ConditionFalse,
					Reason:             workloadconstants.ProgressingReasonConverged,
					ObservedGeneration: 1,
				}},
			},
		}
		if err := c.Create(ctx, set); err != nil {
			t.Fatal(err)
		}
	}
	r := &RoleBasedGroupReconciler{client: c, recorder: record.NewFakeRecorder(16)}

	entryBlocker := rbgEntryOwnBlocker(rbg)
	if entryBlocker == nil || entryBlocker.Reason != workloadconstants.ProgressingReasonWaitingForQuota {
		t.Fatalf("entry snapshot = %+v, want the confirmed WaitingForQuota blocker", entryBlocker)
	}

	// Step 4: the normal status path recomputes the condition from child
	// aggregation while carrying the entry snapshot.
	if err := r.updateRBGStatus(ctx, rbg, roleStatuses, nil, entryBlocker); err != nil {
		t.Fatal(err)
	}
	if cond := findRBGProgressing(rbg.Status.Conditions); cond == nil ||
		cond.Reason != workloadconstants.ProgressingReasonWaitingForQuota {
		got := "<absent>"
		if cond != nil {
			got = cond.Reason
		}
		t.Fatalf("after step-4 recompute: Progressing reason = %q, want WaitingForQuota (the confirmed own-operation family must not be cleared by child aggregation before the operation is retried)", got)
	}

	// Step 8: the role retry fails with an unclassifiable error; the confirmed
	// family is retained with the latest error appended.
	if err := r.reportRBGProgressingBlocker(ctx, rbg, entryBlocker,
		workloadconstants.ProgressingReasonUnclassifiedBlocker, "server timeout"); err != nil {
		t.Fatal(err)
	}
	persisted := rbg.DeepCopy()
	if err := c.Get(ctx, types.NamespacedName{Name: rbg.Name, Namespace: rbg.Namespace}, persisted); err != nil {
		t.Fatal(err)
	}
	cond := findRBGProgressing(persisted.Status.Conditions)
	if cond == nil || cond.Reason != workloadconstants.ProgressingReasonWaitingForQuota {
		got := "<absent>"
		if cond != nil {
			got = cond.Reason
		}
		t.Fatalf("after unclassifiable retry: persisted Progressing reason = %q, want WaitingForQuota (KEP-502: a confirmed family survives unclassifiable follow-up errors)", got)
	}
	if cond != nil && !strings.Contains(cond.Message, "quota exceeded on role update") {
		t.Fatalf("retained message lost the confirmed blocker's base message: %q", cond.Message)
	}

	// Fresh successful retry: the family is cleared by a fresh observation.
	if err := r.finalizeRBGProgressingCondition(ctx, rbg, roleStatuses, nil); err != nil {
		t.Fatal(err)
	}
	final := rbg.DeepCopy()
	if err := c.Get(ctx, types.NamespacedName{Name: rbg.Name, Namespace: rbg.Namespace}, final); err != nil {
		t.Fatal(err)
	}
	cond = findRBGProgressing(final.Status.Conditions)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != workloadconstants.ProgressingReasonConverged {
		got := "<absent>"
		if cond != nil {
			got = string(cond.Status) + "/" + cond.Reason
		}
		t.Fatalf("after a fresh successful retry: persisted Progressing = %q, want False/Converged (a successful retry must clear the confirmed family)", got)
	}
}
