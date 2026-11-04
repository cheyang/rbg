// Command apiserver-sim cross-validates the hand-rolled CRD-derived
// OpenAPI models used by the review harness in
// internal/verifyschema against the models kube-apiserver actually
// builds for server-side apply (apiextensions-apiserver
// builder.BuildOpenAPIV3 + builder.MergeSpecsV3, as in
// customresource_handler.go:buildOpenAPIModelsForApply).
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	crdbuilder "k8s.io/apiextensions-apiserver/pkg/controller/openapi/builder"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/managedfields"
	apimachineryYAML "k8s.io/apimachinery/pkg/util/yaml"
	fieldpath "sigs.k8s.io/structured-merge-diff/v6/fieldpath"

	"k8s.io/kube-openapi/pkg/validation/spec"
	pkgopenapi "sigs.k8s.io/rbgs/pkg/generated/openapi"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "Failed:", err)
		os.Exit(1)
	}
}

func run() error {
	dir := os.Args[1]

	realModels := map[string]*spec.Schema{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".yaml" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return err
		}
		j, err := apimachineryYAML.ToJSON(data)
		if err != nil {
			return err
		}
		crd := &apiextensionsv1.CustomResourceDefinition{}
		if err := json.Unmarshal(j, crd); err != nil {
			return err
		}
		for _, v := range crd.Spec.Versions {
			if !v.Served {
				continue
			}
			s, err := crdbuilder.BuildOpenAPIV3(crd, v.Name, crdbuilder.Options{})
			if err != nil {
				return fmt.Errorf("%s %s: %w", e.Name(), v.Name, err)
			}
			merged, err := crdbuilder.MergeSpecsV3(s)
			if err != nil {
				return err
			}
			for name, schema := range merged.Components.Schemas {
				if _, exists := realModels[name]; !exists {
					realModels[name] = schema
				}
			}
		}
	}

	// Static definitions (the api server's staticOpenAPISpec), so the
	// envelope's ObjectMeta reference resolves.
	static := pkgopenapi.GetOpenAPIDefinitions(func(name string) spec.Ref {
		return spec.MustCreateRef("#/definitions/" + name)
	})
	for name, def := range static {
		sch := def.Schema
		if _, exists := realModels[name]; !exists {
			realModels[name] = &sch
		}
	}

	realConverter, err := managedfields.NewTypeConverter(realModels, false)
	if err != nil {
		return fmt.Errorf("real models converter: %w", err)
	}

	gvk := schema.GroupVersionKind{Group: "workloads.x-k8s.io", Version: "v1alpha2", Kind: "RoleInstance"}
	obj := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "workloads.x-k8s.io/v1alpha2",
		"kind":       "RoleInstance",
		"metadata":   map[string]interface{}{"name": "demo"},
		"spec": map[string]interface{}{
			"components": []interface{}{
				map[string]interface{}{
					"name": "prefill", "size": 1,
					"template": map[string]interface{}{
						"spec": map[string]interface{}{
							"containers": []interface{}{
								map[string]interface{}{"name": "server", "image": "llm"},
							},
						},
					},
				},
			},
		},
	}}
	obj.SetGroupVersionKind(gvk)

	tv, err := realConverter.ObjectToTyped(obj)
	if err != nil {
		return fmt.Errorf("real converter ObjectToTyped: %w", err)
	}
	set, err := tv.ToFieldSet()
	if err != nil {
		return fmt.Errorf("real converter ToFieldSet: %w", err)
	}
	fmt.Println("=== REAL kube-apiserver (BuildOpenAPIV3+MergeSpecsV3) field set for RoleInstance ===")
	var lines []string
	set.Iterate(func(p fieldpath.Path) {
		lines = append(lines, p.String())
	})
	sortStrings(lines)
	for _, l := range lines {
		fmt.Println(l)
	}
	fmt.Println("=== REAL model names (RBG kinds) ===")
	for _, n := range sortedKeys(realModels) {
		if len(n) > 0 && (n == "workloads.x-k8s.io.v1alpha2.RoleInstance" || n == "workloads.x-k8s.io.v1alpha1.Instance" || n == "io.k8s.sigs.rbgs.api.workloads.v1alpha2.RoleInstance") {
			fmt.Println("RBG model:", n)
		}
	}
	for _, n := range sortedKeys(realModels) {
		m := realModels[n]
		if gvks, ok := m.Extensions["x-kubernetes-group-version-kind"]; ok {
			fmt.Printf("GVK-indexed model %q -> %v\n", n, gvks)
		}
	}
	return nil
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func sortedKeys(m map[string]*spec.Schema) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}
