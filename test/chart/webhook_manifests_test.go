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

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	"sigs.k8s.io/yaml"
)

// The chart's templates/webhook/manifests.yaml is hand-maintained: `make
// manifests` regenerates config/webhook/manifests.yaml from the kubebuilder
// markers, but nothing regenerates the chart copy. This test pins the drift
// guard PR #552 verified by hand: every v1alpha1 deprecation webhook entry in
// the shipped config must be present in the rendered chart with identical
// name, path, rules, failurePolicy, matchPolicy, sideEffects and
// admissionReviewVersions.
//
// Round 2 (head 1a4f7ab0): the four per-kind warning-only endpoints were
// merged into a single entry (vv1alpha1-warning-deprecation.kb.io), so the
// v1alpha1 deprecation surface is now exactly three entries: two fail-closed
// enforcement entries (RBG, RBGSet) and one fail-open merged warning entry.

const shippedWebhookManifests = "config/webhook/manifests.yaml"

// expectedV1alpha1DeprecationWebhooks is the exact set of v1alpha1 deprecation
// entries the shipped config and the chart must carry, with the failurePolicy
// each must have. Fail on the two enforcing entries is the point of the gate;
// Ignore on the merged warning entry keeps still-supported kinds writable
// during a webhook outage.
var expectedV1alpha1DeprecationWebhooks = map[string]admissionregistrationv1.FailurePolicyType{
	"vrolebasedgroup-v1alpha1-deprecation.kb.io":    admissionregistrationv1.Fail,
	"vrolebasedgroupset-v1alpha1-deprecation.kb.io": admissionregistrationv1.Fail,
	"vv1alpha1-warning-deprecation.kb.io":           admissionregistrationv1.Ignore,
}

// warningOnlyResources are the four kinds the merged warning entry must cover
// in a single rule.
var expectedMergedWarningResources = []string{
	"instancesets",
	"instances",
	"rolebasedgroupscalingadapters",
	"clusterengineruntimeprofiles",
}

// retiredPerKindWarningWebhooks are the pre-merge entry names: they must not
// reappear in either the shipped config or the rendered chart.
var retiredPerKindWarningWebhooks = []string{
	"vinstanceset-v1alpha1-deprecation.kb.io",
	"vinstance-v1alpha1-deprecation.kb.io",
	"vrolebasedgroupscalingadapter-v1alpha1-deprecation.kb.io",
	"vclusterengineruntimeprofile-v1alpha1-deprecation.kb.io",
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve caller path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
}

func shippedValidatingWebhooks(t *testing.T) map[string]admissionregistrationv1.ValidatingWebhook {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), shippedWebhookManifests))
	if err != nil {
		t.Fatalf("read shipped webhook manifests: %v", err)
	}
	out := map[string]admissionregistrationv1.ValidatingWebhook{}
	for _, chunk := range docSeparator.Split(string(data), -1) {
		if strings.TrimSpace(chunk) == "" {
			continue
		}
		var meta struct {
			Kind string `json:"kind"`
		}
		if err := yaml.Unmarshal([]byte(chunk), &meta); err != nil {
			t.Fatalf("parse shipped doc: %v", err)
		}
		if meta.Kind != "ValidatingWebhookConfiguration" {
			continue
		}
		var vwc admissionregistrationv1.ValidatingWebhookConfiguration
		if err := yaml.Unmarshal([]byte(chunk), &vwc); err != nil {
			t.Fatalf("unmarshal shipped ValidatingWebhookConfiguration: %v", err)
		}
		for _, w := range vwc.Webhooks {
			out[w.Name] = w
		}
	}
	return out
}

func chartValidatingWebhooks(t *testing.T) map[string]admissionregistrationv1.ValidatingWebhook {
	t.Helper()
	objs := splitObjects(t, renderChart(t))
	obj, ok := objs["ValidatingWebhookConfiguration/rbgs-validating-webhook-configuration"]
	if !ok {
		t.Fatalf("rendered chart has no ValidatingWebhookConfiguration/rbgs-validating-webhook-configuration")
	}
	var vwc admissionregistrationv1.ValidatingWebhookConfiguration
	if err := yaml.Unmarshal([]byte(obj.raw), &vwc); err != nil {
		t.Fatalf("unmarshal rendered ValidatingWebhookConfiguration: %v", err)
	}
	out := map[string]admissionregistrationv1.ValidatingWebhook{}
	for _, w := range vwc.Webhooks {
		out[w.Name] = w
	}
	return out
}

func webhookServicePath(w admissionregistrationv1.ValidatingWebhook) string {
	if w.ClientConfig.Service != nil && w.ClientConfig.Service.Path != nil {
		return *w.ClientConfig.Service.Path
	}
	return ""
}

func isV1alpha1DeprecationWebhook(name string) bool {
	return strings.Contains(name, "v1alpha1") && strings.Contains(name, "deprecation")
}

