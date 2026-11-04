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

// Package parity holds the verification harness for PR #549 finding F1: the
// OpenAPI schema embedded in the generated apply configurations must match the
// schema kube-apiserver derives from the CRDs, or fake.NewClientset() and the
// generated Extract<Kind> helpers diverge from real-cluster behavior.
//
// This file builds the server side of the comparison: the type converter that
// kube-apiserver builds for a CRD, replicated from
// k8s.io/apiextensions-apiserver/pkg/apiserver/customresource_handler.go
// (buildOpenAPIModelsForApply): BuildOpenAPIV3 per served version,
// MergeSpecsV3, managedfields.NewTypeConverter. The static OpenAPI spec of
// built-in types is left empty; the RBG CRD schemas are self-contained.
package parity

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/controller/openapi/builder"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/managedfields"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/kube-openapi/pkg/schemaconv"
	"k8s.io/kube-openapi/pkg/spec3"
	"k8s.io/kube-openapi/pkg/validation/spec"
	smdschema "sigs.k8s.io/structured-merge-diff/v6/schema"
	"sigs.k8s.io/structured-merge-diff/v6/typed"
)

// repoRoot returns the repository root; tests run with the package dir as CWD.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	return root
}

// serverModels returns the CRD's OpenAPI v3 schemas, built exactly as
// kube-apiserver's buildOpenAPIModelsForApply builds them.
func serverModels(t *testing.T, crdFile string) map[string]*spec.Schema {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "config", "crd", "bases", crdFile))
	if err != nil {
		t.Fatalf("read CRD %s: %v", crdFile, err)
	}
	jsonData, err := yaml.ToJSON(data)
	if err != nil {
		t.Fatalf("CRD %s is not valid YAML: %v", crdFile, err)
	}
	crd := &apiextensionsv1.CustomResourceDefinition{}
	if err := json.Unmarshal(jsonData, crd); err != nil {
		t.Fatalf("decode CRD %s: %v", crdFile, err)
	}

	var specs []*spec3.OpenAPI
	for _, v := range crd.Spec.Versions {
		if !v.Served {
			continue
		}
		s, err := builder.BuildOpenAPIV3(crd, v.Name, builder.Options{})
		if err != nil {
			t.Fatalf("BuildOpenAPIV3(%s, %s): %v", crd.Name, v.Name, err)
		}
		specs = append(specs, s)
	}
	merged, err := builder.MergeSpecsV3(specs...)
	if err != nil {
		t.Fatalf("MergeSpecsV3(%s): %v", crd.Name, err)
	}
	return merged.Components.Schemas
}

// serverParseableType returns the smd parseable type for definition defName
// (e.g. "workloads.x-k8s.io/v1alpha2.RoleInstance") derived from the CRD,
// i.e. the type kube-apiserver applies against. This is the proposed fix
// direction for F1: extract with the CRD-derived schema instead of the
// Go-derived one.
func serverParseableType(t *testing.T, crdFile, defName string) typed.ParseableType {
	t.Helper()
	smd, err := schemaconv.ToSchemaFromOpenAPI(serverModels(t, crdFile), false)
	if err != nil {
		t.Fatalf("ToSchemaFromOpenAPI(%s): %v", crdFile, err)
	}
	parser := typed.Parser{Schema: smdschema.Schema{Types: smd.Types}}
	// Errors are deferred until the parseable type is used.
	return parser.Type(defName)
}

// serverTypeConverter returns kube-apiserver's apply type converter for the
// CRD stored in crdFile (relative to config/crd/bases).
func serverTypeConverter(t *testing.T, crdFile string, gvk schema.GroupVersionKind) managedfields.TypeConverter {
	t.Helper()
	schemas := serverModels(t, crdFile)
	var err error
	tc, err := managedfields.NewTypeConverter(schemas, false)
	if err != nil {
		t.Fatalf("NewTypeConverter(%s): %v", crdFile, err)
	}

	probe := &unstructured.Unstructured{}
	probe.SetGroupVersionKind(gvk)
	probe.SetName("probe")
	probe.SetNamespace("default")
	if _, err := tc.ObjectToTyped(probe); err != nil {
		// The converter keys types by the x-kubernetes-group-version-kind
		// extension; inject it the way the API server namer does if absent.
		key := fmt.Sprintf("%s/%s.%s", gvk.Group, gvk.Version, gvk.Kind)
		s, ok := schemas[key]
		if !ok {
			t.Fatalf("server models lack a definition for %s (have %d schemas)", key, len(schemas))
		}
		if s.Extensions == nil {
			s.Extensions = spec.Extensions{}
		}
		s.Extensions["x-kubernetes-group-version-kind"] = []interface{}{
			map[string]interface{}{"group": gvk.Group, "version": gvk.Version, "kind": gvk.Kind},
		}
		tc, err = managedfields.NewTypeConverter(schemas, false)
		if err != nil {
			t.Fatalf("NewTypeConverter(%s) after GVK injection: %v", crdFile, err)
		}
		if _, err := tc.ObjectToTyped(probe); err != nil {
			t.Fatalf("server converter still cannot type %s: %v", gvk, err)
		}
	}
	return tc
}

// toUnstructured round-trips any object through JSON into unstructured form,
// exactly what the API server receives on the wire.
func toUnstructured(t *testing.T, obj interface{}) *unstructured.Unstructured {
	t.Helper()
	data, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("marshal object: %v", err)
	}
	u := &unstructured.Unstructured{}
	if err := json.Unmarshal(data, &u.Object); err != nil {
		t.Fatalf("unmarshal object: %v", err)
	}
	return u
}
