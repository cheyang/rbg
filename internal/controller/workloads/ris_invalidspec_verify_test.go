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

// Verification harness for PR #572 ledger finding F2 (Reviewer B / Codex).
// Polarity: CONTRACT — a child RIS blocked by invalid committed ordinals
// (Progressing=True, reason=InvalidSpec) must surface at the RBG as the
// configuration-family blocker InvalidSpec, not as the benign activity reason
// RoleInstanceSetUpdated. Added in round 2: round 1 proved this via the source trace
// and Claude's harness; this file gives the codex harness its own contract test.

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// F2: the RIS->RBG reason mapping and the RBG blocker aggregation must carry the
// configuration family written by validateCommittedMembership.
func TestVerifyLedgerF2InvalidSpecSurfacesAtRBG(t *testing.T) {
	mapped, ok := risReasonToRBG[constants.ProgressingReasonInvalidSpec]
	if !ok || mapped != constants.ProgressingReasonInvalidSpec {
		t.Fatalf("F2 REPRODUCED: risReasonToRBG[%s] = %q, %v; want %q mapped", constants.ProgressingReasonInvalidSpec, mapped, ok, constants.ProgressingReasonInvalidSpec)
	}

	replicas := int32(2)
	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "inference", Namespace: "default", Generation: 5},
		Spec:       workloadsv1alpha2.RoleBasedGroupSpec{Roles: []workloadsv1alpha2.RoleSpec{{Name: "decode", Replicas: &replicas}}},
		Status: workloadsv1alpha2.RoleBasedGroupStatus{
			RoleStatuses: []workloadsv1alpha2.RoleStatus{{Name: "decode", Replicas: 2, ReadyReplicas: 2, UpdatedReplicas: 2}},
		},
	}
	observations := map[string]risObservation{
		"decode": {
			exists:   true,
			generation: 7,
			progressing: &workloadsv1alpha2.RoleInstanceSetCondition{
				Type:               workloadsv1alpha2.RoleInstanceSetProgressing,
				Status:             corev1.ConditionTrue,
				Reason:             constants.ProgressingReasonInvalidSpec,
				Message:            "committed ordinals [1 3] exceed replicas 2",
				ObservedGeneration: 7,
			},
		},
	}
	condition := computeRBGProgressingCondition(rbg, rbg.Status.RoleStatuses, observations, nil, false, nil, nil)
	if condition.Reason != constants.ProgressingReasonInvalidSpec || condition.Status != metav1.ConditionTrue {
		t.Fatalf("F2 REPRODUCED: RBG Progressing = %s/%s, want True/%s (a needs-attention configuration blocker must not surface as benign activity)",
			condition.Status, condition.Reason, constants.ProgressingReasonInvalidSpec)
	}
}

// F2-control: a stale (older-generation) InvalidSpec child condition must NOT confirm
// the current RBG generation.
func TestVerifyLedgerF2ControlStaleInvalidSpecSkipped(t *testing.T) {
	replicas := int32(2)
	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "inference", Namespace: "default", Generation: 5},
		Spec:       workloadsv1alpha2.RoleBasedGroupSpec{Roles: []workloadsv1alpha2.RoleSpec{{Name: "decode", Replicas: &replicas}}},
		Status: workloadsv1alpha2.RoleBasedGroupStatus{
			RoleStatuses: []workloadsv1alpha2.RoleStatus{{Name: "decode", Replicas: 2, ReadyReplicas: 2, UpdatedReplicas: 2}},
		},
	}
	observations := map[string]risObservation{
		"decode": {
			exists:   true,
			generation: 8,
			progressing: &workloadsv1alpha2.RoleInstanceSetCondition{
				Type:               workloadsv1alpha2.RoleInstanceSetProgressing,
				Status:             corev1.ConditionTrue,
				Reason:             constants.ProgressingReasonInvalidSpec,
				ObservedGeneration: 7, // stale
			},
		},
	}
	reason, _ := rbgCurrentBlocker(observations, false, nil, nil)
	if reason == constants.ProgressingReasonInvalidSpec {
		t.Fatalf("control: stale child InvalidSpec must not confirm the current generation, got %s", reason)
	}
}
