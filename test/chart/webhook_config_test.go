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
// Round 3 (PR #552 head 69fc06c1, base ea5e82ae): #556 removed the v1alpha1
// admission webhooks from the serving binary and the shipped manifests, and
// this PR removed the six v1alpha1 deprecation entries from the chart
// template — the chart now renders only the v1alpha2 entries again. The
// tests pin the round-3 contract:
//
//   - TestChartWebhookConfigurationsMatchShippedManifests: the chart-rendered
//     webhook *specs* (names, paths, failurePolicy, matchPolicy, rules) must be
//     identical to the shipped config/webhook/manifests.yaml. Without this,
//     nothing in the repo notices when `make manifests` regenerates the shipped
//     file and the chart silently drifts (the round-1/round-2 gap).
//   - TestChartRendersNoV1alpha1DeprecationWebhookEntries: the chart must
//     render NO v1alpha1 deprecation webhook entries. #556 removed the
//     handlers, so a rendered entry would point the API server at an endpoint
//     nothing serves (fail-closed for failurePolicy=Fail entries).
//   - TestChartDefaultImageServesRenderedWebhookPaths: the chart's default
//     controller image must be built from a commit that registers every webhook
//     path the chart renders (the round-1/2 F1 tracker; green on the merge
//     result where #555 re-pins to v0.9.0-73e05db4).
//   - TestCRDDeprecationWarningsCarryDocumentedTimeline: the CRD bases must
//     mark every v1alpha1 version deprecated with a 0.10.0 timeline — the
//     warning layer the PR's documentation now leans on.
//   - TestPinnedCrdUpgraderImageCarriesDocumentedWarningText: the chart's
//     pinned crdUpgrade image embeds the CRDs; its build commit must carry the
//     same 0.10.0 warning text the docs describe (round-3 gap: the pin
//     v0.9.0-73e05db4 predates #556).
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

// TestChartRendersNoV1alpha1DeprecationWebhookEntries pins the round-3
// contract: #556 removed the v1alpha1 admission handlers from the binary and
// the shipped manifests, so the chart must not render any v1alpha1
// deprecation webhook entries. A rendered entry would route v1alpha1 writes
// to an endpoint nothing serves; for failurePolicy=Fail entries that
// fail-closes every matched write.
func TestChartRendersNoV1alpha1DeprecationWebhookEntries(t *testing.T) {
	vwc, mwc := chartWebhookConfigurations(t)

	for _, wh := range vwc.Webhooks {
		assertNoV1alpha1DeprecationEntry(t, wh.Name, string(*wh.FailurePolicy), wh.Rules, wh.ClientConfig.Service)
	}
	for _, wh := range mwc.Webhooks {
		assertNoV1alpha1DeprecationEntry(t, wh.Name, string(*wh.FailurePolicy), wh.Rules, wh.ClientConfig.Service)
	}
}

func assertNoV1alpha1DeprecationEntry(t *testing.T, name, policy string, rules []admissionregistrationv1.RuleWithOperations, service *admissionregistrationv1.ServiceReference) {
	t.Helper()
	if strings.Contains(name, "v1alpha1") && strings.Contains(name, "deprecation") {
		t.Errorf("chart renders v1alpha1 deprecation webhook entry %q (failurePolicy %s)", name, policy)
	}
	if service != nil && service.Path != nil && strings.Contains(*service.Path, "v1alpha1") && strings.Contains(*service.Path, "deprecation") {
		t.Errorf("chart webhook %q routes v1alpha1 deprecation path %q", name, *service.Path)
	}
	for _, rule := range rules {
		if !strings.Contains(name, "deprecation") {
			continue
		}
		for _, v := range rule.APIVersions {
			if v == "v1alpha1" {
				t.Errorf("chart webhook %q matches v1alpha1 writes (%v)", name, rule.Resources)
			}
		}
	}
}

// crdKindsWithV1alpha1 is every CRD carrying a v1alpha1 version — the six
// kinds the deprecation documentation covers.
var crdKindsWithV1alpha1 = []string{
	"rolebasedgroups",
	"rolebasedgroupsets",
	"instancesets",
	"instances",
	"rolebasedgroupscalingadapters",
	"clusterengineruntimeprofiles",
}

