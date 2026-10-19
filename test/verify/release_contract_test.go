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

// Package verify holds reviewer harness tests for sgl-project/rbg PR #505
// ("chore(rbgs): update Helm chart and default image versions to 0.9.0-alpha.2").
//
// These are CONTRACT tests for the release-bump invariant set (polarity:
// contract — they must PASS on a correct version bump):
//
//	C1 TestVersionStringsConsistent   (unit)        — chart appVersion, values.yaml
//	       image tags, and config/manager kustomize newTag all agree.
//	C2 TestGeneratedManifestsInSync   (unit)        — deploy/kubectl/manifests.yaml
//	       is byte-identical to `kustomize build config/default`.
//	C3 TestChartRenderMatchesValues   (integration) — `helm template` renders both
//	       images with the values.yaml tags and the version ConfigMap with the
//	       Chart.yaml version/appVersion.
//	C4 TestReferencedImagesPublished  (integration) — both default image tags exist
//	       on Docker Hub (default installs must not ImagePullBackOff).
//
// Tool lookups: kustomize (PATH, else <repo>/bin/kustomize), helm (PATH).
// Tests skip when the tool is absent, mirroring test/chart conventions.
// Stdout-only capture is used for helm deliberately: CombinedOutput would mix
// kubeconfig-permission warnings into the manifest stream on developer machines.
package verify

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"sigs.k8s.io/yaml"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve caller path")
	}
	// this file: <repo>/test/verify/release_contract_test.go
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
}

func readYAML(t *testing.T, path string, out interface{}) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := yaml.Unmarshal(data, out); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
}

type chartMeta struct {
	Version    string `json:"version"`
	AppVersion string `json:"appVersion"`
}

type valuesMeta struct {
	Controller struct {
		Image struct {
			Repository string `json:"repository"`
			Tag        string `json:"tag"`
		} `json:"image"`
	} `json:"controller"`
	CrdUpgrade struct {
		Image struct {
			Repository string `json:"repository"`
			Tag        string `json:"tag"`
		} `json:"image"`
	} `json:"crdUpgrade"`
}

func loadVersions(t *testing.T) (chartMeta, valuesMeta, string) {
	t.Helper()
	root := repoRoot(t)
	var chart chartMeta
	readYAML(t, filepath.Join(root, "deploy", "helm", "rbgs", "Chart.yaml"), &chart)
	var values valuesMeta
	readYAML(t, filepath.Join(root, "deploy", "helm", "rbgs", "values.yaml"), &values)
	var kustom struct {
		Images []struct {
			Name   string `json:"name"`
			NewTag string `json:"newTag"`
		} `json:"images"`
	}
	readYAML(t, filepath.Join(root, "config", "manager", "kustomization.yaml"), &kustom)
	managerTag := ""
	for _, img := range kustom.Images {
		if img.Name == "controller" {
			managerTag = img.NewTag
		}
	}
	if managerTag == "" {
		t.Fatal("config/manager/kustomization.yaml has no images entry named 'controller'")
	}
	return chart, values, managerTag
}

// C1: the release-bump invariant — every default install path must reference the
// same image tag, and it must be "v" + chart appVersion.
func TestVersionStringsConsistent(t *testing.T) {
	chart, values, managerTag := loadVersions(t)
	want := "v" + chart.AppVersion
	for name, got := range map[string]string{
		"values.yaml controller.image.tag": values.Controller.Image.Tag,
		"values.yaml crdUpgrade.image.tag": values.CrdUpgrade.Image.Tag,
		"config/manager images[].newTag":   managerTag,
	} {
		if got != want {
			t.Errorf("%s = %q, want %q (v + Chart.yaml appVersion)", name, got, want)
		}
	}
	if chart.Version == "" || chart.AppVersion == "" {
		t.Error("Chart.yaml version/appVersion must both be set")
	}
}

func kustomizeBin(t *testing.T) string {
	t.Helper()
	if p, err := exec.LookPath("kustomize"); err == nil {
		return p
	}
	local := filepath.Join(repoRoot(t), "bin", "kustomize")
	if _, err := os.Stat(local); err == nil {
		return local
	}
	t.Skip("kustomize not found in PATH or bin/; install with `make kustomize`")
	return ""
}

