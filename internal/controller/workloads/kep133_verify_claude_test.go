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

// KEP-133 review harness (Reviewer A, Claude) — PR #570, claim P0/F1.
//
// The rewritten KEP-133 claims "Today every role of an RBG gets its own
// ConfigMap whose content is a full enumeration ... duplicated across all
// roles of the group", that "New RBGs created after the controller upgrade
// get refine", and that existing RBGs are "never migrated automatically"
// because "older controllers only manage per-role ConfigMaps".
//
// This test runs the actual base-branch controller code (fake client) for a
// freshly created RBG and records what really happens today.

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/pkg/discovery"
	wrappersv2 "sigs.k8s.io/rbgs/test/wrappers/v1alpha2"
	"sigs.k8s.io/yaml"
)

// P0/F1: a brand-new RBG reconciled by the *current* (base) controller:
//  1. defaults to discovery-config-mode=refine (no user opt-in),
//  2. gets exactly ONE RBG-level ConfigMap named after the RBG (no per-role
//     ConfigMaps are created — that duplication was already removed),
//  3. whose content is the FULL enumeration format (ClusterConfig with one
//     entry per pod), i.e. today's "refine" mode is already live but stores
//     enumerated content and mounts it directly (no renderer).
//
// Consequence: when the KEP-133 design is implemented, every RBG created
// since v0.7.0 is already pinned mode=refine and WILL be silently migrated
// (enumeration -> compact content, direct mount -> init-container rendering),
// contradicting the KEP's "never migrated automatically" rollout contract.
func TestKEP133Claude_NewRBGDefaultsToRefineWithSingleEnumerationConfigMap(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = workloadsv1alpha2.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	rbg := wrappersv2.BuildBasicRoleBasedGroup("test-rbg", "default").Obj()
	rbg.Spec.Roles = []workloadsv1alpha2.RoleSpec{
		{
			Name:     "role-a",
			Replicas: ptr.To(int32(3)),
			Annotations: map[string]string{
				constants.RoleWorkloadTypeAnnotationKey: "apps/v1/StatefulSet",
			},
		},
		{
			Name:     "role-b",
			Replicas: ptr.To(int32(2)),
			Annotations: map[string]string{
				constants.RoleWorkloadTypeAnnotationKey: "apps/v1/StatefulSet",
			},
		},
	}

	client := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(rbg).Build()
	reconciler := &RoleBasedGroupReconciler{client: client, scheme: scheme}

	// Step the same two phases Reconcile() runs, in order.
	if requeue, err := reconciler.ensureDiscoveryConfigMode(ctx, rbg); err != nil || requeue {
		t.Fatalf("ensureDiscoveryConfigMode() = (%v, %v)", requeue, err)
	}
	if err := reconciler.reconcileRefinedDiscoveryConfigMap(ctx, rbg); err != nil {
		t.Fatalf("reconcileRefinedDiscoveryConfigMap() error: %v", err)
	}

	// 1. mode defaults to refine for a new RBG, today.
	persisted := &workloadsv1alpha2.RoleBasedGroup{}
	key := types.NamespacedName{Name: rbg.Name, Namespace: rbg.Namespace}
	if err := client.Get(ctx, key, persisted); err != nil {
		t.Fatalf("get rbg: %v", err)
	}
	if got := persisted.GetDiscoveryConfigMode(); got != constants.RefineDiscoveryConfigMode {
		t.Fatalf("new RBG discovery mode = %q, want %q (refine is already the default on the base branch)", got, constants.RefineDiscoveryConfigMode)
	}

	// 2. exactly one ConfigMap, named after the RBG; no per-role ConfigMaps.
	cmList := &corev1.ConfigMapList{}
	if err := client.List(ctx, cmList); err != nil {
		t.Fatalf("list configmaps: %v", err)
	}
	if len(cmList.Items) != 1 {
		t.Fatalf("got %d ConfigMaps, want exactly 1 (RBG-level); per-role ConfigMaps are not created today", len(cmList.Items))
	}
	cm := cmList.Items[0]
	if cm.Name != rbg.Name {
		t.Fatalf("ConfigMap name = %q, want RBG-level name %q", cm.Name, rbg.Name)
	}

	// 3. content is the full enumeration (one Instance entry per pod).
	data := cm.Data["config.yaml"]
	var cfg discovery.ClusterConfig
	if err := yaml.Unmarshal([]byte(data), &cfg); err != nil {
		t.Fatalf("unmarshal configmap data: %v", err)
	}
	if len(cfg.Roles) != 2 {
		t.Fatalf("roles in config = %d, want 2", len(cfg.Roles))
	}
	roleA := cfg.Roles["role-a"]
	if roleA.Size != 3 || len(roleA.Instances) != 3 {
		t.Fatalf("role-a: size=%d instances=%d, want enumerated 3/3 (today's refine mode stores full enumeration, not the compact format)", roleA.Size, len(roleA.Instances))
	}
	for _, inst := range roleA.Instances {
		if !strings.Contains(inst.Address, "test-rbg-role-a-") {
			t.Fatalf("unexpected address %q", inst.Address)
		}
	}
	if strings.Contains(data, "{role_index") || strings.Contains(data, "format: refine") {
		t.Fatalf("content already looks compact; expected enumeration format today")
	}
}
