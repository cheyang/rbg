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

// Package chart — reviewer verification harness for PR #555
// (chore(rbgs): update Helm chart and default image versions to 0.9.0-alpha.3).
//
// These tests pin the cross-file contract of a release-manifest bump: the image
// tag referenced by values.yaml (controller + crdUpgrade), config/manager
// kustomization, the generated deploy/kubectl/manifests.yaml, and Chart.yaml's
// appVersion must all agree, and the rendered chart must carry exactly those
// tags. A bump that forgets one of the five reference sites fails here.
//
// Polarity: contract tests. They PASS on a correct bump and FAIL (red) if any
// reference site drifts from Chart.yaml's appVersion.
package chart

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

// chartMeta is the subset of Chart.yaml these tests read.
type chartMeta struct {
	Version    string `yaml:"version"`
	AppVersion string `yaml:"appVersion"`
}

// valuesSubset is the subset of values.yaml these tests read.
type valuesSubset struct {
	Controller struct {
		Image struct {
			Repository string `yaml:"repository"`
			Tag        string `yaml:"tag"`
		} `yaml:"image"`
	} `yaml:"controller"`
	CRDUpgrade struct {
		Image struct {
			Repository string `yaml:"repository"`
			Tag        string `yaml:"tag"`
		} `yaml:"image"`
	} `yaml:"crdUpgrade"`
}

// kustomizeSubset is the subset of config/manager/kustomization.yaml these tests read.
type kustomizeSubset struct {
	Images []struct {
		Name    string `yaml:"name"`
		NewName string `yaml:"newName"`
		NewTag  string `yaml:"newTag"`
	} `yaml:"images"`
}

func repoRoot(t *testing.T) string {
	t.Helper()
	return filepath.Clean(filepath.Join(filepath.Dir(chartDir(t)), "..", ".."))
}

func readYAMLFile(t *testing.T, path string, out interface{}) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := yaml.Unmarshal(data, out); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
}

// TestVersionBumpReferencesConsistent (unit layer) asserts every version
// reference site agrees with Chart.yaml's appVersion:
//
//   - values.yaml controller.image.tag == "v" + appVersion
//   - values.yaml crdUpgrade.image.tag == "v" + appVersion
//   - config/manager/kustomization.yaml controller newTag == "v" + appVersion
//   - deploy/kubectl/manifests.yaml controller image == "rolebasedgroup/rbgs-controller:v" + appVersion
//
// Chart.yaml is the single source of truth because the version-configmap and the
// crd-upgrade Job name are derived from it at render time.
func TestVersionBumpReferencesConsistent(t *testing.T) {
	root := repoRoot(t)

	var meta chartMeta
	readYAMLFile(t, filepath.Join(root, "deploy", "helm", "rbgs", "Chart.yaml"), &meta)
	if !regexp.MustCompile(`^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$`).MatchString(meta.Version) {
		t.Errorf("chart version %q is not a valid semver (pre-)release", meta.Version)
	}
	wantTag := "v" + meta.AppVersion
	if wantTag == "v" || wantTag == "v0" {
		t.Fatalf("appVersion missing in Chart.yaml: %q", meta.AppVersion)
	}

	var vals valuesSubset
	readYAMLFile(t, filepath.Join(root, "deploy", "helm", "rbgs", "values.yaml"), &vals)
	if got := vals.Controller.Image.Tag; got != wantTag {
		t.Errorf("values.yaml controller.image.tag = %q, want %q (Chart.yaml appVersion %q)",
			got, wantTag, meta.AppVersion)
	}
	if got := vals.CRDUpgrade.Image.Tag; got != wantTag {
		t.Errorf("values.yaml crdUpgrade.image.tag = %q, want %q (Chart.yaml appVersion %q)",
			got, wantTag, meta.AppVersion)
	}

	var kust kustomizeSubset
	readYAMLFile(t, filepath.Join(root, "config", "manager", "kustomization.yaml"), &kust)
	found := false
	for _, img := range kust.Images {
		if img.Name == "controller" {
			found = true
			if got := img.NewTag; got != wantTag {
				t.Errorf("config/manager/kustomization.yaml controller newTag = %q, want %q", got, wantTag)
			}
			if img.NewName != "rolebasedgroup/rbgs-controller" {
				t.Errorf("config/manager/kustomization.yaml controller newName = %q, want rolebasedgroup/rbgs-controller", img.NewName)
			}
		}
	}
	if !found {
		t.Errorf("config/manager/kustomization.yaml has no controller image override")
	}

	manifests, err := os.ReadFile(filepath.Join(root, "deploy", "kubectl", "manifests.yaml"))
	if err != nil {
		t.Fatalf("read deploy/kubectl/manifests.yaml: %v", err)
	}
	wantImage := "rolebasedgroup/rbgs-controller:" + wantTag
	imageLine := regexp.MustCompile(`(?m)^\s*image:\s*(\S+)\s*$`)
	var controllerImages []string
	for _, m := range imageLine.FindAllStringSubmatch(string(manifests), -1) {
		controllerImages = append(controllerImages, m[1])
	}
	if len(controllerImages) != 1 {
		t.Errorf("deploy/kubectl/manifests.yaml: found %d container image lines, want exactly 1 (the controller deployment)", len(controllerImages))
	}
	for _, img := range controllerImages {
		if img != wantImage {
			t.Errorf("deploy/kubectl/manifests.yaml controller image = %q, want %q", img, wantImage)
		}
	}

	// No stale release tag may remain anywhere in the four reference files:
	// every image-tag-looking token in them must be the current tag.
	stale := regexp.MustCompile(`v\d+\.\d+\.\d+-[0-9a-f]{6,10}`)
	for _, rel := range []string{
		filepath.Join(root, "deploy", "helm", "rbgs", "values.yaml"),
		filepath.Join(root, "config", "manager", "kustomization.yaml"),
	} {
		data, err := os.ReadFile(rel)
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		for _, tok := range stale.FindAllString(string(data), -1) {
			if tok != wantTag {
				t.Errorf("%s: stale release tag %q, want %q", filepath.Base(rel), tok, wantTag)
			}
		}
	}
}

