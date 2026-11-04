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
	"encoding/json"
	"fmt"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/managedfields"

	workloadsv1alpha1 "sigs.k8s.io/rbgs/api/workloads/v1alpha1"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	applyv1alpha1 "sigs.k8s.io/rbgs/client-go/applyconfiguration/workloads/v1alpha1"
	applyv1alpha2 "sigs.k8s.io/rbgs/client-go/applyconfiguration/workloads/v1alpha2"
)

// serverFieldManager builds the field manager the kube-apiserver CRD
// handler uses (managedfields.NewDefaultCRDFieldManager over the CRD
// models), with the same fakes the apimachinery test suite uses.
func serverFieldManager(t *testing.T, gvk schema.GroupVersionKind) *managedfields.FieldManager {
	t.Helper()
	converter, _ := serverParity(t)
	fm, err := managedfields.NewDefaultCRDFieldManager(
		converter,
		&FakeObjectConvertor{},
		&FakeObjectDefaulter{},
		&FakeObjectCreater{},
		gvk,
		gvk.GroupVersion(),
		"",
		nil,
	)
	if err != nil {
		t.Fatalf("failed to build CRD field manager: %v", err)
	}
	return fm
}

func emptyLive(gvk schema.GroupVersionKind) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": gvk.GroupVersion().String(),
		"kind":       gvk.Kind,
	}}
	return u
}

// TestExtractRoleInstanceKeepsComponentTemplates reproduces the
// extract -> modify -> apply data-loss cycle against real
// server-side-apply semantics:
//
//  1. A RoleInstance with a component pod template is applied by field
//     manager "applier". Managed fields are recorded with the schema
//     kube-apiserver builds from the CRD (spec.components[*].template
//     is x-kubernetes-preserve-unknown-fields there, so the server
//     tracks the container list as a whole).
//  2. The generated client-go ExtractRoleInstance helper interprets
//     those managed fields with the embedded Go-derived schema, which
//     types the template as core/v1.PodTemplateSpec with containers
//     keyed by name.
//  3. The extracted configuration is re-applied after a trivial
//     modification, the documented extract/modify/apply workflow.
//
// Contract: both the extract and the live object keep the container
// the manager applied. On the Go-derived schema embedded by this PR,
// the extract drops the template contents and the re-apply deletes
// them from the live object.
func TestExtractRoleInstanceKeepsComponentTemplates(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "workloads.x-k8s.io", Version: "v1alpha2", Kind: "RoleInstance"}
	fm := serverFieldManager(t, gvk)

	applied := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": gvk.GroupVersion().String(),
		"kind":       gvk.Kind,
		"metadata":   map[string]interface{}{"name": "demo", "namespace": "default"},
		"spec": map[string]interface{}{
			"components": []interface{}{
				map[string]interface{}{
					"name": "prefill",
					"size": 1,
					"template": map[string]interface{}{
						"spec": map[string]interface{}{
							"containers": []interface{}{
								map[string]interface{}{"name": "server", "image": "llm:latest"},
							},
						},
					},
				},
			},
		},
	}}

	live, err := fm.Apply(emptyLive(gvk), applied, "applier", false)
	if err != nil {
		t.Fatalf("server-side apply failed: %v", err)
	}
	liveU := live.(*unstructured.Unstructured)
	liveU.Object = normalizeJSON(liveU.Object).(map[string]interface{})
	if got := containersOf(t, liveU); len(got) != 1 || fieldName(got[0]) != "server" {
		t.Fatalf("live object after apply does not carry the container: %v", got)
	}
	if len(liveU.GetManagedFields()) == 0 {
		t.Fatal("apply recorded no managed fields")
	}

	// Convert the live object (server-recorded managed fields) to the
	// typed API object the generated helper requires.
	roleInstance := &workloadsv1alpha2.RoleInstance{}
	data, err := json.Marshal(liveU.Object)
	if err != nil {
		t.Fatalf("marshal live object: %v", err)
	}
	if err := json.Unmarshal(data, roleInstance); err != nil {
		t.Fatalf("unmarshal live object into typed RoleInstance: %v", err)
	}

	extracted, err := applyv1alpha2.ExtractRoleInstance(roleInstance, "applier")
	if err != nil {
		t.Fatalf("ExtractRoleInstance: %v", err)
	}

	// Contract: the extract of a single manager that applied everything
	// must keep the component template it applied.
	var extractedContainers []interface{}
	if extracted.Spec != nil && len(extracted.Spec.Components) == 1 {
		comp := extracted.Spec.Components[0]
		if comp.Template != nil && comp.Template.Spec != nil {
			extractedContainers = toInterfaces(comp.Template.Spec.Containers)
		}
	}
	if len(extractedContainers) != 1 {
		t.Errorf("extract dropped the component pod template: extracted spec = %s", mustJSON(t, extracted.Spec))
	}

	// The documented workflow: modify the extract and apply it back.
	if extracted.Spec != nil && len(extracted.Spec.Components) == 1 {
		extracted.Spec.Components[0].WithSize(2)
	}
	applyConfigJSON, err := json.Marshal(extracted)
	if err != nil {
		t.Fatalf("marshal extracted apply configuration: %v", err)
	}
	reapply := &unstructured.Unstructured{}
	if err := json.Unmarshal(applyConfigJSON, reapply); err != nil {
		t.Fatalf("unmarshal extracted apply configuration: %v", err)
	}
	reapply.SetGroupVersionKind(gvk)

	live2, err := fm.Apply(live.DeepCopyObject(), reapply, "applier", false)
	if err != nil {
		t.Fatalf("re-apply of the extracted configuration failed: %v", err)
	}
	live2U := live2.(*unstructured.Unstructured)
	live2U.Object = normalizeJSON(live2U.Object).(map[string]interface{})
	got := containersOf(t, live2U)
	if len(got) != 1 || fieldName(got[0]) != "server" {
		t.Errorf("extract -> modify -> apply DELETED the live container (user-visible data loss): containers now = %v", got)
	}
}

