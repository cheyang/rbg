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

// TestEstablishedSetMissingInstanceStaysRecovering is a KEP-502 contract test
// (reviewer verification for PR #572, claim F1).
//
// Contract: "a required RI that does not exist is not Creating once the set is
// established: ... Recovering when an established RI is being repaired or
// replaced" and "fault recovery must not be misclassified as scaling" (KEP-502
// unit-test plan). The classification must hold on EVERY reconcile while the
// instance is still missing, not only on the first one after the loss.
//
// Scenario: an established 3-replica set loses one RoleInstance. Recreation is
// deferred (e.g. stateful OrderedReady blocked by an unhealthy predecessor), so
// the status write records Replicas=2. The next reconcile must still classify
// the missing instance as Recovering, because the target did not grow.
//
// Polarity: contract test. FAILS on the code under review (reproduces the
// defect); PASSES once the missing-instance classification is made durable.
func TestEstablishedSetMissingInstanceStaysRecovering(t *testing.T) {
	target := int32(3)
	// Two surviving instances; neither is recovering at the RI level.
	survivors := []*workloadsv1alpha2.RoleInstance{
		instanceWithProgressing("", workloadsv1alpha2.RoleInstancePhaseRunning),
		instanceWithProgressing("", workloadsv1alpha2.RoleInstancePhaseRunning),
	}

	// Reconcile 1: live status still shows the pre-loss converged state
	// (Replicas=3, reason=Converged). newStatus observes only 2 instances.
	set := &workloadsv1alpha2.RoleInstanceSet{
		ObjectMeta: metav1.ObjectMeta{Generation: 4},
		Spec:       workloadsv1alpha2.RoleInstanceSetSpec{Replicas: &target},
		Status: workloadsv1alpha2.RoleInstanceSetStatus{
			Replicas: 3, ReadyReplicas: 3, UpdatedReplicas: 3, ExpectedUpdatedReplicas: 3,
			CurrentRevision: "rev", UpdateRevision: "rev",
			Conditions: []workloadsv1alpha2.RoleInstanceSetCondition{{
				Type: workloadsv1alpha2.RoleInstanceSetProgressing, Status: corev1.ConditionFalse,
				Reason:             workloadconstants.ProgressingReasonConverged,
				ObservedGeneration: 4,
			}},
		},
	}
	newStatus1 := &workloadsv1alpha2.RoleInstanceSetStatus{
		Replicas: 2, ReadyReplicas: 2, UpdatedReplicas: 2, ExpectedUpdatedReplicas: 3,
		CurrentRevision: "rev", UpdateRevision: "rev",
	}
	cond1 := ComputeSetProgressingCondition(set, newStatus1, survivors, nil, nil)
	if cond1.Reason != workloadconstants.ProgressingReasonRecovering {
		t.Fatalf("reconcile 1: reason = %q, want %q (established set lost an instance)", cond1.Reason, workloadconstants.ProgressingReasonRecovering)
	}

	// Reconcile 2: the first status write persisted Replicas=2 and
	// reason=Recovering. The instance is still missing. Feed reconcile 1's
	// condition back as the live condition, exactly as the persisted object
	// would look.
	set2 := set.DeepCopy()
	set2.Status.Replicas = 2
	set2.Status.ReadyReplicas = 2
	set2.Status.Conditions = []workloadsv1alpha2.RoleInstanceSetCondition{cond1}
	newStatus2 := &workloadsv1alpha2.RoleInstanceSetStatus{
		Replicas: 2, ReadyReplicas: 2, UpdatedReplicas: 2, ExpectedUpdatedReplicas: 3,
		CurrentRevision: "rev", UpdateRevision: "rev",
	}
	cond2 := ComputeSetProgressingCondition(set2, newStatus2, survivors, nil, nil)
	if cond2.Reason != workloadconstants.ProgressingReasonRecovering {
		t.Fatalf("reconcile 2: reason = %q, want %q — the target did not grow, so a still-missing instance of an established set must remain Recovering, not be reported as ScalingUp", cond2.Reason, workloadconstants.ProgressingReasonRecovering)
	}
}

