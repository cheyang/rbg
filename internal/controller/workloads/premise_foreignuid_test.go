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

// Reviewer harness (Claude) for PR #500 — "fix: reject child resources controlled
// by a previous same-named parent".
//
// TestP0* are PREMISE contract tests (claim P0). Polarity: contract — they assert
// the intended correct behavior, FAIL on the base branch 56c4953 (that failure is
// the reproduction of the bug the PR claims exists) and PASS on the PR head
// d2915172. They stay in the harness as the regression suite for the fix itself:
// in later rounds they must keep passing; a flip means the fix regressed.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	wrappersv2 "sigs.k8s.io/rbgs/test/wrappers/v1alpha2"
)

func p0Set(replicas int32) *workloadsv1alpha2.RoleBasedGroupSet {
	role := wrappersv2.BuildStandaloneRole("worker").Obj()
	return &workloadsv1alpha2.RoleBasedGroupSet{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "default", UID: "new-set-uid"},
		Spec: workloadsv1alpha2.RoleBasedGroupSetSpec{
			Replicas:      ptr.To(replicas),
			GroupTemplate: workloadsv1alpha2.RoleBasedGroupTemplateSpec{Spec: workloadsv1alpha2.RoleBasedGroupSpec{Roles: []workloadsv1alpha2.RoleSpec{role}}},
		},
	}
}

// p0ForeignChild returns a child that carries the set-name/index labels but is
// still controlled by the UID of a deleted, same-named predecessor set — the
// exact state GC lag leaves behind after delete + recreate.
func p0ForeignChild(set *workloadsv1alpha2.RoleBasedGroupSet, index int) *workloadsv1alpha2.RoleBasedGroup {
	oldSet := set.DeepCopy()
	oldSet.UID = "old-set-uid"
	child := newRBGForSet(set, index)
	child.OwnerReferences = []metav1.OwnerReference{
		*metav1.NewControllerRef(oldSet, workloadsv1alpha2.GroupVersion.WithKind("RoleBasedGroupSet")),
	}
	return child
}

// TestP0Set_ForeignChildNotDeletedOnScaleDown: a recreated set scaling down must
// not delete a child still controlled by the predecessor's UID.
func TestP0Set_ForeignChildNotDeletedOnScaleDown(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))

	set := p0Set(0)
	child := p0ForeignChild(set, 0)

	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(set, child).WithObjects(set, child).Build()
	r := &RoleBasedGroupSetReconciler{client: c, scheme: scheme}

	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(set)})
	require.NoError(t, err)

	err = c.Get(context.Background(), client.ObjectKeyFromObject(child), &workloadsv1alpha2.RoleBasedGroup{})
	assert.NoError(t, err, "foreign child must survive scale-down of the recreated set")
}

// TestP0Set_ForeignChildNotCountedReady: a recreated set must not report
// readiness earned by a child still controlled by the predecessor's UID.
func TestP0Set_ForeignChildNotCountedReady(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))

	set := p0Set(1)
	child := p0ForeignChild(set, 0)
	child.Status.Conditions = []metav1.Condition{{
		Type: string(workloadsv1alpha2.RoleBasedGroupReady), Status: metav1.ConditionTrue, Reason: "Ready",
	}}

	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(set, child).WithObjects(set, child).Build()
	r := &RoleBasedGroupSetReconciler{client: c, scheme: scheme}

	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(set)})
	require.NoError(t, err)

	after := &workloadsv1alpha2.RoleBasedGroupSet{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(set), after))
	assert.Equal(t, int32(0), after.Status.ReadyReplicas, "foreign child must not be counted as ready")
}

// TestP0Adapter_ForeignBoundAdapterDoesNotOverrideReplicas: a leftover
// RoleBasedGroupScalingAdapter from a same-named predecessor (Bound phase, stale
// spec.replicas, owner ref pointing at the old UID) must not override the
// recreated RBG's role replicas.
func TestP0Adapter_ForeignBoundAdapterDoesNotOverrideReplicas(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))

	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "default", UID: "new-rbg-uid"},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{
			Roles: []workloadsv1alpha2.RoleSpec{{
				Name:          "worker",
				Replicas:      ptr.To(int32(3)),
				ScalingAdapter: &workloadsv1alpha2.ScalingAdapter{Enable: true},
			}},
		},
	}

	adapterName := workloadsv1alpha2.GenerateScalingAdapterName(rbg.Name, "worker")
	adapter := &workloadsv1alpha2.RoleBasedGroupScalingAdapter{
		ObjectMeta: metav1.ObjectMeta{
			Name:      adapterName,
			Namespace: rbg.Namespace,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "workloads.x-k8s.io/v1alpha2",
				Kind:       "RoleBasedGroup",
				Name:       rbg.Name,
				UID:        "old-rbg-uid",
			}},
		},
		Spec: workloadsv1alpha2.RoleBasedGroupScalingAdapterSpec{
			ScaleTargetRef: &workloadsv1alpha2.AdapterScaleTargetRef{Name: rbg.Name, Role: "worker"},
			Replicas:       ptr.To(int32(9)),
		},
		Status: workloadsv1alpha2.RoleBasedGroupScalingAdapterStatus{
			Phase: constants.AdapterPhaseBound,
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(adapter).Build()
	r := &RoleBasedGroupReconciler{client: c, recorder: record.NewFakeRecorder(16)}

	require.NoError(t, r.applyRBGSAReplicasOverride(context.Background(), rbg))
	require.NotNil(t, rbg.Spec.Roles[0].Replicas)
	assert.Equal(t, int32(3), *rbg.Spec.Roles[0].Replicas,
		"a foreign adapter's stale spec.replicas must not pin the recreated RBG")
}
