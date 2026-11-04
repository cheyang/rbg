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

// Verification of PR #549 finding F1 (schema parity with kube-apiserver).
//
// The PR embeds an OpenAPI schema derived from the Go types (openapi-gen) in
// client-go/applyconfiguration/internal. kube-apiserver instead derives the
// field-management schema for a CRD from the CRD itself. The RBG CRDs keep
// several subtrees schemaless (x-kubernetes-preserve-unknown-fields), so the
// two schemas disagree there:
//
//   - fake.NewClientset() (which uses the embedded schema) then tracks field
//     ownership differently from a real cluster, and
//   - the generated Extract<Kind> helpers misinterpret server-recorded
//     managed fields and drop the contents of the schemaless subtrees.
//
// All tests below are contract tests: they assert the client-side schema
// behaves exactly like the API server's CRD-derived schema.
package parity

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/managedfields"
	"k8s.io/apimachinery/pkg/util/managedfields/managedfieldstest"
	"k8s.io/utils/ptr"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/client-go/applyconfiguration"
	applyv1alpha1 "sigs.k8s.io/rbgs/client-go/applyconfiguration/workloads/v1alpha1"
	applyv1alpha2 "sigs.k8s.io/rbgs/client-go/applyconfiguration/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/client-go/clientset/versioned/fake"
)

var (
	roleInstanceGVK   = schema.GroupVersionKind{Group: "workloads.x-k8s.io", Version: "v1alpha2", Kind: "RoleInstance"}
	instanceGVK       = schema.GroupVersionKind{Group: "workloads.x-k8s.io", Version: "v1alpha1", Kind: "Instance"}
	roleBasedGroupGVK = schema.GroupVersionKind{Group: "workloads.x-k8s.io", Version: "v1alpha2", Kind: "RoleBasedGroup"}
	profileGVK        = schema.GroupVersionKind{Group: "workloads.x-k8s.io", Version: "v1alpha2", Kind: "ClusterEngineRuntimeProfile"}
)

func clientTypeConverter(t *testing.T) managedfields.TypeConverter {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := fake.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	return applyconfiguration.NewTypeConverter(scheme)
}

// fieldSets returns the managed-field field sets for the same object under
// both converters.
func fieldSets(t *testing.T, serverTC managedfields.TypeConverter, obj *unstructured.Unstructured) (server string, client string) {
	t.Helper()
	sv, err := serverTC.ObjectToTyped(obj)
	if err != nil {
		t.Fatalf("server ObjectToTyped(%s): %v", obj.GroupVersionKind(), err)
	}
	sfs, err := sv.ToFieldSet()
	if err != nil {
		t.Fatalf("server ToFieldSet: %v", err)
	}
	cv, err := clientTypeConverter(t).ObjectToTyped(obj)
	if err != nil {
		t.Fatalf("client ObjectToTyped(%s): %v", obj.GroupVersionKind(), err)
	}
	cfs, err := cv.ToFieldSet()
	if err != nil {
		t.Fatalf("client ToFieldSet: %v", err)
	}
	return sfs.String(), cfs.String()
}

func roleInstanceObject() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "workloads.x-k8s.io/v1alpha2",
		"kind":       "RoleInstance",
		"metadata":   map[string]interface{}{"name": "parity", "namespace": "default"},
		"spec": map[string]interface{}{
			"components": []interface{}{
				map[string]interface{}{
					"name": "prefill", "size": int64(2), "serviceName": "prefill-svc",
					"template": map[string]interface{}{
						"metadata": map[string]interface{}{"labels": map[string]interface{}{"app": "prefill"}},
						"spec": map[string]interface{}{
							"containers": []interface{}{
								map[string]interface{}{"name": "app", "image": "img:1"},
							},
						},
					},
				},
				map[string]interface{}{
					"name": "decode", "size": int64(1), "serviceName": "decode-svc",
					"template": map[string]interface{}{
						"spec": map[string]interface{}{
							"containers": []interface{}{
								map[string]interface{}{"name": "worker", "image": "img:2"},
							},
						},
					},
				},
			},
		},
	}}
}

