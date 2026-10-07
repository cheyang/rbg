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

// Verification harness for PR #487 (reviewer: codex). ADDITIVE ONLY - no
// production code is touched. These tests deliberately use only the exported,
// pre-existing reconciler API surface (Reconciler / ConstructRoleStatus /
// CheckWorkloadReady / CleanupOrphanedObjs) so the very same file compiles and
// runs on the BASE branch (merge-base 7ed1860c) as well as on the PR head.
// That is what lets one harness both reproduce the premise on base and prove
// the fix / characterize the behavior changes on head.

package reconciler

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	wrappersv2 "sigs.k8s.io/rbgs/test/wrappers/v1alpha2"
)

const (
	verifyOldUID = types.UID("uid-of-previous-rbg-incarnation")
	verifyNewUID = types.UID("uid-of-new-rbg-incarnation")
)

func verifyScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))
	return scheme
}

func verifyRBG(role workloadsv1alpha2.RoleSpec) *workloadsv1alpha2.RoleBasedGroup {
	rbg := wrappersv2.BuildBasicRoleBasedGroup("verify-rbg", "default").
		WithRoles([]workloadsv1alpha2.RoleSpec{role}).Obj()
	rbg.UID = verifyNewUID
	return rbg
}

func verifyControllerRef(rbg *workloadsv1alpha2.RoleBasedGroup, uid types.UID) metav1.OwnerReference {
	return metav1.OwnerReference{
		APIVersion:         workloadsv1alpha2.GroupVersion.String(),
		Kind:               "RoleBasedGroup",
		Name:               rbg.Name,
		UID:                uid,
		Controller:         ptr.To(true),
		BlockOwnerDeletion: ptr.To(true),
	}
}

// verifyLeftoverRIS builds a RoleInstanceSet at the name role maps to, fully
// observed and fully ready, owned by `ownerUID` (pass "" for no controller ref).
func verifyLeftoverRIS(rbg *workloadsv1alpha2.RoleBasedGroup, role *workloadsv1alpha2.RoleSpec, ownerUID types.UID) *workloadsv1alpha2.RoleInstanceSet {
	meta := metav1.ObjectMeta{
		Name:       rbg.GetWorkloadName(role),
		Namespace:  rbg.Namespace,
		Generation: 1,
	}
	if ownerUID != "" {
		meta.OwnerReferences = []metav1.OwnerReference{verifyControllerRef(rbg, ownerUID)}
	}
	return &workloadsv1alpha2.RoleInstanceSet{
		ObjectMeta: meta,
		Spec:       workloadsv1alpha2.RoleInstanceSetSpec{Replicas: ptr.To(int32(1))},
		Status: workloadsv1alpha2.RoleInstanceSetStatus{
			ObservedGeneration: 1, Replicas: 1, ReadyReplicas: 1, UpdatedReplicas: 1,
		},
	}
}

// TestVerifyP0LeftoverReadinessInherited is the premise contract test.
// Polarity: CONTRACT for the fixed behavior. On the BASE branch this test must
// FAIL (the leftover's readyReplicas leaks into the new RBG's status), which is
// the reproduction of the problem the PR claims to solve. On the PR head it
// must PASS.
//
// Claim P0: a RoleInstanceSet left behind by a deleted same-named RBG (still
// controlled by the previous incarnation's UID, fully ready) is read by the new
// RBG as if it were its own: ConstructRoleStatus reports its readyReplicas and
// CheckWorkloadReady returns true, so the RBG can go Ready=True on a workload
// it never created.
func TestVerifyP0LeftoverReadinessInherited(t *testing.T) {
	scheme := verifyScheme(t)
	ctx := context.Background()
	role := wrappersv2.BuildStandaloneRole("worker").Obj()
	rbg := verifyRBG(role)

	leftover := verifyLeftoverRIS(rbg, &role, verifyOldUID)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(leftover).Build()
	rec := NewRoleInstanceSetReconciler(scheme, c)

	status, err := rec.ConstructRoleStatus(ctx, rbg, &role)
	require.NoError(t, err)
	assert.Zero(t, status.ReadyReplicas,
		"P0: readiness of a workload controlled by a previous incarnation leaked into the new RBG's status")
	assert.Zero(t, status.Replicas,
		"P0: replicas of a workload controlled by a previous incarnation leaked into the new RBG's status")

	ready, err := rec.CheckWorkloadReady(ctx, rbg, &role)
	require.NoError(t, err)
	assert.False(t, ready,
		"P0: CheckWorkloadReady trusts a workload controlled by a previous incarnation")

	err = rec.Reconciler(ctx, rbg, &role, nil, "verify-revision")
	require.Error(t, err,
		"P0: the reconciler must refuse to write over a workload controlled by a previous incarnation")

	got := &workloadsv1alpha2.RoleInstanceSet{}
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(leftover), got))
	require.NotNil(t, metav1.GetControllerOf(got))
	assert.Equal(t, verifyOldUID, metav1.GetControllerOf(got).UID,
		"P0: the leftover must not be adopted by the new incarnation")
}

