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

// Verification harness for PR #572 ledger finding F5 (Reviewer B / Codex).
// Polarity: CONTRACT — when Scale and Update both fail in one RoleInstance reconcile,
// the finalized Progressing reason must follow the documented blocker family
// precedence (permission > admission > quota > resources > unclassified), not the
// append order. Added in round 2 (round 1 proved this via Claude's harness + trace).

import (
	"testing"

	corev1 "k8s.io/api/core/v1"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// F5: scale fails with quota (appended first), update fails with RBAC (appended
// second). The finalized condition must select PodUpdateForbidden (permission family)
// over PodCreateQuotaExceeded (quota family).
func TestVerifyLedgerF5BlockerFamilyPrecedenceBeatsAppendOrder(t *testing.T) {
	conditions := []workloadsv1alpha2.RoleInstanceCondition{
		{
			Type:    workloadsv1alpha2.RoleInstanceProgressing,
			Status:  corev1.ConditionTrue,
			Reason:  workloadsv1alpha2.ReasonPodCreateQuotaExceeded,
			Message: "scale quota exceeded",
		},
		{
			Type:    workloadsv1alpha2.RoleInstanceProgressing,
			Status:  corev1.ConditionTrue,
			Reason:  workloadsv1alpha2.ReasonPodUpdateForbidden,
			Message: "update forbidden",
		},
	}
	blocker := instanceProgressingBlocker(conditions)
	if blocker == nil || blocker.Reason != workloadsv1alpha2.ReasonPodUpdateForbidden {
		t.Fatalf("F5 REPRODUCED: selected blocker = %+v, want %s (permission family outranks quota regardless of append order)", blocker, workloadsv1alpha2.ReasonPodUpdateForbidden)
	}
}

// F5-control: reversed append order selects the same family (selection is by
// precedence, not position).
func TestVerifyLedgerF5ControlReversedOrderSameWinner(t *testing.T) {
	conditions := []workloadsv1alpha2.RoleInstanceCondition{
		{
			Type:    workloadsv1alpha2.RoleInstanceProgressing,
			Status:  corev1.ConditionTrue,
			Reason:  workloadsv1alpha2.ReasonPodUpdateForbidden,
			Message: "update forbidden",
		},
		{
			Type:    workloadsv1alpha2.RoleInstanceProgressing,
			Status:  corev1.ConditionTrue,
			Reason:  workloadsv1alpha2.ReasonPodCreateQuotaExceeded,
			Message: "scale quota exceeded",
		},
	}
	blocker := instanceProgressingBlocker(conditions)
	if blocker == nil || blocker.Reason != workloadsv1alpha2.ReasonPodUpdateForbidden {
		t.Fatalf("control: reversed order selected %+v, want %s", blocker, workloadsv1alpha2.ReasonPodUpdateForbidden)
	}
}