// TestRenderedChartMatchesChartVersions (integration layer) renders the chart
// with `helm template` and asserts the rendered objects carry exactly the tags
// Chart.yaml declares: the manager Deployment image, the CRD-upgrade hook Job
// image, and the rbgs-controller-version-info ConfigMap.
//
// Unlike renderChart in deprecated_workload_types_test.go, this helper captures
// stdout only (cmd.Output) so helm warnings on stderr cannot pollute the YAML
// stream being parsed (see finding F1 in the review).
func TestRenderedChartMatchesChartVersions(t *testing.T) {
	root := repoRoot(t)
	if _, err := exec.LookPath("helm"); err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("helm is required in CI: %v", err)
		}
		t.Skip("helm not installed; skipping chart render test")
	}

	var meta chartMeta
	readYAMLFile(t, filepath.Join(root, "deploy", "helm", "rbgs", "Chart.yaml"), &meta)
	wantTag := "v" + meta.AppVersion

	cmd := exec.Command("helm", "template", "rbgs", filepath.Join(root, "deploy", "helm", "rbgs"),
		"--namespace", "rbgs-system")
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("helm template failed: %v\nstderr: %s", err, stderr.String())
	}
	rendered := stdout.String()

	images := regexp.MustCompile(`(?m)^\s*image:\s*"?\S+"?\s*$`).
		FindAllString(rendered, -1)
	if len(images) < 2 {
		t.Fatalf("rendered chart has %d image lines, want >= 2 (controller deployment + crd-upgrade job)", len(images))
	}
	wantController := "rolebasedgroup/rbgs-controller:" + wantTag
	if !strings.Contains(rendered, wantController) {
		t.Errorf("rendered chart does not contain controller image %q", wantController)
	}
	wantUpgrader := "rolebasedgroup/rbgs-upgrade-crd:" + wantTag
	if !strings.Contains(rendered, wantUpgrader) {
		t.Errorf("rendered chart does not contain crd-upgrade image %q", wantUpgrader)
	}
	// helm quotes configmap string values; accept quoted or bare.
	chartVersionRe := regexp.MustCompile(`(?m)^\s*chartVersion:\s*"?` + regexp.QuoteMeta(meta.Version) + `"?\s*$`)
	if !chartVersionRe.MatchString(rendered) {
		t.Errorf("rendered version ConfigMap does not carry chartVersion %q", meta.Version)
	}
	appVersionRe := regexp.MustCompile(`(?m)^\s*appVersion:\s*"?` + regexp.QuoteMeta(meta.AppVersion) + `"?\s*$`)
	if !appVersionRe.MatchString(rendered) {
		t.Errorf("rendered version ConfigMap does not carry appVersion %q", meta.AppVersion)
	}

	// Every release-tag-shaped token in the render must be the current tag:
	// a stale tag in any template would surface here.
	for _, tok := range regexp.MustCompile(`v\d+\.\d+\.\d+-[0-9a-f]{6,10}`).FindAllString(rendered, -1) {
		if tok != wantTag {
			t.Errorf("rendered chart carries stale release tag %q, want %q", tok, wantTag)
		}
	}
}