// TestScaleUpAfterConvergenceStillReportsScalingUp is the control case for the
// fix guard: when an established set's target genuinely grows, the reason must
// be ScalingUp. A fix that classifies every established replicas<target case as
// Recovering would break this contract.
func TestScaleUpAfterConvergenceStillReportsScalingUp(t *testing.T) {
	target := int32(3)
	survivors := []*workloadsv1alpha2.RoleInstance{
		instanceWithProgressing("", workloadsv1alpha2.RoleInstancePhaseRunning),
		instanceWithProgressing("", workloadsv1alpha2.RoleInstancePhaseRunning),
	}
	set := &workloadsv1alpha2.RoleInstanceSet{
		ObjectMeta: metav1.ObjectMeta{Generation: 4},
		Spec:       workloadsv1alpha2.RoleInstanceSetSpec{Replicas: &target},
		Status: workloadsv1alpha2.RoleInstanceSetStatus{
			Replicas: 2, ReadyReplicas: 2, UpdatedReplicas: 2, ExpectedUpdatedReplicas: 2,
			CurrentRevision: "rev", UpdateRevision: "rev",
			Conditions: []workloadsv1alpha2.RoleInstanceSetCondition{{
				Type: workloadsv1alpha2.RoleInstanceSetProgressing, Status: corev1.ConditionFalse,
				Reason:             workloadconstants.ProgressingReasonConverged,
				ObservedGeneration: 4,
			}},
		},
	}
	newStatus := &workloadsv1alpha2.RoleInstanceSetStatus{
		Replicas: 2, ReadyReplicas: 2, UpdatedReplicas: 2, ExpectedUpdatedReplicas: 3,
		CurrentRevision: "rev", UpdateRevision: "rev",
	}
	cond := ComputeSetProgressingCondition(set, newStatus, survivors, nil, nil)
	if cond.Reason != workloadconstants.ProgressingReasonScalingUp {
		t.Fatalf("reason = %q, want %q (target grew from 2 to 3)", cond.Reason, workloadconstants.ProgressingReasonScalingUp)
	}
}

// TestEstablishedSetReadinessFlapThenMemberLossStaysRecovering is a KEP-502
// contract test (reviewer verification for PR #572 round 2; differential
// against the round-1 head 95ddf06a).
//
// Contract: "Creating is reserved for initial establishment ... missing
// instances of an established set classify as Recovering" (KEP-502 RIS rules)
// and "fault recovery must not be misclassified" (test plan). An established
// set that converged, then flapped a pod's readiness (RIS persists
// Progressing=True/InstancesNotReady with ReadyReplicas < Replicas), and then
// loses a member RoleInstance must report Recovering: the target did not grow,
// no update workflow is active, and the set was previously established.
//
// Round-2 commit effc2f8e removed InstancesNotReady from
// setEstablishmentReasons. After that removal, setEstablished falls through to
// the counter fallback (ReadyReplicas == Replicas), which is false during the
// flap, so the established set is treated as never-established and the member
// loss is reported as Creating — the verdict KEP-502 reserves for initial
// establishment.
//
// Polarity: contract test. PASSED on the round-1 head 95ddf06a; FAILS on
// effc2f8e (revision regression).
func TestEstablishedSetReadinessFlapThenMemberLossStaysRecovering(t *testing.T) {
	target := int32(3)
	// Two surviving members; both Running, neither recovering.
	survivors := []*workloadsv1alpha2.RoleInstance{
		instanceWithProgressing("", workloadsv1alpha2.RoleInstancePhaseRunning),
		instanceWithProgressing("", workloadsv1alpha2.RoleInstancePhaseRunning),
	}
	// Persisted state from the readiness-flap reconcile: the set previously
	// converged (all 3 updated to the expected scope), one pod flapped unready,
	// so the RIS wrote Progressing=True/InstancesNotReady with ReadyReplicas 2/3.
	set := &workloadsv1alpha2.RoleInstanceSet{
		ObjectMeta: metav1.ObjectMeta{Generation: 4},
		Spec:       workloadsv1alpha2.RoleInstanceSetSpec{Replicas: &target},
		Status: workloadsv1alpha2.RoleInstanceSetStatus{
			Replicas: 3, ReadyReplicas: 2, UpdatedReplicas: 3, ExpectedUpdatedReplicas: 3,
			CurrentRevision: "rev", UpdateRevision: "rev",
			Conditions: []workloadsv1alpha2.RoleInstanceSetCondition{{
				Type: workloadsv1alpha2.RoleInstanceSetProgressing, Status: corev1.ConditionTrue,
				Reason:             workloadconstants.ProgressingReasonInstancesNotReady,
				ObservedGeneration: 4,
			}},
		},
	}
	// The member loss reconcile: only 2 instances are observed, the third
	// RoleInstance object is gone (recreation happens after this computation).
	newStatus := &workloadsv1alpha2.RoleInstanceSetStatus{
		Replicas: 2, ReadyReplicas: 2, UpdatedReplicas: 2, ExpectedUpdatedReplicas: 3,
		CurrentRevision: "rev", UpdateRevision: "rev",
	}
	cond := ComputeSetProgressingCondition(set, newStatus, survivors, nil, nil)
	if cond.Reason != workloadconstants.ProgressingReasonRecovering {
		t.Fatalf("reason = %q, want %q — an established set (previously converged, all instances at the expected update scope) that loses a member during a readiness flap is fault recovery, not initial establishment; Creating is reserved for initial establishment (KEP-502)", cond.Reason, workloadconstants.ProgressingReasonRecovering)
	}
}

