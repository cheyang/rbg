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

package statelessmode

// Verification harness for PR #510, reviewer branch verify/status-phase-conditions-claude.
//
// F-no-churn (contract test, negative control): with a converged
// RoleInstanceSet, a second reconcile must (a) leave the Ready/Progressing
// conditions' LastTransitionTime unchanged and (b) perform zero status
// writes. This guards the "skip no-op status updates" and "preserve
// condition transition times" claims at the REAL call shape (a fresh
// newStatus, as built by syncInstanceSet), which the PR's own table tests
// only cover with a carried-over status object.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func newConvergedSetFixture() (*workloadsv1alpha2.RoleInstanceSet, []*workloadsv1alpha2.RoleInstance) {
	set := &workloadsv1alpha2.RoleInstanceSet{
		ObjectMeta: metav1.ObjectMeta{Name: "ris", Namespace: "default", Generation: 3},
		Spec:       workloadsv1alpha2.RoleInstanceSetSpec{Replicas: ptr.To[int32](1)},
		Status: workloadsv1alpha2.RoleInstanceSetStatus{
			ObservedGeneration:   3,
			CurrentRevision:      "rev",
			UpdateRevision:       "rev",
			Replicas:             1,
			ReadyReplicas:        1,
			UpdatedReplicas:      1,
			UpdatedReadyReplicas: 1,
		},
	}
	instance := &workloadsv1alpha2.RoleInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "ris-0", Namespace: "default", Generation: 3,
			Labels: map[string]string{apps.ControllerRevisionHashLabelKey: "rev"}},
		Status: workloadsv1alpha2.RoleInstanceStatus{
			ObservedGeneration: 3,
			CurrentRevision:    "ri-rev",
			UpdateRevision:     "ri-rev",
			Conditions: []workloadsv1alpha2.RoleInstanceCondition{{
				Type:   workloadsv1alpha2.RoleInstanceReady,
				Status: corev1.ConditionTrue,
			}},
		},
	}
	return set, []*workloadsv1alpha2.RoleInstance{instance}
}

// freshNewStatus mirrors syncInstanceSet: a brand-new status object whose
// Conditions slice is empty before SetStatusConditions runs.
func freshNewStatus(set *workloadsv1alpha2.RoleInstanceSet) *workloadsv1alpha2.RoleInstanceSetStatus {
	ns := &workloadsv1alpha2.RoleInstanceSetStatus{
		ObservedGeneration: set.Generation,
		CurrentRevision:    "rev",
		UpdateRevision:     "rev",
		CollisionCount:     new(int32),
	}
	*ns.CollisionCount = 0
	return ns
}

func TestClaudeStatelessNoChurnOnSecondReconcile(t *testing.T) {
	set, instances := newConvergedSetFixture()
	scheme := runtime.NewScheme()
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))

	updates := 0
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&workloadsv1alpha2.RoleInstanceSet{}).
		WithObjects(set).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourceUpdate: func(ctx context.Context, cli client.Client, sub string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
				updates++
				return cli.SubResource(sub).Update(ctx, obj, opts...)
			},
		}).
		Build()
	updater := &realStatusUpdater{Client: c}

	// First reconcile: seed conditions (Ready=True etc.).
	reconcileOnce := func() *workloadsv1alpha2.RoleInstanceSet {
		live := &workloadsv1alpha2.RoleInstanceSet{}
		require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(set), live))
		ns := freshNewStatus(live)
		require.NoError(t, updater.UpdateInstanceSetStatus(live, ns, instances))
		return live
	}
	reconcileOnce()
	first := &workloadsv1alpha2.RoleInstanceSet{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(set), first))
	var firstReady, firstProgressing *workloadsv1alpha2.RoleInstanceSetCondition
	for i := range first.Status.Conditions {
		switch first.Status.Conditions[i].Type {
		case workloadsv1alpha2.RoleInstanceSetConditionReady:
			firstReady = &first.Status.Conditions[i]
		case workloadsv1alpha2.RoleInstanceSetConditionProgressing:
			firstProgressing = &first.Status.Conditions[i]
		}
	}
	require.NotNil(t, firstReady, "first reconcile must write the Ready condition")
	require.NotNil(t, firstProgressing, "first reconcile must write the Progressing condition")
	require.Equal(t, corev1.ConditionTrue, firstReady.Status)
	require.Equal(t, workloadsv1alpha2.RoleInstanceSetReasonAllInstancesReady, firstReady.Reason)
	require.False(t, firstReady.LastTransitionTime.IsZero())
	time.Sleep(1100 * time.Millisecond) // > clock granularity so a reset would be observable

	// Second reconcile at the real call shape: fresh newStatus.
	before := updates
	reconcileOnce()
	second := &workloadsv1alpha2.RoleInstanceSet{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(set), second))
	require.Equal(t, before, updates, "F-no-churn: converged steady state must not issue a status write on the second reconcile")
	var secondReady, secondProgressing *workloadsv1alpha2.RoleInstanceSetCondition
	for i := range second.Status.Conditions {
		switch second.Status.Conditions[i].Type {
		case workloadsv1alpha2.RoleInstanceSetConditionReady:
			secondReady = &second.Status.Conditions[i]
		case workloadsv1alpha2.RoleInstanceSetConditionProgressing:
			secondProgressing = &second.Status.Conditions[i]
		}
	}
	require.NotNil(t, secondReady)
	require.NotNil(t, secondProgressing)
	require.Equal(t, firstReady.LastTransitionTime, secondReady.LastTransitionTime,
		"F-no-churn: Ready LastTransitionTime must be preserved when condition status is unchanged")
	require.Equal(t, firstProgressing.LastTransitionTime, secondProgressing.LastTransitionTime,
		"F-no-churn: Progressing LastTransitionTime must be preserved when condition status is unchanged")
}
