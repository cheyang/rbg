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

// Reviewer verification harness for PR #505 — the 0.9.0-alpha.2 release-manifest
// bump. Additive only: the reviewed PR's production diff is untouched.
//
// Contract under test: every shipped default must reference the same release cut,
// and that cut must be the one this PR pins:
//
//   - values.yaml controller.image.tag      == v0.9.0-d88b3186
//   - values.yaml crdUpgrade.image.tag      == v0.9.0-d88b3186
//   - config/manager/kustomization.yaml newTag == v0.9.0-d88b3186
//   - deploy/kubectl/manifests.yaml controller image == rolebasedgroup/rbgs-controller:v0.9.0-d88b3186
//   - Chart.yaml appVersion == tag without the leading "v" (0.9.0-d88b3186)
//   - Chart.yaml version == 0.9.0-alpha.2
//   - `helm template` renders both images at the values.yaml tags
//   - `kubectl kustomize config/default` is byte-identical to deploy/kubectl/manifests.yaml
//
// These are contract tests: on the merge base (which ships the previous
// v0.9.0-eadb6c20 cut) they FAIL — that red run is the proof the harness bites.
// When re-running against a later release cut, set RBG_EXPECTED_RELEASE_TAG and
// RBG_EXPECTED_CHART_VERSION instead of editing the defaults.
package chart

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

const (
	harnessDefaultExpectedTag     = "v0.9.0-d88b3186"
	harnessDefaultExpectedVersion = "0.9.0-alpha.2"
)

func harnessRepoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve caller path")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "Makefile")); err != nil {
		t.Fatalf("repo root not found at %s: %v", root, err)
	}
	return root
}

func harnessExpectedTag(t *testing.T) string {
	t.Helper()
	if v := os.Getenv("RBG_EXPECTED_RELEASE_TAG"); v != "" {
		return v
	}
	return harnessDefaultExpectedTag
}

func harnessExpectedVersion(t *testing.T) string {
	t.Helper()
	if v := os.Getenv("RBG_EXPECTED_CHART_VERSION"); v != "" {
		return v
	}
	return harnessDefaultExpectedVersion
}

// TestReleaseImageTagsConsistentAcrossManifests pins the shipped defaults to the
// exact release cut. Red on the merge base (previous cut), green on the PR head.
func TestReleaseImageTagsConsistentAcrossManifests(t *testing.T) {
	root := harnessRepoRoot(t)
	want := harnessExpectedTag(t)

	// values.yaml: controller.image.tag and crdUpgrade.image.tag
	raw, err := os.ReadFile(filepath.Join(root, "deploy", "helm", "rbgs", "values.yaml"))
	if err != nil {
		t.Fatalf("read values.yaml: %v", err)
	}
	var values map[string]interface{}
	if err := yaml.Unmarshal(raw, &values); err != nil {
		t.Fatalf("parse values.yaml: %v", err)
	}
	for _, keyPath := range [][2]string{
		{"controller", "rbgs-controller"},
		{"crdUpgrade", "rbgs-upgrade-crd"},
	} {
		img, ok := values[keyPath[0]].(map[string]interface{})["image"].(map[string]interface{})
		if !ok {
			t.Fatalf("%s.image missing in values.yaml", keyPath[0])
		}
		if got := img["tag"].(string); got != want {
			t.Errorf("values.yaml %s.image.tag = %q, want %q", keyPath[0], got, want)
		}
	}

	// config/manager/kustomization.yaml: images[controller].newTag
	raw, err = os.ReadFile(filepath.Join(root, "config", "manager", "kustomization.yaml"))
	if err != nil {
		t.Fatalf("read kustomization.yaml: %v", err)
	}
	var kustom struct {
		Images []struct {
			Name    string `yaml:"name"`
			NewName string `yaml:"newName"`
			NewTag  string `yaml:"newTag"`
		} `yaml:"images"`
	}
	if err := yaml.Unmarshal(raw, &kustom); err != nil {
		t.Fatalf("parse kustomization.yaml: %v", err)
	}
	found := false
	for _, img := range kustom.Images {
		if img.Name == "controller" {
			found = true
			if img.NewTag != want {
				t.Errorf("kustomization.yaml controller newTag = %q, want %q", img.NewTag, want)
			}
			if img.NewName != "rolebasedgroup/rbgs-controller" {
				t.Errorf("kustomization.yaml controller newName = %q, want rolebasedgroup/rbgs-controller", img.NewName)
			}
		}
	}
	if !found {
		t.Error("kustomization.yaml has no controller image override")
	}

	// deploy/kubectl/manifests.yaml: the generated controller Deployment image.
	raw, err = os.ReadFile(filepath.Join(root, "deploy", "kubectl", "manifests.yaml"))
	if err != nil {
		t.Fatalf("read manifests.yaml: %v", err)
	}
	imageLine := regexp.MustCompile(`(?m)^\s*image: rolebasedgroup/rbgs-controller:(\S+)\s*$`)
	m := imageLine.FindStringSubmatch(string(raw))
	if m == nil {
		t.Fatal("no `image: rolebasedgroup/rbgs-controller:<tag>` line found in deploy/kubectl/manifests.yaml")
	}
	if got := m[1]; got != want {
		t.Errorf("deploy/kubectl/manifests.yaml controller image tag = %q, want %q", got, want)
	}

	// Chart.yaml: appVersion (tag minus the leading "v") and chart version.
	raw, err = os.ReadFile(filepath.Join(root, "deploy", "helm", "rbgs", "Chart.yaml"))
	if err != nil {
		t.Fatalf("read Chart.yaml: %v", err)
	}
	var chart struct {
		Version    string `yaml:"version"`
		AppVersion string `yaml:"appVersion"`
	}
	if err := yaml.Unmarshal(raw, &chart); err != nil {
		t.Fatalf("parse Chart.yaml: %v", err)
	}
	wantAppVersion := strings.TrimPrefix(want, "v")
	if got := chart.AppVersion; got != wantAppVersion {
		t.Errorf("Chart.yaml appVersion = %q, want %q (tag %q without leading v)", got, wantAppVersion, want)
	}
	if got := chart.Version; got != harnessExpectedVersion(t) {
		t.Errorf("Chart.yaml version = %q, want %q", got, harnessExpectedVersion(t))
	}
}

