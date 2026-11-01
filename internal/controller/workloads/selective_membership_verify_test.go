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

// Reviewer harness for PR #538 (verify/selective-scale-down-codex). Additive
// only; touches no production code. See
// docs/verification/selective-scale-down-codex/README.md.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// F2 canary (documents current semantics; flips if the authors scope the
// filter): when committed-membership validation fails,
// validateCommittedMembership drops EVERY pre-existing FailedScale condition —
// including ones with an unrelated reason written by another flow — and
// replaces them with the single InvalidCommittedOrdinals condition.
func TestVerifyF2FailedScaleConditionsReplacedOnInvalidRecord(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()
	set := &workloadsv1alpha2.RoleInstanceSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "set", Namespace: "default", UID: "set-uid",
			Annotations: map[string]string{constants.CommittedSkipOrdinalsAnnotationKey: "3-1"},
		},
		Spec: workloadsv1alpha2.RoleInstanceSetSpec{
			Replicas: ptr.To[int32](3),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "test"}},
		},
		Status: workloadsv1alpha2.RoleInstanceSetStatus{Conditions: []workloadsv1alpha2.RoleInstanceSetCondition{
			// An unrelated, still-meaningful scale-failure record.
			{Type: workloadsv1alpha2.RoleInstanceSetConditionFailedScale, Status: corev1.ConditionTrue, Reason: "ScaleOutFailed", Message: "quota exceeded"},
			// A different condition type that must be preserved.
			{Type: "Progressing", Status: corev1.ConditionTrue, Reason: "Rolling"},
		}},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(set).WithObjects(set).Build()
	r := &RoleInstanceSetReconciler{client: c, recorder: record.NewFakeRecorder(4)}
	_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "set"}})
	require.Error(t, err)
	actual := &workloadsv1alpha2.RoleInstanceSet{}
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(set), actual))
	// CANARY: current behavior deletes the unrelated FailedScale/ScaleOutFailed
	// record. If the authors scope the filter to reason == InvalidCommittedOrdinals
	// (the proposed fix), the two assertions below flip and this test must be
	// inverted to a contract test.
	for _, cond := range actual.Status.Conditions {
		require.NotEqual(t, "ScaleOutFailed", cond.Reason,
			"unrelated FailedScale condition survived: replace semantics changed")
	}
	var found bool
	for _, cond := range actual.Status.Conditions {
		if cond.Type == workloadsv1alpha2.RoleInstanceSetConditionFailedScale && cond.Reason == "InvalidCommittedOrdinals" {
			found = true
		}
	}
	require.True(t, found, "InvalidCommittedOrdinals condition must be reported")
	// Unrelated condition TYPES are preserved; only FailedScale entries are replaced.
	var progressing bool
	for _, cond := range actual.Status.Conditions {
		if cond.Type == "Progressing" {
			progressing = true
		}
	}
	require.True(t, progressing, "non-FailedScale conditions must be preserved")
}