// normalizeJSON converts values produced by smd's value.Unstructured()
// (map[interface{}]interface{} for deduced subtrees) into plain JSON
// types.
func normalizeJSON(v interface{}) interface{} {
	switch t := v.(type) {
	case map[interface{}]interface{}:
		out := make(map[string]interface{}, len(t))
		for k, val := range t {
			out[fmt.Sprintf("%v", k)] = normalizeJSON(val)
		}
		return out
	case map[string]interface{}:
		out := make(map[string]interface{}, len(t))
		for k, val := range t {
			out[k] = normalizeJSON(val)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(t))
		for i := range t {
			out[i] = normalizeJSON(t[i])
		}
		return out
	case int:
		// json.Unmarshal produces float64; smd round-trips typed scalars
		// as int64. Coerce so DeepCopyJSONValue accepts the result.
		return int64(t)
	case int32:
		return int64(t)
	case float32:
		return float64(t)
	default:
		return v
	}
}

func containersOf(t *testing.T, u *unstructured.Unstructured) []interface{} {
	t.Helper()
	comps, _, err := unstructured.NestedSlice(u.Object, "spec", "components")
	if err != nil || len(comps) == 0 {
		return nil
	}
	comp, _ := comps[0].(map[string]interface{})
	if comp == nil {
		return nil
	}
	containers, _, err := unstructured.NestedSlice(comp, "template", "spec", "containers")
	if err != nil {
		t.Fatalf("reading containers from live object: %v", err)
	}
	return containers
}

func toInterfaces[T any](in []T) []interface{} {
	out := make([]interface{}, len(in))
	for i := range in {
		out[i] = in[i]
	}
	return out
}

func fieldName(v interface{}) string {
	if m, ok := v.(map[string]interface{}); ok {
		if n, ok := m["name"].(string); ok {
			return n
		}
	}
	return ""
}

func mustJSON(t *testing.T, v interface{}) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("<error: %v>", err)
	}
	return string(data)
}

