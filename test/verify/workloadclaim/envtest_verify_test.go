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

// Integration-layer (L2) verification for PR #487 against a real API server
// (envtest). ADDITIVE ONLY. No controller manager is started: the tests drive
// the reconciler units and raw server-side applies directly.
//
// Run with:
//   KUBEBUILDER_ASSETS=$(setup-envtest use 1.31.0 -p path) \
//     go test ./test/verify/workloadclaim/ -v
//
// What this layer proves that the fake-client unit layer cannot:
//   - the API server enforces the single-controller ownerReference rule, so on
//     the BASE branch the new incarnation's SSA apply over a foreign-controlled
//     leftover fails with a 422 (the write path was already broken; only the
//     status/readiness reads silently inherited), and
//   - an orphan (no controller ref) IS adoptable via SSA on the base branch,
//     which is the behavior PR #487 removes (finding F1).

package workloadclaim

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/pkg/reconciler"
	wrappersv2 "sigs.k8s.io/rbgs/test/wrappers/v1alpha2"
)

var testClient client.Client

func TestMain(m *testing.M) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		// Unit layer still runs without envtest; this package is no-op then.
		os.Exit(0)
	}
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)
	_ = workloadsv1alpha2.AddToScheme(scheme)

	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := env.Start()
	if err != nil {
		panic(err)
	}
	testClient, err = client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		panic(err)
	}
	code := m.Run()
	_ = env.Stop()
	os.Exit(code)
}

func ownerRef(uid types.UID) metav1.OwnerReference {
	return metav1.OwnerReference{
		APIVersion:         workloadsv1alpha2.GroupVersion.String(),
		Kind:               "RoleBasedGroup",
		Name:               "verify-rbg",
		UID:                uid,
		Controller:         ptr.To(true),
		BlockOwnerDeletion: ptr.To(true),
	}
}

// applyDeployWithOwner mimics one RBG incarnation's server-side apply of the
// role workload: field manager per incarnation, controller ownerRef with the
// incarnation's UID.
func applyDeployWithOwner(ctx context.Context, name, ns, fieldManager string, uid types.UID) error {
	dep := &appsv1.Deployment{
		TypeMeta:   metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(int32(1)),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "verify"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "verify"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: "nginx"}}},
			},
		},
	}
	if uid != "" {
		dep.OwnerReferences = []metav1.OwnerReference{ownerRef(uid)}
	}
	return testClient.Patch(ctx, dep, client.Apply, client.FieldOwner(fieldManager), client.ForceOwnership)
}

