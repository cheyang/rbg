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

// Integration-layer verification harness for sgl-project/rbg PR #533.
// ADDITIVE ONLY: part of the review harness (branch
// verify/pr-533-updatedreplicas-status-codex), not of the PR under review.
//
// Runs only when KUBEBUILDER_ASSETS is set (envtest binaries). It drives
// updateRBGStatus against a REAL API server to prove the SSA status patch
// persists updatedReplicas server-side (the unit layer only inspects the
// outbound payload).
//
// Polarity: CONTRACT. Fails on base (7894133d), passes on PR head (4ab4a5ee).

package workloads

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func TestVerifyPR533UpdatedReplicasPersistedServerSide(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS not set; skipping envtest integration layer")
	}
	if err := workloadsv1alpha2.AddToScheme(scheme.Scheme); err != nil {
		t.Fatal(err)
	}
	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatalf("start envtest: %v", err)
	}
	defer func() {
		if err := testEnv.Stop(); err != nil {
			t.Errorf("stop envtest: %v", err)
		}
	}()

	c, err := client.New(cfg, client.Options{Scheme: scheme.Scheme})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "verify-rbg-envtest", Namespace: "default"},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{Roles: []workloadsv1alpha2.RoleSpec{
			{Name: "prefill", Replicas: ptr.To(int32(2))},
		}},
	}
	if err := c.Create(ctx, rbg); err != nil {
		t.Fatalf("create rbg: %v", err)
	}

	// Seed a fully-settled status whose only staleness is updatedReplicas=1.
	rbg.Status = workloadsv1alpha2.RoleBasedGroupStatus{
		ObservedGeneration: rbg.Generation,
		Conditions: []metav1.Condition{{
			Type:               string(workloadsv1alpha2.RoleBasedGroupReady),
			Status:             metav1.ConditionTrue,
			ObservedGeneration: rbg.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             "AllRolesReady",
			Message:            "All roles are ready",
		}},
		RoleStatuses: []workloadsv1alpha2.RoleStatus{
			{Name: "prefill", Replicas: 2, ReadyReplicas: 2, UpdatedReplicas: 1},
		},
	}
	if err := c.Status().Update(ctx, rbg); err != nil {
		t.Fatalf("seed status: %v", err)
	}

	// Fresh fetch (cache-free), then reconcile status with only updatedReplicas moved.
	fresh := &workloadsv1alpha2.RoleBasedGroup{}
	if err := c.Get(ctx, types.NamespacedName{Name: rbg.Name, Namespace: rbg.Namespace}, fresh); err != nil {
		t.Fatal(err)
	}
	observed := []workloadsv1alpha2.RoleStatus{
		{Name: "prefill", Replicas: 2, ReadyReplicas: 2, UpdatedReplicas: 2},
	}
	r := &RoleBasedGroupReconciler{client: c}
	if err := r.updateRBGStatus(ctx, fresh, observed); err != nil {
		t.Fatalf("updateRBGStatus: %v", err)
	}

	// Read back from the API server: updatedReplicas must be persisted, not stale.
	got := &workloadsv1alpha2.RoleBasedGroup{}
	if err := c.Get(ctx, types.NamespacedName{Name: rbg.Name, Namespace: rbg.Namespace}, got); err != nil {
		t.Fatal(err)
	}
	if len(got.Status.RoleStatuses) != 1 {
		t.Fatalf("server roleStatuses = %+v, want exactly 1 entry", got.Status.RoleStatuses)
	}
	if ur := got.Status.RoleStatuses[0].UpdatedReplicas; ur != 2 {
		t.Errorf("server-side updatedReplicas = %d, want 2 (stale value persisted: SSA patch never sent)", ur)
	}

	// Second no-change reconcile must not bump resourceVersion (no redundant write).
	rv := got.ResourceVersion
	if err := r.updateRBGStatus(ctx, got, observed); err != nil {
		t.Fatal(err)
	}
	again := &workloadsv1alpha2.RoleBasedGroup{}
	if err := c.Get(ctx, types.NamespacedName{Name: rbg.Name, Namespace: rbg.Namespace}, again); err != nil {
		t.Fatal(err)
	}
	if again.ResourceVersion != rv {
		t.Errorf("no-change reconcile wrote to the API server (resourceVersion %s -> %s)", rv, again.ResourceVersion)
	}
}
