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

// Webhook-configuration assertions for the rbgs Helm chart.
//
// PR #552 ports the v1alpha1 deprecation webhooks from the shipped
// config/webhook/manifests.yaml (landed by #524) into the chart-rendered
// ValidatingWebhookConfiguration. The tests here pin that port:
//
//   - TestChartWebhookConfigurationsMatchShippedManifests: the chart-rendered
//     webhook *specs* (names, paths, failurePolicy, matchPolicy, rules) must be
//     identical to the shipped config/webhook/manifests.yaml. Without this,
//     nothing in the repo notices when `make manifests` regenerates the shipped
//     file (e.g. the 0.10.0 v1alpha1 removal) and the chart silently drifts
//     back into the intermediate state #552 exists to close.
//   - TestChartV1alpha1DeprecationWebhookFields: the deprecation entries
//     with the exact enforcing/warning failurePolicy split the PR documents
//     (two Fail entries + one merged warning entry since head 1a4f7ab0,
//     which folded the four warning-only kinds into one endpoint).
//   - TestChartDefaultImageServesRenderedWebhookPaths: the chart's default
//     controller image must be built from a commit that registers every webhook
//     path the chart renders (contract test — RED on head is the F1
//     reproduction: v0.9.0-d88b3186 predates #524's path registrations).
package chart

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	"sigs.k8s.io/yaml"
)

// v1alpha1DeprecationWebhooks is the exact set of v1alpha1 deprecation webhook
// entries PR #552 renders, with the failurePolicy each entry must carry. Since
// head 1a4f7ab0 the four warning-only kinds share ONE merged endpoint. Keep in
// sync with the kubebuilder markers in
// api/workloads/v1alpha1/deprecation_webhook.go.
var v1alpha1DeprecationWebhooks = map[string]string{
	"vrolebasedgroup-v1alpha1-deprecation.kb.io":    "Fail",
	"vrolebasedgroupset-v1alpha1-deprecation.kb.io": "Fail",
	"vv1alpha1-warning-deprecation.kb.io":           "Ignore",
}

// v1alpha1DeprecationPaths pins each deprecation entry's service path against
// the path the Go code actually registers (marker + Register call in
// deprecation_webhook.go). A mismatch means the API server calls an endpoint
// the webhook server does not serve.
var v1alpha1DeprecationPaths = map[string]string{
	"vrolebasedgroup-v1alpha1-deprecation.kb.io":    "/validate-workloads-x-k8s-io-v1alpha1-rolebasedgroup",
	"vrolebasedgroupset-v1alpha1-deprecation.kb.io": "/validate-workloads-x-k8s-io-v1alpha1-rolebasedgroupset",
	"vv1alpha1-warning-deprecation.kb.io":           "/validate-workloads-v1alpha1-deprecation",
}

// v1alpha1WarningResources are the four kinds the merged warning endpoint
// covers, in the order the marker lists them.
var v1alpha1WarningResources = []string{
	"instancesets", "instances", "rolebasedgroupscalingadapters", "clusterengineruntimeprofiles",
}

// webhookSpec is the comparable projection of a webhook entry. Service
// name/namespace and caBundle are deliberately excluded: they legitimately
// differ between the chart (Release.Namespace, rbgs-webhook-service, caBundle
// patched at runtime by the controller) and the kustomize-shipped file.
type webhookSpec struct {
	Name                   string
	Path                   string
	FailurePolicy          string
	MatchPolicy            string
	SideEffects            string
	AdmissionReviewVersion []string
	Rules                  []admissionregistrationv1.RuleWithOperations
}

func projectWebhook(wh admissionregistrationv1.ValidatingWebhook) webhookSpec {
	spec := webhookSpec{
		Name:                   wh.Name,
		FailurePolicy:          string(*wh.FailurePolicy),
		MatchPolicy:            matchPolicyOf(wh.MatchPolicy),
		SideEffects:            string(*wh.SideEffects),
		AdmissionReviewVersion: wh.AdmissionReviewVersions,
		Rules:                  wh.Rules,
	}
	if wh.ClientConfig.Service != nil && wh.ClientConfig.Service.Path != nil {
		spec.Path = *wh.ClientConfig.Service.Path
	}
	return spec
}

