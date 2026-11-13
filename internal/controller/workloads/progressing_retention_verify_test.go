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

// Verification harness for PR #572 ledger finding F3 (Reviewer B / Codex).
// Polarity: CONTRACT tests — they assert the KEP-502 documented retention rule:
//   "When a blocker family is already confirmed for the current target, an
//    unclassifiable error does not replace it: the confirmed blocker remains until a
//    fresh successful observation confirms it no longer holds."
// A failure on the code under review IS the reproduction of the finding.
//
// Round 2 (head effc2f8e): the author added the entryBlocker snapshot parameter, so
// the tests now mirror the real Reconcile flow: snapshot at entry, carried through the
// step-4 recompute, retained on the step-8 unclassifiable error, and cleared only by
// the fresh successful observation (finalizeRBGProgressingCondition).

import (
	"context"
	"encoding/json"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	workloadconstants "sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// f3Fixture builds an RBG (one RIS role, generation 6) whose persisted status carries a
// confirmed current-generation WaitingForQuota blocker from this controller's own
// operation in a previous reconcile, plus a converged child RoleInstanceSet.
func f3Fixture() (*workloadsv1alpha2.RoleBasedGroup, *workloadsv1alpha2.RoleInstanceSet, []workloadsv1alpha2.RoleStatus) {
	replicas := int32(2)
	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "inference", Namespace: "default", Generation: 6},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{
			Roles: []workloadsv1alpha2.RoleSpec{{Name: "decode", Replicas: &replicas}},
		},
		Status: workloadsv1alpha2.RoleBasedGroupStatus{
			ObservedGeneration: 6,
			RoleStatuses: []workloadsv1alpha2.RoleStatus{
				{Name: "decode", Replicas: 2, ReadyReplicas: 2, UpdatedReplicas: 2},
			},
			Conditions: []metav1.Condition{{
				Type:               string(workloadsv1alpha2.RoleBasedGroupProgressing),
				Status:             metav1.ConditionTrue,
				Reason:             workloadconstants.ProgressingReasonWaitingForQuota,
				Message:            "role decode: exceeded quota: gpu-quota",
				ObservedGeneration: 6,
				LastTransitionTime: metav1.Now(),
			}},
		},
	}
	set := &workloadsv1alpha2.RoleInstanceSet{
		ObjectMeta: metav1.ObjectMeta{Name: "inference-decode", Namespace: "default", Generation: 3},
		Status: workloadsv1alpha2.RoleInstanceSetStatus{
			Replicas: 2, ReadyReplicas: 2, UpdatedReplicas: 2,
			Conditions: []workloadsv1alpha2.RoleInstanceSetCondition{{
				Type:               workloadsv1alpha2.RoleInstanceSetProgressing,
				Status:             corev1.ConditionFalse,
				Reason:             workloadconstants.ProgressingReasonConverged,
				ObservedGeneration: 3,
			}},
		},
	}
	roleStatuses := []workloadsv1alpha2.RoleStatus{{Name: "decode", Replicas: 2, ReadyReplicas: 2, UpdatedReplicas: 2}}
	return rbg, set, roleStatuses
}

// patchRecorder captures the Progressing reason of every SSA status patch, in order.
type patchRecorder struct{ reasons []string }

func (p *patchRecorder) interceptor() interceptor.Funcs {
	return interceptor.Funcs{
		SubResourcePatch: func(_ context.Context, _ client.Client, subresource string, obj client.Object, patch client.Patch, _ ...client.SubResourcePatchOption) error {
			data, err := patch.Data(obj)
			if err != nil {
				return err
			}
			var payload struct {
				Status struct {
					Conditions []metav1.Condition `json:"conditions"`
				} `json:"status"`
			}
			if err := json.Unmarshal(data, &payload); err != nil {
				return err
			}
			for _, cond := range payload.Status.Conditions {
				if cond.Type == string(workloadsv1alpha2.RoleBasedGroupProgressing) {
					p.reasons = append(p.reasons, cond.Reason)
				}
			}
			return nil
		},
	}
}

func newRecorderClient(t *testing.T, set *workloadsv1alpha2.RoleInstanceSet, rec *patchRecorder) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := workloadsv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(set).
		WithInterceptorFuncs(rec.interceptor()).Build()
}