// TestVerifyF1OrphanWorkloadIsAdopted is the F1 CONTRACT test for the
// behavior an orphan-cascade delete + same-name recreate relies on: an
// RIS whose controller reference was stripped (`kubectl delete rbg
// --cascade=orphan`) is adopted by the new same-named RBG's apply.
// On BASE this passes (SSA adds the new controller ref; the apiserver's
// single-controller rule is satisfied because the orphan has none).
// On the PR HEAD this FAILS: checkWorkloadClaimable rejects the orphan and
// nothing ever adopts or removes it, so the RBG wedges with a reconcile error
// on every retry until someone manually deletes a healthy workload.
func TestVerifyF1OrphanWorkloadIsAdopted(t *testing.T) {
	scheme := verifyScheme(t)
	ctx := context.Background()
	role := wrappersv2.BuildStandaloneRole("worker").Obj()
	rbg := verifyRBG(role)

	orphan := verifyLeftoverRIS(rbg, &role, "") // no controller ref at all
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(orphan).Build()
	rec := NewRoleInstanceSetReconciler(scheme, c)

	err := rec.Reconciler(ctx, rbg, &role, nil, "verify-revision")
	require.NoError(t, err,
		"F1: an orphaned workload should be adoptable; reconcile must not wedge on it")

	got := &workloadsv1alpha2.RoleInstanceSet{}
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(orphan), got))
	controller := metav1.GetControllerOf(got)
	require.NotNil(t, controller, "F1: orphan should have been adopted")
	assert.Equal(t, verifyNewUID, controller.UID,
		"F1: orphan should be controlled by the new RBG incarnation after adoption")
}

// TestVerifyF1OrphanWedgesAndSurvivesCleanup is the F1 CANARY documenting the
// PR-head behavior: the orphan is neither adopted nor deleted by any RBG
// mechanism, so the role reports zero and every reconcile errors. It PASSES on
// the PR head (documents the wedge) and must FLIP to fail if the maintainers
// restore adoption or add another recovery path.
func TestVerifyF1OrphanWedgesAndSurvivesCleanup(t *testing.T) {
	scheme := verifyScheme(t)
	ctx := context.Background()
	role := wrappersv2.BuildStandaloneRole("worker").Obj()
	rbg := verifyRBG(role)

	orphan := verifyLeftoverRIS(rbg, &role, "")
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(orphan).Build()
	rec := NewRoleInstanceSetReconciler(scheme, c)

	status, err := rec.ConstructRoleStatus(ctx, rbg, &role)
	require.NoError(t, err)
	assert.Zero(t, status.ReadyReplicas, "orphan contributes no readiness")

	reconcileErr := rec.Reconciler(ctx, rbg, &role, nil, "verify-revision")
	require.Error(t, reconcileErr, "head refuses to manage an orphan")
	assert.Contains(t, reconcileErr.Error(), "not claimable")

	// The only workload-cleanup mechanism, CleanupOrphanedObjs, skips anything
	// not controlled by THIS rbg, so the orphan survives it too.
	require.NoError(t, CleanupOrphanedObjs(ctx, c, rbg,
		workloadsv1alpha2.GroupVersion.WithKind("RoleInstanceSet")))
	got := &workloadsv1alpha2.RoleInstanceSet{}
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(orphan), got),
		"orphan survives cleanup: no controller component ever removes or adopts it")
	assert.Nil(t, metav1.GetControllerOf(got))

	// Second reconcile still errors: the wedge is permanent, not a retryable race.
	require.Error(t, rec.Reconciler(ctx, rbg, &role, nil, "verify-revision"),
		"the wedge does not resolve itself on retry")
}

// TestVerifyF2TerminatingOwnedWorkloadWaits is the F2 CANARY documenting the
// behavior change for a workload that IS controlled by this RBG but is
// terminating (e.g. user deleted the child Deployment/StatefulSet directly, or
// a role was removed and quickly re-added). On BASE the reconciler keeps
// applying over the terminating object and status keeps reporting its last
// (still running) replicas. On the PR HEAD the role reports 0/0 and the
// reconciler errors on every retry until the object is fully gone. It PASSES on
// head and must FLIP if the "terminating" rule is revisited.
func TestVerifyF2TerminatingOwnedWorkloadWaits(t *testing.T) {
	scheme := verifyScheme(t)
	ctx := context.Background()
	role := wrappersv2.BuildStandaloneRole("worker").Obj()
	rbg := verifyRBG(role)

	terminating := verifyLeftoverRIS(rbg, &role, verifyNewUID) // owned by THIS rbg
	terminating.Finalizers = []string{"verify.example.com/hold"}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(terminating).Build()
	rec := NewRoleInstanceSetReconciler(scheme, c)

	// Delete holds the object in terminating state because of the finalizer.
	require.NoError(t, c.Delete(ctx, terminating))
	stuck := &workloadsv1alpha2.RoleInstanceSet{}
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(terminating), stuck))
	require.NotNil(t, stuck.DeletionTimestamp, "fixture must be terminating")

	status, err := rec.ConstructRoleStatus(ctx, rbg, &role)
	require.NoError(t, err)
	assert.Zero(t, status.ReadyReplicas,
		"F2: a still-terminating workload owned by THIS rbg now reports 0 ready (base reported its live replicas)")

	ready, err := rec.CheckWorkloadReady(ctx, rbg, &role)
	require.NoError(t, err)
	assert.False(t, ready)

	err = rec.Reconciler(ctx, rbg, &role, nil, "verify-revision")
	require.Error(t, err, "F2: reconcile errors while the owned workload is terminating")
	assert.True(t, strings.Contains(err.Error(), "terminating"),
		"F2: error should name the terminating condition, got: %v", err)
}
