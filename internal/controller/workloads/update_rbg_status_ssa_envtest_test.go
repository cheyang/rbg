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

package workloads

// Reviewer harness (PR #533) — integration layer (L2).
//
// The PR's own unit test (TestUpdateRBGStatusUpdatedReplicas) intercepts
// SubResourcePatch on a fake client, so it can assert the *payload* but cannot
// decide whether a real API server actually persists the SSA patch — in
// particular whether an explicitly-applied zero really clears a previously
// nonzero status.roleStatuses[].updatedReplicas.
//
// This test drives updateRBGStatus (the exact function the PR changed) against
// a real envtest API server and asserts the persisted object:
//
//   - P0/BUG (differential): when only updatedReplicas changes, the persisted
//     value must follow. On the merge-base this test FAILS (value stays stale,
//     no patch is issued); on the PR head it passes.
//   - CONTRACT: an explicit zero is persisted (clears a previous nonzero).
//   - CONTRACT: a no-change reconcile issues no write (resourceVersion stable).
//
// Polarity: contract test for the fixed behavior (red on base, green on head).

import (
	"context"
	"fmt"
	goruntime "runtime"
	"testing"
	"time"

	"os"
	"path/filepath"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func TestUpdateRBGStatusSSAPersistence(t *testing.T) {
	// envtest needs local apiserver/etcd binaries; skip (not fail) without them
	// so `go test ./...` stays runnable on machines without assets.
	assets := os.Getenv("KUBEBUILDER_ASSETS")
	if assets == "" {
		if home, err := os.UserHomeDir(); err == nil {
			candidates, _ := filepath.Glob(filepath.Join(home, ".local/share/kubebuilder-envtest/k8s/*-linux-amd64"))
			if len(candidates) > 0 {
				assets = candidates[len(candidates)-1]
			}
		}
	}
	if assets == "" {
		t.Skip("KUBEBUILDER_ASSETS not set and no ~/.local/share/kubebuilder-envtest binaries found; skipping envtest integration layer")
	}
	t.Setenv("KUBEBUILDER_ASSETS", assets)

	_, thisFile, _, _ := goruntime.Caller(0)
	crdPath := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "config", "crd", "bases")

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{crdPath},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatalf("starting envtest: %v", err)
	}
	defer func() { _ = testEnv.Stop() }()

	testScheme := runtime.NewScheme()
	if err := workloadsv1alpha2.AddToScheme(testScheme); err != nil {
		t.Fatal(err)
	}
	if err := scheme.AddToScheme(testScheme); err != nil {
		t.Fatal(err)
	}
	c, err := client.New(cfg, client.Options{Scheme: testScheme})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	rbgName := fmt.Sprintf("ssa-updated-replicas-%d", time.Now().UnixNano())
	key := client.ObjectKey{Namespace: "default", Name: rbgName}
	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: rbgName, Namespace: "default"},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{Roles: []workloadsv1alpha2.RoleSpec{
			{Name: "prefill", Replicas: ptr.To(int32(2))},
			{Name: "decode", Replicas: ptr.To(int32(1))},
		}},
	}
	if err := c.Create(ctx, rbg); err != nil {
		t.Fatalf("creating RBG: %v", err)
	}
	r := &RoleBasedGroupReconciler{client: c}

	// roleStatuses as the workload reconcilers would observe them.
	observe := func(prefillUpdated int32) []workloadsv1alpha2.RoleStatus {
		return []workloadsv1alpha2.RoleStatus{
			{Name: "prefill", Replicas: 2, ReadyReplicas: 2, UpdatedReplicas: prefillUpdated},
			{Name: "decode", Replicas: 1, ReadyReplicas: 1, UpdatedReplicas: 1},
		}
	}

	// persist runs one "reconcile": fresh read from the API server (as the
	// informer cache would give), updateRBGStatus, then read back what the API
	// server actually kept.
	persist := func(statuses []workloadsv1alpha2.RoleStatus) *workloadsv1alpha2.RoleBasedGroup {
		t.Helper()
		fresh := &workloadsv1alpha2.RoleBasedGroup{}
		if err := c.Get(ctx, key, fresh); err != nil {
			t.Fatalf("getting RBG before update: %v", err)
		}
		if err := r.updateRBGStatus(ctx, fresh, statuses); err != nil {
			t.Fatalf("updateRBGStatus: %v", err)
		}
		persisted := &workloadsv1alpha2.RoleBasedGroup{}
		if err := c.Get(ctx, key, persisted); err != nil {
			t.Fatalf("getting RBG after update: %v", err)
		}
		return persisted
	}
	roleStatus := func(obj *workloadsv1alpha2.RoleBasedGroup, name string) workloadsv1alpha2.RoleStatus {
		t.Helper()
		for _, rs := range obj.Status.RoleStatuses {
			if rs.Name == name {
				return rs
			}
		}
		t.Fatalf("role %q missing from persisted roleStatuses: %+v", name, obj.Status.RoleStatuses)
		return workloadsv1alpha2.RoleStatus{}
	}

	// Step 1: first observation lands (replicas/ready/updated all appear).
	// This step also passes on the merge-base: the first write always went
	// through because Replicas/ReadyReplicas changed from unset.
	got := persist(observe(1))
	if rs := roleStatus(got, "prefill"); rs.UpdatedReplicas != 1 {
		t.Errorf("step1: persisted updatedReplicas = %d, want 1", rs.UpdatedReplicas)
	}

	// Step 2 (the bug being fixed): ONLY updatedReplicas changes
	// (1 -> 2), replicas/ready stay 2/2. The persisted value must follow.
	got = persist(observe(2))
	if rs := roleStatus(got, "prefill"); rs.UpdatedReplicas != 2 {
		t.Errorf("step2 (updatedReplicas-only change): persisted updatedReplicas = %d, want 2 — stale rollout progress on status.roleStatuses", rs.UpdatedReplicas)
	}

	// Step 3 (contract): an explicit zero must clear a previously nonzero
	// value through real SSA (this is what a new revision / scale-to-zero
	// reset looks like).
	got = persist(observe(0))
	if rs := roleStatus(got, "prefill"); rs.UpdatedReplicas != 0 {
		t.Errorf("step3 (reset to zero): persisted updatedReplicas = %d, want 0", rs.UpdatedReplicas)
	}

	// Step 4 (contract): a no-change reconcile must not write — the
	// resourceVersion proves no SSA patch was sent to the API server.
	before := &workloadsv1alpha2.RoleBasedGroup{}
	if err := c.Get(ctx, key, before); err != nil {
		t.Fatal(err)
	}
	got = persist(observe(0))
	if got.ResourceVersion != before.ResourceVersion {
		t.Errorf("step4 (no-op): resourceVersion changed %s -> %s; redundant status write on an unchanged RBG",
			before.ResourceVersion, got.ResourceVersion)
	}
}
