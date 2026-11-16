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

package reconciler

// Verification harness for PR #591 (reviewer: codex). Additive test file only;
// no production code is modified.
//
// Claim under test (head side, polarity: contract):
//   When the informer cache has not yet observed a just-created/applied
//   workload (simulated by a client whose workload Gets always miss), the
//   role reconcile must still succeed and the headless Service must carry the
//   API-server-assigned UID of the fresh workload, because the reconciler
//   passes the workload object returned by the API server instead of
//   re-reading it from the cache.
//
// The base-side counterpart (old signature, reconcileHeadlessService without
// the workload parameter) lives in docs/verification/<topic>/base/ and proves
// the premise: on base the same stale cache makes the reconcile fail with
// NotFound and no Service is written.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/pkg/utils"
	wrappersv2 "sigs.k8s.io/rbgs/test/wrappers/v1alpha2"
)

// staleWorkloadCacheClient simulates an informer cache that has not yet
// observed a workload write: workload kinds always miss, everything else
// (writes, service reads) goes straight to the backing store.
type staleWorkloadCacheClient struct {
	client.Client
}

func (c *staleWorkloadCacheClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	switch obj.(type) {
	case *workloadsv1alpha2.RoleInstanceSet, *appsv1.StatefulSet:
		return apierrors.NewNotFound(workloadsv1alpha2.GroupVersion.WithResource("roleinstancesets").GroupResource(), key.Name)
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

func verifyScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))
	return scheme
}

func TestVerifyRoleInstanceSetReconcileToleratesStaleWorkloadCache(t *testing.T) {
	ctx := context.Background()
	scheme := verifyScheme(t)

	role := wrappersv2.BuildStandaloneRole("worker").Obj()
	rbg := wrappersv2.BuildBasicRoleBasedGroup("group", "default").
		WithRoles([]workloadsv1alpha2.RoleSpec{role}).Obj()
	rbg.APIVersion = workloadsv1alpha2.GroupVersion.String()
	rbg.Kind = "RoleBasedGroup"
	rbg.UID = "rbg-uid"
	rbg.Generation = 1

	backend := fake.NewClientBuilder().WithScheme(scheme).WithObjects(rbg).Build()
	stale := &staleWorkloadCacheClient{Client: backend}

	r := NewRoleInstanceSetReconciler(scheme, stale)
	// Uncached membership reads go straight to the store, as in production.
	r.SetAPIReader(backend)

	require.NoError(t, r.Reconciler(ctx, rbg, &rbg.Spec.Roles[0], nil, "stable-hash"),
		"reconcile must succeed even though the workload cache is stale")

	workload := &workloadsv1alpha2.RoleInstanceSet{}
	require.NoError(t, backend.Get(ctx, types.NamespacedName{
		Name: rbg.GetWorkloadName(&rbg.Spec.Roles[0]), Namespace: rbg.Namespace}, workload))
	// The fake backend does not allocate UIDs; server-assigned UID fidelity is
	// proven against a real API server in TestVerifyReconcileUsesServerIdentity
	// (pkg/utils and docs/verification envtest layer). Here we assert the svc
	// owner reference is byte-identical to the stored workload's identity.

	svcName, err := utils.GetCompatibleHeadlessServiceName(ctx, backend, rbg, &rbg.Spec.Roles[0])
	require.NoError(t, err)
	svc := &corev1.Service{}
	require.NoError(t, backend.Get(ctx, types.NamespacedName{Name: svcName, Namespace: rbg.Namespace}, svc),
		"the headless service must be written in the same reconcile that created the workload")
	require.Len(t, svc.OwnerReferences, 1)
	owner := svc.OwnerReferences[0]
	assert.Equal(t, workload.UID, owner.UID, "owner reference must use the fresh workload UID, not a cached read")
	assert.Equal(t, workload.Name, owner.Name)
	assert.Equal(t, rbg.Spec.Roles[0].GetWorkloadSpec().APIVersion, owner.APIVersion)
	assert.Equal(t, rbg.Spec.Roles[0].GetWorkloadSpec().Kind, owner.Kind)

	// A second reconcile (workload now exists; cache still stale for reads the
	// reconciler performs through the passed object) must stay stable.
	require.NoError(t, r.Reconciler(ctx, rbg, &rbg.Spec.Roles[0], nil, "stable-hash"))
	svcAgain := &corev1.Service{}
	require.NoError(t, backend.Get(ctx, types.NamespacedName{Name: svcName, Namespace: rbg.Namespace}, svcAgain))
	require.Len(t, svcAgain.OwnerReferences, 1)
	assert.Equal(t, workload.UID, svcAgain.OwnerReferences[0].UID)
}