// TestRenderedImagesMatchValuesTag renders the chart and asserts the two images
// the chart actually schedules match the pinned release tags. Uses cmd.Output()
// (stderr kept separate) so helm's kubeconfig warnings cannot pollute the stream.
// Skips when helm is not installed.
func TestRenderedImagesMatchValuesTag(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm not installed; skipping chart render check")
	}
	root := harnessRepoRoot(t)
	want := harnessExpectedTag(t)

	var stdout, stderr bytes.Buffer
	cmd := exec.Command("helm", "template", "rbgs", filepath.Join(root, "deploy", "helm", "rbgs"))
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("helm template failed: %v\nstderr: %s", err, stderr.String())
	}
	rendered := stdout.String()
	for _, image := range []string{
		"rolebasedgroup/rbgs-controller:" + want,
		"rolebasedgroup/rbgs-upgrade-crd:" + want,
	} {
		if !strings.Contains(rendered, `image: "`+image+`"`) {
			t.Errorf("rendered chart does not schedule %q (tag %q not rendered)", image, want)
		}
	}
}

// TestKustomizeBuildMatchesShippedManifests guards that the one-line change in
// the generated deploy/kubectl/manifests.yaml is exactly what
// `kustomize build config/default` produces — i.e. the generated file is in sync
// with the kustomize config, not hand-edited. Skips when kubectl is absent.
func TestKustomizeBuildMatchesShippedManifests(t *testing.T) {
	if _, err := exec.LookPath("kubectl"); err != nil {
		t.Skip("kubectl not installed; skipping kustomize sync check")
	}
	root := harnessRepoRoot(t)

	var stdout, stderr bytes.Buffer
	cmd := exec.Command("kubectl", "kustomize", filepath.Join(root, "config", "default"))
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("kubectl kustomize config/default failed: %v\nstderr: %s", err, stderr.String())
	}
	shipped, err := os.ReadFile(filepath.Join(root, "deploy", "kubectl", "manifests.yaml"))
	if err != nil {
		t.Fatalf("read manifests.yaml: %v", err)
	}
	if !bytes.Equal(bytes.TrimSpace(stdout.Bytes()), bytes.TrimSpace(shipped)) {
		t.Error("deploy/kubectl/manifests.yaml differs from `kubectl kustomize config/default`; regenerate with `make kubectl-deploy`")
	}
}
