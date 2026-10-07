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

// P0 premise check for PR #474 (Reviewer B / Codex), run against the BASE branch
// (merge-base fc201cdf), without the patch. The PR claims: "when the template changes,
// every outdated child is updated within a single reconcile, with no ordering and no
// availability gating". This test pins exactly that: one Reconcile after a template
// change rewrites ALL outdated children at once.

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func TestVerify_P0_BaseUpdatesEveryOutdatedChildInOnePass(t *testing.T) {
	scheme := runtime.NewScheme()
	assert.NoError(t, workloadsv1alpha2.AddToScheme(scheme))

	oldRoles := []workloadsv1alpha2.RoleSpec{{Name: "worker", Replicas: ptr.To(int32(1))}}
	newRoles := []workloadsv1alpha2.RoleSpec{{Name: "worker", Replicas: ptr.To(int32(1)), MinReadySeconds: 10}}

	set := &workloadsv1alpha2.RoleBasedGroupSet{
		ObjectMeta: metav1.ObjectMeta{Name: "s", Namespace: "default"},
		Spec: workloadsv1alpha2.RoleBasedGroupSetSpec{
			Replicas: ptr.To(int32(3)),
			GroupTemplate: workloadsv1alpha2.RoleBasedGroupTemplateSpec{
				Spec: workloadsv1alpha2.RoleBasedGroupSpec{Roles: newRoles},
			},
		},
	}

	objs := []runtime.Object{set}
	for i := 0; i < 3; i++ {
		objs = append(objs, &workloadsv1alpha2.RoleBasedGroup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("s-%d", i),
				Namespace: "default",
				Labels: map[string]string{
					constants.GroupSetNameLabelKey:  "s",
					constants.GroupSetIndexLabelKey: fmt.Sprintf("%d", i),
				},
			},
			Spec: workloadsv1alpha2.RoleBasedGroupSpec{Roles: oldRoles},
			Status: workloadsv1alpha2.RoleBasedGroupStatus{
				Conditions: []metav1.Condition{{
					Type:               string(workloadsv1alpha2.RoleBasedGroupReady),
					Status:             metav1.ConditionTrue,
					Reason:             "Test",
					LastTransitionTime: metav1.Now(),
				}},
			},
		})
	}

	r := &RoleBasedGroupSetReconciler{
		client: fake.NewClientBuilder().WithScheme(scheme).
			WithRuntimeObjects(objs...).
			WithStatusSubresource(&workloadsv1alpha2.RoleBasedGroupSet{}).Build(),
		scheme:   scheme,
		recorder: record.NewFakeRecorder(100),
	}

	_, err := r.Reconcile(
		context.TODO(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "s"}},
	)
	assert.NoError(t, err)

	updated := 0
	for i := 0; i < 3; i++ {
		child := &workloadsv1alpha2.RoleBasedGroup{}
		assert.NoError(t, r.client.Get(
			context.Background(), types.NamespacedName{Namespace: "default", Name: fmt.Sprintf("s-%d", i)}, child,
		))
		if len(child.Spec.Roles) == 1 && child.Spec.Roles[0].MinReadySeconds == 10 {
			updated++
		}
	}
	assert.Equal(t, 3, updated,
		"premise P0: on the base branch a single reconcile rewrites every outdated child at once (un-paced, un-gated)")
}
