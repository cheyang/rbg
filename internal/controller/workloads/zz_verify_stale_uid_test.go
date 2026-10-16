/*
Verification harness for PR #500 review (Reviewer B / Codex).
Additive test file — production code untouched.

Polarity: all tests here are CONTRACT tests. They assert the intended correct
behavior, so they FAIL on the base branch (where the stale-UID bug class is
live) and PASS on the PR head (where it is fixed). See
docs/verification/stale-uid-children-codex/README.md.
*/

package workloads

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/pkg/scale"
	wrappersv2 "sigs.k8s.io/rbgs/test/wrappers/v1alpha2"
)

// P0-a: a recreated RoleBasedGroupSet must not delete a child RBG that is still
// controlled by the previous same-named set's UID when scaling down.
//
// Base behavior (bug): the child is listed purely by the set-name label, its
// index (0) is out of range for replicas=0, so scaleDown deletes it.
// Fixed behavior: the foreign-UID child is skipped and survives.
func TestVerifyPremiseRBGSetForeignChildSurvivesScaleDown(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))

	role := wrappersv2.BuildStandaloneRole("worker").Obj()
	set := &workloadsv1alpha2.RoleBasedGroupSet{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "default", UID: "new-set-uid"},
		Spec: workloadsv1alpha2.RoleBasedGroupSetSpec{
			Replicas:      ptr.To[int32](0), // scaled to zero: every managed child is deleted
			GroupTemplate: workloadsv1alpha2.RoleBasedGroupTemplateSpec{Spec: workloadsv1alpha2.RoleBasedGroupSpec{Roles: []workloadsv1alpha2.RoleSpec{role}}},
		},
	}
	oldSet := set.DeepCopy()
	oldSet.UID = "old-set-uid"
	child := newRBGForSet(set, 0)
	child.OwnerReferences = []metav1.OwnerReference{*metav1.NewControllerRef(oldSet, workloadsv1alpha2.GroupVersion.WithKind("RoleBasedGroupSet"))}

	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(set, child).WithObjects(set, child).Build()
	r := &RoleBasedGroupSetReconciler{client: c, scheme: scheme}

	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(set)})
	require.NoError(t, err)

	err = c.Get(context.Background(), client.ObjectKeyFromObject(child), &workloadsv1alpha2.RoleBasedGroup{})
	assert.NoError(t, err, "contract: a child RBG controlled by a different set UID must survive this set's scale-down")
}

// P0-b: a bound RoleBasedGroupScalingAdapter left behind by a previous
// same-named RBG must not override the new RBG's role replicas.
//
// Base behavior (bug): the override honored any same-named bound adapter, so a
// stale adapter pinned the new RBG to the predecessor's replica count.
// Fixed behavior: only an adapter owned by the current RBG UID may override.
func TestVerifyPremiseForeignBoundAdapterDoesNotOverrideReplicas(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))

	role := wrappersv2.BuildStandaloneRole("worker").Obj()
	role.ScalingAdapter = &workloadsv1alpha2.ScalingAdapter{Enable: true}
	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "default", UID: "new-rbg-uid"},
		Spec:       workloadsv1alpha2.RoleBasedGroupSpec{Roles: []workloadsv1alpha2.RoleSpec{role}},
	}
	adapter := &workloadsv1alpha2.RoleBasedGroupScalingAdapter{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scale.GenerateScalingAdapterName(rbg.Name, "worker"),
			Namespace: rbg.Namespace,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "workloads.x-k8s.io/v1alpha2",
				Kind:       "RoleBasedGroup",
				Name:       rbg.Name,
				UID:        "old-rbg-uid", // leftover of the previous same-named RBG
			}},
		},
		Spec: workloadsv1alpha2.RoleBasedGroupScalingAdapterSpec{
			ScaleTargetRef: &workloadsv1alpha2.AdapterScaleTargetRef{Name: rbg.Name, Role: "worker"},
			Replicas:       ptr.To[int32](5),
		},
		Status: workloadsv1alpha2.RoleBasedGroupScalingAdapterStatus{Phase: constants.AdapterPhaseBound},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(adapter).Build()
	r := &RoleBasedGroupReconciler{client: c, scheme: scheme, recorder: record.NewFakeRecorder(10)}

	require.NoError(t, r.applyRBGSAReplicasOverride(context.Background(), rbg))
	assert.Equal(t, int32(1), *rbg.Spec.Roles[0].Replicas,
		"contract: a bound adapter owned by a different RBG UID must not override role replicas")
}

// P0-c: when scaling is enabled, a same-named adapter not owned by the current
// RBG must be replaced by a fresh adapter carrying this RBG's owner reference
// and no inherited spec.replicas.
//
// Base behavior (bug): the existing adapter was kept (labels synced), so the
// predecessor's owner reference and spec.replicas were inherited.
// Fixed behavior: delete + recreate with a fresh owner reference and clean spec.
func TestVerifyPremiseForeignAdapterRecreatedWithFreshOwner(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))

	role := wrappersv2.BuildStandaloneRole("worker").Obj()
	role.ScalingAdapter = &workloadsv1alpha2.ScalingAdapter{Enable: true}
	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "default", UID: "new-rbg-uid"},
		Spec:       workloadsv1alpha2.RoleBasedGroupSpec{Roles: []workloadsv1alpha2.RoleSpec{role}},
	}
	rbg.APIVersion = "workloads.x-k8s.io/v1alpha2"
	rbg.Kind = "RoleBasedGroup"
	adapter := &workloadsv1alpha2.RoleBasedGroupScalingAdapter{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scale.GenerateScalingAdapterName(rbg.Name, "worker"),
			Namespace: rbg.Namespace,
			Labels: map[string]string{
				constants.GroupNameLabelKey: rbg.Name,
				constants.RoleNameLabelKey:  "worker",
			},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "workloads.x-k8s.io/v1alpha2",
				Kind:       "RoleBasedGroup",
				Name:       rbg.Name,
				UID:        "old-rbg-uid",
			}},
		},
		Spec: workloadsv1alpha2.RoleBasedGroupScalingAdapterSpec{
			ScaleTargetRef: &workloadsv1alpha2.AdapterScaleTargetRef{Name: rbg.Name, Role: "worker"},
			Replicas:       ptr.To[int32](5),
		},
		Status: workloadsv1alpha2.RoleBasedGroupScalingAdapterStatus{Phase: constants.AdapterPhaseBound},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(adapter).Build()
	r := &RoleBasedGroupReconciler{client: c, scheme: scheme, recorder: record.NewFakeRecorder(10)}

	require.NoError(t, r.ReconcileScalingAdapter(context.Background(), rbg, &rbg.Spec.Roles[0]))

	after := &workloadsv1alpha2.RoleBasedGroupScalingAdapter{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: adapter.Name, Namespace: adapter.Namespace}, after))
	assert.True(t, after.ContainsRBGOwner(rbg), "contract: adapter must carry the current RBG UID as owner after reconcile")
	for _, ref := range after.OwnerReferences {
		assert.NotEqual(t, "old-rbg-uid", string(ref.UID), "contract: predecessor owner reference must be gone")
	}
	assert.Nil(t, after.Spec.Replicas, "contract: recreated adapter must not inherit the predecessor's spec.replicas")
}
