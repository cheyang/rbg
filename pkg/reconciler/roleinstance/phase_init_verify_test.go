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

// Verification harness for PR #572 finding F5 (Reviewer B / Codex).
// Polarity: CONTRACT — asserts the KEP-502 upgrade rule:
//   "The current phase cannot be confirmed -> Leave phase absent; do not default to
//    initial creation or Running."
// A failure on the code under review IS the reproduction.

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// F5: an existing (pre-KEP-502) RoleInstance carries persisted status but no phase.
// When its upgrade-time facts cannot confirm the workflow — no LastReadyGeneration
// latch, no active update, and Pods currently missing — the composed
// phaseFacts + nextRoleInstancePhase must leave the phase absent. The implementation
// cannot distinguish this object from a genuinely new instance and defaults to
// Creating, contradicting the KEP-502 upgrade table.
func TestVerifyF5UnconfirmableLegacyInstanceStaysAbsent(t *testing.T) {
	replicas := int32(1)
	legacy := &workloadsv1alpha2.RoleInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "decode-0", Namespace: "default", Generation: 3},
		Spec: workloadsv1alpha2.RoleInstanceSpec{
			Components: []workloadsv1alpha2.RoleInstanceComponent{{Name: "main", Size: &replicas}},
		},
		Status: workloadsv1alpha2.RoleInstanceStatus{
			// Persisted pre-KEP-502 status: reconciled before, never latched ready,
			// phase absent. Pod currently missing for an unknown reason.
			ObservedGeneration: 3,
			CurrentRevision:    "rev-a",
			UpdateRevision:     "rev-a",
			Conditions: []workloadsv1alpha2.RoleInstanceCondition{{
				Type:   workloadsv1alpha2.RoleInstanceReady,
				Status: "False",
			}},
		},
	}
	r := &reconciler{}
	newStatus := &workloadsv1alpha2.RoleInstanceStatus{
		CurrentRevision: legacy.Status.CurrentRevision,
		UpdateRevision:  legacy.Status.UpdateRevision,
	}
	got := nextRoleInstancePhase(legacy.Status.Phase, r.phaseFacts(legacy, newStatus, nil))
	if got != "" {
		t.Fatalf("F5 REPRODUCED: unconfirmable legacy instance initialized to phase %q, want absent per KEP-502 upgrade table", got)
	}
}

// F5-control: a genuinely new instance (no persisted status at all) starts at Creating.
func TestVerifyF5ControlNewInstanceStartsCreating(t *testing.T) {
	replicas := int32(1)
	fresh := &workloadsv1alpha2.RoleInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "decode-1", Namespace: "default", Generation: 1},
		Spec: workloadsv1alpha2.RoleInstanceSpec{
			Components: []workloadsv1alpha2.RoleInstanceComponent{{Name: "main", Size: &replicas}},
		},
	}
	r := &reconciler{}
	newStatus := &workloadsv1alpha2.RoleInstanceStatus{CurrentRevision: "rev-a", UpdateRevision: "rev-a"}
	got := nextRoleInstancePhase(fresh.Status.Phase, r.phaseFacts(fresh, newStatus, nil))
	if got != workloadsv1alpha2.RoleInstancePhaseCreating {
		t.Fatalf("control: new instance phase = %q, want Creating", got)
	}
}
