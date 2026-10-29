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

// Package verify491r4 is the round-4 integration-layer harness for PR #491
// (KEP-455: RBGSet rolling update). It verifies the KEP's "Canonical payload"
// design claim against a real kube-apiserver:
//
//   - E7a (contract test): a ControllerRevision whose data is already canonical
//     (sorted-keys) JSON survives a strategic-merge patch with data byte-identical —
//     the property the KEP says orphan cascading depends on ("kubectl delete
//     --cascade=orphan" patches dependents to strip ownerReferences).
//   - E7b (mechanism test): a ControllerRevision whose data is in Go struct field
//     order (non-sorted) fails the same patch. The KEP claims the API server
//     re-serializes the whole object with sorted keys on any strategic-merge patch
//     and rejects the resulting data change ("ControllerRevision.data is immutable
//     and compared byte-wise"), so a struct-ordered payload blocks the patch.
//
// envtest runs no kube-controller-manager, so the garbage collector itself is
// not exercised here; the GC half of the claim (orphan finalizer stalls because
// the GC patch is rejected) follows from E7b plus the GC patching dependents
// with strategic-merge on built-in types.
//
// Run: KUBEBUILDER_ASSETS=<envtest assets> go test ./test/envtest/verify491r4/ -count=1 -v
package verify491r4

import (
	"bytes"
	"context"
	"os"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

var (
	testEnv *envtest.Environment
	restCfg *rest.Config
)

func TestMain(m *testing.M) {
	testEnv = &envtest.Environment{}
	cfg, err := testEnv.Start()
	if err != nil {
		panic("envtest start failed: " + err.Error() + " (set KUBEBUILDER_ASSETS)")
	}
	restCfg = cfg
	code := m.Run()
	_ = testEnv.Stop()
	os.Exit(code)
}

func createControllerRevisionRaw(ctx context.Context, cs *kubernetes.Clientset, name, dataJSON string) error {
	raw := `{"apiVersion":"apps/v1","kind":"ControllerRevision","metadata":{"name":"` + name + `","namespace":"default"},"revision":1,"data":` + dataJSON + `}`
	return cs.AppsV1().RESTClient().Post().
		Resource("controllerrevisions").
		Namespace("default").
		Body([]byte(raw)).
		Do(ctx).Error()
}

func getDataBytes(ctx context.Context, cs *kubernetes.Clientset, name string) ([]byte, error) {
	got, err := cs.AppsV1().ControllerRevisions("default").Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return got.Data.Raw, nil
}

func patchControllerRevision(ctx context.Context, cs *kubernetes.Clientset, name string) error {
	_, err := cs.AppsV1().ControllerRevisions("default").Patch(ctx, name, types.StrategicMergePatchType,
		[]byte(`{"metadata":{"labels":{"verify491-probe":"1"}}}`), metav1.PatchOptions{})
	return err
}

func TestVerify491E7CanonicalPayload(t *testing.T) {
	ctx := context.Background()
	cs := kubernetes.NewForConfigOrDie(restCfg)

	// E7a — contract: sorted (canonical) data survives a strategic-merge patch
	// byte-identically, which is what keeps orphan cascading working.
	t.Run("E7a-sorted-payload-byte-stable", func(t *testing.T) {
		name := "e7a-sorted"
		sorted := `{"aa":2,"mm":{"b":false,"q":true},"zz":1}`
		if err := createControllerRevisionRaw(ctx, cs, name, sorted); err != nil {
			t.Fatalf("create: %v", err)
		}
		before, err := getDataBytes(ctx, cs, name)
		if err != nil {
			t.Fatalf("get before patch: %v", err)
		}
		if err := patchControllerRevision(ctx, cs, name); err != nil {
			t.Fatalf("strategic-merge patch on canonical payload failed: %v", err)
		}
		after, err := getDataBytes(ctx, cs, name)
		if err != nil {
			t.Fatalf("get after patch: %v", err)
		}
		if !bytes.Equal(before, after) {
			t.Errorf("E7a CONTRACT FAIL: data bytes changed across strategic-merge patch.\nbefore: %s\nafter:  %s", before, after)
		} else {
			t.Logf("E7a PASS: data byte-identical across SMP (%s)", after)
		}
	})

	// E7b — mechanism: struct-ordered (non-sorted) data must FAIL the same patch,
	// per the KEP's claim ("a struct-ordered payload fails validation on any such
	// patch"). A patch rejection here is the KEP's mechanism CONFIRMED: the API
	// server refuses the data change the re-serialization would cause.
	t.Run("E7b-struct-ordered-payload-rejected", func(t *testing.T) {
		name := "e7b-structorder"
		structOrder := `{"zz":1,"mm":{"q":true,"b":false},"aa":2}`
		if err := createControllerRevisionRaw(ctx, cs, name, structOrder); err != nil {
			t.Fatalf("create: %v", err)
		}
		before, err := getDataBytes(ctx, cs, name)
		if err != nil {
			t.Fatalf("get before patch: %v", err)
		}
		t.Logf("stored struct-ordered data: %s", before)
		patchErr := patchControllerRevision(ctx, cs, name)
		after, err := getDataBytes(ctx, cs, name)
		if err != nil {
			t.Fatalf("get after patch attempt: %v", err)
		}
		switch {
		case patchErr != nil && apierrors.IsForbidden(patchErr):
			t.Logf("E7b CONFIRMED (rejection): SMP rejected with Forbidden (data may not be changed): %v", patchErr)
			if !bytes.Equal(before, after) {
				t.Errorf("E7b: data changed even though the patch was rejected — investigate")
			}
		case patchErr != nil:
			t.Logf("E7b CONFIRMED (rejection, status %s): %v", apierrors.ReasonForError(patchErr), patchErr)
			if !bytes.Equal(before, after) {
				t.Errorf("E7b: data changed even though the patch was rejected — investigate")
			}
		default:
			// The patch was accepted. Either the server re-serialized (bytes
			// changed) — the hazard the KEP describes, but without the rejection
			// it claims — or it preserved the bytes, refuting the mechanism.
			if !bytes.Equal(before, after) {
				t.Errorf("E7b PARTIAL: patch ACCEPTED and data re-serialized (bytes changed)\nbefore: %s\nafter:  %s\nThe byte-instability hazard is real, but the KEP's 'fails validation' claim is inaccurate.", before, after)
			} else {
				t.Errorf("E7b REFUTED: patch ACCEPTED and data bytes preserved — the API server does not re-serialize RawExtension data on strategic-merge patches as the KEP claims")
			}
		}
	})
}
