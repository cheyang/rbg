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

package utils

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Round-2 static contract tests for PR #530 revision 2 (head f8d67fa7).
//
// The PR's second revision turns KEP-284 into the transform-stub design and
// ships a prototype harness in test/podcacheunion (behind //go:build
// podcacheunion). These tests pin the three gaps in that revision that are
// checkable from the repository's own files. Polarity: contract — RED on the
// current head means the gap is present; the tests go green when the author
// closes them.

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate repository root")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repository root not found: %v", err)
	}
	return root
}

func readRepoFile(t *testing.T, parts ...string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(append([]string{repoRoot(t)}, parts...)...))
	if err != nil {
		t.Fatalf("cannot read %s: %v", filepath.Join(parts...), err)
	}
	return string(data)
}

// R2-F1 — the measurement campaign must not be able to escape to a real
// cluster through the caller's environment.
//
// envtest's Environment.Start() with UseExistingCluster == nil falls back to
// the USE_EXISTING_CLUSTER environment variable (vendored
// controller-runtime/pkg/envtest/server.go: useExistingCluster). The fixture
// (test/podcacheunion/envtest_test.go) does not set the field, and
// scripts/transform-campaign.sh does not force the variable, so a contributor
// with USE_EXISTING_CLUSTER=true exported (a common controller-runtime
// convention) runs the whole campaign against whatever cluster the kubeconfig
// points at: it installs the RoleInstance CRD there (Stop() does not remove
// CRDs from an existing cluster), creates up to ~5,000 benchmark Pods per
// shape, and silently drops the intended apiserver feature-gates/advertise
// settings, so the numbers are wrong too. The README instructs
// `export USE_EXISTING_CLUSTER=false`, which nothing enforces.
//
// Contract: the campaign script (or the fixture's TestMain) must force the
// isolated control plane. RED on f8d67fa7.
func TestKEP284CampaignForcesIsolatedEnvtestCluster(t *testing.T) {
	script := readRepoFile(t, "test", "podcacheunion", "scripts", "transform-campaign.sh")
	fixture := readRepoFile(t, "test", "podcacheunion", "envtest_test.go")

	scriptForces := strings.Contains(script, "USE_EXISTING_CLUSTER=false")
	fixtureForces := strings.Contains(fixture, "UseExistingCluster") &&
		(strings.Contains(fixture, "ptr.To(false)") || strings.Contains(fixture, "new(bool)"))
	if !scriptForces && !fixtureForces {
		t.Errorf(
			"R2-F1: neither transform-campaign.sh nor TestMain pins the isolated envtest control "+
				"plane; with USE_EXISTING_CLUSTER=true in the caller's environment the campaign "+
				"targets the kubeconfig cluster, installs the RoleInstance CRD (never removed on "+
				"Stop), creates thousands of benchmark Pods, and silently ignores the intended "+
				"apiserver feature gates; expected `export USE_EXISTING_CLUSTER=false` in the "+
				"script or `UseExistingCluster: ptr.To(false)` in TestMain",
		)
	}
}

// R2-F2 — the tagged prototype package must be compiled by something.
//
// test/podcacheunion is behind //go:build podcacheunion and no workflow or
// Makefile target uses that tag (checked: copyright-check, docker, docs,
// e2e-test, envtest, project-check, release-test; `make build/test/vet/lint`
// run untagged; .golangci.yml has no build-tags setting). The KEP's measured
// sample, its read-path claims, and the client-go v0.34.2 internals the watch
// wrapper depends on are all validated only by a package that never compiles
// in CI, so a future dependency bump can silently rot the KEP's evidence.
//
// Contract: at least one CI workflow or Makefile target references the
// podcacheunion tag. RED on f8d67fa7.
func TestKEP284PodcacheunionCompiledInCI(t *testing.T) {
	root := repoRoot(t)
	places := []string{"Makefile"}
	entries, err := os.ReadDir(filepath.Join(root, ".github", "workflows"))
	if err == nil {
		for _, e := range entries {
			places = append(places, filepath.Join(".github", "workflows", e.Name()))
		}
	}
	for _, rel := range places {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			continue
		}
		if strings.Contains(string(data), "podcacheunion") {
			return
		}
	}
	t.Errorf(
		"R2-F2: no Makefile target or CI workflow references the podcacheunion build tag, so "+
			"test/podcacheunion is never compiled by CI; the KEP-284 prototype evidence can rot "+
			"silently on the next client-go/controller-runtime bump; add a CI step such as "+
			"`go vet -mod=vendor -tags=podcacheunion ./test/podcacheunion`",
	)
}

// R2-F3 — the KEP's read-path audit claim about spec.nodeName must match the
// code it audits.
//
// KEP-284 revision 2, Cache Implementation item 4, states "No code reads Pods
// by spec.nodeName." That is false as written: RecordNodeBindings
// (pkg/reconciler/roleinstance/sync/node_binding.go:216,239) reads
// pod.Spec.NodeName. The conclusion survives — those Pods come from
// getOwnedPods, and the ownerReference admission condition keeps every
// owner-index result whole — but the sentence that the alpha read-path audit
// is supposed to rest on does not survive a grep, and an implementer auditing
// from the KEP text would either be confused or skip auditing that path. If a
// future refactor ever feeds RecordNodeBindings from an unscoped List, stubs
// would silently record empty node bindings.
//
// Contract: the README must either drop the unqualified sentence or qualify
// it (name RecordNodeBindings and why it is safe). RED on f8d67fa7.
func TestKEP284NodeNameReadPathAuditClaim(t *testing.T) {
	readme := readRepoFile(t, "keps", "284-pod-cache-filter", "README.md")

	if strings.Contains(readme, "No code reads Pods by") {
		if !strings.Contains(readme, "RecordNodeBindings") && !strings.Contains(readme, "node_binding") {
			t.Errorf(
				"R2-F3: KEP-284 README claims \"No code reads Pods by `spec.nodeName`\" without "+
					"qualification, but RecordNodeBindings (node_binding.go) reads pod.Spec.NodeName "+
					"on getOwnedPods results; qualify the claim (safe because the ownerReference "+
					"admission condition keeps every owner-index result whole) so the alpha "+
					"read-path audit starts from a true statement",
			)
		}
	}
}
