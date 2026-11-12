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

package chart

// Verification harness, PR #552 round 3 (reviewer: codex).
//
// Round 3 re-scoped the PR: the chart no longer ships v1alpha1 deprecation
// webhook entries at all (the handlers were removed at the source by #556).
// The two load-bearing properties are now:
//
//  1. The rendered chart registers NO v1alpha1 deprecation webhook endpoints
//     (the PR body's own check: `helm template rbgs deploy/helm/rbgs | grep
//     v1alpha1-deprecation` must be empty). With the handlers gone, a rendered
//     Fail-policy entry would fail-close every matching write with a 404.
//  2. Generalized parity: the chart's hand-maintained webhook entries stay
//     identical to config/webhook/manifests.yaml (which `make manifests`
//     regenerates from the kubebuilder markers). Nothing in the repo enforces
//     this; the base branch already drifted once (#524 added six entries to
//     the shipped config that the chart never carried), so this guard compares
//     the FULL entry sets, not a hard-coded subset.
//
// Bites: (1) fails on the round-2 head 1a4f7ab0 (chart rendered three
// v1alpha1 deprecation entries); (2) fails on the raw round-3 head 69fc06c1
// tree, whose pre-#556 base leaves six v1alpha1 entries in the shipped config
// that the chart does not render.

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	"sigs.k8s.io/yaml"
)

const shippedWebhookManifests = "config/webhook/manifests.yaml"

func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve caller path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
}

// renderChartClean renders the chart with cmd.Output() (not CombinedOutput):
// helm writes kubeconfig permission warnings to stderr, and merging the
// streams corrupts the YAML doc stream. KUBECONFIG is pointed at an empty
// config for the same reason.
func renderChartClean(t *testing.T, extraArgs ...string) string {
	t.Helper()
	if _, err := exec.LookPath("helm"); err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("helm is required in CI: %v", err)
		}
		t.Skip("helm not installed; skipping chart render test")
	}
	kc := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(kc, []byte("apiVersion: v1\nkind: Config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	args := append([]string{"template", "rbgs", chartDir(t), "--namespace", "rbgs-system"}, extraArgs...)
	cmd := exec.Command("helm", args...)
	cmd.Env = append(os.Environ(), "KUBECONFIG="+kc)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("helm template failed: %v", err)
	}
	return string(out)
}

// webhookKey is the comparable identity of one webhook entry: everything the
// admission contract depends on. The service name/namespace and caBundle are
// install-environment details (templated namespace, runtime-patched CA) and
// are deliberately excluded; the path is not.
type webhookIdentity struct {
	Path                    string
	FailurePolicy           string
	MatchPolicy             string // "" normalized to "Equivalent" (the API default)
	SideEffects             string
	AdmissionReviewVersions []string
	Rules                   []ruleIdentity
}

