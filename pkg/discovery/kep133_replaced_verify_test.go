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

// Verification harness for the review of PR #577 (KEP-133: mark replaced by
// KEP-576). Additive only: the PR is docs-only (keps/133-configmap-refine/*),
// so these tests pin down (a) the factual premise the tombstone asserts about
// the shipped code, (b) that the new kep.yaml status is a value the KEP
// template allows, and (c) the one finding — the tombstone's link target must
// exist in the same tree that carries the tombstone (merge-order dependency
// on PR #578). Production code is untouched.

package discovery

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/yaml"

	corev1 "k8s.io/api/core/v1"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	wrappersv2 "sigs.k8s.io/rbgs/test/wrappers/v1alpha2"
)

func kep133RepoRoot(t *testing.T) string {
	t.Helper()
	// pkg/discovery -> repo root is two levels up.
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	return root
}

// P0 — premise. The tombstone (and PR body) claims: "KEP-133's compact-format
// proposal was only partially implemented by #134, which shipped a single
// RBG-level ConfigMap carrying the full address enumeration (released in
// v0.7.0/v0.8.0)". The code half of that claim: the shipped `refine` mode
// exists and its builder emits a full per-instance address enumeration, NOT
// the compact {replicas,size,excludes} format KEP-133 proposed.
// Contract test: passes on base and on the PR head (the premise is real).
func TestKEP133_Premise_RefineModeShipsFullEnumeration(t *testing.T) {
	// (a) the refine mode exists in the shipped API surface.
	if constants.RefineDiscoveryConfigMode != "refine" {
		t.Fatalf("RefineDiscoveryConfigMode = %q, want %q", constants.RefineDiscoveryConfigMode, "refine")
	}

	// (b) the shipped builder enumerates every instance address.
	scheme := runtime.NewScheme()
	if err := workloadsv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatalf("add v1alpha2 scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add corev1 scheme: %v", err)
	}
	rbg := wrappersv2.BuildBasicRoleBasedGroup("demo", "default").Obj()
	rbg.Spec.Roles = []workloadsv1alpha2.RoleSpec{
		wrappersv2.BuildStandaloneRole("worker").WithReplicas(3).Obj(),
	}
	client := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(rbg).Build()
	out, err := NewConfigBuilder(client, rbg, nil).Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	t.Logf("shipped refine-mode config.yaml:\n%s", string(out))

	var parsed struct {
		Roles map[string]struct {
			Size      int `yaml:"size"`
			Instances []struct {
				Address string `yaml:"address"`
			} `yaml:"instances"`
		} `yaml:"roles"`
	}
	if err := yaml.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("unmarshal built config: %v", err)
	}
	w, ok := parsed.Roles["worker"]
	if !ok {
		t.Fatalf("roles section missing 'worker'; output:\n%s", string(out))
	}
	// Full enumeration: one address entry per replica (size grows O(replicas)).
	if got, want := len(w.Instances), 3; got != want {
		t.Errorf("worker instances = %d, want %d (full per-instance enumeration)", got, want)
	}
	for i, inst := range w.Instances {
		if inst.Address == "" {
			t.Errorf("instance %d has empty address; full enumeration expected", i)
		}
	}
	// The compact format KEP-133 proposed (roles.<name>.{replicas,size,excludes}
	// with NO per-instance list) is NOT what shipped: every instance is listed.
	compactKeys := regexp.MustCompile(`(?m)^\s+excludes:`).FindString(string(out))
	if compactKeys != "" {
		t.Errorf("shipped output contains compact-format 'excludes:' key; premise says compact was never implemented. Output:\n%s", string(out))
	}
	t.Logf("premise confirmed in code: refine mode ships %d enumerated addresses for 3 replicas; no compact 'excludes' format", len(w.Instances))
}

// Status validity — kep.yaml may only take a status the KEP template allows.
// Contract test: passes on the PR head (status: replaced is template-legal).
func TestKEP133_ReplacedStatusAllowedByTemplate(t *testing.T) {
	root := kep133RepoRoot(t)
	tpl, err := os.ReadFile(filepath.Join(root, "keps", "NNNN-kep-template", "kep.yaml"))
	if err != nil {
		t.Fatalf("read KEP template: %v", err)
	}
	m := regexp.MustCompile(`(?m)^status:\s*(.+)$`).FindSubmatch(tpl)
	if m == nil {
		t.Fatalf("template kep.yaml has no status line")
	}
	allowed := strings.Split(string(m[1]), "|")
	found := false
	for _, a := range allowed {
		if strings.TrimSpace(a) == "replaced" {
			found = true
		}
	}
	if !found {
		t.Fatalf("template status enum %v does not contain 'replaced'", allowed)
	}

	kep, err := os.ReadFile(filepath.Join(root, "keps", "133-configmap-refine", "kep.yaml"))
	if err != nil {
		t.Fatalf("read KEP-133 kep.yaml: %v", err)
	}
	km := regexp.MustCompile(`(?m)^status:\s*(\S+)\s*$`).FindSubmatch(kep)
	if km == nil {
		t.Fatalf("KEP-133 kep.yaml has no status line")
	}
	if string(km[1]) != "replaced" {
		t.Errorf("KEP-133 status = %q, want %q after this PR", string(km[1]), "replaced")
	}
	t.Logf("'replaced' is template-legal (%s) and KEP-133 now uses it", strings.TrimSpace(string(m[1])))
}

// F1 — the README tombstone links to /keps/576-compact-discovery-configmap and
// the kep.yaml status says the KEP is "replaced", but the superseding KEP-576
// directory only exists in the still-open PR #578. On the tree this PR lands
// on by itself, the link target is absent (dead link; "replaced" with no
// replacement present). Contract test: FAILS while keps/576-compact-discovery-configmap
// is missing, PASSES once PR #578 (or an equivalent tree) has landed first —
// i.e. this test encodes the required merge order.
func TestKEP133_TombstoneLinkTargetExists(t *testing.T) {
	root := kep133RepoRoot(t)
	readme, err := os.ReadFile(filepath.Join(root, "keps", "133-configmap-refine", "README.md"))
	if err != nil {
		t.Fatalf("read KEP-133 README: %v", err)
	}
	lm := regexp.MustCompile(`\]\(/keps/([0-9a-z-]+)\)`).FindSubmatch(readme)
	if lm == nil {
		t.Fatalf("no root-relative /keps/ link found in KEP-133 README tombstone")
	}
	target := filepath.Join(root, "keps", string(lm[1]))
	if _, err := os.Stat(filepath.Join(target, "kep.yaml")); err != nil {
		t.Errorf("tombstone link target %q missing kep.yaml: %v\n"+
			"Merge order: PR #578 (KEP-576) must land before PR #577, or the tombstone points into the void.",
			"/keps/"+string(lm[1]), err)
	}
}