func instanceObject(component string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "workloads.x-k8s.io/v1alpha1",
		"kind":       "Instance",
		"metadata":   map[string]interface{}{"name": "parity", "namespace": "default"},
		"spec": map[string]interface{}{
			"components": []interface{}{
				map[string]interface{}{
					"name": component, "size": int64(1), "serviceName": "svc",
					"template": map[string]interface{}{
						"spec": map[string]interface{}{
							"containers": []interface{}{
								map[string]interface{}{"name": "app", "image": "img:1"},
							},
						},
					},
				},
			},
		},
	}}
}

// TestFieldSetParityRoleInstance compares the field sets computed for the same
// RoleInstance by the API server's CRD-derived converter and by the embedded
// generated converter. They must be identical.
func TestFieldSetParityRoleInstance(t *testing.T) {
	serverTC := serverTypeConverter(t, "workloads.x-k8s.io_roleinstances.yaml", roleInstanceGVK)
	server, client := fieldSets(t, serverTC, roleInstanceObject())
	if server != client {
		t.Errorf("field sets diverge between API server (CRD schema) and generated client schema\n--- server ---\n%s\n--- client ---\n%s", server, client)
	}
}

// TestFieldSetParityInstance covers v1alpha1 Instance, whose components list
// is atomic on the API server (no x-kubernetes-list-type in the CRD).
func TestFieldSetParityInstance(t *testing.T) {
	serverTC := serverTypeConverter(t, "workloads.x-k8s.io_instances.yaml", instanceGVK)
	server, client := fieldSets(t, serverTC, instanceObject("prefill"))
	if server != client {
		t.Errorf("field sets diverge between API server (CRD schema) and generated client schema\n--- server ---\n%s\n--- client ---\n%s", server, client)
	}
}

// TestFieldSetParityRoleBasedGroup covers the schemaless templateRef.patch
// subtree of RoleBasedGroup.
func TestFieldSetParityRoleBasedGroup(t *testing.T) {
	serverTC := serverTypeConverter(t, "workloads.x-k8s.io_rolebasedgroups.yaml", roleBasedGroupGVK)
	obj := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "workloads.x-k8s.io/v1alpha2",
		"kind":       "RoleBasedGroup",
		"metadata":   map[string]interface{}{"name": "parity", "namespace": "default"},
		"spec": map[string]interface{}{
			"roles": []interface{}{
				map[string]interface{}{
					"name": "prefill", "replicas": int64(1),
					"standalonePattern": map[string]interface{}{
						"templateRef": map[string]interface{}{
							"name": "base-template",
							"patch": map[string]interface{}{
								"spec": map[string]interface{}{
									"containers": []interface{}{
										map[string]interface{}{"name": "patched", "image": "img:2"},
									},
								},
							},
						},
					},
				},
			},
		},
	}}
	server, client := fieldSets(t, serverTC, obj)
	if server != client {
		t.Errorf("field sets diverge between API server (CRD schema) and generated client schema\n--- server ---\n%s\n--- client ---\n%s", server, client)
	}
}

// TestFieldSetParityControlKind is the control: ClusterEngineRuntimeProfile
// has no schemaless subtrees, so the field sets must agree. If this control
// fails, the harness itself is broken rather than the PR.
func TestFieldSetParityControlKind(t *testing.T) {
	serverTC := serverTypeConverter(t, "workloads.x-k8s.io_clusterengineruntimeprofiles.yaml", profileGVK)
	obj := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "workloads.x-k8s.io/v1alpha2",
		"kind":       "ClusterEngineRuntimeProfile",
		"metadata":   map[string]interface{}{"name": "parity"},
		"spec": map[string]interface{}{
			"updateStrategy": "RollingUpdate",
			"containers": []interface{}{
				map[string]interface{}{"name": "engine", "image": "img:1"},
			},
		},
	}}
	server, client := fieldSets(t, serverTC, obj)
	if server != client {
		t.Errorf("control kind diverged; harness is broken\n--- server ---\n%s\n--- client ---\n%s", server, client)
	}
}