// TestExtractControlEmbeddedRecordingKeepsTemplates is the control for
// TestExtractRoleInstanceKeepsComponentTemplates: when the managed
// fields are recorded with the SAME schema the extractor uses (the
// embedded one), the extract -> modify -> apply cycle keeps the
// template. This proves the failing test is red because of the
// schema divergence, not because the workflow itself is broken.
func TestExtractControlEmbeddedRecordingKeepsTemplates(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "workloads.x-k8s.io", Version: "v1alpha2", Kind: "RoleInstance"}
	embedded, _ := embeddedConverter(t)
	fm, err := managedfields.NewDefaultCRDFieldManager(
		embedded,
		&FakeObjectConvertor{},
		&FakeObjectDefaulter{},
		&FakeObjectCreater{},
		gvk,
		gvk.GroupVersion(),
		"",
		nil,
	)
	if err != nil {
		t.Fatalf("failed to build field manager: %v", err)
	}

	applied := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": gvk.GroupVersion().String(),
		"kind":       gvk.Kind,
		"metadata":   map[string]interface{}{"name": "demo", "namespace": "default"},
		"spec": map[string]interface{}{
			"components": []interface{}{
				map[string]interface{}{
					"name": "prefill",
					"size": 1,
					"template": map[string]interface{}{
						"spec": map[string]interface{}{
							"containers": []interface{}{
								map[string]interface{}{"name": "server", "image": "llm:latest"},
							},
						},
					},
				},
			},
		},
	}}

	live, err := fm.Apply(emptyLive(gvk), applied, "applier", false)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	liveU := live.(*unstructured.Unstructured)
	liveU.Object = normalizeJSON(liveU.Object).(map[string]interface{})

	roleInstance := &workloadsv1alpha2.RoleInstance{}
	data, err := json.Marshal(liveU.Object)
	if err != nil {
		t.Fatalf("marshal live object: %v", err)
	}
	if err := json.Unmarshal(data, roleInstance); err != nil {
		t.Fatalf("unmarshal into typed RoleInstance: %v", err)
	}

	extracted, err := applyv1alpha2.ExtractRoleInstance(roleInstance, "applier")
	if err != nil {
		t.Fatalf("ExtractRoleInstance: %v", err)
	}
	var extractedContainers []interface{}
	if extracted.Spec != nil && len(extracted.Spec.Components) == 1 {
		comp := extracted.Spec.Components[0]
		if comp.Template != nil && comp.Template.Spec != nil {
			extractedContainers = toInterfaces(comp.Template.Spec.Containers)
		}
	}
	if len(extractedContainers) != 1 {
		t.Errorf("control: extraction dropped the template even with matching schemas: %s", mustJSON(t, extracted.Spec))
	}
}

// TestExtractInstanceV1alpha1KeepsComponents is the v1alpha1 variant of
// the data-loss cycle, driven by the atomic-list divergence: the CRD
// for v1alpha1 Instance declares no x-kubernetes-list-type on
// spec.components, so the API server tracks the list as a whole; the
// embedded schema (from the +patchStrategy=merge/+patchMergeKey=name
// markers added by this PR) treats it as a merge-by-name list. The
// extract of a manager that applied the components drops them, and the
// re-apply removes them from the live object.
func TestExtractInstanceV1alpha1KeepsComponents(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "workloads.x-k8s.io", Version: "v1alpha1", Kind: "Instance"}
	fm := serverFieldManager(t, gvk)

	applied := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": gvk.GroupVersion().String(),
		"kind":       gvk.Kind,
		"metadata":   map[string]interface{}{"name": "demo", "namespace": "default"},
		"spec": map[string]interface{}{
			"components": []interface{}{
				map[string]interface{}{
					"name": "prefill",
					"size": 1,
					"template": map[string]interface{}{
						"spec": map[string]interface{}{
							"containers": []interface{}{
								map[string]interface{}{"name": "server", "image": "llm:latest"},
							},
						},
					},
				},
			},
		},
	}}

	live, err := fm.Apply(emptyLive(gvk), applied, "applier", false)
	if err != nil {
		t.Fatalf("server-side apply failed: %v", err)
	}
	liveU := live.(*unstructured.Unstructured)
	liveU.Object = normalizeJSON(liveU.Object).(map[string]interface{})

	instance := &workloadsv1alpha1.Instance{}
	data, err := json.Marshal(liveU.Object)
	if err != nil {
		t.Fatalf("marshal live object: %v", err)
	}
	if err := json.Unmarshal(data, instance); err != nil {
		t.Fatalf("unmarshal into typed Instance: %v", err)
	}

	extracted, err := applyv1alpha1.ExtractInstance(instance, "applier")
	if err != nil {
		t.Fatalf("ExtractInstance: %v", err)
	}
	if extracted.Spec == nil || len(extracted.Spec.Components) != 1 {
		t.Errorf("extract dropped v1alpha1 spec.components: extracted spec = %s", mustJSON(t, extracted.Spec))
	}
}
