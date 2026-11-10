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
// F1 mechanism check (integration layer, real apiserver via envtest):
// DeleteInstance now issues a foreground deletion. envtest runs without
// kube-controller-manager, so no garbage collector ever removes the
// foregroundDeletion finalizer: the RoleInstance object stays terminating
// forever and its stable name stays reserved. This is the mechanism behind
// the observed envtest suite failure (see docs/verification).
//
// Polarity: bug-canary — PASSES while the observed (hazardous-in-envtest)
// behavior holds; FLIPS to fail once deletion no longer pins the object
// (e.g. background propagation is restored, or the suite runs a GC).

package statefulmode

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func TestVerifyForegroundDeletePinsInstanceWithoutGC(t *testing.T) {
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

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "verify-fg"}}
	if err := c.Create(ctx, ns); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatal(err)
	}

	instance := &workloadsv1alpha2.RoleInstance{
		ObjectMeta: metav1.ObjectMeta{Namespace: "verify-fg", Name: "ord-0"},
		Spec: workloadsv1alpha2.RoleInstanceSpec{
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
	stored := &workloadsv1alpha2.RoleInstance{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "verify-fg", Name: "ord-0"}, stored); err != nil {
		t.Fatal(err)
	}

	om := &realStatefulInstanceControlObjectManager{client: c}
	if err := om.DeleteInstance(stored); err != nil {
		t.Fatalf("DeleteInstance: %v", err)
	}

	// Without a garbage collector, a foreground deletion can never complete:
	// the object must remain, terminating, holding the foregroundDeletion
	// finalizer — and the stable name stays reserved.
	deadline := time.Now().Add(3 * time.Second)
	var observed *workloadsv1alpha2.RoleInstance
	for time.Now().Before(deadline) {
		cur := &workloadsv1alpha2.RoleInstance{}
		err := c.Get(ctx, types.NamespacedName{Namespace: "verify-fg", Name: "ord-0"}, cur)
		if err == nil {
			observed = cur
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if apierrors.IsNotFound(err) {
			t.Fatalf("instance object disappeared without a garbage collector; foreground deletion did not pin it")
		}
		t.Fatal(err)
	}
	if observed == nil || observed.DeletionTimestamp == nil {
		t.Fatalf("instance should be terminating after DeleteInstance, got %+v", observed)
	}
	found := false
	for _, f := range observed.Finalizers {
		if f == "foregroundDeletion" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected foregroundDeletion finalizer, got %v", observed.Finalizers)
	}

	// A replacement with the same stable name cannot be created while the old
	// incarnation is pinned.
	dup := instance.DeepCopy()
	dup.ResourceVersion = ""
	if err := c.Create(ctx, dup); !apierrors.IsAlreadyExists(err) {
		t.Fatalf("expected AlreadyExists recreating the pinned name, got %v", err)
	}
	t.Logf("instance pinned: deletionTimestamp=%v finalizers=%v", observed.DeletionTimestamp, observed.Finalizers)
}
