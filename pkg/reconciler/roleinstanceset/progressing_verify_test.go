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

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	workloadconstants "sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// Verification harness for PR #572 findings (Reviewer B / Codex).
// Polarity: these are CONTRACT tests — they assert the KEP-502 documented behavior.
// A failure on the code under review IS the reproduction of the finding.

// persistedSet builds an established set whose persisted status reflects `persistedReplicas`
// counted instances and a persisted Progressing condition with the given reason.
func persistedSet(target, persistedReplicas int32, persistedReason string) *workloadsv1alpha2.RoleInstanceSet {
	return &workloadsv1alpha2.RoleInstanceSet{
		ObjectMeta: metav1.ObjectMeta{Generation: 4},
		Spec:       workloadsv1alpha2.RoleInstanceSetSpec{Replicas: &target},
		Status: workloadsv1alpha2.RoleInstanceSetStatus{
			Replicas:                persistedReplicas,
			ReadyReplicas:           persistedReplicas,
			UpdatedReplicas:         persistedReplicas,
			ExpectedUpdatedReplicas: target,
			CurrentRevision:         "rev",
			UpdateRevision:          "rev",
			Conditions: []workloadsv1alpha2.RoleInstanceSetCondition{{
				Type:               workloadsv1alpha2.RoleInstanceSetProgressing,
				Status:             corev1.ConditionTrue,
				Reason:             persistedReason,
				ObservedGeneration: 4,
			}},
		},
	}
}

// F1-control: on the reconcile that first observes the loss (persisted counters still
// show the pre-loss target), an established set missing one instance reports Recovering.
// This matches the PR's own "established set with missing instance reports recovering" case.
func TestVerifyF1ControlFreshLossReportsRecovering(t *testing.T) {
	set := persistedSet(3, 3, workloadconstants.ProgressingReasonConverged)
	// Recovering persisted as True above would also work; Converged persisted as True is
	// impossible, so build the converged prior state explicitly.
	set.Status.Conditions[0].Status = corev1.ConditionFalse

	newStatus := &workloadsv1alpha2.RoleInstanceSetStatus{
		Replicas: 2, ReadyReplicas: 2, UpdatedReplicas: 2,
		CurrentRevision: "rev", UpdateRevision: "rev", ExpectedUpdatedReplicas: 3,
	}
	got := ComputeSetProgressingCondition(set, newStatus, nil, nil, nil)
	if got.Reason != workloadconstants.ProgressingReasonRecovering || got.Status != corev1.ConditionTrue {
		t.Fatalf("control case: got status=%s reason=%s, want True/Recovering", got.Status, got.Reason)
	}
}

// F1: once the reduced replica count has been persisted (the write that recorded the
// loss), the SAME missing instance is reclassified as ScalingUp on the next reconcile:
// establishedInstanceMissing requires set.Status.Replicas >= target, which the persisted
// count no longer satisfies. The set is not scaling — the target never grew — the missing
// instance is fault recovery, which KEP-502 classifies as Recovering:
//   - "Recovering when an established RI is being repaired or replaced"
//   - test plan: "Surge, terminating instances, and fault recovery must not be
//     misclassified as scaling."
// This state persists for every reconcile in which the replacement RoleInstance does not
// yet appear in the cache (creation expectations pending, create still failing without a
// classified error, informer lag, or a gate/hook delaying recreation).
func TestVerifyF1PersistedLossMisclassifiedAsScalingUp(t *testing.T) {
	set := persistedSet(3, 2, workloadconstants.ProgressingReasonRecovering)

	newStatus := &workloadsv1alpha2.RoleInstanceSetStatus{
		Replicas: 2, ReadyReplicas: 2, UpdatedReplicas: 2,
		CurrentRevision: "rev", UpdateRevision: "rev", ExpectedUpdatedReplicas: 3,
	}
	got := ComputeSetProgressingCondition(set, newStatus, nil, nil, nil)
	if got.Reason != workloadconstants.ProgressingReasonRecovering || got.Status != corev1.ConditionTrue {
		t.Fatalf("F1 REPRODUCED: persisted loss on an established set reports status=%s reason=%s, want True/Recovering (KEP-502: missing instances of an established set classify as Recovering, not ScalingUp)", got.Status, got.Reason)
	}
}

// F1-boundary control: a genuine target grow (established at 2, target now 5) must
// still report ScalingUp — the recovery classification must not swallow real scaling.
func TestVerifyF1GenuineScaleUpStillScalingUp(t *testing.T) {
	set := persistedSet(5, 2, workloadconstants.ProgressingReasonConverged)
	set.Status.Conditions[0].Status = corev1.ConditionFalse

	newStatus := &workloadsv1alpha2.RoleInstanceSetStatus{
		Replicas: 2, ReadyReplicas: 2, UpdatedReplicas: 2,
		CurrentRevision: "rev", UpdateRevision: "rev", ExpectedUpdatedReplicas: 5,
	}
	got := ComputeSetProgressingCondition(set, newStatus, nil, nil, nil)
	if got.Reason != workloadconstants.ProgressingReasonScalingUp || got.Status != corev1.ConditionTrue {
		t.Fatalf("boundary: got status=%s reason=%s, want True/ScalingUp", got.Status, got.Reason)
	}
}
