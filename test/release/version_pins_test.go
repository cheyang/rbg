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

// Package release holds the reviewer harness for PR #555 (release-manifest bump
// to chart 0.9.0-alpha.3 / image tag v0.9.0-73e05db4). These are CONTRACT tests:
// they assert the invariants every release bump must keep, and they fail when a
// bump is incomplete or the generated artifact is stale. They are additive only;
// no production code is touched.
//
// Invariants checked:
//  1. TestReleasePinsAreConsistent — Chart.yaml appVersion, values.yaml
//     controller/crdUpgrade tags, config/manager kustomize newTag and the
//     deploy/kubectl/manifests.yaml controller image all pin the SAME image tag,
//     and Chart appVersion equals that tag minus the leading "v". No stale
//     previous-cut tag remains in the release files.
//  2. TestKubectlManifestsInSyncWithKustomize — deploy/kubectl/manifests.yaml is
//     byte-identical to `kustomize build config/default`.
//  3. TestHelmChartRendersPinnedVersions — `helm template` renders the controller
//     and crd-upgrade images with the values.yaml tags, and the version ConfigMap
//     carries .Chart.Version / .Chart.AppVersion.
package release

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	// test/release -> repo root is two levels up.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Dir(filepath.Dir(wd))
}

func readFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

func mustMatch1(t *testing.T, re *regexp.Regexp, s, what string) string {
	t.Helper()
	m := re.FindStringSubmatch(s)
	if m == nil {
		t.Fatalf("could not find %s", what)
	}
	return m[1]
}

// chartMeta extracts the release-relevant fields from the four release files.
func chartMeta(t *testing.T) (chartVersion, appVersion, valuesControllerTag, valuesCrdTag, kustomizeTag, manifestsTag string) {
	t.Helper()
	chart := readFile(t, "deploy/helm/rbgs/Chart.yaml")
	chartVersion = mustMatch1(t, regexp.MustCompile(`(?m)^version:\s*(\S+)\s*$`), chart, "Chart.yaml version")
	appVersion = mustMatch1(t, regexp.MustCompile(`(?m)^appVersion:\s*"([^"]+)"`), chart, "Chart.yaml appVersion")

	values := readFile(t, "deploy/helm/rbgs/values.yaml")
	tags := regexp.MustCompile(`(?m)^\s+tag:\s*"([^"]+)"`).FindAllStringSubmatch(values, -1)
	if len(tags) != 2 {
		t.Fatalf("expected exactly 2 image tags in values.yaml (controller + crdUpgrade), found %d", len(tags))
	}
	valuesControllerTag, valuesCrdTag = tags[0][1], tags[1][1]

	kust := readFile(t, "config/manager/kustomization.yaml")
	kustomizeTag = mustMatch1(t, regexp.MustCompile(`(?m)^\s+newTag:\s*(\S+)\s*$`), kust, "kustomization.yaml newTag")

	manifests := readFile(t, "deploy/kubectl/manifests.yaml")
	manifestsTag = mustMatch1(t, regexp.MustCompile(`(?m)^\s+image:\s*rolebasedgroup/rbgs-controller:(\S+)\s*$`), manifests,
		"controller image tag in deploy/kubectl/manifests.yaml")
	return
}