type ruleIdentity struct {
	APIGroups   []string
	APIVersions []string
	Operations  []string
	Resources   []string
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func matchPolicyOf(raw string) string {
	if raw == "" {
		return string(admissionregistrationv1.Equivalent)
	}
	return raw
}

func identityOf(name string, w map[string]interface{}) webhookIdentity {
	id := webhookIdentity{}
	if v, ok := w["failurePolicy"].(string); ok {
		id.FailurePolicy = v
	}
	mp, _ := w["matchPolicy"].(string)
	id.MatchPolicy = matchPolicyOf(mp)
	if v, ok := w["sideEffects"].(string); ok {
		id.SideEffects = v
	}
	if arr, ok := w["admissionReviewVersions"].([]interface{}); ok {
		for _, v := range arr {
			id.AdmissionReviewVersions = append(id.AdmissionReviewVersions, v.(string))
		}
	}
	id.AdmissionReviewVersions = sortedCopy(id.AdmissionReviewVersions)
	if cc, ok := w["clientConfig"].(map[string]interface{}); ok {
		if svc, ok := cc["service"].(map[string]interface{}); ok {
			id.Path, _ = svc["path"].(string)
		}
	}
	if rules, ok := w["rules"].([]interface{}); ok {
		for _, r := range rules {
			rm := r.(map[string]interface{})
			ri := ruleIdentity{}
			get := func(k string) []string {
				var out []string
				if arr, ok := rm[k].([]interface{}); ok {
					for _, v := range arr {
						out = append(out, v.(string))
					}
				}
				return sortedCopy(out)
			}
			ri.APIGroups, ri.APIVersions, ri.Operations, ri.Resources = get("apiGroups"), get("apiVersions"), get("operations"), get("resources")
			id.Rules = append(id.Rules, ri)
		}
	}
	_ = name
	return id
}

// loadWebhooks parses a YAML stream and returns
// map[configKind]map[webhookName]identity for the two webhook configuration
// kinds.
func loadWebhooks(t *testing.T, stream string) map[string]map[string]webhookIdentity {
	t.Helper()
	out := map[string]map[string]webhookIdentity{
		"ValidatingWebhookConfiguration": {},
		"MutatingWebhookConfiguration":   {},
	}
	for _, doc := range strings.Split(stream, "\n---") {
		doc = strings.TrimSpace(doc)
		if doc == "" {
			continue
		}
		var obj map[string]interface{}
		if err := yaml.Unmarshal([]byte(doc), &obj); err != nil {
			continue // non-YAML comments/fragments are not webhook configs
		}
		kind, _ := obj["kind"].(string)
		configs, ok := out[kind]
		if !ok {
			continue
		}
		hooks, ok := obj["webhooks"].([]interface{})
		if !ok {
			continue
		}
		for _, h := range hooks {
			hm := h.(map[string]interface{})
			name := hm["name"].(string)
			if _, dup := configs[name]; dup {
				t.Fatalf("duplicate webhook name %q in %s", name, kind)
			}
			configs[name] = identityOf(name, hm)
		}
	}
	return out
}

func TestChartRendersNoV1alpha1DeprecationWebhooks(t *testing.T) {
	rendered := renderChartClean(t)
	hooks := loadWebhooks(t, rendered)
	for kind, configs := range hooks {
		for name, id := range configs {
			if strings.Contains(name, "v1alpha1") || strings.Contains(id.Path, "-v1alpha1-") {
				t.Errorf("%s renders v1alpha1 webhook entry %q (path %s): the v1alpha1 deprecation handlers were removed at the source (#556), so a rendered entry calls an unserved path", kind, name, id.Path)
			}
		}
	}
}

func TestChartWebhookConfigurationsMatchShippedManifests(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(t), shippedWebhookManifests))
	if err != nil {
		t.Fatalf("read shipped webhook manifests: %v", err)
	}
	shipped := loadWebhooks(t, string(data))
	rendered := loadWebhooks(t, renderChartClean(t))

	for kind, shippedHooks := range shipped {
		renderedHooks := rendered[kind]
		if len(shippedHooks) != len(renderedHooks) {
			t.Errorf("%s: shipped config has %d entries, chart renders %d (missing in chart: %v; extra in chart: %v)",
				kind, len(shippedHooks), len(renderedHooks),
				setDifference(shippedHooks, renderedHooks), setDifference(renderedHooks, shippedHooks))
			continue
		}
		for name, want := range shippedHooks {
			got, ok := renderedHooks[name]
			if !ok {
				t.Errorf("%s: shipped entry %q is not rendered by the chart", kind, name)
				continue
			}
			if !reflect.DeepEqual(want, got) {
				t.Errorf("%s entry %q drifted:\n  shipped: %+v\n  chart:   %+v", kind, name, want, got)
			}
		}
	}
}

func setDifference(a, b map[string]webhookIdentity) []string {
	var out []string
	for k := range a {
		if _, ok := b[k]; !ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