func projectMutatingWebhook(wh admissionregistrationv1.MutatingWebhook) webhookSpec {
	spec := webhookSpec{
		Name:                   wh.Name,
		FailurePolicy:          string(*wh.FailurePolicy),
		MatchPolicy:            matchPolicyOf(wh.MatchPolicy),
		SideEffects:            string(*wh.SideEffects),
		AdmissionReviewVersion: wh.AdmissionReviewVersions,
		Rules:                  wh.Rules,
	}
	if wh.ClientConfig.Service != nil && wh.ClientConfig.Service.Path != nil {
		spec.Path = *wh.ClientConfig.Service.Path
	}
	return spec
}

// matchPolicyOf renders a (pointer) MatchPolicy into its comparable string,
// normalizing the unset case to the API default "Equivalent".
func matchPolicyOf(p *admissionregistrationv1.MatchPolicyType) string {
	if p == nil {
		return "Equivalent"
	}
	return string(*p)
}

func sortSpecs(specs []webhookSpec) []webhookSpec {
	sort.Slice(specs, func(i, j int) bool { return specs[i].Name < specs[j].Name })
	return specs
}

// chartWebhookConfigurations renders the chart and returns its
// Validating/MutatingWebhookConfiguration objects.
func chartWebhookConfigurations(t *testing.T) (*admissionregistrationv1.ValidatingWebhookConfiguration, *admissionregistrationv1.MutatingWebhookConfiguration) {
	t.Helper()
	manifest := renderChart(t, "--namespace", "rbgs-system")
	objs := splitObjects(t, manifest)

	vwcRaw, ok := objs["ValidatingWebhookConfiguration/rbgs-validating-webhook-configuration"]
	if !ok {
		t.Fatalf("ValidatingWebhookConfiguration rbgs-validating-webhook-configuration not found in rendered chart")
	}
	mwcRaw, ok := objs["MutatingWebhookConfiguration/rbgs-mutating-webhook-configuration"]
	if !ok {
		t.Fatalf("MutatingWebhookConfiguration rbgs-mutating-webhook-configuration not found in rendered chart")
	}

	var vwc admissionregistrationv1.ValidatingWebhookConfiguration
	if err := yaml.Unmarshal([]byte(vwcRaw.raw), &vwc); err != nil {
		t.Fatalf("unmarshal rendered ValidatingWebhookConfiguration: %v", err)
	}
	var mwc admissionregistrationv1.MutatingWebhookConfiguration
	if err := yaml.Unmarshal([]byte(mwcRaw.raw), &mwc); err != nil {
		t.Fatalf("unmarshal rendered MutatingWebhookConfiguration: %v", err)
	}
	return &vwc, &mwc
}

// shippedWebhookConfigurations parses config/webhook/manifests.yaml — the file
// `make manifests` regenerates and kustomize release rendering consumes.
func shippedWebhookConfigurations(t *testing.T) (*admissionregistrationv1.ValidatingWebhookConfiguration, *admissionregistrationv1.MutatingWebhookConfiguration) {
	t.Helper()
	path := filepath.Join(chartDir(t), "..", "..", "..", "config", "webhook", "manifests.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read shipped webhook manifests: %v", err)
	}
	var vwc *admissionregistrationv1.ValidatingWebhookConfiguration
	var mwc *admissionregistrationv1.MutatingWebhookConfiguration
	for _, chunk := range docSeparator.Split(string(data), -1) {
		if strings.TrimSpace(chunk) == "" {
			continue
		}
		var meta struct {
			Kind string `json:"kind"`
		}
		if err := yaml.Unmarshal([]byte(chunk), &meta); err != nil {
			t.Fatalf("parse shipped doc: %v\n%s", err, chunk)
		}
		switch meta.Kind {
		case "ValidatingWebhookConfiguration":
			vwc = &admissionregistrationv1.ValidatingWebhookConfiguration{}
			if err := yaml.Unmarshal([]byte(chunk), vwc); err != nil {
				t.Fatalf("unmarshal shipped ValidatingWebhookConfiguration: %v", err)
			}
		case "MutatingWebhookConfiguration":
			mwc = &admissionregistrationv1.MutatingWebhookConfiguration{}
			if err := yaml.Unmarshal([]byte(chunk), mwc); err != nil {
				t.Fatalf("unmarshal shipped MutatingWebhookConfiguration: %v", err)
			}
		}
	}
	if vwc == nil || mwc == nil {
		t.Fatalf("shipped manifests must contain both a Validating and a MutatingWebhookConfiguration")
	}
	return vwc, mwc
}

