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

// Package verifyschema is a review harness (additive, test-only) for
// PR #549 "fix(client-go): embed OpenAPI schema in generated apply
// configurations". It rebuilds, from config/crd/bases, the OpenAPI
// models kube-apiserver uses for server-side apply on RBG custom
// resources, and exposes them as a managedfields.TypeConverter so the
// embedded (generated, Go-derived) schema can be compared against the
// schema the API server actually enforces.
//
// Model construction mirrors k8s.io/apiextensions-apiserver
// pkg/apiserver/customresource_handler.go:buildOpenAPIModelsForApply:
// the CRD's openAPIV3Schema (already a structural OpenAPI v3 schema,
// self-contained, no $refs) becomes the kind definition, the envelope
// adds apiVersion/kind/metadata, and the definition carries the
// x-kubernetes-group-version-kind extension that
// apimachinery/pkg/util/managedfields uses to index it by GVK. The
// static definitions (ObjectMeta and other k8s.io built-ins referenced
// by the envelope) are taken from the repository's own
// pkg/generated/openapi, which is what hack/models-schema feeds to
// applyconfiguration-gen.
package verifyschema

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/managedfields"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/kube-openapi/pkg/common"
	kubeopenapiutil "k8s.io/kube-openapi/pkg/util"
	"k8s.io/kube-openapi/pkg/validation/spec"

	pkgopenapi "sigs.k8s.io/rbgs/pkg/generated/openapi"
)

// KindModel pairs a GVK with the server-side OpenAPI model built for it.
type KindModel struct {
	GVK    schema.GroupVersionKind
	Name   string
	Schema *spec.Schema
}

// ServerModels holds the models for every served version of every CRD
// in config/crd/bases, plus the static k8s.io definitions the envelope
// references.
type ServerModels struct {
	Models map[string]*spec.Schema
	Kinds  []KindModel
}

// TypeConverter returns the TypeConverter kube-apiserver would use for
// these CRDs (managedfields.NewTypeConverter over the merged models).
func (s *ServerModels) TypeConverter() (managedfields.TypeConverter, error) {
	return managedfields.NewTypeConverter(s.Models, false)
}

// LoadServerModels finds config/crd/bases (walking up from startDir, the
// package directory at test time) and builds the server-side models.
func LoadServerModels(startDir string) (*ServerModels, error) {
	crdDir, err := findCRDDir(startDir)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(crdDir)
	if err != nil {
		return nil, err
	}

	models := map[string]*spec.Schema{}
	var kinds []KindModel

	for _, e := range entries {
		if e.IsDir() || (!strings.HasSuffix(e.Name(), ".yaml") && !strings.HasSuffix(e.Name(), ".json")) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(crdDir, e.Name()))
		if err != nil {
			return nil, err
		}
		obj, err := yaml.ToJSON(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		crd := &apiextensionsv1.CustomResourceDefinition{}
		if err := json.Unmarshal(obj, crd); err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		for _, v := range crd.Spec.Versions {
			if !v.Served {
				continue
			}
			if v.Schema == nil || v.Schema.OpenAPIV3Schema == nil {
				continue
			}
			gvk := schema.GroupVersionKind{
				Group:   crd.Spec.Group,
				Version: v.Name,
				Kind:    crd.Spec.Names.Kind,
			}
			s, err := jsonSchemaToSpec(v.Schema.OpenAPIV3Schema)
			if err != nil {
				return nil, fmt.Errorf("%s %s: %w", e.Name(), v.Name, err)
			}
			name := fmt.Sprintf("%s.%s.%s", crd.Spec.Group, v.Name, crd.Spec.Names.Kind)
			models[name] = s
			kinds = append(kinds, KindModel{GVK: gvk, Name: name, Schema: s})
		}
	}
	if len(kinds) == 0 {
		return nil, fmt.Errorf("no CRD schemas found under %s", crdDir)
	}

	// Static definitions (k8s.io built-ins only) so the envelope's
	// metadata reference resolves. RBG definitions are excluded: the
	// RBG models in this map come from the CRDs, not from the Go types.
	static := pkgopenapi.GetOpenAPIDefinitions(func(name string) spec.Ref {
		return spec.MustCreateRef("#/definitions/" + kubeopenapiutil.ToRESTFriendlyName(name))
	})
	for name, def := range static {
		if !strings.HasPrefix(name, "k8s.io/") {
			continue
		}
		s := def.Schema
		// Prefer the embedded v2 schema, exactly like hack/models-schema.
		if v2, ok := def.Schema.Extensions[common.ExtensionV2Schema]; ok {
			if v2Schema, isOpenAPISchema := v2.(spec.Schema); isOpenAPISchema {
				s = v2Schema
			}
		}
		sp := &s
		rest := kubeopenapiutil.ToRESTFriendlyName(name)
		if _, exists := models[rest]; !exists {
			models[rest] = sp
		}
	}

	return &ServerModels{Models: models, Kinds: kinds}, nil
}

