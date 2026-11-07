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

package verifyv1a1

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestCRDDeprecationWarningMatchesMarker (L1 contract) pins the shipped CRD
// deprecationWarning to the +kubebuilder:deprecatedversion:warning marker on
// each v1alpha1 types file, and pins the shared "will be removed in 0.10.0"
// timeline. After PR #556 removed the v1alpha1 admission webhooks, the CRD
// warning is the only channel carrying the removal timeline, so drift between
// the marker and the shipped manifests — or a lost timeline — must fail here.
func TestCRDDeprecationWarningMatchesMarker(t *testing.T) {
	kindToTypesFile := map[string]string{
		"RoleBasedGroup":               "rolebasedgroup_types.go",
		"RoleBasedGroupSet":            "rolebasedgroupset_types.go",
		"Instance":                     "instance_types.go",
		"InstanceSet":                  "instanceset_types.go",
		"RoleBasedGroupScalingAdapter": "rolebasedgroupscalingadapter_types.go",
		"ClusterEngineRuntimeProfile":  "clusterengineruntimeprofile_types.go",
	}

	thisDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	apiDir := filepath.Join(thisDir, "..", "..", "..", "..", "api", "workloads", "v1alpha1")
	crdDir := filepath.Join(thisDir, "..", "..", "..", "..", "config", "crd", "bases")

	markerRe := regexp.MustCompile(`\+kubebuilder:deprecatedversion:warning="([^"]+)"`)

	for kind, file := range kindToTypesFile {
		t.Run(kind, func(t *testing.T) {
			src, err := os.ReadFile(filepath.Join(apiDir, file))
			if err != nil {
				t.Fatalf("read types file: %v", err)
			}
			m := markerRe.FindStringSubmatch(string(src))
			if m == nil {
				t.Fatalf("no +kubebuilder:deprecatedversion:warning marker in %s", file)
			}
			marker := m[1]

			if !strings.Contains(marker, "will be removed in 0.10.0") {
				t.Errorf("marker lost the removal timeline: %q", marker)
			}
			if !strings.Contains(marker, "use workloads.x-k8s.io/v1alpha2") {
				t.Errorf("marker lost the replacement pointer: %q", marker)
			}

			crdFile := "workloads.x-k8s.io_" + strings.ToLower(kind) + "s.yaml"
			crd, err := os.ReadFile(filepath.Join(crdDir, crdFile))
			if err != nil {
				t.Fatalf("read CRD manifest: %v", err)
			}
			// Fold the YAML scalar: the warning is emitted on one logical line,
			// so every run of whitespace in the raw file collapses to one space
			// before comparing against the single-line marker text.
			warningRe := regexp.MustCompile(`(?s)deprecationWarning:(.*?)\n\s+name: v1alpha1`)
			w := warningRe.FindStringSubmatch(string(crd))
			if w == nil {
				t.Fatalf("no deprecationWarning for v1alpha1 in %s", crdFile)
			}
			folded := strings.Join(strings.Fields(strings.TrimSpace(w[1])), " ")
			if folded != marker {
				t.Errorf("shipped CRD warning != source marker:\n  crd:    %q\n  marker: %q", folded, marker)
			}
		})
	}
}

// TestNoV1alpha1WebhookRemainsInShippedManifests (L1 contract) asserts the
// shipped admission webhook manifests carry no v1alpha1 entry: the handlers
// were deleted, so any surviving entry would fail every v1alpha1 write in that
// deployment path.
func TestNoV1alpha1WebhookRemainsInShippedManifests(t *testing.T) {
	thisDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	manifests := []string{
		filepath.Join(thisDir, "..", "..", "..", "..", "config", "webhook", "manifests.yaml"),
		filepath.Join(thisDir, "..", "..", "..", "..", "deploy", "kubectl", "manifests.yaml"),
	}
	for _, path := range manifests {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, forbidden := range []string{
			"validate-workloads-x-k8s-io-v1alpha1",
			"v1alpha1-deprecation.kb.io",
		} {
			if strings.Contains(string(data), forbidden) {
				t.Errorf("%s still references removed v1alpha1 webhook %q", path, forbidden)
			}
		}
	}
}
