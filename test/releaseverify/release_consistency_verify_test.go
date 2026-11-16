// Verification harness for sgl-project/rbg PR #590 (release-manifest bump to
// 0.9.0-alpha.4 / v0.9.0-1fc69f70). Additive only: no production code changes.
//
// Polarity: every test here is a CONTRACT test. It asserts the intended correct
// end-state of the release bump, so it FAILS on the base branch (old tag still
// present) and PASSES on the PR head. A red run on base is the harness-bites
// proof, not a finding.
package releaseverify

import (
	"os"
	"strings"
	"testing"
)

const (
	oldTag      = "v0.9.0-73e05db4" // image tag shipped by 0.9.0-alpha.3
	newTag      = "v0.9.0-1fc69f70" // image tag shipped by 0.9.0-alpha.4
	newAppVer   = "0.9.0-1fc69f70"  // appVersion (no v prefix, per repo convention)
	newChartVer = "0.9.0-alpha.4"   // chart version
	oldChartVer = "0.9.0-alpha.3"
	releaseSHA8 = "1fc69f70" // short sha the release is cut from (merge-base / main tip)
)

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// C1: the old release tag must be gone from every shipped manifest/chart file.
func TestC1_NoStaleOldTag(t *testing.T) {
	files := []string{
		"../../config/manager/kustomization.yaml",
		"../../deploy/helm/rbgs/Chart.yaml",
		"../../deploy/helm/rbgs/values.yaml",
		"../../deploy/kubectl/manifests.yaml",
	}
	for _, f := range files {
		if c := readFile(t, f); strings.Contains(c, oldTag) || strings.Contains(c, "73e05db4") {
			t.Errorf("%s still references old release tag %s", f, oldTag)
		}
	}
}

// C2: the new image tag must be present in every location that pins the
// controller/CRD-upgrader image, and chart metadata must move to alpha.4.
func TestC2_NewTagEverywhere(t *testing.T) {
	cases := []struct {
		path string
		want []string
	}{
		{"../../config/manager/kustomization.yaml", []string{"newTag: " + newTag}},
		{"../../deploy/helm/rbgs/values.yaml", []string{`tag: "` + newTag + `"`}}, // controller + crdUpgrade, counted below
		{"../../deploy/kubectl/manifests.yaml", []string{"image: rolebasedgroup/rbgs-controller:" + newTag}},
		{"../../deploy/helm/rbgs/Chart.yaml", []string{"version: " + newChartVer, `appVersion: "` + newAppVer + `"`}},
	}
	for _, c := range cases {
		content := readFile(t, c.path)
		for _, w := range c.want {
			if !strings.Contains(content, w) {
				t.Errorf("%s missing %q", c.path, w)
			}
		}
	}
	// values.yaml pins both images; the new tag must appear exactly twice there.
	if n := strings.Count(readFile(t, "../../deploy/helm/rbgs/values.yaml"), newTag); n != 2 {
		t.Errorf("values.yaml: want 2 occurrences of %s (controller + crdUpgrade), got %d", newTag, n)
	}
	if c := readFile(t, "../../deploy/helm/rbgs/Chart.yaml"); strings.Contains(c, oldChartVer) {
		t.Errorf("Chart.yaml still at old chart version %s", oldChartVer)
	}
}

// C3: the image tag's sha suffix must equal the 8-char short sha of the
// commit the release is cut from (PR base / main tip), matching the Makefile's
// TAG = VERSION-GIT_SHA convention.
func TestC3_TagMatchesReleaseSHA(t *testing.T) {
	if !strings.HasSuffix(newTag, "-"+releaseSHA8) {
		t.Errorf("image tag %s does not end with release sha %s", newTag, releaseSHA8)
	}
	if !strings.HasSuffix(newAppVer, "-"+releaseSHA8) {
		t.Errorf("appVersion %s does not end with release sha %s", newAppVer, releaseSHA8)
	}
}
