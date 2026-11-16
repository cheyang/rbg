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

// PR #591 verification harness — premise claim P0 (contract test).
//
// Claim: on the merge-base, `ServiceReconciler.reconcileHeadlessService`
// re-reads the workload through the *cached* client immediately after the
// same reconcile pass created/applied it. An informer cache that has not yet
// observed the write returns NotFound, so the whole `Reconciler` call errors
// and the headless Service is only created on a later retry.
//
// This file compiles against both the merge-base and the PR head (it only
// uses public APIs present on both), so the same contract test can run on
// either ref:
//   - merge-base: expected to FAIL (red = the reproduction),
//   - PR head:    expected to PASS (the fix reuses the server response).
//
// The `laggingCacheClient` models the manager's informer-backed client:
// writes mark the workload key "not yet observed", Gets of a lagging
// workload return NotFound, and Gets otherwise restore the GVK the way
// informer objects carry it (typed clients strip it).

import (
	"context"
	"encoding/json"
	"path/filepath"
	goruntime "runtime"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	wrappers "sigs.k8s.io/rbgs/test/wrappers/v1alpha2"
)

type laggingCacheClient struct {
	client.Client
	lagging map[client.ObjectKey]struct{}
}

func newLaggingCacheClient(c client.Client) *laggingCacheClient {
	return &laggingCacheClient{Client: c, lagging: map[client.ObjectKey]struct{}{}}
}

// MarkSynced models the informer eventually observing the writes.
func (c *laggingCacheClient) MarkSynced() {
	c.lagging = map[client.ObjectKey]struct{}{}
}

func (c *laggingCacheClient) markWritten(kind, namespace, name string) {
	if kind != "RoleInstanceSet" && kind != "StatefulSet" {
		return
	}
	c.lagging[client.ObjectKey{Namespace: namespace, Name: name}] = struct{}{}
}

func (c *laggingCacheClient) markWrittenObject(obj client.Object) {
	gvk := obj.GetObjectKind().GroupVersionKind()
	kind := gvk.Kind
	if kind == "" {
		if _, ok := obj.(*workloadsv1alpha2.RoleInstanceSet); ok {
			kind = "RoleInstanceSet"
		}
	}
	c.markWritten(kind, obj.GetNamespace(), obj.GetName())
}

func (c *laggingCacheClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if err := c.Client.Create(ctx, obj, opts...); err != nil {
		return err
	}
	c.markWrittenObject(obj)
	return nil
}

func (c *laggingCacheClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	if err := c.Client.Patch(ctx, obj, patch, opts...); err != nil {
		return err
	}
	c.markWrittenObject(obj)
	return nil
}

// Apply is only reached on the PR head (the merge-base uses Patch for SSA).
func (c *laggingCacheClient) Apply(ctx context.Context, obj runtime.ApplyConfiguration, opts ...client.ApplyOption) error {
	if err := c.Client.Apply(ctx, obj, opts...); err != nil {
		return err
	}
	raw, err := json.Marshal(obj)
	if err == nil {
		var body map[string]interface{}
		if json.Unmarshal(raw, &body) == nil {
			metadata, _ := body["metadata"].(map[string]interface{})
			name, _ := metadata["name"].(string)
			namespace, _ := metadata["namespace"].(string)
			kind, _ := body["kind"].(string)
			c.markWritten(kind, namespace, name)
		}
	}
	return nil
}

func (c *laggingCacheClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if c.isLaggingTarget(obj, key) {
		gr := workloadsv1alpha2.GroupVersion.WithResource("roleinstancesets").GroupResource()
		if _, ok := obj.(*appsv1.StatefulSet); ok {
			gr = appsv1.SchemeGroupVersion.WithResource("statefulsets").GroupResource()
		}
		return apierrors.NewNotFound(gr, key.Name)
	}
	if err := c.Client.Get(ctx, key, obj, opts...); err != nil {
		return err
	}
	// Informer objects carry their GVK; typed clients strip it. The real
	// controller reads workloads through the informer cache.
	kinds, _, err := c.Scheme().ObjectKinds(obj)
	if err == nil && len(kinds) > 0 {
		obj.GetObjectKind().SetGroupVersionKind(kinds[0])
	}
	return nil
}

func (c *laggingCacheClient) isLaggingTarget(obj client.Object, key client.ObjectKey) bool {
	switch obj.(type) {
	case *workloadsv1alpha2.RoleInstanceSet, *appsv1.StatefulSet:
		_, lagging := c.lagging[key]
		return lagging
	}
	return false
}