// jsonSchemaToSpec converts a CRD JSONSchemaProps into a kube-openapi
// spec.Schema via JSON round-trip. CRD structural schemas are already
// OpenAPI v3 schemas and are self-contained (no $ref).
func jsonSchemaToSpec(props *apiextensionsv1.JSONSchemaProps) (*spec.Schema, error) {
	data, err := json.Marshal(props)
	if err != nil {
		return nil, err
	}
	s := &spec.Schema{}
	if err := json.Unmarshal(data, s); err != nil {
		return nil, err
	}
	return s, nil
}

// KindSchema returns the spec schema of the given model with the
// apiVersion/kind/metadata envelope applied, as buildKubeNative does.
func KindSchema(s *spec.Schema) *spec.Schema {
	out := *s
	out.Properties = map[string]spec.Schema{}
	for k, v := range s.Properties {
		out.Properties[k] = v
	}
	out.Properties["apiVersion"] = spec.Schema{SchemaProps: spec.SchemaProps{Type: []string{"string"}}}
	out.Properties["kind"] = spec.Schema{SchemaProps: spec.SchemaProps{Type: []string{"string"}}}
	out.Properties["metadata"] = *spec.RefSchema("#/definitions/io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta")
	out.AddExtension("x-kubernetes-group-version-kind", []interface{}{
		map[string]interface{}{"group": "", "version": "", "kind": ""},
	})
	return &out
}

// withGVK sets the x-kubernetes-group-version-kind extension the
// managedfields type converter indexes by.
func withGVK(s *spec.Schema, gvk schema.GroupVersionKind) *spec.Schema {
	out := *s
	out.Extensions = map[string]interface{}{}
	for k, v := range s.Extensions {
		out.Extensions[k] = v
	}
	out.Extensions["x-kubernetes-group-version-kind"] = []interface{}{
		map[string]interface{}{"group": gvk.Group, "version": gvk.Version, "kind": gvk.Kind},
	}
	return &out
}

// BuildServerModels is LoadServerModels plus the envelope: it returns
// models ready for managedfields.NewTypeConverter, with the
// group-version-kind extension set on each kind definition.
func BuildServerModels(startDir string) (*ServerModels, error) {
	base, err := LoadServerModels(startDir)
	if err != nil {
		return nil, err
	}
	for _, km := range base.Kinds {
		enveloped := KindSchema(km.Schema)
		base.Models[km.Name] = withGVK(enveloped, km.GVK)
	}
	return base, nil
}

// findCRDDir walks up from dir until it finds config/crd/bases.
func findCRDDir(dir string) (string, error) {
	start := dir
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	for {
		candidate := filepath.Join(dir, "config", "crd", "bases")
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find config/crd/bases above %s", start)
		}
		dir = parent
	}
}

// --- fakes for the field manager (mirrors of
// k8s.io/apimachinery/pkg/util/managedfields/internal/testing) ---

// FakeObjectCreater creates empty unstructured objects.
type FakeObjectCreater struct{}

func (f *FakeObjectCreater) New(gvk schema.GroupVersionKind) (runtime.Object, error) {
	u := &unstructured.Unstructured{Object: map[string]interface{}{}}
	u.SetAPIVersion(gvk.GroupVersion().String())
	u.SetKind(gvk.Kind)
	return u, nil
}

// FakeObjectConvertor returns its input unchanged.
type FakeObjectConvertor struct{}

func (c *FakeObjectConvertor) Convert(in, out, context interface{}) error {
	out = in
	return nil
}

func (c *FakeObjectConvertor) ConvertToVersion(in runtime.Object, _ runtime.GroupVersioner) (runtime.Object, error) {
	return in, nil
}

func (c *FakeObjectConvertor) ConvertFieldLabel(_ schema.GroupVersionKind, _, _ string) (string, string, error) {
	return "", "", fmt.Errorf("not implemented")
}

// FakeObjectDefaulter does nothing.
type FakeObjectDefaulter struct{}

func (d *FakeObjectDefaulter) Default(in runtime.Object) {}