func TestReleasePinsAreConsistent(t *testing.T) {
	chartVersion, appVersion, vTag, crdTag, kTag, mTag := chartMeta(t)

	if !strings.HasPrefix(vTag, "v") {
		t.Fatalf("controller image tag %q must carry the leading v (values.yaml controller.image.tag)", vTag)
	}
	if appVersion != strings.TrimPrefix(vTag, "v") {
		t.Fatalf("Chart.yaml appVersion %q must equal controller image tag %q minus the leading v", appVersion, vTag)
	}
	if crdTag != vTag {
		t.Fatalf("values.yaml crdUpgrade.image.tag %q != controller.image.tag %q (release skew)", crdTag, vTag)
	}
	if kTag != vTag {
		t.Fatalf("config/manager/kustomization.yaml newTag %q != values.yaml controller tag %q (release skew)", kTag, vTag)
	}
	if mTag != vTag {
		t.Fatalf("deploy/kubectl/manifests.yaml controller image tag %q != values.yaml tag %q (stale generated manifest)", mTag, vTag)
	}

	// chart version must be a semver-ish release id (e.g. 0.9.0-alpha.3)
	if !regexp.MustCompile(`^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$`).MatchString(chartVersion) {
		t.Fatalf("Chart.yaml version %q is not a valid semver", chartVersion)
	}

	// No release file may still reference a *different* vX.Y.Z-<sha> tag than the
	// pinned one (the signature of an incomplete bump).
	stale := regexp.MustCompile(`v\d+\.\d+\.\d+-[0-9a-f]{8}`)
	for _, rel := range []string{
		"deploy/helm/rbgs/Chart.yaml",
		"deploy/helm/rbgs/values.yaml",
		"config/manager/kustomization.yaml",
		"deploy/kubectl/manifests.yaml",
	} {
		body := readFile(t, rel)
		for _, tag := range stale.FindAllString(body, -1) {
			if tag != vTag {
				t.Fatalf("%s still references stale image tag %q (pinned tag is %q)", rel, tag, vTag)
			}
		}
	}
	t.Logf("consistent release pins: chart=%s appVersion=%s tag=%s", chartVersion, appVersion, vTag)
}

func kustomizeBin(t *testing.T) (string, []string) {
	t.Helper()
	root := repoRoot(t)
	if st, err := os.Stat(filepath.Join(root, "bin", "kustomize")); err == nil && !st.IsDir() {
		return filepath.Join(root, "bin", "kustomize"), nil
	}
	if p, err := exec.LookPath("kustomize"); err == nil {
		return p, nil
	}
	// Fallback: run from the module cache/network. Slow the first time.
	return "go", []string{"run", "sigs.k8s.io/kustomize/kustomize/v5@v5.5.0"}
}

func TestKubectlManifestsInSyncWithKustomize(t *testing.T) {
	root := repoRoot(t)
	bin, args := kustomizeBin(t)
	args = append(args, "build", "config/default")
	cmd := exec.Command(bin, args...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			t.Fatalf("kustomize build failed: %v\n%s", err, ee.Stderr)
		}
		t.Fatalf("kustomize build failed: %v", err)
	}
	committed := readFile(t, "deploy/kubectl/manifests.yaml")
	if string(out) != committed {
		t.Fatalf("deploy/kubectl/manifests.yaml is stale: `kustomize build config/default` output differs (%d bytes rendered vs %d committed). Regenerate with `make manifests`.", len(out), len(committed))
	}
	t.Logf("deploy/kubectl/manifests.yaml in sync with kustomize build (%d bytes)", len(out))
}

func TestHelmChartRendersPinnedVersions(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Fatalf("helm is required for the release harness: %v", err)
	}
	root := repoRoot(t)
	chartVersion, appVersion, vTag, crdTag, _, _ := chartMeta(t)

	// NOTE: use Output(), not CombinedOutput(): helm writes kubeconfig-permission
	// warnings to stderr whenever a kube config file exists, and merging stderr
	// corrupts the rendered YAML stream on any dev machine.
	cmd := exec.Command("helm", "template", "rbgs", filepath.Join(root, "deploy/helm/rbgs"))
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("helm template failed: %v", err)
	}
	rendered := string(out)

	if !strings.Contains(rendered, `image: "rolebasedgroup/rbgs-controller:`+vTag+`"`) {
		t.Fatalf("rendered chart does not pin controller image %q", vTag)
	}
	if !strings.Contains(rendered, `image: "rolebasedgroup/rbgs-upgrade-crd:`+crdTagFromValues(t)+`"`) {
		t.Fatalf("rendered chart does not pin crd-upgrade image %q", crdTagFromValues(t))
	}
	if !strings.Contains(rendered, `chartVersion: "`+chartVersion+`"`) {
		t.Fatalf("rendered version ConfigMap does not carry chartVersion %q", chartVersion)
	}
	if !strings.Contains(rendered, `appVersion: "`+appVersion+`"`) {
		t.Fatalf("rendered version ConfigMap does not carry appVersion %q", appVersion)
	}
	t.Logf("helm template renders pinned images (%s, %s) and version ConfigMap (%s / %s)", vTag, crdTag, chartVersion, appVersion)
}

func crdTagFromValues(t *testing.T) string {
	t.Helper()
	_, _, _, crdTag, _, _ := chartMeta(t)
	return crdTag
}
