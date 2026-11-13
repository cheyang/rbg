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

package roleinstanceset

// Verification harness for PR #572 round-2 finding (Reviewer B / Codex).
// Polarity: CONTRACT — a partition-scoped rolling update that has updated every
// in-scope instance is complete at the RIS scope: ExpectedUpdatedReplicas is the
// partition-reduced target, and the RBG's own completion check
// (rbgOwnTargetComplete, "per-role update scope including partition") treats it as
// done. The RIS must report Converged so the RBG can; reporting RoleInstanceUpdated
// forever while a partition is held makes every canary rollout hang for consumers
// waiting on Converged.
//
// Revision delta: on the previous head (95ddf06a) updateActive required
// UpdatedReplicas < ExpectedUpdatedReplicas, so a partition-complete rollout
// reported Converged. On the reviewed head revision inequality alone keeps the
// update active indefinitely (shouldAdvanceCurrentRevision never advances
// CurrentRevision while partition > 0).

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	workloadconstants "sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// F12: replicas=4, partition=1 -> scope = 3 updated instances. All 3 in-scope
// instances updated and ready; the partitioned instance deliberately stays on the
// old revision, so CurrentRevision never advances while the partition is held.
// The update scope is complete: the RIS must report Converged.
func TestVerifyF12PartitionScopedRolloutCompleteReportsConverged(t *testing.T) {
	target := int32(4)
	set := &workloadsv1alpha2.RoleInstanceSet{
		ObjectMeta: metav1.ObjectMeta{Generation: 9},
		Spec:       workloadsv1alpha2.RoleInstanceSetSpec{Replicas: &target},
		Status: workloadsv1alpha2.RoleInstanceSetStatus{
			Replicas: 4, ReadyReplicas: 4, AvailableReplicas: 4,
			UpdatedReplicas: 3, ExpectedUpdatedReplicas: 3,
			CurrentRevision: "old", UpdateRevision: "new",
			Conditions: []workloadsv1alpha2.RoleInstanceSetCondition{{
				Type:               workloadsv1alpha2.RoleInstanceSetProgressing,
				Status:             corev1.ConditionFalse,
				Reason:             workloadconstants.ProgressingReasonConverged,
				ObservedGeneration: 8,
			}},
		},
	}
	newStatus := &workloadsv1alpha2.RoleInstanceSetStatus{
		Replicas: 4, ReadyReplicas: 4, AvailableReplicas: 4,
		UpdatedReplicas: 3, ExpectedUpdatedReplicas: 3,
		CurrentRevision: "old", UpdateRevision: "new",
	}
	got := ComputeSetProgressingCondition(set, newStatus, bareObservedInstances(4), nil, nil)
	if got.Status != corev1.ConditionFalse || got.Reason != workloadconstants.ProgressingReasonConverged {
		t.Fatalf("F12 REPRODUCED: partition-scoped rollout with completed scope reports status=%s reason=%s, want False/Converged (the partition-held instance is deliberate, not update work in progress)", got.Status, got.Reason)
	}
}

// F12-control: a rollout short of its partition-reduced scope still reports
// RoleInstanceUpdated on every head.
func TestVerifyF12ControlIncompleteScopeStillUpdating(t *testing.T) {
	target := int32(4)
	set := &workloadsv1alpha2.RoleInstanceSet{
		ObjectMeta: metav1.ObjectMeta{Generation: 9},
		Spec:       workloadsv1alpha2.RoleInstanceSetSpec{Replicas: &target},
		Status: workloadsv1alpha2.RoleInstanceSetStatus{
			Replicas: 4, ReadyReplicas: 4, UpdatedReplicas: 2, ExpectedUpdatedReplicas: 3,
			CurrentRevision: "old", UpdateRevision: "new",
			Conditions: []workloadsv1alpha2.RoleInstanceSetCondition{{
				Type:               workloadsv1alpha2.RoleInstanceSetProgressing,
				Status:             corev1.ConditionTrue,
				Reason:             workloadconstants.ProgressingReasonRoleInstanceUpdated,
				ObservedGeneration: 9,
			}},
		},
	}
	newStatus := &workloadsv1alpha2.RoleInstanceSetStatus{
		Replicas: 4, ReadyReplicas: 4, UpdatedReplicas: 2, ExpectedUpdatedReplicas: 3,
		CurrentRevision: "old", UpdateRevision: "new",
	}
	got := ComputeSetProgressingCondition(set, newStatus, bareObservedInstances(4), nil, nil)
	if got.Reason != workloadconstants.ProgressingReasonRoleInstanceUpdated || got.Status != corev1.ConditionTrue {
		t.Fatalf("control: incomplete partition scope = %s/%s, want True/RoleInstanceUpdated", got.Status, got.Reason)
	}
}