// F3-control: the entry snapshot carries the confirmed family and
// retainRBGBlockerFamily retains it for an unclassifiable follow-up error.
func TestVerifyF3ControlEntrySnapshotRetainsFamily(t *testing.T) {
	rbg, _, _ := f3Fixture()
	entry := rbgEntryOwnBlocker(rbg)
	if entry == nil || entry.Reason != workloadconstants.ProgressingReasonWaitingForQuota {
		t.Fatalf("control: entryBlocker = %+v, want WaitingForQuota snapshot", entry)
	}
	reason, _ := retainRBGBlockerFamily(rbg, entry, workloadconstants.ProgressingReasonUnclassifiedBlocker, "timed out")
	if reason != workloadconstants.ProgressingReasonWaitingForQuota {
		t.Fatalf("control: retained reason = %s, want %s", reason, workloadconstants.ProgressingReasonWaitingForQuota)
	}
}

// F3: the confirmed current-generation blocker family must survive the full reconcile
// flow when the step-8 role operation fails with an unclassifiable error:
// entry snapshot -> step-4 recompute (must not publish an unconfirmed clearing) ->
// step-8 reportRBGProgressingBlocker(unclassified) must retain WaitingForQuota.
// On head 95ddf06a the step-4 recompute overwrote the in-memory condition and the
// step-8 retention found nothing, persisting UnclassifiedBlocker.
func TestVerifyF3RetentionSurvivesRecomputeAndUnclassifiedError(t *testing.T) {
	ctx := context.Background()
	rbg, set, roleStatuses := f3Fixture()
	rec := &patchRecorder{}
	r := &RoleBasedGroupReconciler{client: newRecorderClient(t, set, rec)}

	// Reconcile entry: snapshot the confirmed family.
	entryBlocker := rbgEntryOwnBlocker(rbg)

	// Step 4 equivalent: recompute Progressing from (healthy) child facts, carrying
	// the entry snapshot exactly like updateRBGStatus does.
	r.updateRBGProgressingCondition(ctx, rbg, roleStatuses, nil, false, entryBlocker)

	// Step 8 equivalent: the role reconcile then fails with an unclassifiable error
	// (e.g. a timeout), which must NOT replace the confirmed WaitingForQuota family.
	if err := r.reportRBGProgressingBlocker(ctx, rbg, entryBlocker, workloadconstants.ProgressingReasonUnclassifiedBlocker, "Put \"...\": dial tcp: i/o timeout"); err != nil {
		t.Fatal(err)
	}
	if len(rec.reasons) == 0 {
		t.Fatal("no SSA status patch captured")
	}
	for i, reason := range rec.reasons {
		if reason != workloadconstants.ProgressingReasonWaitingForQuota {
			t.Fatalf("F3 REPRODUCED: patch %d persisted reason = %s, want retained %s (all patches: %v)", i, reason, workloadconstants.ProgressingReasonWaitingForQuota, rec.reasons)
		}
	}
}

// F3-clearing control: once the role operation succeeds, the fresh observation must
// clear the carried family (retention must not pin the blocker forever).
func TestVerifyF3ControlFreshSuccessClearsFamily(t *testing.T) {
	ctx := context.Background()
	rbg, set, roleStatuses := f3Fixture()
	rec := &patchRecorder{}
	r := &RoleBasedGroupReconciler{client: newRecorderClient(t, set, rec)}

	entryBlocker := rbgEntryOwnBlocker(rbg)
	r.updateRBGProgressingCondition(ctx, rbg, roleStatuses, nil, false, entryBlocker)
	// Role operations succeeded: finalize without the entry snapshot.
	if err := r.finalizeRBGProgressingCondition(ctx, rbg, roleStatuses, nil); err != nil {
		t.Fatal(err)
	}
	if len(rec.reasons) == 0 {
		t.Fatal("no SSA status patch captured")
	}
	last := rec.reasons[len(rec.reasons)-1]
	if last == workloadconstants.ProgressingReasonWaitingForQuota {
		t.Fatalf("F3 clearing control: persisted reason still %s after fresh success; want the child-aggregated verdict (patches: %v)", last, rec.reasons)
	}
}
