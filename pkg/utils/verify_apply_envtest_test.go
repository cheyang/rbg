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

package utils

// Verification harness for PR #591 (reviewer: codex). Additive test file only.
//
// Claims under test (polarity: contract), against a real API server (envtest):
//
//  1. ApplyObjectConfiguration returns the object as populated by the API
//     server: UID, resourceVersion and persisted spec are present in the
//     returned *unstructured.Unstructured. The whole PR depends on this —
//     reconcileHeadlessService builds the Service owner reference from it.
//  2. A metadata.resourceVersion carried in the apply configuration remains a
//     concurrency precondition through the new client.Apply() path: a stale
//     resourceVersion must produce a Conflict. RoleInstanceSet selective
//     membership relies on this to invalidate ordinary updates that race a
//     membership commit.
//
// Skipped when KUBEBUILDER_ASSETS is not set.

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	coreapplyv1 "k8s.io/client-go/applyconfigurations/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

func verifyEnvtestClient(t *testing.T) (client.Client, func()) {
	t.Helper()
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS not set; skipping envtest layer")
	}
	environment := &envtest.Environment{}
	cfg, err := environment.Start()
	require.NoError(t, err)
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	require.NoError(t, err)
	require.NoError(t, c.Create(context.Background(), &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "verify-apply"}}))
	return c, func() { require.NoError(t, environment.Stop()) }
}

func TestVerifyApplyReturnsServerPopulatedObject(t *testing.T) {
	ctx := context.Background()
	c, stop := verifyEnvtestClient(t)
	defer stop()

	svcConfig := coreapplyv1.Service("verify-svc", "verify-apply").
		WithSpec(coreapplyv1.ServiceSpec().
			WithType(corev1.ServiceTypeClusterIP).
			WithClusterIP(corev1.ClusterIPNone).
			WithSelector(map[string]string{"app": "verify"}))

	applied, err := ApplyObjectConfiguration(ctx, c, svcConfig, ApplySpec)
	require.NoError(t, err)
	require.NotNil(t, applied)

	uid := applied.GetUID()
	rv := applied.GetResourceVersion()
	assert.NotEmpty(t, uid, "the returned object must carry the server-assigned UID")
	assert.NotEmpty(t, rv, "the returned object must carry the server resourceVersion")
	assert.Equal(t, "verify-svc", applied.GetName())

	// Server-side defaulting must be visible in the returned object, proving it
	// is the server response and not the request echoed back.
	spec, found, err := unstructured.NestedMap(applied.Object, "spec")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "ClusterIP", spec["type"])

	// A second apply of a changed field must advance the resourceVersion.
	svcConfig2 := coreapplyv1.Service("verify-svc", "verify-apply").
		WithLabels(map[string]string{"verify": "second"}).
		WithSpec(coreapplyv1.ServiceSpec().
			WithType(corev1.ServiceTypeClusterIP).
			WithClusterIP(corev1.ClusterIPNone).
			WithSelector(map[string]string{"app": "verify"}))
	applied2, err := ApplyObjectConfiguration(ctx, c, svcConfig2, ApplySpec)
	require.NoError(t, err)
	assert.Equal(t, uid, applied2.GetUID(), "UID is stable across applies")
	assert.NotEqual(t, rv, applied2.GetResourceVersion(), "resourceVersion advances on change")
}

func TestVerifyApplyHonorsResourceVersionPrecondition(t *testing.T) {
	ctx := context.Background()
	c, stop := verifyEnvtestClient(t)
	defer stop()

	svcConfig := coreapplyv1.Service("verify-rv-svc", "verify-apply").
		WithSpec(coreapplyv1.ServiceSpec().
			WithType(corev1.ServiceTypeClusterIP).
			WithClusterIP(corev1.ClusterIPNone).
			WithSelector(map[string]string{"app": "verify"}))
	applied, err := ApplyObjectConfiguration(ctx, c, svcConfig, ApplySpec)
	require.NoError(t, err)
	committedRV := applied.GetResourceVersion()

	// A concurrent writer moves the object ahead.
	bump := coreapplyv1.Service("verify-rv-svc", "verify-apply").
		WithLabels(map[string]string{"bump": "true"}).
		WithSpec(coreapplyv1.ServiceSpec().
			WithType(corev1.ServiceTypeClusterIP).
			WithClusterIP(corev1.ClusterIPNone).
			WithSelector(map[string]string{"app": "verify"}))
	_, err = ApplyObjectConfiguration(ctx, c, bump, ApplySpec)
	require.NoError(t, err)

	// Applying with the now-stale resourceVersion must conflict, through the
	// same client.Apply() path the RoleInstanceSet reconciler now uses.
	stale := coreapplyv1.Service("verify-rv-svc", "verify-apply").
		WithResourceVersion(committedRV).
		WithLabels(map[string]string{"stale": "write"}).
		WithSpec(coreapplyv1.ServiceSpec().
			WithType(corev1.ServiceTypeClusterIP).
			WithClusterIP(corev1.ClusterIPNone).
			WithSelector(map[string]string{"app": "verify"}))
	_, err = ApplyObjectConfiguration(ctx, c, stale, ApplySpec)
	require.Error(t, err, "stale resourceVersion must not be applied")
	assert.True(t, apierrors.IsConflict(err), "expected Conflict, got: %v", err)

	svc := &corev1.Service{}
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: "verify-rv-svc", Namespace: "verify-apply"}, svc))
	assert.NotContains(t, svc.Labels, "stale", "the stale write must not land")
}
