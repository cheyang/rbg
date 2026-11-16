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

// PR #591 verification harness — mechanism claims (PR head only).
//
// M1: utils.ApplyObjectConfiguration (ApplySpec) returns the object as
//     populated by the API server — UID, resourceVersion and defaulted
//     fields are present, so callers can build the Service ownerRef
//     without re-reading through the informer cache.
// M2: the exact conversion used at roleinstanceset_reconciler.go
//     (`FromUnstructured(applied.Object, ...)`) preserves the server
//     identity in the typed workload object handed to the Service
//     reconciler.
//
// These tests reference symbols introduced by the PR
// (`utils.ApplyObjectConfiguration`), so they only compile on the PR head
// and its descendants; re-verify grafts them onto the current PR head.

import (
	"context"
	"path/filepath"
	goruntime "runtime"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/pkg/utils"
	wrappers "sigs.k8s.io/rbgs/test/wrappers/v1alpha2"
)

func TestPR591ApplyResponseCarriesServerIdentity(t *testing.T) {
	_, file, _, _ := goruntime.Caller(0)
	env := &envtest.Environment{
		CRDDirectoryPaths:  []string{filepath.Join(filepath.Dir(file), "../../config/crd/bases")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := env.Start()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, env.Stop()) })

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	require.NoError(t, err)
	ctx := context.Background()
	require.NoError(t, c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "pr591-mech"}}))

	role := wrappers.BuildStandaloneRole("worker").WithReplicas(2).Obj()
	parent := wrappers.BuildBasicRoleBasedGroup("group", "pr591-mech").
		WithRoles([]workloadsv1alpha2.RoleSpec{role}).Obj()
	require.NoError(t, c.Create(ctx, parent))
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(parent), parent))

	key := client.ObjectKey{Name: parent.GetWorkloadName(&parent.Spec.Roles[0]), Namespace: parent.Namespace}

	// Create a valid child first through the reconciler itself.
	r := NewRoleInstanceSetReconciler(scheme, c)
	r.SetAPIReader(c)
	parent.SetGroupVersionKind(workloadsv1alpha2.GroupVersion.WithKind("RoleBasedGroup"))
	require.NoError(t, r.Reconciler(ctx, parent, &parent.Spec.Roles[0], nil, "stable-hash"))
	old := &workloadsv1alpha2.RoleInstanceSet{}
	require.NoError(t, c.Get(ctx, key, old))

	// M1: the apply response is the committed server object. Use the
	// reconciler's own apply-config builder so the request is realistic.
	applyConfig, err2 := r.constructRoleInstanceSetApplyConfiguration(ctx, parent, &parent.Spec.Roles[0], nil, "stable-hash", old)
	require.NoError(t, err2)
	applied, err := utils.ApplyObjectConfiguration(ctx, c, applyConfig, utils.ApplySpec)
	require.NoError(t, err)
	require.NotNil(t, applied)

	live := &workloadsv1alpha2.RoleInstanceSet{}
	require.NoError(t, c.Get(ctx, key, live))
	require.NotEmpty(t, live.UID)

	uid, found := nestedString(applied.Object, "metadata", "uid")
	require.True(t, found, "applied response must carry metadata.uid")
	require.Equal(t, string(live.UID), uid)
	rv, found := nestedString(applied.Object, "metadata", "resourceVersion")
	require.True(t, found, "applied response must carry metadata.resourceVersion")
	require.Equal(t, live.ResourceVersion, rv, "applied resourceVersion must match the live object")
	require.Equal(t, int64(2), nestedInt64(t, applied.Object, "spec", "replicas"))

	// M2: the exact typed conversion used by the reconciler preserves identity.
	typed := &workloadsv1alpha2.RoleInstanceSet{}
	require.NoError(t, runtime.DefaultUnstructuredConverter.FromUnstructured(applied.Object, typed))
	require.Equal(t, live.UID, typed.UID)
	require.Equal(t, live.ResourceVersion, typed.ResourceVersion)
	require.EqualValues(t, 2, *typed.Spec.Replicas)
	require.NotEmpty(t, typed.ManagedFields, "applied response must carry managedFields")
	managerSeen := false
	for _, mf := range typed.ManagedFields {
		if mf.Manager == utils.FieldManager {
			managerSeen = true
		}
	}
	require.True(t, managerSeen, "the rbg field manager must own fields after the apply")
}

func nestedString(obj map[string]interface{}, fields ...string) (string, bool) {
	cur := obj
	for i, f := range fields {
		next, ok := cur[f].(map[string]interface{})
		if i == len(fields)-1 {
			v, ok := cur[f].(string)
			return v, ok
		}
		if !ok {
			return "", false
		}
		cur = next
	}
	return "", false
}

func nestedInt64(t *testing.T, obj map[string]interface{}, fields ...string) int64 {
	t.Helper()
	cur := obj
	for i, f := range fields {
		if i == len(fields)-1 {
			require.IsType(t, int64(0), cur[f])
			return cur[f].(int64)
		}
		next, ok := cur[f].(map[string]interface{})
		require.True(t, ok)
		cur = next
	}
	return 0
}
