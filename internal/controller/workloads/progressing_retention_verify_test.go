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

// Verification harness for PR #572 finding F2 (Reviewer B / Codex).
// Polarity: CONTRACT tests — they assert the KEP-502 documented retention rule:
//   "When a blocker family is already confirmed for the current target, an
//    unclassifiable error does not replace it: the confirmed blocker remains until a
//    fresh successful observation confirms it no longer holds."
// A failure on the code under review IS the reproduction of the finding.

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

// f2Fixture builds an RBG (one RIS role, generation 6) whose persisted status carries a
// confirmed current-generation WaitingForQuota blocker from this controller's own
// operation in a previous reconcile, plus a converged child RoleInstanceSet.
func f2Fixture() (*workloadsv1alpha2.RoleBasedGroup, *workloadsv1alpha2.RoleInstanceSet, []workloadsv1alpha2.RoleStatus) {
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

// F2-control: retainRBGBlockerFamily retains the confirmed current-generation family
// when the persisted condition is still intact in memory. This is the path the PR's own
// unit test covers (e.g. the preCheck path, which runs before any status rewrite).
func TestVerifyF2ControlRetentionWorksBeforeStatusRewrite(t *testing.T) {
	rbg, _, _ := f2Fixture()
	reason, _ := retainRBGBlockerFamily(rbg, workloadconstants.ProgressingReasonUnclassifiedBlocker, "timed out")
	if reason != workloadconstants.ProgressingReasonWaitingForQuota {
		t.Fatalf("control: retained reason = %s, want %s", reason, workloadconstants.ProgressingReasonWaitingForQuota)
	}
}

// F2: in Reconcile, step 4 (updateRBGStatus -> updateRBGProgressingCondition) recomputes
// the Progressing condition from child facts and OVERWRITES the in-memory condition
// before step 8's roleErr handling calls reportRBGProgressingBlocker. The previously
// confirmed current-generation family is therefore invisible to retainRBGBlockerFamily,
// and an unclassifiable role-reconcile error replaces it with UnclassifiedBlocker —
// exactly what KEP-502 forbids. The child observations say nothing about the RBG's own
// failing operation, so they are not a "fresh successful observation" of that blocker.
func TestVerifyF2RetentionLostAfterProgressingRecompute(t *testing.T) {
	ctx := context.Background()
	rbg, set, roleStatuses := f2Fixture()

	var persistedReason string
	scheme := runtime.NewScheme()
	if err := workloadsv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(set).
		WithInterceptorFuncs(interceptor.Funcs{
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
						persistedReason = cond.Reason
					}
				}
				return nil
			},
		}).Build()
	r := &RoleBasedGroupReconciler{client: c}

	// Step 4 equivalent: recompute Progressing from (healthy) child facts. This mirrors
	// updateRBGStatus and mutates rbg.Status.Conditions in place.
	r.updateRBGProgressingCondition(ctx, rbg, roleStatuses, nil, false)

	// Step 8 equivalent: the role reconcile then fails with an unclassifiable error
	// (e.g. a timeout), which must NOT replace the confirmed WaitingForQuota family.
	if err := r.reportRBGProgressingBlocker(ctx, rbg, workloadconstants.ProgressingReasonUnclassifiedBlocker, "Put \"...\": dial tcp: i/o timeout"); err != nil {
		t.Fatal(err)
	}
	if persistedReason != workloadconstants.ProgressingReasonWaitingForQuota {
		t.Fatalf("F2 REPRODUCED: persisted Progressing reason = %s, want retained %s", persistedReason, workloadconstants.ProgressingReasonWaitingForQuota)
	}
}
