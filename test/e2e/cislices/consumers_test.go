/*
Copyright 2025 The RBG Authors.

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

// Round-2 additions to the PR #494 verification harness: repro tests for the
// findings this reviewer carries in the round ledger that previously had no
// test on THIS branch (they lived only on the other reviewer's branch). IDs
// follow the round-ledger S-numbering:
//
//   - TestReleaseTestBareMakeTestE2eKeepsV1alpha1Suite CONTRACT (S1): bare
//     `make test-e2e` consumers of the narrowed Makefile default — the
//     release-test.yml qualification path — must keep the full default suite.
//   - TestWebhookCABundleGateRequiresEveryWebhook    CONTRACT (S5): the
//     caBundle readiness gate must require every webhook in a configuration to
//     be patched, not any one of them.
//   - TestSetupGateVerifiesAllCommittedCRDs          CONTRACT (S7): the setup
//     readiness gate must verify every CRD committed under config/crd/bases.
package cislices

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	jsonpath "k8s.io/client-go/util/jsonpath"
)

const releaseWorkflowRelPath = ".github/workflows/release-test.yml"
const actionRelPath = ".github/actions/setup-e2e-cluster/action.yaml"

func fileText(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// makefileDefaultLabelFilter extracts the `E2E_LABEL_FILTER ?= '...'` default
// from the Makefile.
func makefileDefaultLabelFilter(t *testing.T) string {
	t.Helper()
	mk := fileText(t, "Makefile")
	re := regexp.MustCompile(`(?m)^E2E_LABEL_FILTER \?= '([^']+)'`)
	m := re.FindStringSubmatch(mk)
	if m == nil {
		t.Fatal("Makefile no longer sets an E2E_LABEL_FILTER default (Makefile shape changed?)")
	}
	return m[1]
}

// makeTestE2eInvocations returns the 1-based line numbers of release-test.yml
// lines that invoke the `test-e2e` target specifically (so `test-e2e-upgrade`,
// which runs ./test/e2e/upgrade/ and never uses E2E_LABEL_FILTER, is excluded),
// paired with whether that line sets E2E_LABEL_FILTER.
func makeTestE2eInvocations(t *testing.T) map[int]bool {
	t.Helper()
	wf := fileText(t, releaseWorkflowRelPath)
	re := regexp.MustCompile(`make test-e2e([^[:alnum:]_-]|$)`)
	out := map[int]bool{}
	for i, line := range strings.Split(wf, "\n") {
		if re.MatchString(line) {
			out[i+1] = strings.Contains(line, "E2E_LABEL_FILTER")
		}
	}
	return out
}

// TestReleaseTestBareMakeTestE2eKeepsV1alpha1Suite is the S1 repro (CONTRACT):
// every bare `make test-e2e` consumer must keep running the full default suite
// — in particular the 28-spec [v1alpha1] compat tree that the base Makefile
// default (merge-base 798e5d6, Makefile:113) included via the inclusive
// `!volcano && !scheduler-plugins` filter. The PR narrows the default to
// `!v1alpha1 && !volcano && !scheduler-plugins` (head Makefile:105), so a bare
// consumer silently loses those specs (ginkgo exits green on a narrower
// filter). Fails while the defect is present; passes once either the call
// sites override E2E_LABEL_FILTER or the default is inclusive again.
func TestReleaseTestBareMakeTestE2eKeepsV1alpha1Suite(t *testing.T) {
	def := makefileDefaultLabelFilter(t)
	invocations := makeTestE2eInvocations(t)
	if len(invocations) == 0 {
		t.Fatalf("S1: release-test.yml no longer invokes `make test-e2e` at all — the qualification path's e2e step moved or was renamed; re-check manually")
	}
	var bare []int
	for line, overridden := range invocations {
		if !overridden {
			bare = append(bare, line)
		}
	}
	sort.Ints(bare)
	narrowed := strings.Contains(def, "v1alpha1")
	t.Logf("S1: Makefile default filter = %q (narrowed=%v); make test-e2e invocations in release-test.yml: %d, bare (no E2E_LABEL_FILTER on the line): %v",
		def, narrowed, len(invocations), bare)
	if narrowed && len(bare) > 0 {
		t.Errorf("S1 CONTRACT: %d bare `make test-e2e` call site(s) %v in release-test.yml inherit the narrowed Makefile default %q, which excludes the [v1alpha1] compat specs — the /release-test qualification path silently loses 28 specs (ginkgo stays green on a narrower filter). Fix: pass E2E_LABEL_FILTER='!volcano && !scheduler-plugins' at the call sites, or keep the Makefile default inclusive and override only in e2e-test.yml.",
			len(bare), bare, def)
	}
}

// TestWebhookCABundleGateRequiresEveryWebhook is the S5 repro (CONTRACT): the
// composite action's webhook caBundle readiness gate must require EVERY
// webhook in a configuration to carry a caBundle. The head gate
// (action.yaml:249) treats non-empty flattened
// `{.webhooks[*].clientConfig.caBundle}` jsonpath output as ready — jsonpath
// drops missing keys, so one patched webhook masks an unpatched sibling. The
// base gate (removed base e2e-test.yml:206) used jq
// `all(.webhooks[]; (.clientConfig.caBundle // "") != "")`.
func TestWebhookCABundleGateRequiresEveryWebhook(t *testing.T) {
	action := fileText(t, actionRelPath)
	// The gate line: ca_bundle=$(kubectl get "$config" -o jsonpath='{.webhooks[*].clientConfig.caBundle}' ...)
	reGate := regexp.MustCompile(`(?m)^\s*ca_bundle=\$\(kubectl get.*caBundle.*$`)
	gateLine := reGate.FindString(action)
	if gateLine == "" {
		t.Fatalf("S5: caBundle gate line not found in action.yaml (action shape changed?)")
	}
	flattening := strings.Contains(gateLine, "{.webhooks[*].clientConfig.caBundle}")
	// A per-webhook gate is either the restored jq all() expression or an
	// explicit loop over webhooks; both are acceptable fix shapes. Restrict the
	// probe to the gate line itself (`.webhooks[` alone would also match the
	// flattening `.webhooks[*]`).
	perWebhook := strings.Contains(gateLine, "all(") ||
		(strings.Contains(gateLine, ".webhooks[") && !strings.Contains(gateLine, ".webhooks[*]"))
	if flattening && !perWebhook {
		t.Errorf("S5 CONTRACT: the caBundle readiness gate uses the flattening jsonpath %q — jsonpath drops missing keys, so ONE patched webhook masks unpatched siblings in the same configuration (base required every webhook via jq all()). Restore a per-webhook check, e.g. jq 'all(.webhooks[]; (.clientConfig.caBundle // \"\") != \"\")'.",
			strings.TrimSpace(gateLine))
	}

	// Semantic half: prove the flattening jsonpath really does mask an
	// unpatched sibling, using the same jsonpath library kubectl uses.
	cfg := `{"metadata":{"name":"rbgs-validating-webhook-configuration"},"webhooks":[` +
		`{"name":"a.workloads.x-k8s.io","clientConfig":{"caBundle":"cHVueGVk"}},` +
		`{"name":"b.workloads.x-k8s.io","clientConfig":{"service":{"name":"svc"}}}]}`
	jp := jsonpath.New("caBundle-gate")
	// kubectl evaluates -o jsonpath with missing keys allowed (and dropped);
	// replicate that or the demo errors out instead of showing the drop.
	jp.AllowMissingKeys(true)
	if err := jp.Parse("{.webhooks[*].clientConfig.caBundle}"); err != nil {
		t.Fatalf("parse jsonpath: %v", err)
	}
	var obj interface{}
	if err := json.Unmarshal([]byte(cfg), &obj); err != nil {
		t.Fatalf("parse fixture json: %v", err)
	}
	var buf strings.Builder
	if err := jp.Execute(&buf, obj); err != nil {
		t.Fatalf("execute jsonpath: %v", err)
	}
	out := strings.TrimSpace(buf.String())
	t.Logf("S5: flattening jsonpath on a 2-webhook config (webhook b has NO caBundle) yields %q — non-empty, so the current gate declares the configuration ready while one webhook is still unpatched", out)
	if flattening && !perWebhook && out == "" {
		t.Errorf("S5: semantic demo unexpectedly empty — jsonpath semantics changed; update this test")
	}
}

// crdPluralNames lists the plural CRD names committed under config/crd/bases
// (filename convention: <group>_<resource>.yaml).
func crdPluralNames(t *testing.T) []string {
	t.Helper()
	dir := filepath.Join(repoRoot(t), "config/crd/bases")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read config/crd/bases: %v", err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		base := strings.TrimSuffix(e.Name(), ".yaml")
		i := strings.IndexByte(base, '_')
		if i < 0 {
			continue
		}
		names = append(names, base[i+1:]+"."+base[:i])
	}
	sort.Strings(names)
	return names
}

// TestSetupGateVerifiesAllCommittedCRDs is the S7 repro (CONTRACT): the setup
// action's readiness gate must verify every committed CRD after install. Head
// hardcodes rolebasedgroups/coordinatedpolicies/roleinstances
// (action.yaml:210-212); the base main job looped over config/crd/bases/*.yaml.
// Passes once every CRD name is checked OR the directory loop is restored.
func TestSetupGateVerifiesAllCommittedCRDs(t *testing.T) {
	action := fileText(t, actionRelPath)
	names := crdPluralNames(t)
	if len(names) < 10 {
		t.Fatalf("S7: expected >=10 committed CRDs in config/crd/bases, got %d: %v", len(names), names)
	}
	// Fix shape A: the restored directory loop over config/crd/bases/*.yaml.
	if strings.Contains(action, "config/crd/bases/") || strings.Contains(action, "config/crd/bases/*.yaml") {
		t.Logf("S7: gate loops over config/crd/bases — all %d CRDs verified", len(names))
		return
	}
	// Fix shape B: every committed CRD name appears in the action, delimited as
	// a whole token (`\b` so e.g. instances is not "found" inside roleinstances).
	var missing []string
	for _, n := range names {
		reName := regexp.MustCompile(`\b` + regexp.QuoteMeta(n) + `\b`)
		if !reName.MatchString(action) {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		t.Errorf("S7 CONTRACT: setup readiness gate verifies only %d of %d committed CRDs; missing: %v — restore the `for f in config/crd/bases/*.yaml` loop (base behavior) or list all CRDs.",
			len(names)-len(missing), len(names), missing)
	}
}