// C2: deploy/kubectl/manifests.yaml must be exactly what
// `kustomize build config/default` produces (the Makefile's generation rule).
func TestGeneratedManifestsInSync(t *testing.T) {
	root := repoRoot(t)
	cmd := exec.Command(kustomizeBin(t), "build", "config/default")
	cmd.Dir = root
	built, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			t.Fatalf("kustomize build failed: %v\n%s", err, ee.Stderr)
		}
		t.Fatalf("kustomize build failed: %v", err)
	}
	committed, err := os.ReadFile(filepath.Join(root, "deploy", "kubectl", "manifests.yaml"))
	if err != nil {
		t.Fatalf("read committed manifests.yaml: %v", err)
	}
	if string(built) != string(committed) {
		t.Errorf("deploy/kubectl/manifests.yaml is out of sync with `kustomize build config/default` (built %d bytes, committed %d bytes); regenerate with `make build-installer`/`make manifests`",
			len(built), len(committed))
	}
}

func helmBin(t *testing.T) string {
	t.Helper()
	if p, err := exec.LookPath("helm"); err == nil {
		return p
	}
	t.Skip("helm not found in PATH")
	return ""
}

// C3: the rendered chart must put the values.yaml tags on both images and record
// the Chart.yaml version/appVersion in the version ConfigMap.
func TestChartRenderMatchesValues(t *testing.T) {
	root := repoRoot(t)
	chart, values, _ := loadVersions(t)
	cmd := exec.Command(helmBin(t), "template", "rbgs", filepath.Join(root, "deploy", "helm", "rbgs"))
	cmd.Dir = root
	out, err := cmd.Output() // stdout only: stderr carries kubeconfig warnings locally
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			t.Fatalf("helm template failed: %v\n%s", err, ee.Stderr)
		}
		t.Fatalf("helm template failed: %v", err)
	}
	rendered := string(out)
	for _, want := range []string{
		fmt.Sprintf(`image: "%s:%s"`, values.Controller.Image.Repository, values.Controller.Image.Tag),
		fmt.Sprintf(`image: "%s:%s"`, values.CrdUpgrade.Image.Repository, values.CrdUpgrade.Image.Tag),
		fmt.Sprintf(`chartVersion: "%s"`, chart.Version),
		fmt.Sprintf(`appVersion: "%s"`, chart.AppVersion),
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered chart does not contain %q", want)
		}
	}
}

// C4: both default images must already exist in the registry; a version bump that
// points defaults at an unpublished tag breaks every fresh install.
func TestReferencedImagesPublished(t *testing.T) {
	_, values, _ := loadVersions(t)
	client := &http.Client{Timeout: 15 * time.Second}
	for _, img := range []struct{ repo, tag string }{
		{values.Controller.Image.Repository, values.Controller.Image.Tag},
		{values.CrdUpgrade.Image.Repository, values.CrdUpgrade.Image.Tag},
	} {
		url := fmt.Sprintf("https://hub.docker.com/v2/repositories/%s/tags/%s", img.repo, img.tag)
		resp, err := client.Get(url)
		if err != nil {
			t.Skipf("registry unreachable (%v); cannot verify %s:%s", err, img.repo, img.tag)
		}
		body := struct {
			Images []struct {
				Architecture string `json:"architecture"`
			} `json:"images"`
		}{}
		func() {
			defer resp.Body.Close()
			_ = json.NewDecoder(resp.Body).Decode(&body)
		}()
		if resp.StatusCode == http.StatusNotFound {
			t.Errorf("image %s:%s NOT FOUND on Docker Hub — default installs would ImagePullBackOff", img.repo, img.tag)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			t.Skipf("registry returned %d for %s; cannot verify %s:%s", resp.StatusCode, url, img.repo, img.tag)
		}
		t.Logf("%s:%s exists (architectures: %d)", img.repo, img.tag, len(body.Images))
	}
}