// TestChartDeprecationWebhooksMatchShippedConfig asserts the v1alpha1
// deprecation webhook entries from config/webhook/manifests.yaml are exactly
// the expected three, and that the chart renders each of them identically
// except for the service name/namespace (which the chart must point at its own
// service) and the runtime-patched caBundle.
func TestChartDeprecationWebhooksMatchShippedConfig(t *testing.T) {
	shipped := shippedValidatingWebhooks(t)
	chart := chartValidatingWebhooks(t)

	// The shipped config must carry exactly the expected three-entry surface:
	// this pins the merged-endpoint refactor as well as any future drift.
	var shippedDeprecation []string
	for name := range shipped {
		if isV1alpha1DeprecationWebhook(name) {
			shippedDeprecation = append(shippedDeprecation, name)
		}
	}
	sort.Strings(shippedDeprecation)
	var wantNames []string
	for name := range expectedV1alpha1DeprecationWebhooks {
		wantNames = append(wantNames, name)
	}
	sort.Strings(wantNames)
	if !reflect.DeepEqual(shippedDeprecation, wantNames) {
		t.Fatalf("shipped config v1alpha1 deprecation webhooks = %v, want exactly %v", shippedDeprecation, wantNames)
	}

	// The retired per-kind warning entries must not survive anywhere.
	for _, retired := range retiredPerKindWarningWebhooks {
		if _, ok := shipped[retired]; ok {
			t.Errorf("shipped config still carries retired per-kind warning webhook %q", retired)
		}
		if _, ok := chart[retired]; ok {
			t.Errorf("rendered chart still carries retired per-kind warning webhook %q", retired)
		}
	}

	for name, wantFailurePolicy := range expectedV1alpha1DeprecationWebhooks {
		want := shipped[name]
		if want.FailurePolicy == nil || *want.FailurePolicy != wantFailurePolicy {
			t.Errorf("shipped %s: failurePolicy = %v, want %v", name, want.FailurePolicy, wantFailurePolicy)
		}
		got, ok := chart[name]
		if !ok {
			t.Errorf("chart is missing deprecation webhook %q", name)
			continue
		}
		if !reflect.DeepEqual(got.Rules, want.Rules) {
			t.Errorf("%s: rules differ:\n  chart:   %+v\n  shipped: %+v", name, got.Rules, want.Rules)
		}
		if !reflect.DeepEqual(got.FailurePolicy, want.FailurePolicy) {
			t.Errorf("%s: failurePolicy chart=%v shipped=%v", name, got.FailurePolicy, want.FailurePolicy)
		}
		if !reflect.DeepEqual(got.MatchPolicy, want.MatchPolicy) {
			t.Errorf("%s: matchPolicy chart=%v shipped=%v", name, got.MatchPolicy, want.MatchPolicy)
		}
		if !reflect.DeepEqual(got.SideEffects, want.SideEffects) {
			t.Errorf("%s: sideEffects chart=%v shipped=%v", name, got.SideEffects, want.SideEffects)
		}
		if !reflect.DeepEqual(got.AdmissionReviewVersions, want.AdmissionReviewVersions) {
			t.Errorf("%s: admissionReviewVersions chart=%v shipped=%v", name, got.AdmissionReviewVersions, want.AdmissionReviewVersions)
		}
		if gotPath, wantPath := webhookServicePath(got), webhookServicePath(want); gotPath != wantPath {
			t.Errorf("%s: service path chart=%q shipped=%q", name, gotPath, wantPath)
		}
	}

	// The merged warning entry must cover exactly the four warning-only kinds
	// in one rule, for CREATE and UPDATE, Exact match (so v1alpha2 writes do
	// not route here).
	merged, ok := shipped["vv1alpha1-warning-deprecation.kb.io"]
	if !ok {
		t.Fatalf("shipped config is missing the merged warning webhook")
	}
	if len(merged.Rules) != 1 {
		t.Fatalf("merged warning webhook has %d rules, want 1", len(merged.Rules))
	}
	rule := merged.Rules[0]
	gotResources := append([]string(nil), rule.Resources...)
	sort.Strings(gotResources)
	wantResources := append([]string(nil), expectedMergedWarningResources...)
	sort.Strings(wantResources)
	if !reflect.DeepEqual(gotResources, wantResources) {
		t.Errorf("merged warning webhook resources = %v, want %v", gotResources, wantResources)
	}
	var gotOps []string
	for _, op := range rule.Operations {
		gotOps = append(gotOps, string(op))
	}
	sort.Strings(gotOps)
	if !reflect.DeepEqual(gotOps, []string{"CREATE", "UPDATE"}) {
		t.Errorf("merged warning webhook operations = %v, want [CREATE UPDATE]", gotOps)
	}
	if merged.MatchPolicy == nil || *merged.MatchPolicy != admissionregistrationv1.Exact {
		t.Errorf("merged warning webhook matchPolicy = %v, want Exact", merged.MatchPolicy)
	}
}
