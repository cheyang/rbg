// Command harness runs the offline integration checks (L2) of the verification
// harness for PR #568 (rbg-conventions skill). It needs no cluster.
//
// Usage:
//
//	go run ./docs/verification/rbg-conventions/harness <subcommand>
//
// Subcommands:
//
//	jsonpath  evaluate the exact kubectl jsonpath templates the scripts use,
//	          with client-go's jsonpath (the library kubectl itself uses).
//	fixtures  validate .agents/skills/rbg-conventions/scripts/fixtures/rbg-fixtures.yaml
//	          against config/crd/bases/workloads.x-k8s.io_rolebasedgroups.yaml
//	          (JSON-schema validation, CEL x-kubernetes-validations, and unknown-field
//	          pruning), the way a real API server would.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"sigs.k8s.io/yaml"

	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/pruning"
	apiextensionsvalidation "k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	clientgojsonpath "k8s.io/client-go/util/jsonpath"

	// for the CEL cost constants
	apiservercel "k8s.io/apiserver/pkg/apis/cel"
)

const perCallLimit = apiservercel.PerCallLimit

const (
	crdPath     = "config/crd/bases/workloads.x-k8s.io_rolebasedgroups.yaml"
	fixturePath = ".agents/skills/rbg-conventions/scripts/fixtures/rbg-fixtures.yaml"
)

