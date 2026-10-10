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

// Package warmupcrdverify is a standalone envtest harness for the review of
// PR #478 (reviewer B / codex). Additive only.
//
// It verifies premise P0-3 of the PR: the item-level CEL rule
// "customized action container image must not be empty" added by #466 is
// re-evaluated on every write of the whole object, and on Kubernetes <= 1.32
// the status subresource strategy validates without ratcheting, so a Warmup
// object that was stored BEFORE the rule existed (missing container image)
// can no longer have its status written at all once the rule is installed.
//
// Scenario (the real upgrade path):
//  1. install the PR-head CRD (no item rule)        -> store an invalid object
//  2. status write succeeds                          (fix leg, before)
//  3. upgrade the CRD to the base version (rule on)  (the #466 upgrade)
//  4. control: fresh invalid creates are rejected    (rule is live)
//  5. premise leg: status write on the pre-existing invalid object
//     - apiserver <= 1.32: must be REJECTED (premise confirmed)
//     - apiserver >= 1.33: accepted (status ratcheting), logged
//  6. restore the PR-head CRD -> status write succeeds (fix leg, after)
//
// Requires KUBEBUILDER_ASSETS (setup-envtest); skipped otherwise.
package warmupcrdverify

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/version"
	"k8s.io/client-go/discovery"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/yaml"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

const (
	ruleMessage = "customized action container image must not be empty"
	crdName     = "rolebasedgroupwarmups.workloads.x-k8s.io"
)

var (
	prCRDPath   = filepath.Join("..", "..", "..", "config", "crd", "bases", "workloads.x-k8s.io_rolebasedgroupwarmups.yaml")
	baseCRDPath = filepath.Join("..", "..", "..", "docs", "verification", "warmup-target-waits", "assets", "warmup-crd-base-with-cel-rule.yaml")
)

func readCRD(t *testing.T, path string) *apiextensionsv1.CustomResourceDefinition {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read CRD %s: %v", path, err)
	}
	crd := &apiextensionsv1.CustomResourceDefinition{}
	if err := yaml.Unmarshal(raw, crd); err != nil {
		t.Fatalf("decode CRD %s: %v", path, err)
	}
	return crd
}

// applyCRD creates or updates the CRD from the given manifest.
func applyCRD(ctx context.Context, t *testing.T, c client.Client, path string) {
	t.Helper()
	desired := readCRD(t, path)
	existing := &apiextensionsv1.CustomResourceDefinition{}
	err := c.Get(ctx, client.ObjectKey{Name: desired.Name}, existing)
	if apierrors.IsNotFound(err) {
		if err := c.Create(ctx, desired); err != nil {
			t.Fatalf("create CRD from %s: %v", path, err)
		}
		return
	}
	if err != nil {
		t.Fatalf("get CRD: %v", err)
	}
	existing.Spec = desired.Spec
	existing.Labels = desired.Labels
	existing.Annotations = desired.Annotations
	if err := c.Update(ctx, existing); err != nil {
		t.Fatalf("update CRD from %s: %v", path, err)
	}
}

// waitCRDReady polls until the CRD is Established and - for schema updates - the
// new validator is actually serving (verified by a probe closure).
func waitCRDReady(ctx context.Context, t *testing.T, c client.Client, probe func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		crd := &apiextensionsv1.CustomResourceDefinition{}
		if err := c.Get(ctx, client.ObjectKey{Name: crdName}, crd); err != nil {
			t.Fatalf("get CRD while waiting for %s: %v", what, err)
		}
		established := false
		for _, cond := range crd.Status.Conditions {
			if cond.Type == apiextensionsv1.Established && cond.Status == apiextensionsv1.ConditionTrue {
				established = true
			}
		}
		if established && (probe == nil || probe()) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for CRD state: %s", what)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func invalidWarmup(name string) *workloadsv1alpha2.RoleBasedGroupWarmup {
	return &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: workloadsv1alpha2.RoleBasedGroupWarmupSpec{
			TargetNodes: &workloadsv1alpha2.TargetNodes{
				NodeNames: []string{"node-1"},
				WarmupActions: workloadsv1alpha2.WarmupActions{
					CustomizedAction: &workloadsv1alpha2.CustomizedAction{
						// No Image: violates the #466 item-level CEL rule
						// (has(self.image) && self.image != '') once that rule exists.
						Containers: []corev1.Container{{Name: "c1"}},
					},
				},
			},
		},
	}
}

