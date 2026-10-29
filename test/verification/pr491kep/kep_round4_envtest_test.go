/*
Round-4 integration layer (L2, envtest) for PR #491, Reviewer B (Codex).
Additive only — no production code is touched.

Claim under test (KEP round 4, "Canonical payload" paragraph): a
ControllerRevision whose stored data bytes are not key-sorted canonical JSON
fails apiserver update validation ("field is immutable") when any
strategic-merge patch re-serializes the object with sorted keys — the patch
flavor the garbage collector uses to orphan built-in dependents
(k8s.io/kubernetes v1.34.2 pkg/controller/garbagecollector/patch.go:92, SMP
first, JSON merge patch fallback only on UnsupportedMediaType). Immutability
itself: pkg/apis/apps/validation/validation.go:366 (ValidateImmutableField on
Data), byte-wise because apiequality.Semantic registers no RawExtension
equality (apimachinery pkg/api/equality/semantic.go).

This test proves the apiserver-side half empirically against a real
kube-apiserver (envtest): SMP and JSON-merge metadata patches against a
ControllerRevision with struct-ordered (unsorted) data are REJECTED as
invalid, while the same patches against a canonically sorted payload SUCCEED
and leave data byte-identical. The GC half (orphan finalizer blocking) follows
from patch.go:92 + orphanDependents error propagation and is source-cited.

Polarity: contract. PASS means the KEP's hazard claim reproduces (and the
canonicalization requirement it motivates is load-bearing). If a future
apiserver stops re-serializing untouched RawExtension bytes, the unsorted
case flips: invert the expectation then.

Requires KUBEBUILDER_ASSETS (skipped otherwise with a clear message).
*/
package pr491kep

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

var (
	r4EnvOnce sync.Once
	r4Env     *envtest.Environment
	r4Cfg     *rest.Config
	r4EnvErr  error
)

func r4EnvtestConfig(t *testing.T) *rest.Config {
	t.Helper()
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS unset: skipping envtest integration layer " +
			"(export $(setup-envtest use -p path), e.g. 1.34.x)")
	}
	r4EnvOnce.Do(func() {
		r4Env = &envtest.Environment{}
		cfg, err := r4Env.Start()
		if err != nil {
			r4EnvErr = err
			return
		}
		r4Cfg = cfg
	})
	if r4EnvErr != nil {
		t.Skipf("envtest start failed (assets present but unusable): %v", r4EnvErr)
	}
	t.Cleanup(func() {
		if r4Env != nil {
			_ = r4Env.Stop()
		}
	})
	return r4Cfg
}

// TestVerify_R4CR_ControllerRevisionDataImmutableUnderPatch proves the round-4
// canonical-payload hazard claim against a real apiserver.
func TestVerify_R4CR_ControllerRevisionDataImmutableUnderPatch(t *testing.T) {
	cfg := r4EnvtestConfig(t)
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("clientset: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// structOrdered mimics json.Marshal of a Go struct whose field order is
	// NOT alphabetical: {"zebra":...,"alpha":...} under a non-sorted outer key
	// order too.
	structOrdered := []byte(`{"zebra":{"b":1,"a":2},"alpha":[3,1],"mid":{"y":"&<>","x":0}}`)
	var probe map[string]interface{}
	if err := json.Unmarshal(structOrdered, &probe); err != nil {
		t.Fatalf("fixture not valid JSON: %v", err)
	}
	sortedCanonical, err := json.Marshal(probe) // encoding/json sorts map keys
	if err != nil {
		t.Fatalf("canonicalize fixture: %v", err)
	}
	if string(sortedCanonical) == string(structOrdered) {
		t.Fatalf("fixture ineffective: struct-ordered payload is already key-sorted")
	}
	t.Logf("fixture: struct-ordered=%s canonical=%s", structOrdered, sortedCanonical)

	mk := func(name string, data []byte) *appsv1.ControllerRevision {
		return &appsv1.ControllerRevision{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Data:       runtime.RawExtension{Raw: data},
			Revision:   1,
		}
	}
	patchSMP := []byte(`{"metadata":{"annotations":{"verify.pr491/r4":"smp"}}}`)
	patchJMP := []byte(`{"metadata":{"annotations":{"verify.pr491/r4":"jmp"}}}`)

	t.Run("struct-ordered payload is rejected under strategic merge patch", func(t *testing.T) {
		name := "r4cr-unsorted-smp"
		cr := mk(name, structOrdered)
		if _, err := cs.AppsV1().ControllerRevisions("default").Create(ctx, cr, metav1.CreateOptions{}); err != nil {
			t.Fatalf("create: %v", err)
		}
		defer cs.AppsV1().ControllerRevisions("default").Delete(ctx, name, metav1.DeleteOptions{})
		_, err := cs.AppsV1().ControllerRevisions("default").Patch(ctx, name, types.StrategicMergePatchType, patchSMP, metav1.PatchOptions{})
		if err == nil {
			t.Fatalf("KEP hazard claim REFUTED by apiserver: SMP metadata patch on struct-ordered data succeeded")
		}
		if !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), "immutable") {
			t.Fatalf("patch failed but not with the predicted immutability error: %v", err)
		}
		t.Logf("observed predicted rejection: %v", err)
	})

	t.Run("struct-ordered payload is rejected under JSON merge patch", func(t *testing.T) {
		name := "r4cr-unsorted-jmp"
		cr := mk(name, structOrdered)
		if _, err := cs.AppsV1().ControllerRevisions("default").Create(ctx, cr, metav1.CreateOptions{}); err != nil {
			t.Fatalf("create: %v", err)
		}
		defer cs.AppsV1().ControllerRevisions("default").Delete(ctx, name, metav1.DeleteOptions{})
		_, err := cs.AppsV1().ControllerRevisions("default").Patch(ctx, name, types.MergePatchType, patchJMP, metav1.PatchOptions{})
		if err == nil {
			t.Fatalf("JMP metadata patch on struct-ordered data succeeded; hazard narrower than KEP states for the JMP flavor")
		}
		if !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), "immutable") {
			t.Fatalf("patch failed but not with the predicted immutability error: %v", err)
		}
		t.Logf("observed predicted rejection: %v", err)
	})

	t.Run("canonical payload survives both patch flavors byte-identical", func(t *testing.T) {
		name := "r4cr-sorted"
		cr := mk(name, sortedCanonical)
		if _, err := cs.AppsV1().ControllerRevisions("default").Create(ctx, cr, metav1.CreateOptions{}); err != nil {
			t.Fatalf("create: %v", err)
		}
		defer cs.AppsV1().ControllerRevisions("default").Delete(ctx, name, metav1.DeleteOptions{})
		for flavor, patch := range map[types.PatchType][]byte{
			types.StrategicMergePatchType: patchSMP,
			types.MergePatchType:          patchJMP,
		} {
			got, err := cs.AppsV1().ControllerRevisions("default").Patch(ctx, name, flavor, patch, metav1.PatchOptions{})
			if err != nil {
				t.Fatalf("%s patch on canonical payload must succeed (KEP canonicalization rationale): %v", flavor, err)
			}
			if string(got.Data.Raw) != string(sortedCanonical) {
				t.Fatalf("%s patch mutated canonical data bytes: got %s want %s", flavor, got.Data.Raw, sortedCanonical)
			}
		}
		t.Logf("canonical payload byte-identical after SMP and JMP metadata patches")
	})
}