// TestChartWebhookConfigurationsMatchShippedManifests asserts the chart renders
// exactly the webhook specs the shipped config/webhook/manifests.yaml carries.
// This is the parity PR #552 claims ("byte-matched ... names, paths, rules,
// matchPolicy: Exact") — before this test nothing in the repo enforced it.
func TestChartWebhookConfigurationsMatchShippedManifests(t *testing.T) {
	chartVWC, chartMWC := chartWebhookConfigurations(t)
	shippedVWC, shippedMWC := shippedWebhookConfigurations(t)

	var chartValidating []webhookSpec
	for _, wh := range chartVWC.Webhooks {
		chartValidating = append(chartValidating, projectWebhook(wh))
	}
	var shippedValidating []webhookSpec
	for _, wh := range shippedVWC.Webhooks {
		shippedValidating = append(shippedValidating, projectWebhook(wh))
	}
	if got, want := len(chartValidating), len(shippedValidating); got != want {
		t.Errorf("chart renders %d validating webhooks, shipped manifests carry %d", got, want)
	}
	if !reflect.DeepEqual(sortSpecs(chartValidating), sortSpecs(shippedValidating)) {
		t.Errorf("chart validating webhook specs diverge from shipped config/webhook/manifests.yaml:\nchart:  %+v\nshipped: %+v",
			sortSpecs(chartValidating), sortSpecs(shippedValidating))
	}

	var chartMutating []webhookSpec
	for _, wh := range chartMWC.Webhooks {
		chartMutating = append(chartMutating, projectMutatingWebhook(wh))
	}
	var shippedMutating []webhookSpec
	for _, wh := range shippedMWC.Webhooks {
		shippedMutating = append(shippedMutating, projectMutatingWebhook(wh))
	}
	if got, want := len(chartMutating), len(shippedMutating); got != want {
		t.Errorf("chart renders %d mutating webhooks, shipped manifests carry %d", got, want)
	}
	if !reflect.DeepEqual(sortSpecs(chartMutating), sortSpecs(shippedMutating)) {
		t.Errorf("chart mutating webhook specs diverge from shipped config/webhook/manifests.yaml:\nchart:  %+v\nshipped: %+v",
			sortSpecs(chartMutating), sortSpecs(shippedMutating))
	}
}