// TestPR591LaggingCacheHeadlessService is the P0 contract test: the public
// Reconciler entry point must create (and keep in sync) the headless Service
// even while the informer cache lags the workload write performed by the
// very same reconcile pass.
func TestPR591LaggingCacheHeadlessService(t *testing.T) {
	_, file, _, _ := goruntime.Caller(0)
	env := &envtest.Environment{
		CRDDirectoryPaths: []string{filepath.Join(filepath.Dir(file), "../../config/crd/bases")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := env.Start()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, env.Stop()) })

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))
	backend, err := client.New(cfg, client.Options{Scheme: scheme})
	require.NoError(t, err)
	ctx := context.Background()
	require.NoError(t, backend.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "pr591-lag"}}))

	wrapped := newLaggingCacheClient(backend)
	r := NewRoleInstanceSetReconciler(scheme, wrapped)
	r.SetAPIReader(backend)

	role := wrappers.BuildStandaloneRole("worker").WithReplicas(2).Obj()
	parent := wrappers.BuildBasicRoleBasedGroup("group", "pr591-lag").
		WithRoles([]workloadsv1alpha2.RoleSpec{role}).Obj()
	require.NoError(t, backend.Create(ctx, parent))
	require.NoError(t, backend.Get(ctx, client.ObjectKeyFromObject(parent), parent))
	parent.SetGroupVersionKind(workloadsv1alpha2.GroupVersion.WithKind("RoleBasedGroup"))

	sync := func() error {
		return r.Reconciler(ctx, parent, &parent.Spec.Roles[0], nil, "stable-hash")
	}
	risKey := client.ObjectKey{Name: parent.GetWorkloadName(&parent.Spec.Roles[0]), Namespace: parent.Namespace}
	liveRIS := func() *workloadsv1alpha2.RoleInstanceSet {
		s := &workloadsv1alpha2.RoleInstanceSet{}
		require.NoError(t, backend.Get(ctx, risKey, s))
		return s
	}
	headlessSvc := func() *corev1.Service {
		list := &corev1.ServiceList{}
		require.NoError(t, backend.List(ctx, list, client.InNamespace(parent.Namespace)))
		for i := range list.Items {
			if list.Items[i].Spec.ClusterIP == corev1.ClusterIPNone {
				return &list.Items[i]
			}
		}
		return nil
	}

	// First sync: the RoleInstanceSet is created in this pass.
	require.NoError(t, sync(), "reconcile must not fail because the informer cache lags the workload create")
	ris := liveRIS()
	svc := headlessSvc()
	require.NotNil(t, svc, "headless Service must exist after the first sync")
	require.NotEmpty(t, ris.UID)
	require.Len(t, svc.OwnerReferences, 1)
	require.Equal(t, ris.UID, svc.OwnerReferences[0].UID, "ownerRef UID must be the live workload UID")
	require.Equal(t, "RoleInstanceSet", svc.OwnerReferences[0].Kind)
	require.Equal(t, workloadsv1alpha2.GroupVersion.String(), svc.OwnerReferences[0].APIVersion)

	// The informer catches up; then the parent scales, so the second sync
	// goes through the apply path and again writes the workload first.
	wrapped.MarkSynced()
	require.NoError(t, backend.Get(ctx, client.ObjectKeyFromObject(parent), parent))
	parent.Spec.Roles[0].Replicas = ptr.To[int32](3)
	require.NoError(t, backend.Update(ctx, parent))
	require.NoError(t, backend.Get(ctx, client.ObjectKeyFromObject(parent), parent))
	parent.SetGroupVersionKind(workloadsv1alpha2.GroupVersion.WithKind("RoleBasedGroup"))
	require.NoError(t, sync(), "reconcile must not fail because the informer cache lags the workload apply")
	require.Equal(t, int32(3), *liveRIS().Spec.Replicas)

	// Selective-scaling path: the conditional writer creates the child and
	// the returned object must still feed the headless Service ownerRef.
	wrapped.MarkSynced()
	require.NoError(t, backend.Get(ctx, client.ObjectKeyFromObject(parent), parent))
	if parent.Spec.Roles[0].Annotations == nil {
		parent.Spec.Roles[0].Annotations = map[string]string{}
	}
	parent.Spec.Roles[0].Annotations[constants.ExcludedOrdinalsAnnotationKey] = "0"
	require.NoError(t, backend.Update(ctx, parent))
	require.NoError(t, backend.Get(ctx, client.ObjectKeyFromObject(parent), parent))
	parent.SetGroupVersionKind(workloadsv1alpha2.GroupVersion.WithKind("RoleBasedGroup"))
	require.NoError(t, sync())
	require.Equal(t, ris.UID, headlessSvc().OwnerReferences[0].UID, "ownerRef UID must stay the live workload UID on the selective path")

	// Determinism: a settled group with a synced cache reconciles cleanly.
	wrapped.MarkSynced()
	require.NoError(t, sync())
}
