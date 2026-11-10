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

package lifecycle

// Reviewer A (Claude) verification harness for sgl-project/rbg PR #558,
// finding F2: UpdateRoleInstanceLifecycle with markPodNotReady=true first
// writes the readiness-gate status (bumping resourceVersion) and then issues
// the lifecycle-label patch preconditions on the caller's stale resource
// version, so the announcement self-conflicts and only lands on a later
// reconcile after the informer catches up.
//
// CONTRACT: a single announcement call must succeed.
// Red on the PR head = the reproduction.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// TestVerify558MarkNotReadyAnnounceSingleCall: with a readiness gate present,
// announcing PreparingDelete with MarkNotReady must succeed in ONE call from
// the state the controller observes at reconcile start (cache-fresh object).
// Observed on the PR head: conflict error, because the status write inside
// the same call bumps the resourceVersion the subsequent optimistic-lock
// patch is preconditioned on.
func TestVerify558MarkNotReadyAnnounceSingleCall(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))

	inst := &workloadsv1alpha2.RoleInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "announce-0",
			Namespace:       "default",
			UID:             types.UID("uid-announce-0"),
			ResourceVersion: "1",
			Labels:          map[string]string{},
		},
		Spec: workloadsv1alpha2.RoleInstanceSpec{
			ReadinessGates: []workloadsv1alpha2.RoleInstanceReadinessGate{
				{ConditionType: workloadsv1alpha2.RoleInstanceCustomReady},
			},
		},
		Status: workloadsv1alpha2.RoleInstanceStatus{
			Conditions: []workloadsv1alpha2.RoleInstanceCondition{
				{Type: workloadsv1alpha2.RoleInstanceReady, Status: corev1.ConditionTrue},
				{Type: workloadsv1alpha2.RoleInstanceCustomReady, Status: corev1.ConditionTrue},
			},
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(inst).
		WithStatusSubresource(&workloadsv1alpha2.RoleInstance{}).
		Build()
	ctrl := New(c)

	// The controller holds a cache-fresh copy (RV matches the API server).
	cached := &workloadsv1alpha2.RoleInstance{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(inst), cached))

	updated, _, err := ctrl.UpdateRoleInstanceLifecycle(cached, constants.RoleInstanceLifecycleStatePreparingDelete, true)
	require.NoError(t, err, "a single announcement call must succeed")
	require.True(t, updated)

	got := &workloadsv1alpha2.RoleInstance{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(inst), got))
	require.Equal(t, constants.RoleInstanceLifecycleStatePreparingDelete, GetRoleInstanceLifecycleState(got))
}
