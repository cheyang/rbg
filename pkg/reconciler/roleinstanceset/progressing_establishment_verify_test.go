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
// Polarity: CONTRACT — KEP-502 reserves Creating for initial set establishment and
// classifies missing instances of an established set as Recovering. These tests pin
// the behavior for sets whose only persisted Progressing evidence is a True
// InstancesNotReady condition — a state every established set passes through on any
// readiness flap, scale-up readiness window, or post-recovery readiness window.
//
// Revision delta: on the previous head (95ddf06a) setEstablishmentReasons included
// InstancesNotReady, so both scenarios classified as Recovering. On the reviewed head
// the reason was removed, so the same persisted facts classify as Creating.

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	workloadconstants "sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// bareObservedInstances builds n observed instances with no phase and no conditions:
// their pods exist and run (not starting, not recovering) but readiness lags.
func bareObservedInstances(n int) []*workloadsv1alpha2.RoleInstance {
	instances := make([]*workloadsv1alpha2.RoleInstance, 0, n)
	for i := 0; i < n; i++ {
		instances = append(instances, &workloadsv1alpha2.RoleInstance{})
	}
	return instances
}

// N1: an established set (converged at 3 earlier; a readiness flap then overwrote the
// single Progressing slot with InstancesNotReady) loses a member. The persisted facts
// below are exactly what the controller itself wrote after the flap. Classifying the
// loss as Creating asserts initial establishment for a long-established set.
func TestVerifyN1EstablishedSetLossDuringReadinessFlapIsNotCreating(t *testing.T) {
	target := int32(3)
	set := &workloadsv1alpha2.RoleInstanceSet{
		ObjectMeta: metav1.ObjectMeta{Generation: 9},
		Spec:       workloadsv1alpha2.RoleInstanceSetSpec{Replicas: &target},
		Status: workloadsv1alpha2.RoleInstanceSetStatus{
			Replicas: 3, ReadyReplicas: 2, UpdatedReplicas: 3, ExpectedUpdatedReplicas: 3,
			CurrentRevision: "rev", UpdateRevision: "rev",
			Conditions: []workloadsv1alpha2.RoleInstanceSetCondition{{
				Type:               workloadsv1alpha2.RoleInstanceSetProgressing,
				Status:             corev1.ConditionTrue,
				Reason:             workloadconstants.ProgressingReasonInstancesNotReady,
				ObservedGeneration: 9,
			}},
		},
	}
	newStatus := &workloadsv1alpha2.RoleInstanceSetStatus{
		Replicas: 2, ReadyReplicas: 2, UpdatedReplicas: 2, ExpectedUpdatedReplicas: 3,
		CurrentRevision: "rev", UpdateRevision: "rev",
	}
	got := ComputeSetProgressingCondition(set, newStatus, bareObservedInstances(2), nil, nil)
	if got.Reason != workloadconstants.ProgressingReasonRecovering {
		t.Fatalf("N1 REPRODUCED: established set losing a member during a readiness flap reports %s, want Recovering (KEP-502: Creating is reserved for initial set establishment)", got.Reason)
	}
}

// N2: mid-scale-up window. The set converged at 3, the target grew to 5, all 5
// instances were created but readiness lags (InstancesNotReady persisted), and then a
// member is lost. The set is past initial establishment; Creating is a
// misclassification. Recovering (a required member is missing and the target did not
// grow since) and ScalingUp (the target did grow) are both defensible; Creating is
// not.
func TestVerifyN2LossDuringScaleUpReadinessWindowIsNotCreating(t *testing.T) {
	target := int32(5)
	set := &workloadsv1alpha2.RoleInstanceSet{
		ObjectMeta: metav1.ObjectMeta{Generation: 9},
		Spec:       workloadsv1alpha2.RoleInstanceSetSpec{Replicas: &target},
		Status: workloadsv1alpha2.RoleInstanceSetStatus{
			Replicas: 5, ReadyReplicas: 3, UpdatedReplicas: 5, ExpectedUpdatedReplicas: 5,
			CurrentRevision: "rev", UpdateRevision: "rev",
			Conditions: []workloadsv1alpha2.RoleInstanceSetCondition{{
				Type:               workloadsv1alpha2.RoleInstanceSetProgressing,
				Status:             corev1.ConditionTrue,
				Reason:             workloadconstants.ProgressingReasonInstancesNotReady,
				ObservedGeneration: 9,
			}},
		},
	}
	newStatus := &workloadsv1alpha2.RoleInstanceSetStatus{
		Replicas: 4, ReadyReplicas: 3, UpdatedReplicas: 4, ExpectedUpdatedReplicas: 5,
		CurrentRevision: "rev", UpdateRevision: "rev",
	}
	got := ComputeSetProgressingCondition(set, newStatus, bareObservedInstances(4), nil, nil)
	if got.Reason == workloadconstants.ProgressingReasonCreating {
		t.Fatalf("N2 REPRODUCED: member loss during a scale-up readiness window reports Creating (initial establishment) for an established set; got %s", got.Reason)
	}
}

// N-control: the author's pinned semantics for a genuinely never-established set stay
// intact — a fresh set that only ever reached InstancesNotReady reports Creating on
// member loss (no establishment evidence anywhere in its persisted state).
func TestVerifyNControlNeverReadySetStaysCreating(t *testing.T) {
	target := int32(3)
	set := &workloadsv1alpha2.RoleInstanceSet{
		ObjectMeta: metav1.ObjectMeta{Generation: 4},
		Spec:       workloadsv1alpha2.RoleInstanceSetSpec{Replicas: &target},
		Status: workloadsv1alpha2.RoleInstanceSetStatus{
			CurrentRevision: "rev", UpdateRevision: "rev",
			Conditions: []workloadsv1alpha2.RoleInstanceSetCondition{{
				Type:   workloadsv1alpha2.RoleInstanceSetProgressing,
				Status: corev1.ConditionTrue,
				Reason: workloadconstants.ProgressingReasonInstancesNotReady,
			}},
		},
	}
	newStatus := &workloadsv1alpha2.RoleInstanceSetStatus{
		Replicas: 2, CurrentRevision: "rev", UpdateRevision: "rev", ExpectedUpdatedReplicas: 3,
	}
	got := ComputeSetProgressingCondition(set, newStatus, bareObservedInstances(2), nil, nil)
	if got.Reason != workloadconstants.ProgressingReasonCreating {
		t.Fatalf("control: never-established set member loss = %s, want Creating", got.Reason)
	}
}
