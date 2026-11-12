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

// Review verification harness for PR #570 (KEP-133). ADDITIVE ONLY.
//
// These tests check the KEP's claims about the CURRENT controller generation
// (the one a cluster would downgrade to), against the real reconciler code.
package workloads

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

func kep133Scheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	_ = workloadsv1alpha2.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	return scheme
}

// --- F3: version skew / downgrade --------------------------------------------
//
// KEP-133 "Version Skew Strategy": "The RBG-level ConfigMap is only written by
// controllers that understand `refine` mode; older controllers ignore it (they
// only manage per-role ConfigMaps)."
// KEP-133 "Upgrade / Downgrade Strategy": "`refine` RBGs keep their RBG-level
// ConfigMap, which the older controller will not overwrite ... does not
// corrupt it."
//
// The immediately-preceding controller generation (current main) DOES manage
// the RBG-level ConfigMap for refine-mode RBGs and rewrites it with the legacy
// full-enumeration schema. This test asserts the KEP's claim (content
// preserved); on current code it FAILS == reproduction.
func TestF3_OlderController_PreservesCompactSharedConfigMap(t *testing.T) {
	scheme := kep133Scheme(t)
	rbg := wrappersv2.BuildBasicRoleBasedGroup("test-rbg", "default").Obj()
	rbg.SetDiscoveryConfigMode(constants.RefineDiscoveryConfigMode)

	const compactDoc = "format: refine\nnamespace: default\ngroup: test-rbg\nroles: []\n"
	existing := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: rbg.Name, Namespace: rbg.Namespace},
		Data:       map[string]string{"config.yaml": compactDoc},
	}
	client := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(rbg, existing).Build()
	reconciler := &RoleBasedGroupReconciler{client: client, scheme: scheme}

	// This is exactly what a downgraded (current-generation) controller does on
	// every reconcile of a refine-mode RBG.
	if err := reconciler.reconcileRefinedDiscoveryConfigMap(context.Background(), rbg); err != nil {
		t.Fatalf("reconcileRefinedDiscoveryConfigMap() error = %v", err)
	}

	cm := &corev1.ConfigMap{}
	if err := client.Get(context.Background(), types.NamespacedName{Name: rbg.Name, Namespace: rbg.Namespace}, cm); err != nil {
		t.Fatalf("get shared configmap error: %v", err)
	}
	if cm.Data["config.yaml"] != compactDoc {
		t.Fatalf("KEP-133 claims older controllers do not overwrite the RBG-level ConfigMap, "+
			"but the current controller rewrote it.\n--- before (compact):\n%s\n--- after (current controller):\n%s",
			compactDoc, cm.Data["config.yaml"])
	}
}

// CANARY pin (passes today): documents what the current controller actually
// writes into the shared ConfigMap for refine-mode RBGs: the legacy
// full-enumeration schema with no `format` marker. This is the content a
// renderer init container (KEP-133) would reject as malformed after a
// downgrade, while pods still carry the renderer template.
func TestF3_CurrentController_WritesLegacySchemaIntoSharedConfigMap(t *testing.T) {
	scheme := kep133Scheme(t)
	rbg := wrappersv2.BuildBasicRoleBasedGroup("test-rbg", "default").Obj()
	rbg.SetDiscoveryConfigMode(constants.RefineDiscoveryConfigMode)

	client := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(rbg).Build()
	reconciler := &RoleBasedGroupReconciler{client: client, scheme: scheme}
	if err := reconciler.reconcileRefinedDiscoveryConfigMap(context.Background(), rbg); err != nil {
		t.Fatalf("reconcileRefinedDiscoveryConfigMap() error = %v", err)
	}
	cm := &corev1.ConfigMap{}
	if err := client.Get(context.Background(), types.NamespacedName{Name: rbg.Name, Namespace: rbg.Namespace}, cm); err != nil {
		t.Fatalf("get shared configmap error: %v", err)
	}
	if strings.Contains(cm.Data["config.yaml"], "format: refine") {
		t.Fatalf("current controller unexpectedly wrote the compact schema")
	}
	var cfg discovery.ClusterConfig
	if err := yaml.Unmarshal([]byte(cm.Data["config.yaml"]), &cfg); err != nil {
		t.Fatalf("shared configmap content does not parse as the legacy ClusterConfig schema: %v", err)
	}
	if len(cfg.Roles) == 0 {
		t.Fatalf("expected full-enumeration content, got: %q", cm.Data["config.yaml"])
	}
	t.Logf("current controller writes legacy full-enumeration schema into the shared ConfigMap")
}