// TestEstablishedSetRecoveryAfterBlockerInterruptionStaysRecovering is a KEP-502
// contract test (reviewer verification for PR #572 round 2).
//
// Contract: "missing instances of an established set classify as Recovering"
// (KEP-502 RIS rules) — durable across the whole recreation window, including
// when recreation was temporarily blocked by an own-operation failure. An
// established set that lost a member, whose recreation failed with quota, and
// whose quota blocker has now cleared (fresh success) must report Recovering
// while the replacement is still pending, not Creating.
//
// Mechanism of the defect: establishment evidence lives only in the persisted
// Progressing reason (setEstablishmentReasons) or in the counter shape
// ReadyReplicas == Replicas && UpdatedReplicas == ExpectedUpdatedReplicas. The
// quota interlude replaces the reason with WaitingForQuota and the counters no
// longer match the fallback shape, so the previously established set is
// classified as never-established and its recovery reports Creating.
//
// Polarity: contract test. FAILS on the code under review (and on the round-1
// head 95ddf06a equally — pre-existing within this PR, not a regression of
// this round).
func TestEstablishedSetRecoveryAfterBlockerInterruptionStaysRecovering(t *testing.T) {
	target := int32(3)
	survivors := []*workloadsv1alpha2.RoleInstance{
		instanceWithProgressing("", workloadsv1alpha2.RoleInstancePhaseRunning),
		instanceWithProgressing("", workloadsv1alpha2.RoleInstancePhaseRunning),
	}
	// Persisted state after the quota-blocked recovery attempt: the set
	// previously converged with 3 instances (UpdatedReplicas reached the
	// expected scope of the pre-loss target), lost one, and the recreation
	// attempt failed with quota, which replaced the Progressing reason.
	set := &workloadsv1alpha2.RoleInstanceSet{
		ObjectMeta: metav1.ObjectMeta{Generation: 4},
		Spec:       workloadsv1alpha2.RoleInstanceSetSpec{Replicas: &target},
		Status: workloadsv1alpha2.RoleInstanceSetStatus{
			Replicas: 2, ReadyReplicas: 2, UpdatedReplicas: 2, ExpectedUpdatedReplicas: 3,
			CurrentRevision: "rev", UpdateRevision: "rev",
			Conditions: []workloadsv1alpha2.RoleInstanceSetCondition{{
				Type: workloadsv1alpha2.RoleInstanceSetProgressing, Status: corev1.ConditionTrue,
				Reason:             workloadconstants.ProgressingReasonWaitingForQuota,
				Message:            "exceeded quota",
				ObservedGeneration: 4,
			}},
		},
	}
	newStatus := &workloadsv1alpha2.RoleInstanceSetStatus{
		Replicas: 2, ReadyReplicas: 2, UpdatedReplicas: 2, ExpectedUpdatedReplicas: 3,
		CurrentRevision: "rev", UpdateRevision: "rev",
	}
	cond := ComputeSetProgressingCondition(set, newStatus, survivors, nil, nil)
	if cond.Reason != workloadconstants.ProgressingReasonRecovering {
		t.Fatalf("reason = %q, want %q — a previously established set (3 instances converged at the expected update scope) whose member-loss recovery was interrupted by a now-cleared quota blocker is still fault recovery, not initial establishment", cond.Reason, workloadconstants.ProgressingReasonRecovering)
	}
}
