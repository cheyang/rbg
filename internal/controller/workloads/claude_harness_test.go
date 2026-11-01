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

// F4 (reviewer A harness, PR #538): an invalid committed-skip record must not
// erase unrelated FailedScale status conditions.
//
// statelessmode/reconciler.go appends RoleInstanceSetConditionFailedScale with
// an empty Reason whenever a real scale fails. validateCommittedMembership
// drops EVERY FailedScale condition while a record is invalid and, when the
// record becomes valid again, removes only the "InvalidCommittedOrdinals"
// one — so the genuine scale-failure signal is silently lost forever.
//
// polarity: contract — FAILS on the PR head (07ae1194), captured in
// results/unit-head.out.
func TestClaudeInvalidRecordPreservesUnrelatedFailedScaleCondition(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()
	set := &workloadsv1alpha2.RoleInstanceSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "claude-set", Namespace: "default", UID: "set-uid",
			Annotations: map[string]string{constants.CommittedSkipOrdinalsAnnotationKey: "3-1"},
		},
		Spec: workloadsv1alpha2.RoleInstanceSetSpec{
			Replicas: ptr.To[int32](3),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "test"}},
		},
		Status: workloadsv1alpha2.RoleInstanceSetStatus{
			// A genuine, pre-existing scale failure written by statelessmode:
			// Type=FailedScale, Reason empty, message set.
			Conditions: []workloadsv1alpha2.RoleInstanceSetCondition{{
				Type:               workloadsv1alpha2.RoleInstanceSetConditionFailedScale,
				Status:             corev1.ConditionTrue,
				LastTransitionTime: metav1.Now(),
				Message:            "scale failed: instance claude-set-2 deletion timeout",
			}},
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(set).WithObjects(set).Build()
	recorder := record.NewFakeRecorder(4)
	r := &RoleInstanceSetReconciler{client: c, recorder: recorder}

	// Invalid record: reconcile blocks, condition replaced.
	_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: set.Namespace, Name: set.Name}})
	require.Error(t, err)

	// The operator repairs the record, as documented.
	actual := &workloadsv1alpha2.RoleInstanceSet{}
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(set), actual))
	actual.Annotations[constants.CommittedSkipOrdinalsAnnotationKey] = "1"
	require.NoError(t, c.Update(ctx, actual))
	require.NoError(t, r.validateCommittedMembership(ctx, actual))

	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(set), actual))
	found := false
	for _, condition := range actual.Status.Conditions {
		if condition.Type == workloadsv1alpha2.RoleInstanceSetConditionFailedScale &&
			condition.Message == "scale failed: instance claude-set-2 deletion timeout" {
			found = true
		}
	}
	require.True(t, found,
		"the genuine FailedScale condition was erased by the invalid-record episode and never restored; "+
			"only the InvalidCommittedOrdinals condition should be removed when the record is repaired")
}
