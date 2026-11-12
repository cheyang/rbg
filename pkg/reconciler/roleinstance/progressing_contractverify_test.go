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

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// TestInstanceBlockerSelectionFollowsFamilyPriority is a KEP-502 contract test
// (reviewer verification for PR #572, claim F4).
//
// Contract: when multiple blockers are observed in one reconcile, the reported
// Progressing reason must follow the documented blocker precedence
// (permission > admission > quota > resources > unclassified), "instead of the
// append order" — the RoleInstanceSet layer implements exactly this
// (extractOwnSetBlocker); the RoleInstance layer must not silently drop a
// higher-priority family.
//
// Scenario: in one reconcile, the Scale step fails with a quota error
// (PodCreateQuotaExceeded, appended first) and the Update step fails with an
// RBAC error (PodUpdateForbidden, appended second). The finalized Progressing
// condition must report the permission family.
//
// Polarity: contract test. FAILS on the code under review; PASSES once the RI
// layer competes blockers through family precedence like the RIS layer does.
func TestInstanceBlockerSelectionFollowsFamilyPriority(t *testing.T) {
	instance := &workloadsv1alpha2.RoleInstance{
		ObjectMeta: metav1.ObjectMeta{Generation: 2},
	}
	// Order mirrors the append order in syncInstance: scale error first, update
	// error second.
	newInnerConditions := []workloadsv1alpha2.RoleInstanceCondition{
		{
			Type: workloadsv1alpha2.RoleInstanceProgressing, Status: corev1.ConditionTrue,
			Reason: workloadsv1alpha2.ReasonPodCreateQuotaExceeded,
			Message: "exceeded quota: pods",
			LastTransitionTime: metav1.Now(),
		},
		{
			Type: workloadsv1alpha2.RoleInstanceProgressing, Status: corev1.ConditionTrue,
			Reason: workloadsv1alpha2.ReasonPodUpdateForbidden,
			Message: "pods is forbidden",
			LastTransitionTime: metav1.Now(),
		},
	}
	cond := computeInstanceProgressingCondition(instance, workloadsv1alpha2.RoleInstancePhaseUpdating, false, newInnerConditions, "")
	if cond.Reason != workloadsv1alpha2.ReasonPodUpdateForbidden {
		t.Fatalf("reason = %q, want %q: the permission family outranks the quota family in the KEP-502 blocker precedence, so the finalized condition must not keep the first-appended (scale) reason and drop the higher-priority family", cond.Reason, workloadsv1alpha2.ReasonPodUpdateForbidden)
	}
}