// TestChartV1alpha1DeprecationWebhookFields pins the deprecation entries
// and their failurePolicy split: Fail on the two enforcing RBG/RBGSet
// endpoints, Ignore on the merged warning endpoint covering the four
// warning-only kinds.
func TestChartV1alpha1DeprecationWebhookFields(t *testing.T) {
	vwc, _ := chartWebhookConfigurations(t)

	rendered := map[string]admissionregistrationv1.ValidatingWebhook{}
	for _, wh := range vwc.Webhooks {
		rendered[wh.Name] = wh
	}
	if len(rendered) != len(vwc.Webhooks) {
		t.Fatalf("duplicate webhook names in rendered ValidatingWebhookConfiguration")
	}

	for name, wantPolicy := range v1alpha1DeprecationWebhooks {
		wh, ok := rendered[name]
		if !ok {
			t.Errorf("deprecation webhook %q missing from rendered chart", name)
			continue
		}
		if got := string(*wh.FailurePolicy); got != wantPolicy {
			t.Errorf("deprecation webhook %q failurePolicy = %q, want %q", name, got, wantPolicy)
		}
		if got := matchPolicyOf(wh.MatchPolicy); got != "Exact" {
			t.Errorf("deprecation webhook %q matchPolicy = %q, want Exact", name, got)
		}
		if wh.ClientConfig.Service == nil || wh.ClientConfig.Service.Path == nil {
			t.Fatalf("deprecation webhook %q has no service path", name)
		}
		if path := *wh.ClientConfig.Service.Path; path != v1alpha1DeprecationPaths[name] {
			t.Errorf("deprecation webhook %q path = %q, want %q", name, path, v1alpha1DeprecationPaths[name])
		}
		if len(wh.Rules) != 1 {
			t.Fatalf("deprecation webhook %q has %d rules, want 1", name, len(wh.Rules))
		}
		rule := wh.Rules[0]
		if len(rule.Operations) != 2 || rule.Operations[0] != admissionregistrationv1.Create || rule.Operations[1] != admissionregistrationv1.Update {
			t.Errorf("deprecation webhook %q operations = %v, want [CREATE UPDATE]", name, rule.Operations)
		}
		if len(rule.APIVersions) != 1 || rule.APIVersions[0] != "v1alpha1" {
			t.Errorf("deprecation webhook %q apiVersions = %v, want [v1alpha1]", name, rule.APIVersions)
		}
		if wantPolicy == "Ignore" {
			// The merged warning endpoint must cover exactly the four
			// warning-only kinds.
			if len(rule.Resources) != len(v1alpha1WarningResources) {
				t.Errorf("merged warning webhook %q covers %d resources %v, want %v", name, len(rule.Resources), rule.Resources, v1alpha1WarningResources)
			} else if !reflect.DeepEqual([]string(rule.Resources), v1alpha1WarningResources) {
				t.Errorf("merged warning webhook %q resources = %v, want %v", name, rule.Resources, v1alpha1WarningResources)
			}
		} else if len(rule.Resources) != 1 {
			t.Errorf("enforcing webhook %q covers %d resources %v, want exactly its own kind", name, len(rule.Resources), rule.Resources)
		}
	}

	// The three v1alpha2 admission entries must still be there untouched.
	for _, name := range []string{
		"vrolebasedgroup.kb.io",
		"vrolebasedgroupset.kb.io",
		"vcoordinatedpolicy.kb.io",
	} {
		if _, ok := rendered[name]; !ok {
			t.Errorf("v1alpha2 validating webhook %q missing from rendered chart", name)
		}
	}
}