func TestVerifyStatefulSetReconcileToleratesStaleWorkloadCache(t *testing.T) {
	ctx := context.Background()
	scheme := verifyScheme(t)

	role := wrappersv2.BuildStandaloneRole("worker").
		WithWorkload("apps/v1", "StatefulSet").Obj()
	rbg := wrappersv2.BuildBasicRoleBasedGroup("group", "default").
		WithRoles([]workloadsv1alpha2.RoleSpec{role}).Obj()
	rbg.APIVersion = workloadsv1alpha2.GroupVersion.String()
	rbg.Kind = "RoleBasedGroup"
	rbg.UID = "rbg-uid"
	rbg.Generation = 1

	backend := fake.NewClientBuilder().WithScheme(scheme).WithObjects(rbg).Build()
	stale := &staleWorkloadCacheClient{Client: backend}

	r := NewStatefulSetReconciler(scheme, stale)
	require.NoError(t, r.Reconciler(ctx, rbg, &rbg.Spec.Roles[0], nil, "stable-hash"),
		"reconcile must succeed even though the workload cache is stale")

	workload := &appsv1.StatefulSet{}
	require.NoError(t, backend.Get(ctx, types.NamespacedName{
		Name: rbg.GetWorkloadName(&rbg.Spec.Roles[0]), Namespace: rbg.Namespace}, workload))

	svcName, err := utils.GetCompatibleHeadlessServiceName(ctx, backend, rbg, &rbg.Spec.Roles[0])
	require.NoError(t, err)
	svc := &corev1.Service{}
	require.NoError(t, backend.Get(ctx, types.NamespacedName{Name: svcName, Namespace: rbg.Namespace}, svc))
	require.Len(t, svc.OwnerReferences, 1)
	assert.Equal(t, workload.UID, svc.OwnerReferences[0].UID)
	assert.Equal(t, "apps/v1", svc.OwnerReferences[0].APIVersion)
	assert.Equal(t, "StatefulSet", svc.OwnerReferences[0].Kind)
}

// Guard: the returned workload object must never be nil on a successful
// reconcile, or reconcileHeadlessService would dereference it. This pins the
// contract that the refactor relies on.
func TestVerifyReconcileNeverReturnsNilWorkloadOnSuccess(t *testing.T) {
	ctx := context.Background()
	scheme := verifyScheme(t)

	role := wrappersv2.BuildStandaloneRole("worker").Obj()
	rbg := wrappersv2.BuildBasicRoleBasedGroup("group", "default").
		WithRoles([]workloadsv1alpha2.RoleSpec{role}).Obj()
	rbg.APIVersion = workloadsv1alpha2.GroupVersion.String()
	rbg.Kind = "RoleBasedGroup"
	rbg.UID = "rbg-uid"

	backend := fake.NewClientBuilder().WithScheme(scheme).WithObjects(rbg).Build()
	r := NewRoleInstanceSetReconciler(scheme, backend)
	r.SetAPIReader(backend)

	workload, err := r.reconcileRoleInstanceSet(ctx, rbg, &rbg.Spec.Roles[0], nil, "stable-hash")
	require.NoError(t, err)
	require.NotNil(t, workload)

	// Existing workload, ordinary apply path.
	workload, err = r.reconcileRoleInstanceSet(ctx, rbg, &rbg.Spec.Roles[0], nil, "stable-hash")
	require.NoError(t, err)
	require.NotNil(t, workload)
}