// --- F4: stale "today" description --------------------------------------------
//
// KEP-133 Motivation: "Today the controller creates one ConfigMap per role,
// while the discovery content is identical for every role of the same RBG".
// Goal 1: "One ConfigMap per RBG instead of one per role."
//
// CANARY pin (passes today): on current main, a fresh RBG defaults to refine
// mode and gets exactly one RBG-level ConfigMap named after the RBG; the
// injector mounts that same object for stateful roles regardless of mode.
// Per-role ConfigMaps are only a legacy-detection signal, never created.
func TestF4_SingleSharedConfigMap_AlreadyDefault(t *testing.T) {
	scheme := kep133Scheme(t)
	rbg := wrappersv2.BuildBasicRoleBasedGroup("test-rbg", "default").Obj()

	client := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(rbg).Build()
	reconciler := &RoleBasedGroupReconciler{client: client, scheme: scheme}

	current := &workloadsv1alpha2.RoleBasedGroup{}
	key := types.NamespacedName{Name: rbg.Name, Namespace: rbg.Namespace}
	if err := client.Get(context.Background(), key, current); err != nil {
		t.Fatalf("get rbg error: %v", err)
	}
	if _, err := reconciler.ensureDiscoveryConfigMode(context.Background(), current); err != nil {
		t.Fatalf("ensureDiscoveryConfigMode() error = %v", err)
	}
	if got := current.GetDiscoveryConfigMode(); got != constants.RefineDiscoveryConfigMode {
		t.Fatalf("fresh RBG mode = %q, want %q (KEP Goal 1 is already the default)", got, constants.RefineDiscoveryConfigMode)
	}
	if err := reconciler.reconcileRefinedDiscoveryConfigMap(context.Background(), current); err != nil {
		t.Fatalf("reconcileRefinedDiscoveryConfigMap() error = %v", err)
	}
	cmList := &corev1.ConfigMapList{}
	if err := client.List(context.Background(), cmList); err != nil {
		t.Fatalf("list configmaps error: %v", err)
	}
	if len(cmList.Items) != 1 || cmList.Items[0].Name != rbg.Name {
		names := []string{}
		for _, cm := range cmList.Items {
			names = append(names, cm.Name)
		}
		t.Fatalf("expected exactly one RBG-level ConfigMap named %q, got %v", rbg.Name, names)
	}

	// The injector mounts the RBG-level ConfigMap for stateful roles even in
	// legacy mode - per-role mounts no longer exist.
	legacyRBG := wrappersv2.BuildBasicRoleBasedGroup("legacy-rbg", "default").Obj()
	legacyRBG.SetDiscoveryConfigMode(constants.LegacyDiscoveryConfigMode)
	podTemplate := &corev1.PodTemplateSpec{
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "main", Image: "img"}}},
	}
	injector := discovery.NewDefaultInjector(scheme, client)
	if err := injector.InjectConfig(context.Background(), podTemplate, legacyRBG, &legacyRBG.Spec.Roles[0]); err != nil {
		t.Fatalf("InjectConfig() error = %v", err)
	}
	if len(podTemplate.Spec.Volumes) != 1 ||
		podTemplate.Spec.Volumes[0].ConfigMap == nil ||
		podTemplate.Spec.Volumes[0].ConfigMap.LocalObjectReference.Name != legacyRBG.Name {
		t.Fatalf("legacy-mode stateful role should still mount the RBG-level ConfigMap %q, got %+v",
			legacyRBG.Name, podTemplate.Spec.Volumes)
	}
	if want := "legacy-rbg-test-role"; podTemplate.Spec.Volumes[0].ConfigMap.LocalObjectReference.Name == want {
		t.Fatalf("per-role ConfigMap %q mounted; per-role mounts were removed", want)
	}
	t.Logf("single RBG-level ConfigMap named after the RBG is already the default behavior")
}

var _ = ptr.To[int32]