// TestChartDefaultImageServesRenderedWebhookPaths is the F1 contract test.
//
// Contract: every webhook path the chart renders must be registered by the Go
// code at the commit the chart's default controller image tag embeds. If it is
// not, a chart install with default values points the API server at endpoints
// the running binary does not serve: the two failurePolicy=Fail v1alpha1
// RoleBasedGroup/RoleBasedGroupSet entries then hard-reject every v1alpha1
// RBG/RBGSet create and update (a 404 from the webhook server is an admission
// error, and fail-closed turns it into a denial).
//
// RED on the PR head = the F1 reproduction (v0.9.0-d88b3186 predates #524).
// GREEN once the default image tag is bumped to a post-#524 commit.
func TestChartDefaultImageServesRenderedWebhookPaths(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("git is required in CI: %v", err)
		}
		t.Skip("git not installed; skipping image-commit check")
	}

	valuesPath := filepath.Join(chartDir(t), "values.yaml")
	data, err := os.ReadFile(valuesPath)
	if err != nil {
		t.Fatalf("read chart values.yaml: %v", err)
	}
	tagRe := regexp.MustCompile(`(?m)^\s*tag:\s*"?(v[0-9][^\s"]*)"?\s*$`)
	var tag string
	for _, m := range tagRe.FindAllStringSubmatch(string(data), -1) {
		if tag == "" {
			tag = m[1]
		}
	}
	if tag == "" {
		t.Fatalf("no image tag found in %s", valuesPath)
	}

	shaRe := regexp.MustCompile(`-([0-9a-f]{7,12})$`)
	m := shaRe.FindStringSubmatch(tag)
	if m == nil {
		t.Skipf("image tag %q embeds no commit sha; cannot check which code it serves", tag)
	}
	imageCommit := m[1]

	repoRoot := filepath.Clean(filepath.Join(chartDir(t), "..", "..", ".."))
	if err := exec.Command("git", "rev-parse", "--verify", "--quiet", imageCommit+"^{commit}").Run(); err != nil {
		t.Skipf("image commit %s not present in local git history; cannot check which code it serves", imageCommit)
	}

	vwc, mwc := chartWebhookConfigurations(t)
	paths := map[string]bool{}
	for _, wh := range vwc.Webhooks {
		if wh.ClientConfig.Service != nil && wh.ClientConfig.Service.Path != nil {
			paths[*wh.ClientConfig.Service.Path] = true
		}
	}
	for _, wh := range mwc.Webhooks {
		if wh.ClientConfig.Service != nil && wh.ClientConfig.Service.Path != nil {
			paths[*wh.ClientConfig.Service.Path] = true
		}
	}

	var unserved []string
	for path := range paths {
		// kubebuilder webhook markers carry path=/<endpoint> for each registered path.
		cmd := exec.Command("git", "grep", "-l", "path="+path+",", imageCommit, "--", "api/")
		cmd.Dir = repoRoot
		if out, err := cmd.CombinedOutput(); err != nil {
			unserved = append(unserved, path+"\n    "+strings.TrimSpace(string(out)))
		}
	}
	sort.Strings(unserved)
	if len(unserved) > 0 {
		t.Errorf("chart default image %q (commit %s) does not register %d of the %d webhook paths the chart renders:\n%s\n"+
			"A chart install with default values will fail-close every write matched by a failurePolicy=Fail entry above\n"+
			"(v1alpha1 RoleBasedGroup/RoleBasedGroupSet create+update are rejected with a webhook call error).",
			tag, imageCommit, len(unserved), len(paths), strings.Join(unserved, "\n"))
	}
}

// TestChartNamespaceTemplatedIntoWebhookService is a small wiring check: the
// webhook clientConfigs must point at the release namespace so a chart
// installed into any namespace reaches its own webhook service.
func TestChartNamespaceTemplatedIntoWebhookService(t *testing.T) {
	manifest := renderChart(t, "--namespace", "custom-ns-42")
	objs := splitObjects(t, manifest)
	obj, ok := objs["ValidatingWebhookConfiguration/rbgs-validating-webhook-configuration"]
	if !ok {
		t.Fatalf("ValidatingWebhookConfiguration not found in rendered chart")
	}
	var vwc admissionregistrationv1.ValidatingWebhookConfiguration
	if err := yaml.Unmarshal([]byte(obj.raw), &vwc); err != nil {
		t.Fatalf("unmarshal rendered ValidatingWebhookConfiguration: %v", err)
	}
	if len(vwc.Webhooks) == 0 {
		t.Fatal("no webhooks rendered")
	}
	for _, wh := range vwc.Webhooks {
		if wh.ClientConfig.Service == nil {
			t.Fatalf("webhook %q has no service clientConfig", wh.Name)
		}
		if ns := wh.ClientConfig.Service.Namespace; ns != "custom-ns-42" {
			t.Errorf("webhook %q service namespace = %q, want custom-ns-42 (templated from .Release.Namespace)", wh.Name, ns)
		}
		if name := wh.ClientConfig.Service.Name; name != "rbgs-webhook-service" {
			t.Errorf("webhook %q service name = %q, want rbgs-webhook-service", wh.Name, name)
		}
	}
}
