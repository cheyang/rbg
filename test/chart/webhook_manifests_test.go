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

const shippedWebhookManifests = "config/webhook/manifests.yaml"

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

// TestChartDeprecationWebhooksMatchShippedConfig asserts every v1alpha1
// deprecation webhook entry from config/webhook/manifests.yaml is rendered by
// the chart byte-identically except for the service name/namespace (which the
// chart must point at its own service) and the runtime-patched caBundle.
func TestChartDeprecationWebhooksMatchShippedConfig(t *testing.T) {
	shipped := shippedValidatingWebhooks(t)
	chart := chartValidatingWebhooks(t)

	var shippedDeprecation []string
	for name := range shipped {
		if strings.HasSuffix(name, "-v1alpha1-deprecation.kb.io") {
			shippedDeprecation = append(shippedDeprecation, name)
		}
	}
	if len(shippedDeprecation) != 6 {
		t.Fatalf("shipped config has %d v1alpha1 deprecation webhooks, want 6", len(shippedDeprecation))
	}

	for _, name := range shippedDeprecation {
		want := shipped[name]
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
}