func writeStatus(ctx context.Context, t *testing.T, c client.Client, name, phase string) error {
	obj := &workloadsv1alpha2.RoleBasedGroupWarmup{}
	if err := c.Get(ctx, client.ObjectKey{Name: name, Namespace: "default"}, obj); err != nil {
		t.Fatalf("get warmup %s: %v", name, err)
	}
	obj.Status.Phase = workloadsv1alpha2.WarmupJobPhase(phase)
	obj.Status.Conditions = []metav1.Condition{{
		Type:               "VerifyProbe",
		Status:             metav1.ConditionTrue,
		Reason:             "Probe",
		Message:            "status write probe at " + time.Now().Format(time.RFC3339Nano),
		LastTransitionTime: metav1.Now(),
	}}
	return c.Status().Update(ctx, obj)
}

func TestWarmupCRDItemLevelCELFreezesStoredInvalidObjects(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS not set; run via setup-envtest")
	}
	ctx := context.Background()

	env := &envtest.Environment{}
	cfg, err := env.Start()
	if err != nil {
		t.Fatalf("start envtest: %v", err)
	}
	defer func() {
		if err := env.Stop(); err != nil {
			t.Logf("stop envtest: %v", err)
		}
	}()

	scheme := runtime.NewScheme()
	if err := apiextensionsv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := workloadsv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}

	disco, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	sv, err := disco.ServerVersion()
	if err != nil {
		t.Fatal(err)
	}
	parsed := version.MustParseGeneric(sv.GitVersion)
	t.Logf("envtest apiserver version: %s", parsed)

	// Step 1: install the PR-head CRD (no item-level CEL image rule).
	applyCRD(ctx, t, c, prCRDPath)
	waitCRDReady(ctx, t, c, nil, "PR CRD established")

	// Step 2: store an object that violates the not-yet-installed rule.
	if err := c.Create(ctx, invalidWarmup("verify-freeze")); err != nil {
		t.Fatalf("create pre-existing invalid warmup under PR CRD: %v", err)
	}

	// Step 3 (fix leg, before): status write works under the PR CRD.
	if err := writeStatus(ctx, t, c, "verify-freeze", "Running"); err != nil {
		t.Fatalf("fix leg (before): status write under PR CRD must succeed, got %v", err)
	}
	t.Log("fix leg (before): status write under PR-head CRD accepted")

	// Step 4: upgrade the CRD to the base (#466) version carrying the rule.
	applyCRD(ctx, t, c, baseCRDPath)
	// Probe until the new validator is live: a fresh invalid create must be rejected.
	waitCRDReady(ctx, t, c, func() bool {
		err := c.Create(ctx, invalidWarmup("verify-probe"))
		if apierrors.IsInvalid(err) && strings.Contains(err.Error(), ruleMessage) {
			return true
		}
		if err == nil {
			// Validator not live yet; clean up and keep waiting.
			_ = c.Delete(ctx, invalidWarmup("verify-probe"))
		}
		return false
	}, "base CRD with CEL rule live")
	t.Log("control: fresh invalid create rejected with the #466 rule message (rule is live)")

	// Step 5 (premise leg): status write on the PRE-EXISTING invalid object.
	err = writeStatus(ctx, t, c, "verify-freeze", "Failed")
	if parsed.Major() == 1 && parsed.Minor() <= 32 {
		if err == nil {
			t.Fatalf("premise REFUTED on %s: status write on a pre-existing invalid object was accepted with the #466 rule installed", parsed)
		}
		if !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), ruleMessage) {
			t.Fatalf("premise: expected Invalid error mentioning %q, got %v", ruleMessage, err)
		}
		t.Logf("premise CONFIRMED on %s: status write on the pre-existing invalid object is rejected: %v", parsed, err)
	} else {
		if err != nil {
			t.Fatalf("on %s status ratcheting should exempt the unchanged spec field, got %v", parsed, err)
		}
		t.Logf("on %s the status write is accepted (status ratcheting wired into the status strategy)", parsed)
	}

	// Step 6 (fix leg, after): restore the PR-head CRD; status write works again.
	applyCRD(ctx, t, c, prCRDPath)
	waitCRDReady(ctx, t, c, func() bool {
		return writeStatus(ctx, t, c, "verify-freeze", "Running") == nil
	}, "PR CRD restored")
	t.Log("fix leg (after): status write accepted again after restoring the PR-head CRD")
}
