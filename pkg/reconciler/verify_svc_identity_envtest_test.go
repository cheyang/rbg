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

// Verification harness for PR #591 (reviewer: codex). Additive test file only.
//
// Claim under test (polarity: contract), against a real API server (envtest):
// the headless Service written in the same reconcile that creates or applies
// the RoleInstanceSet carries the API-server-assigned UID of that workload in
// its owner reference — on the create path and on the re-apply path alike.
//
// Skipped when KUBEBUILDER_ASSETS is not set.

import (
	"context"
	"os"
	"path/filepath"
	goruntime "runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/pkg/utils"
	wrappersv2 "sigs.k8s.io/rbgs/test/wrappers/v1alpha2"
)

func TestVerifyReconcileUsesServerIdentityForServiceOwnerRef(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS not set; skipping envtest layer")
	}
	_, file, _, _ := goruntime.Caller(0)
	environment := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join(filepath.Dir(file), "../../config/crd/bases")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := environment.Start()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, environment.Stop()) })

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	require.NoError(t, err)
	ctx := context.Background()
	require.NoError(t, c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "verify-svc-identity"}}))

	role := wrappersv2.BuildStandaloneRole("worker").Obj()
	rbg := wrappersv2.BuildBasicRoleBasedGroup("group", "verify-svc-identity").
		WithRoles([]workloadsv1alpha2.RoleSpec{role}).Obj()
	require.NoError(t, c.Create(ctx, rbg))
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(rbg), rbg))
	rbg.SetGroupVersionKind(workloadsv1alpha2.GroupVersion.WithKind("RoleBasedGroup"))

	r := NewRoleInstanceSetReconciler(scheme, c)
	r.SetAPIReader(c)

	// Create path.
	require.NoError(t, r.Reconciler(ctx, rbg, &rbg.Spec.Roles[0], nil, "stable-hash"))

	workload := &workloadsv1alpha2.RoleInstanceSet{}
	require.NoError(t, c.Get(ctx, types.NamespacedName{
		Name: rbg.GetWorkloadName(&rbg.Spec.Roles[0]), Namespace: rbg.Namespace}, workload))
	require.NotEmpty(t, workload.UID)

	svcName, err := utils.GetCompatibleHeadlessServiceName(ctx, c, rbg, &rbg.Spec.Roles[0])
	require.NoError(t, err)
	svc := &corev1.Service{}
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: svcName, Namespace: rbg.Namespace}, svc),
		"the headless service must exist immediately after the first reconcile")
	require.Len(t, svc.OwnerReferences, 1)
	owner := svc.OwnerReferences[0]
	assert.Equal(t, workload.UID, owner.UID,
		"owner reference must carry the server-assigned UID of the just-created workload")
	assert.Equal(t, workload.Name, owner.Name)
	assert.Equal(t, "workloads.x-k8s.io/v1alpha2", owner.APIVersion)
	assert.Equal(t, "RoleInstanceSet", owner.Kind)

	// Re-apply path (workload already exists; ordinary SSA with the
	// resourceVersion precondition through client.Apply()).
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(rbg), rbg))
	rbg.SetGroupVersionKind(workloadsv1alpha2.GroupVersion.WithKind("RoleBasedGroup"))
	require.NoError(t, r.Reconciler(ctx, rbg, &rbg.Spec.Roles[0], nil, "stable-hash"))

	svcAgain := &corev1.Service{}
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: svcName, Namespace: rbg.Namespace}, svcAgain))
	require.Len(t, svcAgain.OwnerReferences, 1)
	assert.Equal(t, workload.UID, svcAgain.OwnerReferences[0].UID,
		"the re-apply path must keep the owner reference pinned to the same workload UID")
}