// findRepoRoot walks up from dir until the RBG repo layout is found.
func findRepoRoot(dir string) string {
	for {
		if _, err := os.Stat(filepath.Join(dir, "config", "crd", "bases", "workloads.x-k8s.io_rolebasedgroups.yaml")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: harness <jsonpath|fixtures>")
		os.Exit(2)
	}
	var failed bool
	switch os.Args[1] {
	case "jsonpath":
		failed = checkJSONPath()
	case "fixtures":
		failed = checkFixtures()
	default:
		fmt.Fprintln(os.Stderr, "unknown subcommand", os.Args[1])
		os.Exit(2)
	}
	if failed {
		os.Exit(1)
	}
	fmt.Println("OK")
}

func cwd() string {
	d, _ := os.Getwd()
	return d
}

// --- jsonpath checks -------------------------------------------------------

// evalJSONPath evaluates tpl against obj the way kubectl does
// (AllowMissingKeys(true), single template string).
func evalJSONPath(tpl string, obj map[string]interface{}) ([]string, error) {
	jp := clientgojsonpath.New("check")
	jp.AllowMissingKeys(true)
	if err := jp.Parse(tpl); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	results, err := jp.FindResults(obj)
	if err != nil {
		return nil, fmt.Errorf("find: %w", err)
	}
	var vals []string
	for _, rr := range results {
		for _, v := range rr {
			b, err := json.Marshal(v.Interface())
			if err != nil {
				return nil, err
			}
			vals = append(vals, string(b))
		}
	}
	return vals, nil
}

func checkJSONPath() bool {
	nsObj := map[string]interface{}{
		"kind": "Namespace",
		"metadata": map[string]interface{}{
			"uid":             "uid-1",
			"resourceVersion": "42",
			"labels": map[string]interface{}{
				"rbg-conventions/verification": "true",
				"rbg-conventions/owner":        "tok123",
			},
		},
		"spec": map[string]interface{}{
			"conversion": map[string]interface{}{"strategy": "Webhook"},
		},
	}
	type tc struct {
		name     string
		tpl      string
		wantVals []string
	}
	cases := []tc{
		// common.sh require_owned_namespace
		{"slash key in dot notation", `{.metadata.labels.rbg-conventions/owner}`, []string{`"tok123"`}},
		{"combined owner+uid template", `{.metadata.labels.rbg-conventions/owner} {.metadata.uid}`, []string{`"tok123"`, `" "`, `"uid-1"`}},
		// rbg-fixtures.sh delete loop
		{"uid alone", `{.metadata.uid}`, []string{`"uid-1"`}},
		// rbg-fixtures.sh apply create
		{"uid on create", `{.metadata.uid}`, []string{`"uid-1"`}},
		// crd-default-on-read.sh A3/A4
		{"resourceVersion", `{.metadata.resourceVersion}`, []string{`"42"`}},
		// v1alpha1-roundtrip.sh conversion strategy
		{"conversion strategy", `{.spec.conversion.strategy}`, []string{`"Webhook"`}},
		// label missing (foreign namespace): the expression yields nothing,
		// the rest of the template still evaluates, so ownership checks fail closed.
		{"missing label yields no value", `{.metadata.labels.rbg-conventions/owner} {.metadata.uid}`, []string{`" "`, `"uid-1"`}},
	}
	failed := false
	for _, c := range cases {
		obj := nsObj
		if c.name == "missing label yields no value" {
			clone, _ := json.Marshal(nsObj)
			obj = map[string]interface{}{}
			_ = json.Unmarshal(clone, &obj)
			meta := obj["metadata"].(map[string]interface{})
			delete(meta["labels"].(map[string]interface{}), "rbg-conventions/owner")
		}
		got, err := evalJSONPath(c.tpl, obj)
		if err != nil {
			fmt.Printf("FAIL %s: %v\n", c.name, err)
			failed = true
			continue
		}
		if !reflect.DeepEqual(got, c.wantVals) {
			fmt.Printf("FAIL %s: got %v, want %v\n", c.name, got, c.wantVals)
			failed = true
			continue
		}
		fmt.Printf("PASS %s\n", c.name)
	}
	return failed
}

// --- fixture validation ----------------------------------------------------

func checkFixtures() bool {
	repoRoot := findRepoRoot(cwd())
	if repoRoot == "" {
		fmt.Println("FAIL cannot locate the rbg repo root (run from a directory inside the checkout)")
		return true
	}
	crdPath := filepath.Join(repoRoot, crdPath)
	crdData, err := os.ReadFile(crdPath)
	if err != nil {
		fmt.Println("FAIL cannot read CRD:", err)
		return true
	}
	var crdV1 apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(crdData, &crdV1); err != nil {
		fmt.Println("FAIL cannot parse CRD:", err)
		return true
	}
	scheme := runtime.NewScheme()
	if err := apiextensions.AddToScheme(scheme); err != nil {
		panic(err)
	}
	if err := apiextensionsv1.AddToScheme(scheme); err != nil {
		panic(err)
	}
	crd := &apiextensions.CustomResourceDefinition{}
	if err := scheme.Convert(&crdV1, crd, nil); err != nil {
		fmt.Println("FAIL cannot convert CRD to internal:", err)
		return true
	}

	var version *apiextensions.CustomResourceDefinitionVersion
	for i := range crd.Spec.Versions {
		if crd.Spec.Versions[i].Name == "v1alpha2" {
			version = &crd.Spec.Versions[i]
		}
	}
	if version == nil {
		fmt.Println("FAIL CRD has no v1alpha2 version")
		return true
	}
	structural, err := structuralschema.NewStructural(version.Schema.OpenAPIV3Schema)
	if err != nil {
		fmt.Println("FAIL cannot build structural schema:", err)
		return true
	}
	schemaValidator, _, err := apiextensionsvalidation.NewSchemaValidator(version.Schema.OpenAPIV3Schema)
	if err != nil {
		fmt.Println("FAIL cannot build schema validator:", err)
		return true
	}
	celValidator := cel.NewValidator(structural, true, perCallLimit)

	fixturePath := filepath.Join(repoRoot, fixturePath)
	fixtureData, err := os.ReadFile(fixturePath)
	if err != nil {
		fmt.Println("FAIL cannot read fixtures:", err)
		return true
	}
	fixtureData = []byte(strings.ReplaceAll(string(fixtureData), "__IMAGE__", "busybox:1.36"))

	failed := false
	docs := strings.Split(string(fixtureData), "\n---")
	for _, doc := range docs {
		var obj map[string]interface{}
		if err := yaml.Unmarshal([]byte(doc), &obj); err != nil {
			fmt.Println("FAIL cannot parse fixture document:", err)
			failed = true
			continue
		}
		if len(obj) == 0 {
			continue
		}
		name := obj["metadata"].(map[string]interface{})["name"]

		// 1. JSON-schema validation (as admission performs it).
		result := schemaValidator.Validate(obj)
		if !result.IsValid() {
			fmt.Printf("FAIL %s: schema validation errors:\n", name)
			for _, e := range result.Errors {
				fmt.Printf("  %v\n", e)
			}
			failed = true
		} else {
			fmt.Printf("PASS %s: schema validation\n", name)
		}

		// 2. CEL x-kubernetes-validations.
		if celValidator != nil {
			errs, _ := celValidator.Validate(context.Background(), field.NewPath(""),
				structural, obj, nil, apiservercel.RuntimeCELCostBudget)
			if len(errs) > 0 {
				fmt.Printf("FAIL %s: CEL validation errors:\n", name)
				for _, e := range errs {
					fmt.Printf("  %v\n", e)
				}
				failed = true
			} else {
				fmt.Printf("PASS %s: CEL validation\n", name)
			}
		}

		// 3. Unknown fields: what a real apply would prune.
		before, _ := json.Marshal(obj)
		pruning.Prune(obj, structural, true)
		after, _ := json.Marshal(obj)
		if string(before) != string(after) {
			fmt.Printf("FAIL %s: fields pruned as unknown (fixture would not survive a real apply):\n  %s\n", name, diffJSON(before, after))
			failed = true
		} else {
			fmt.Printf("PASS %s: no unknown fields (nothing pruned)\n", name)
		}
	}
	return failed
}

func diffJSON(before, after []byte) string {
	var b, a map[string]interface{}
	_ = json.Unmarshal(before, &b)
	_ = json.Unmarshal(after, &a)
	bm, _ := json.MarshalIndent(b, "", "  ")
	am, _ := json.MarshalIndent(a, "", "  ")
	return fmt.Sprintf("before:\n%s\nafter pruning:\n%s", bm, am)
}
