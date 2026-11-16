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

// Premise check for PR #591 (P0), run against the BASE branch (without the
// patch). Polarity: premise reproduction — this test documents the claimed
// symptom, so it EXPECTS the failure mode on base.
//
// Claimed symptom (PR body): the headless Service reconcile reads the workload
// from the informer cache immediately after creating/applying it, so a cache
// that has not observed the write yet breaks or delays the reconcile.
//
// Simulation: a client whose workload Gets always miss (stale informer cache)
// while writes and all other reads go straight to the backing store.

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

func TestVerifyPremiseStaleCacheBreaksServiceReconcileOnBase(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))

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
	r.SetAPIReader(backend) // uncached membership reads see the store, like production

	err := r.Reconciler(ctx, rbg, &rbg.Spec.Roles[0], nil, "stable-hash")
	require.Error(t, err, "base: a stale workload cache must break the same reconcile that created the workload")
	assert.True(t, apierrors.IsNotFound(err), "base: the failure is the stale cache read, got: %v", err)

	// The workload was really created; only the Service is missing.
	workload := &workloadsv1alpha2.RoleInstanceSet{}
	require.NoError(t, backend.Get(ctx, types.NamespacedName{
		Name: rbg.GetWorkloadName(&rbg.Spec.Roles[0]), Namespace: rbg.Namespace}, workload))
	svcName, svcErr := utils.GetCompatibleHeadlessServiceName(ctx, backend, rbg, &rbg.Spec.Roles[0])
	require.NoError(t, svcErr)
	svc := &corev1.Service{}
	getErr := backend.Get(ctx, types.NamespacedName{Name: svcName, Namespace: rbg.Namespace}, svc)
	assert.True(t, apierrors.IsNotFound(getErr), "base: no headless service is written while the workload cache is stale")
	t.Logf("P0 reproduced on base: Reconciler error = %v", err)
}