// TestCRDDeprecationWarningsCarryDocumentedTimeline verifies the warning
// layer the PR's documentation now leans on: every v1alpha1 CRD version is
// marked deprecated with a warning that names the 0.10.0 removal timeline
// (#556). "Every v1alpha1 request returns a deprecation warning" is only
// true if these markers survive.
func TestCRDDeprecationWarningsCarryDocumentedTimeline(t *testing.T) {
	for _, kind := range crdKindsWithV1alpha1 {
		path := filepath.Join(chartDir(t), "..", "..", "..", "config", "crd", "bases", "workloads.x-k8s.io_"+kind+".yaml")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read CRD %s: %v", kind, err)
		}
		var crd struct {
			Spec struct {
				Versions []struct {
					Name               string `json:"name"`
					Deprecated         bool   `json:"deprecated"`
					DeprecationWarning string `json:"deprecationWarning"`
				} `json:"versions"`
			} `json:"spec"`
		}
		if err := yaml.Unmarshal(data, &crd); err != nil {
			t.Fatalf("parse CRD %s: %v", kind, err)
		}
		found := false
		for _, v := range crd.Spec.Versions {
			if v.Name != "v1alpha1" {
				continue
			}
			found = true
			if !v.Deprecated {
				t.Errorf("%s v1alpha1 is not marked deprecated; every v1alpha1 request would silently lose the documented warning", kind)
			}
			if !strings.Contains(v.DeprecationWarning, "deprecated") || !strings.Contains(v.DeprecationWarning, "0.10.0") {
				t.Errorf("%s v1alpha1 deprecationWarning = %q, want a warning naming deprecation and the 0.10.0 removal timeline", kind, v.DeprecationWarning)
			}
		}
		if !found {
			t.Errorf("CRD %s has no v1alpha1 version", kind)
		}
	}
}

// TestPinnedCrdUpgraderImageCarriesDocumentedWarningText pins the round-3
// finding: the chart installs CRDs through the crdUpgrade Job image, which
// embeds config/crd/bases at its build commit. The docs this PR lands say
// every v1alpha1 request warns that v1alpha1 "will be removed in 0.10.0";
// that timeline only reaches users once the pinned image is built from a
// commit carrying #556's warning texts. RED on the merge result documents
// the gap (pin v0.9.0-73e05db4 predates #556).
func TestPinnedCrdUpgraderImageCarriesDocumentedWarningText(t *testing.T) {
	pin := pinnedImageCommit(t, "crdUpgrade")
	if pin == "" {
		return // skipped inside helper
	}
	for _, kind := range crdKindsWithV1alpha1 {
		out, err := exec.Command("git", "show", pin+":config/crd/bases/workloads.x-k8s.io_"+kind+".yaml").Output()
		if err != nil {
			t.Skipf("cannot read CRD %s at pinned image commit %s: %v", kind, pin, err)
		}
		if !strings.Contains(string(out), "0.10.0") {
			t.Errorf("CRD %s embedded in the pinned crdUpgrade image %s carries no 0.10.0 timeline in its v1alpha1 deprecation warning; default-values installs emit the old text until the pin is bumped past #556", kind, pin)
		}
	}
}

// pinnedImageCommit reads the image tag under the given values.yaml key and
// returns the commit sha it embeds ("" + skip when it does not).
func pinnedImageCommit(t *testing.T, section string) string {
	t.Helper()
	valuesPath := filepath.Join(chartDir(t), "values.yaml")
	data, err := os.ReadFile(valuesPath)
	if err != nil {
		t.Fatalf("read chart values.yaml: %v", err)
	}
	secRe := regexp.MustCompile(`(?m)^` + section + `:
(?:[ 	]+.*
)*?[ 	]+tag:\s*"?(v[0-9][^\s"]*)"?\s*$`)
	m := secRe.FindStringSubmatch(string(data))
	if m == nil {
		t.Skipf("no %s image tag found in values.yaml", section)
	}
	shaRe := regexp.MustCompile(`-([0-9a-f]{7,12})$`)
	sm := shaRe.FindStringSubmatch(m[1])
	if sm == nil {
		t.Skipf("image tag %q embeds no commit sha; cannot check which code it carries", m[1])
	}
	commit := sm[1]
	if err := exec.Command("git", "rev-parse", "--verify", "--quiet", commit+"^{commit}").Run(); err != nil {
		t.Skipf("image commit %s not present in local git history; cannot check which code it carries", commit)
	}
	return commit
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
