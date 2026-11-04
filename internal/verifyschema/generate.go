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

package verifyschema

import (
	"fmt"

	"k8s.io/kube-openapi/pkg/validation/spec"
)

// GenerateObject walks a (server-side) spec.Schema and produces an
// object that sets every property, with one element per list and map,
// and a pod-template-like value inside every schemaless
// (x-kubernetes-preserve-unknown-fields) subtree. The result exercises
// the whole field set the server would track.
//
// Values are chosen to be structurally valid (right JSON types); they
// do not need to satisfy value validation, which server-side apply
// field tracking does not consult.
func GenerateObject(s *spec.Schema, models map[string]*spec.Schema) (map[string]interface{}, error) {
	v, err := generateValue(s, models, 0)
	if err != nil {
		return nil, err
	}
	m, ok := v.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("schema did not produce an object: %T", v)
	}
	return m, nil
}

const maxDepth = 12

func generateValue(s *spec.Schema, models map[string]*spec.Schema, depth int) (interface{}, error) {
	if depth > maxDepth {
		return nil, nil
	}
	if s == nil {
		return nil, nil
	}
	// Follow top-level references (CRD schemas are self-contained; this
	// is only for safety).
	if ref := s.Ref.String(); len(ref) > 0 {
		name := refName(ref)
		if target, ok := models[name]; ok {
			return generateValue(target, models, depth+1)
		}
		return nil, nil
	}

	if isIntOrString(s) {
		return 1, nil
	}

	if isSchemaless(s) {
		// A schemaless subtree: emit a pod-template-like value with a
		// nested map and a list, so both map-per-key and list-as-whole
		// tracking are exercised. The keys are chosen to be valid
		// PodTemplateSpec fields as well, so the object is also
		// acceptable to the (differently typed) embedded schema and the
		// two field sets stay comparable.
		return map[string]interface{}{
			"spec": map[string]interface{}{
				"containers": []interface{}{
					map[string]interface{}{"name": "c0", "image": "img0"},
				},
			},
		}, nil
	}

	typ := ""
	if len(s.Type) > 0 {
		typ = s.Type[0]
	}
	switch typ {
	case "object":
		return generateObject(s, models, depth)
	case "array":
		return generateArray(s, models, depth)
	case "string":
		return "v", nil
	case "integer", "number":
		return 1, nil
	case "boolean":
		return true, nil
	case "":
		// No type: any. Emit an object to exercise map tracking.
		return map[string]interface{}{"untyped": "v"}, nil
	default:
		return "v", nil
	}
}

func generateObject(s *spec.Schema, models map[string]*spec.Schema, depth int) (interface{}, error) {
	out := map[string]interface{}{}
	for name, member := range s.Properties {
		v, err := generateValue(&member, models, depth+1)
		if err != nil {
			return nil, fmt.Errorf("property %q: %w", name, err)
		}
		if v != nil {
			out[name] = v
		}
	}
	if s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil && len(s.Properties) == 0 {
		v, err := generateValue(s.AdditionalProperties.Schema, models, depth+1)
		if err != nil {
			return nil, err
		}
		if v != nil {
			out["k0"] = v
		}
	}
	return out, nil
}

func generateArray(s *spec.Schema, models map[string]*spec.Schema, depth int) (interface{}, error) {
	keys := listMapKeys(s)
	var elem *spec.Schema
	if s.Items != nil && s.Items.Schema != nil {
		elem = s.Items.Schema
	}
	member, err := generateValue(elem, models, depth+1)
	if err != nil {
		return nil, err
	}
	if member == nil {
		return []interface{}{}, nil
	}
	if len(keys) > 0 {
		if m, ok := member.(map[string]interface{}); ok {
			for i, k := range keys {
				if _, exists := m[k]; !exists {
					m[k] = fmt.Sprintf("v%d", i)
				}
			}
		}
	}
	return []interface{}{member}, nil
}

func listMapKeys(s *spec.Schema) []string {
	if v, ok := s.Extensions["x-kubernetes-list-map-keys"]; ok {
		if keys, ok := v.([]string); ok {
			return keys
		}
		if keys, ok := v.([]interface{}); ok {
			var out []string
			for _, k := range keys {
				if ks, ok := k.(string); ok {
					out = append(out, ks)
				}
			}
			return out
		}
	}
	return nil
}

// isSchemaless reports whether the node carries
// x-kubernetes-preserve-unknown-fields and declares no structure of its
// own (no type, no properties): the server tracks its contents as
// deduced types (maps per key, lists as a whole).
func isSchemaless(s *spec.Schema) bool {
	v, ok := s.Extensions["x-kubernetes-preserve-unknown-fields"]
	return ok && v == true && len(s.Type) == 0 && len(s.Properties) == 0
}

func isIntOrString(s *spec.Schema) bool {
	if v, ok := s.Extensions["x-kubernetes-int-or-string"]; ok && v == true {
		return true
	}
	return s.Format == "int-or-string"
}

func refName(ref string) string {
	// strip #/definitions/ or #/components/schemas/
	for _, prefix := range []string{"#/definitions/", "#/components/schemas/"} {
		if len(ref) > len(prefix) && ref[:len(prefix)] == prefix {
			return ref[len(prefix):]
		}
	}
	return ref
}
