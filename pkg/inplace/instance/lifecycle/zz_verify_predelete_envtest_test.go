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

// Verification harness for PR #558 review (Reviewer B / Codex).
// Additive test file only; production code is untouched.
//
// F2 integration-layer confirmation: the first PreparingDelete transition with
// MarkNotReady=true conflicts against a REAL apiserver (not just the fake
// client), because executeRoleInstanceNotReadyPolicy bumps the resourceVersion
// before the optimistic-lock patch is computed from the stale cached object.
//
// Polarity: contract — fails on the PR head, passes once the stale-RV write
// ordering is fixed.

package lifecycle

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func TestVerifyPreDeleteMarkNotReadySingleCallSucceedsEnvtest(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS not set; skipping envtest layer")
	}
	env := &envtest.Environment{
		CRDDirectoryPaths: []string{filepath.Join("..", "..", "..", "..", "config", "crd", "bases")},
	}
	cfg, err := env.Start()
	if err != nil {
		t.Fatalf("start envtest: %v", err)
	}
	defer func() { _ = env.Stop() }()

	scheme := runtime.NewScheme()
	if err := workloadsv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "verify-mnr"}}
	if err := c.Create(ctx, ns); err != nil && !apierrorsIsAlreadyExists(err) {
		t.Fatal(err)
	}

	instance := &workloadsv1alpha2.RoleInstance{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "verify-mnr",
			Name:      "hooked-0",
			Labels:    map[string]string{"consumer.example/held": "true"},
		},
		Spec: workloadsv1alpha2.RoleInstanceSpec{
			ReadinessGates: []workloadsv1alpha2.RoleInstanceReadinessGate{
				{ConditionType: workloadsv1alpha2.RoleInstanceCustomReady},
			},
			Components: []workloadsv1alpha2.RoleInstanceComponent{{
				Name: "main",
				Size: ptr.To(int32(1)),
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{{Name: "main", Image: "rbg:latest"}},
					},
				},
			}},
		},
	}
	if err := c.Create(ctx, instance); err != nil {
		t.Fatal(err)
	}

	// what the controller holds: a cached copy delivered by the informer
	cached := &workloadsv1alpha2.RoleInstance{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "verify-mnr", Name: "hooked-0"}, cached); err != nil {
		t.Fatal(err)
	}

	lc := New(c)
	if _, _, err := lc.UpdateRoleInstanceLifecycle(cached, constants.RoleInstanceLifecycleStatePreparingDelete, true); err != nil {
		t.Fatalf("first UpdateRoleInstanceLifecycle(markNotReady=true) returned error: %v", err)
	}
	stored := &workloadsv1alpha2.RoleInstance{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "verify-mnr", Name: "hooked-0"}, stored); err != nil {
		t.Fatal(err)
	}
	if got := GetRoleInstanceLifecycleState(stored); got != constants.RoleInstanceLifecycleStatePreparingDelete {
		t.Fatalf("lifecycle state after first call = %q, want %q", got, constants.RoleInstanceLifecycleStatePreparingDelete)
	}
}

func apierrorsIsAlreadyExists(err error) bool {
	return err != nil && strings.Contains(err.Error(), "already exists")
}
