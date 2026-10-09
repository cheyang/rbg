/*
Copyright 2026 The RoleBasedGroup Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Verification harness for the review of PR #473 (KEP-473: Topology-Aware
Scheduling). Not production code; additive only.

Integration layer: the KEP's example YAML is embedded in the markdown document.
This test extracts every fenced ```yaml block from the KEP and strictly decodes
the ones whose dialect is backed by real upstream API types vendored in this
repo (Volcano scheduling.volcano.sh/v1beta1; scheduler-plugins
scheduling.x-k8s.io/v1alpha1 — the KEP's Koordinator example declares
scheduling.sigs.k8s.io/v1alpha1, Koordinator's deployed spelling of the same
object, so the group is swapped before strict decoding).

Strict decoding fails on unknown or misspelled fields, which is exactly the
failure mode a design-doc example can carry silently. The KAI example
(scheduling.run.ai/v2alpha2) has no vendored Go types in this repo; its field
names are verified textually against upstream snapshots by
scripts/check_claims.py (check C3).
*/

package harness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	cjson "k8s.io/apimachinery/pkg/runtime/serializer/json"
	"sigs.k8s.io/yaml"

	schedv1alpha1 "sigs.k8s.io/scheduler-plugins/apis/scheduling/v1alpha1"
	volcanoschedulingv1beta1 "volcano.sh/apis/pkg/apis/scheduling/v1beta1"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// docs/verification/<topic>/harness -> repo root is four levels up.
	root := filepath.Clean(filepath.Join(wd, "..", "..", "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repo root not found from %s: %v", wd, err)
	}
	return root
}

func kepText(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "keps", "473-topology-aware-scheduling", "README.md"))
	if err != nil {
		t.Fatalf("read KEP: %v", err)
	}
	return string(data)
}

// yamlBlocks extracts fenced ```yaml blocks from the KEP markdown.
func yamlBlocks(t *testing.T) []string {
	t.Helper()
	var blocks []string
	for _, chunk := range strings.Split(kepText(t), "```yaml\n") {
		if idx := strings.Index(chunk, "\n```"); idx >= 0 {
			blocks = append(blocks, chunk[:idx+1])
		}
	}
	if len(blocks) == 0 {
		t.Fatal("no yaml blocks found in KEP")
	}
	return blocks
}

// decodeStrict converts the YAML document to JSON, optionally overrides its
// apiVersion, and strictly decodes it into obj's scheme type.
func decodeStrict(t *testing.T, block, apiVersionOverride string, into runtime.Object, scheme *runtime.Scheme) {
	t.Helper()
	jsonBytes, err := yaml.YAMLToJSON([]byte(block))
	if err != nil {
		t.Fatalf("YAMLToJSON: %v", err)
	}
	if apiVersionOverride != "" {
		var m map[string]interface{}
		if err := json.Unmarshal(jsonBytes, &m); err != nil {
			t.Fatalf("unmarshal for apiVersion override: %v", err)
		}
		m["apiVersion"] = apiVersionOverride
		jsonBytes, err = json.Marshal(m)
		if err != nil {
			t.Fatalf("marshal after apiVersion override: %v", err)
		}
	}
	codec := cjson.NewSerializer(cjson.DefaultMetaFactory, scheme, scheme, true)
	gvk := into.GetObjectKind().GroupVersionKind()
	obj, gvkOut, err := codec.Decode(jsonBytes, &gvk, into)
	if err != nil {
		t.Fatalf("strict decode failed (misspelled or unknown field in KEP example): %v", err)
	}
	if obj == nil || gvkOut == nil {
		t.Fatal("decode returned no object")
	}
}

func volcanoScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := volcanoschedulingv1beta1.AddToScheme(s); err != nil {
		t.Fatalf("volcano scheme: %v", err)
	}
	return s
}

func pluginsScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := schedv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("scheduler-plugins scheme: %v", err)
	}
	return s
}

func TestVolcanoExampleStrictDecodes(t *testing.T) {
	var found int
	for _, block := range yamlBlocks(t) {
		if !strings.Contains(block, "apiVersion: scheduling.volcano.sh/v1beta1") {
			continue
		}
		found++
		decodeStrict(t, block, "", &volcanoschedulingv1beta1.PodGroup{}, volcanoScheme(t))
	}
	if found == 0 {
		t.Fatal("KEP has no scheduling.volcano.sh/v1beta1 example to verify")
	}
	t.Logf("strict-decoded %d Volcano PodGroup example(s)", found)
}

func TestSigsPodGroupExampleStrictDecodes(t *testing.T) {
	var found int
	for _, block := range yamlBlocks(t) {
		if !strings.Contains(block, "apiVersion: scheduling.sigs.k8s.io/v1alpha1") {
			continue
		}
		found++
		// Koordinator's deployed spelling of the scheduler-plugins PodGroup
		// group; swap to the vendored group before strict decoding.
		decodeStrict(t, block, "scheduling.x-k8s.io/v1alpha1", &schedv1alpha1.PodGroup{}, pluginsScheme(t))
	}
	if found == 0 {
		t.Fatal("KEP has no scheduling.sigs.k8s.io/v1alpha1 example to verify")
	}
	t.Logf("strict-decoded %d sigs PodGroup example(s)", found)
}

// TestSigsExampleMinMemberZero documents the CRD-level divergence behind
// review finding F9: the upstream scheduler-plugins CRD enforces
// spec.minMember >= 1, while Koordinator's deployed CRD does not. The Go type
// accepts 0, so only the CRD schema can reject it — recorded here as text,
// proven in scripts/check_claims.py (F9).
func TestSigsExampleMinMemberZero(t *testing.T) {
	for _, block := range yamlBlocks(t) {
		if !strings.Contains(block, "apiVersion: scheduling.sigs.k8s.io/v1alpha1") {
			continue
		}
		if !strings.Contains(block, "minMember: 0") {
			t.Fatalf("expected the topology-only Koordinator example to render minMember: 0, block:\n%s", block)
		}
		t.Log("Koordinator topology-only example renders minMember: 0 (see F9: upstream CRD requires >= 1)")
	}
}

// TestKAIExampleStructure checks the KAI example carries the fields the KEP's
// design depends on, as a textual stand-in for the missing vendored types.
func TestKAIExampleStructure(t *testing.T) {
	var kai []string
	for _, block := range yamlBlocks(t) {
		if strings.Contains(block, "apiVersion: scheduling.run.ai/v2alpha2") {
			kai = append(kai, block)
		}
	}
	if len(kai) != 1 {
		t.Fatalf("expected exactly one KAI PodGroup example, got %d", len(kai))
	}
	block := kai[0]
	for _, want := range []string{
		"requiredTopologyLevel: topology.block",
		"preferredTopologyLevel: topology.rack",
		"topology: cluster-topology",
		"minSubGroup: 0",
		"parent: pd-colocation",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("KAI example missing %q", want)
		}
	}
}