// TestFieldSetParitySchemalessSubtrees covers the remaining kinds whose CRDs
// keep a subtree schemaless (x-kubernetes-preserve-unknown-fields): the whole
// InstanceSet/RoleInstanceSet instance templates and the RoleBasedGroupSet
// group template. The client-side and server-side field sets must agree.
func TestFieldSetParitySchemalessSubtrees(t *testing.T) {
	containerTemplate := map[string]interface{}{
		"spec": map[string]interface{}{
			"containers": []interface{}{map[string]interface{}{"name": "app", "image": "img:1"}},
		},
	}
	cases := []struct {
		name    string
		crdFile string
		gvk     schema.GroupVersionKind
		spec    map[string]interface{}
	}{
		{
			name:    "InstanceSet v1alpha1",
			crdFile: "workloads.x-k8s.io_instancesets.yaml",
			gvk:     schema.GroupVersionKind{Group: "workloads.x-k8s.io", Version: "v1alpha1", Kind: "InstanceSet"},
			spec: map[string]interface{}{
				"instanceTemplate": map[string]interface{}{
					"spec": map[string]interface{}{
						"components": []interface{}{
							map[string]interface{}{"name": "prefill", "size": int64(1), "template": containerTemplate},
						},
					},
				},
			},
		},
		{
			name:    "RoleInstanceSet v1alpha2",
			crdFile: "workloads.x-k8s.io_roleinstancesets.yaml",
			gvk:     schema.GroupVersionKind{Group: "workloads.x-k8s.io", Version: "v1alpha2", Kind: "RoleInstanceSet"},
			spec: map[string]interface{}{
				"roleInstanceTemplate": map[string]interface{}{
					"spec": map[string]interface{}{
						"components": []interface{}{
							map[string]interface{}{"name": "prefill", "size": int64(1), "template": containerTemplate},
						},
					},
				},
			},
		},
		{
			name:    "RoleBasedGroupSet v1alpha2",
			crdFile: "workloads.x-k8s.io_rolebasedgroupsets.yaml",
			gvk:     schema.GroupVersionKind{Group: "workloads.x-k8s.io", Version: "v1alpha2", Kind: "RoleBasedGroupSet"},
			spec: map[string]interface{}{
				"groupTemplate": map[string]interface{}{
					"spec": map[string]interface{}{
						"roles": []interface{}{
							map[string]interface{}{
								"name": "prefill", "replicas": int64(1),
								"standalonePattern": map[string]interface{}{
									"templateRef": map[string]interface{}{
										"name":  "base-template",
										"patch": map[string]interface{}{"spec": map[string]interface{}{"containers": []interface{}{map[string]interface{}{"name": "p", "image": "i"}}}},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name:    "RoleBasedGroup v1alpha1",
			crdFile: "workloads.x-k8s.io_rolebasedgroups.yaml",
			gvk:     schema.GroupVersionKind{Group: "workloads.x-k8s.io", Version: "v1alpha1", Kind: "RoleBasedGroup"},
			spec: map[string]interface{}{
				"roles": []interface{}{
					map[string]interface{}{
						"name": "prefill", "replicas": int64(1),
						"templatePatch": map[string]interface{}{"spec": map[string]interface{}{"containers": []interface{}{map[string]interface{}{"name": "p", "image": "i"}}}},
					},
				},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			serverTC := serverTypeConverter(t, tc.crdFile, tc.gvk)
			obj := &unstructured.Unstructured{Object: map[string]interface{}{
				"apiVersion": tc.gvk.GroupVersion().String(),
				"kind":       tc.gvk.Kind,
				"metadata":   map[string]interface{}{"name": "parity", "namespace": "default"},
				"spec":       tc.spec,
			}}
			server, client := fieldSets(t, serverTC, obj)
			if server != client {
				t.Errorf("field sets diverge between API server (CRD schema) and generated client schema\n--- server ---\n%s\n--- client ---\n%s", server, client)
			}
		})
	}
}

// TestExtractRoleInstanceKeepsServerManagedTemplate reproduces the
// extract-on-real-cluster flow: the API server (CRD schema) applies an object
// and records managed fields; the generated ExtractRoleInstance then reads
// them with the embedded schema. The pod template content must survive.
func TestExtractRoleInstanceKeepsServerManagedTemplate(t *testing.T) {
	serverTC := serverTypeConverter(t, "workloads.x-k8s.io_roleinstances.yaml", roleInstanceGVK)
	fm := managedfieldstest.NewTestFieldManager(serverTC, roleInstanceGVK)
	if err := fm.Apply(roleInstanceObject(), "applier", false); err != nil {
		t.Fatalf("server-side apply failed: %v", err)
	}
	live, ok := fm.Live().(*unstructured.Unstructured)
	if !ok {
		t.Fatalf("unexpected live object type %T", fm.Live())
	}
	live = live.DeepCopy()
	live.SetManagedFields(fm.ManagedFields())

	// A client gets the typed object with the server-recorded managed fields.
	data, err := json.Marshal(live)
	if err != nil {
		t.Fatalf("marshal live object: %v", err)
	}
	typedObj := &workloadsv1alpha2.RoleInstance{}
	if err := json.Unmarshal(data, typedObj); err != nil {
		t.Fatalf("unmarshal live object: %v", err)
	}

	extracted, err := applyv1alpha2.ExtractRoleInstance(typedObj, "applier")
	if err != nil {
		t.Fatalf("ExtractRoleInstance: %v", err)
	}
	if extracted.Spec == nil || len(extracted.Spec.Components) != 2 {
		t.Fatalf("extracted config lost components: %+v", extracted.Spec)
	}
	for _, c := range extracted.Spec.Components {
		if c.Template == nil || c.Template.Spec == nil || len(c.Template.Spec.Containers) != 1 {
			t.Errorf("extracted component %q lost its pod template containers: template=%+v", *c.Name, c.Template)
		}
	}
}

// TestExtractModifyApplyDropsServerManagedTemplate demonstrates the
// user-facing consequence of F1 end to end: an extract -> modify -> apply
// round trip against server-recorded managed fields deletes the pod template
// containers from the live object, because the extracted configuration no
// longer contains them and SSA prunes owned fields that are omitted from a
// new apply.
func TestExtractModifyApplyDropsServerManagedTemplate(t *testing.T) {
	serverTC := serverTypeConverter(t, "workloads.x-k8s.io_roleinstances.yaml", roleInstanceGVK)
	fm := managedfieldstest.NewTestFieldManager(serverTC, roleInstanceGVK)
	if err := fm.Apply(roleInstanceObject(), "applier", false); err != nil {
		t.Fatalf("server-side apply failed: %v", err)
	}
	live := fm.Live().(*unstructured.Unstructured).DeepCopy()
	live.SetManagedFields(fm.ManagedFields())

	data, err := json.Marshal(live)
	if err != nil {
		t.Fatalf("marshal live object: %v", err)
	}
	typedObj := &workloadsv1alpha2.RoleInstance{}
	if err := json.Unmarshal(data, typedObj); err != nil {
		t.Fatalf("unmarshal live object: %v", err)
	}

	extracted, err := applyv1alpha2.ExtractRoleInstance(typedObj, "applier")
	if err != nil {
		t.Fatalf("ExtractRoleInstance: %v", err)
	}
	// modify: bump a scalar the manager owns, as a real extract/modify/apply
	// client would.
	extracted.Spec.Components[0].Size = ptr.To[int32](5)

	reapplied := toUnstructured(t, extracted)
	if err := fm.Apply(reapplied, "applier", false); err != nil {
		t.Fatalf("re-apply of extracted config failed: %v", err)
	}
	final := fm.Live().(*unstructured.Unstructured)
	components, _, _ := unstructured.NestedSlice(final.Object, "spec", "components")
	if len(components) != 2 {
		t.Fatalf("components lost after extract/apply round trip: %v", components)
	}
	for _, c := range components {
		comp := c.(map[string]interface{})
		containers, found, _ := unstructured.NestedSlice(comp, "template", "spec", "containers")
		if !found || len(containers) != 1 {
			t.Errorf("DATA LOSS: component %q template containers after extract->modify->apply: found=%v value=%v",
				comp["name"], found, containers)
		}
	}
}

// TestExtractWithServerSchemaKeepsTemplate is the harness-bites control for
// TestExtractRoleInstanceKeepsServerManagedTemplate: extracting the very same
// server-recorded managed fields with a parser built from the CRD-derived
// schema (the proposed fix direction) must preserve the pod template
// containers. If this control fails, the harness or the extraction flow is
// broken rather than the embedded schema.
func TestExtractWithServerSchemaKeepsTemplate(t *testing.T) {
	serverTC := serverTypeConverter(t, "workloads.x-k8s.io_roleinstances.yaml", roleInstanceGVK)
	fm := managedfieldstest.NewTestFieldManager(serverTC, roleInstanceGVK)
	if err := fm.Apply(roleInstanceObject(), "applier", false); err != nil {
		t.Fatalf("server-side apply failed: %v", err)
	}
	live, ok := fm.Live().(*unstructured.Unstructured)
	if !ok {
		t.Fatalf("unexpected live object type %T", fm.Live())
	}
	live = live.DeepCopy()
	live.SetManagedFields(fm.ManagedFields())

	data, err := json.Marshal(live)
	if err != nil {
		t.Fatalf("marshal live object: %v", err)
	}
	typedObj := &workloadsv1alpha2.RoleInstance{}
	if err := json.Unmarshal(data, typedObj); err != nil {
		t.Fatalf("unmarshal live object: %v", err)
	}

	serverPT := serverParseableType(t, "workloads.x-k8s.io_roleinstances.yaml", "io.x-k8s.workloads.v1alpha2.RoleInstance")
	extracted := &applyv1alpha2.RoleInstanceApplyConfiguration{}
	if err := managedfields.ExtractInto(typedObj, serverPT, "applier", extracted, ""); err != nil {
		t.Fatalf("ExtractInto with CRD-derived schema: %v", err)
	}
	if extracted.Spec == nil || len(extracted.Spec.Components) != 2 {
		t.Fatalf("extracted config lost components: %+v", extracted.Spec)
	}
	for _, c := range extracted.Spec.Components {
		if c.Template == nil || c.Template.Spec == nil || len(c.Template.Spec.Containers) != 1 {
			t.Errorf("control failed: CRD-derived schema also lost component %q template containers: template=%+v", *c.Name, c.Template)
		}
	}
}

// TestFakeMatchesServerInstanceComponents checks that the fake clientset
// reproduces the API server's atomic-list behavior for v1alpha1
// InstanceSpec.Components: a second manager applying a different component
// list must conflict, not silently merge.
func TestFakeMatchesServerInstanceComponents(t *testing.T) {
	serverTC := serverTypeConverter(t, "workloads.x-k8s.io_instances.yaml", instanceGVK)
	serverFM := managedfieldstest.NewTestFieldManager(serverTC, instanceGVK)
	if err := serverFM.Apply(instanceObject("prefill"), "manager-a", false); err != nil {
		t.Fatalf("server apply manager-a: %v", err)
	}
	serverErr := serverFM.Apply(instanceObject("decode"), "manager-b", false)
	if serverErr == nil {
		t.Fatalf("harness assumption broken: API server accepted a second manager's components without conflict")
	}
	t.Logf("server (CRD schema) rejected the second apply as expected: %v", serverErr)

	ctx := context.Background()
	client := fake.NewClientset().WorkloadsV1alpha1().Instances("default")
	apply := func(component, manager string) error {
		_, err := client.Apply(ctx, applyv1alpha1.Instance("parity", "default").
			WithSpec(applyv1alpha1.InstanceSpec().
				WithComponents(applyv1alpha1.InstanceComponent().WithName(component))),
			metav1.ApplyOptions{FieldManager: manager})
		return err
	}
	if err := apply("prefill", "manager-a"); err != nil {
		t.Fatalf("fake apply manager-a: %v", err)
	}
	fakeErr := apply("decode", "manager-b")
	if fakeErr == nil {
		got, _ := client.Get(ctx, "parity", metav1.GetOptions{})
		names := []string{}
		for _, c := range got.Spec.Components {
			names = append(names, c.Name)
		}
		t.Errorf("fake diverges from API server: server rejected the second apply with a conflict, fake merged silently (components now %v)", names)
	} else if !strings.Contains(fakeErr.Error(), "conflict") {
		t.Errorf("fake rejected the second apply, but not with a conflict: %v", fakeErr)
	}
}

// TestFakeMatchesServerRoleInstanceComponents is the control: v1alpha2
// RoleInstanceSpec.Components is a list-map on the server too, so both the
// server and the fake must merge two managers' components by name.
func TestFakeMatchesServerRoleInstanceComponents(t *testing.T) {
	serverTC := serverTypeConverter(t, "workloads.x-k8s.io_roleinstances.yaml", roleInstanceGVK)
	serverFM := managedfieldstest.NewTestFieldManager(serverTC, roleInstanceGVK)
	singleComponent := func(component string) *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "workloads.x-k8s.io/v1alpha2",
			"kind":       "RoleInstance",
			"metadata":   map[string]interface{}{"name": "parity", "namespace": "default"},
			"spec": map[string]interface{}{
				"components": []interface{}{
					map[string]interface{}{
						"name": component, "size": int64(1), "serviceName": component + "-svc",
						"template": map[string]interface{}{
							"spec": map[string]interface{}{
								"containers": []interface{}{map[string]interface{}{"name": "app", "image": "img:1"}},
							},
						},
					},
				},
			},
		}}
	}
	if err := serverFM.Apply(singleComponent("prefill"), "manager-a", false); err != nil {
		t.Fatalf("server apply manager-a: %v", err)
	}
	if err := serverFM.Apply(singleComponent("decode"), "manager-b", false); err != nil {
		t.Fatalf("server unexpectedly rejected the second list-map apply: %v", err)
	}

	ctx := context.Background()
	client := fake.NewClientset().WorkloadsV1alpha2().RoleInstances("default")
	if _, err := client.Apply(ctx, applyv1alpha2.RoleInstance("parity", "default").
		WithSpec(applyv1alpha2.RoleInstanceSpec().
			WithComponents(applyv1alpha2.RoleInstanceComponent().WithName("prefill"))),
		metav1.ApplyOptions{FieldManager: "manager-a"}); err != nil {
		t.Fatalf("fake apply manager-a: %v", err)
	}
	if _, err := client.Apply(ctx, applyv1alpha2.RoleInstance("parity", "default").
		WithSpec(applyv1alpha2.RoleInstanceSpec().
			WithComponents(applyv1alpha2.RoleInstanceComponent().WithName("decode"))),
		metav1.ApplyOptions{FieldManager: "manager-b"}); err != nil {
		t.Errorf("fake diverges from API server on list-map components: %v", err)
	}
}