func TestVerifyIntegration(t *testing.T) {
	if testClient == nil {
		t.Skip("KUBEBUILDER_ASSETS not set")
	}
	ctx := context.Background()
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "verify-claim"}}
	if err := testClient.Create(ctx, ns); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatal(err)
	}

	oldUID := types.UID("uid-of-previous-rbg-incarnation")
	newUID := types.UID("uid-of-new-rbg-incarnation")

	t.Run("foreign_controlled_leftover_rejects_second_controller_apply", func(t *testing.T) {
		// Base-branch write-path behavior: old incarnation applied the workload
		// with its UID as controller; the new incarnation's apply adds a second
		// controller=true ownerRef and the API server must reject it.
		require.NoError(t, applyDeployWithOwner(ctx, "verify-rbg-worker", ns.Name, "rbg-old", oldUID))
		err := applyDeployWithOwner(ctx, "verify-rbg-worker", ns.Name, "rbg-new", newUID)
		require.Error(t, err, "apiserver must reject two controller=true ownerReferences")
		assert.True(t, apierrors.IsInvalid(err), "expected Invalid, got: %v", err)
		assert.Contains(t, err.Error(), "Only one reference can have Controller set to true")
		t.Logf("observed apiserver rejection (base write-path behavior): %v", err)
	})

	t.Run("orphan_is_adopted_by_ssa_apply", func(t *testing.T) {
		// Base-branch behavior for --cascade=orphan leftovers: no controller
		// ref, so the new incarnation's apply adopts the object cleanly.
		require.NoError(t, applyDeployWithOwner(ctx, "verify-rbg-orphan", ns.Name, "someone-else", ""))
		pre := &appsv1.Deployment{}
		require.NoError(t, testClient.Get(ctx, client.ObjectKey{Namespace: ns.Name, Name: "verify-rbg-orphan"}, pre))
		require.Nil(t, metav1.GetControllerOf(pre), "fixture must start as an orphan")

		require.NoError(t, applyDeployWithOwner(ctx, "verify-rbg-orphan", ns.Name, "rbg-new", newUID))
		post := &appsv1.Deployment{}
		require.NoError(t, testClient.Get(ctx, client.ObjectKey{Namespace: ns.Name, Name: "verify-rbg-orphan"}, post))
		controller := metav1.GetControllerOf(post)
		require.NotNil(t, controller, "orphan should have been adopted by the apply")
		assert.Equal(t, newUID, controller.UID)
	})

	t.Run("head_reconciler_refuses_leftover_without_touching_it", func(t *testing.T) {
		// PR-head behavior wired against a real apiserver: the reconciler must
		// refuse the foreign-controlled leftover with the claimable error and
		// leave the object byte-identical.
		require.NoError(t, applyDeployWithOwner(ctx, "verify-rbg-leftover", ns.Name, "rbg-old", oldUID))
		pre := &appsv1.Deployment{}
		require.NoError(t, testClient.Get(ctx, client.ObjectKey{Namespace: ns.Name, Name: "verify-rbg-leftover"}, pre))

		role := wrappersv2.BuildStandaloneRole("leftover").Obj()
		rbg := wrappersv2.BuildBasicRoleBasedGroup("verify-rbg", ns.Name).
			WithRoles([]workloadsv1alpha2.RoleSpec{role}).Obj()
		rbg.UID = newUID
		rec := reconciler.NewDeploymentReconciler(testClient.Scheme(), testClient)

		err := rec.Reconciler(ctx, rbg, &role, nil, "verify-revision")
		require.Error(t, err)
		assert.True(t, strings.Contains(err.Error(), "not claimable"), "got: %v", err)
		assert.False(t, apierrors.IsInvalid(err),
			"head must fail with the claimable error, not the apiserver 422 base produced")

		status, serr := rec.ConstructRoleStatus(ctx, rbg, &role)
		require.NoError(t, serr)
		assert.Zero(t, status.ReadyReplicas)
		ready, rerr := rec.CheckWorkloadReady(ctx, rbg, &role)
		require.NoError(t, rerr)
		assert.False(t, ready)

		post := &appsv1.Deployment{}
		require.NoError(t, testClient.Get(ctx, client.ObjectKeyFromObject(pre), post))
		assert.Equal(t, pre.ResourceVersion, post.ResourceVersion, "leftover must be untouched")
	})

	t.Run("head_reconciler_wedges_on_orphan", func(t *testing.T) {
		// F1 against a real apiserver: the orphan that L2 proved adoptable above
		// is refused by the head reconciler and left in place forever.
		require.NoError(t, applyDeployWithOwner(ctx, "verify-rbg-stuck", ns.Name, "someone-else", ""))
		pre := &appsv1.Deployment{}
		require.NoError(t, testClient.Get(ctx, client.ObjectKey{Namespace: ns.Name, Name: "verify-rbg-stuck"}, pre))
		require.Nil(t, metav1.GetControllerOf(pre))

		role := wrappersv2.BuildStandaloneRole("stuck").Obj()
		rbg := wrappersv2.BuildBasicRoleBasedGroup("verify-rbg", ns.Name).
			WithRoles([]workloadsv1alpha2.RoleSpec{role}).Obj()
		rbg.UID = newUID
		rec := reconciler.NewDeploymentReconciler(testClient.Scheme(), testClient)

		err := rec.Reconciler(ctx, rbg, &role, nil, "verify-revision")
		require.Error(t, err, "F1: head refuses to adopt the orphan")
		assert.Contains(t, err.Error(), "not claimable")

		post := &appsv1.Deployment{}
		require.NoError(t, testClient.Get(ctx, client.ObjectKeyFromObject(pre), post))
		assert.Equal(t, pre.ResourceVersion, post.ResourceVersion, "orphan is left in place, still unowned")
		assert.Nil(t, metav1.GetControllerOf(post))
	})
}
