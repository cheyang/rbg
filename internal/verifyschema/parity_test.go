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
	"sort"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"sigs.k8s.io/rbgs/client-go/applyconfiguration"
	versionedfake "sigs.k8s.io/rbgs/client-go/clientset/versioned/fake"
)

// serverParity (in helpers_test.go) builds the type converter
// kube-apiserver uses for the RBG CRDs (models rebuilt from
// config/crd/bases).

// embeddedConverter builds the type converter shipped in
// client-go/applyconfiguration (the Go-derived schema embedded by this
// PR), exactly as the generated code uses it.
func embeddedConverter(t *testing.T) (managedFieldsTypeConverter, *runtime.Scheme) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := versionedfake.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to build scheme: %v", err)
	}
	return applyconfiguration.NewTypeConverter(scheme), scheme
}

// TestServerParityFieldSets checks the core contract of the embedded
// apply schema: for every served kind, the field set the server would
// track for an object must equal the field set the embedded schema
// tracks. Server-side apply records managed fields with the schema
// kube-apiserver builds from the CRD; the generated Extract<Kind>
// helpers and the field-managed fake clientset interpret those fields
// with the embedded schema. A divergence means extraction silently
// drops (or invents) ownership, and an extract -> modify -> apply cycle
// then deletes live fields.
//
// Contract test: fails on the current (Go-derived) schema, passes once
// the embedded schema matches the CRD schema.
func TestServerParityFieldSets(t *testing.T) {
	server, kinds := serverParity(t)
	embedded, scheme := embeddedConverter(t)

	for _, km := range kinds {
		km := km
		t.Run(km.GVK.Kind+"/"+km.GVK.Version, func(t *testing.T) {
			if _, err := scheme.New(km.GVK); err != nil {
				t.Skipf("no clientset type for %v: %v", km.GVK, err)
			}
			if applyconfiguration.ForKind(km.GVK) == nil {
				// No apply configuration is generated for this kind (no
				// +genclient), so the embedded schema deliberately has
				// no type for it and no generated code exercises it.
				t.Skipf("no apply configuration for %v", km.GVK)
			}

			content, err := GenerateObject(km.Schema, nil)
			if err != nil {
				t.Fatalf("failed to generate object: %v", err)
			}
			obj := &unstructured.Unstructured{Object: content}
			obj.SetGroupVersionKind(km.GVK)

			serverSet, err := fieldSetOf(server, obj)
			if err != nil {
				t.Fatalf("server converter failed on generated object: %v", err)
			}
			embeddedSet, err := fieldSetOf(embedded, obj)
			if err != nil {
				// The object is valid per the CRD the API server serves; if
				// the embedded schema cannot type it, that is itself a
				// divergence.
				t.Errorf("embedded converter rejects an object that is valid per the CRD: %v", err)
				return
			}

			if diff := cmp.Diff(leaves(serverSet), leaves(embeddedSet)); diff != "" {
				t.Errorf("field sets diverge between kube-apiserver's CRD schema and the embedded Go-derived schema (-server +embedded):\n%s", diff)
			}
		})
	}
}

// TestServerParityRoleInstanceContainers is the focused version of the
// parity contract for the schemaless component template subtree:
// spec.components[*].template is x-kubernetes-preserve-unknown-fields in
// the CRD, so the server tracks spec.containers under it as a whole
// list; the embedded schema types it as core/v1.PodTemplateSpec with
// containers keyed by name.
func TestServerParityRoleInstanceContainers(t *testing.T) {
	server, kinds := serverParity(t)
	embedded, _ := embeddedConverter(t)

	gvk := schema.GroupVersionKind{Group: "workloads.x-k8s.io", Version: "v1alpha2", Kind: "RoleInstance"}
	var roleInstance *KindModel
	for i := range kinds {
		if kinds[i].GVK == gvk {
			roleInstance = &kinds[i]
		}
	}
	if roleInstance == nil {
		t.Fatal("RoleInstance v1alpha2 model not found")
	}

	content := map[string]interface{}{
		"spec": map[string]interface{}{
			"components": []interface{}{
				map[string]interface{}{
					"name": "prefill",
					"size": 1,
					"template": map[string]interface{}{
						"spec": map[string]interface{}{
							"containers": []interface{}{
								map[string]interface{}{"name": "c0", "image": "img0"},
							},
						},
					},
				},
			},
		},
	}
	obj := &unstructured.Unstructured{Object: content}
	obj.SetGroupVersionKind(gvk)

	serverSet, err := fieldSetOf(server, obj)
	if err != nil {
		t.Fatalf("server converter: %v", err)
	}
	embeddedSet, err := fieldSetOf(embedded, obj)
	if err != nil {
		t.Fatalf("embedded converter: %v", err)
	}

	serverLeaves := leaves(serverSet)
	embeddedLeaves := leaves(embeddedSet)

	// The server must track the container list as a whole (atomic list
	// under a schemaless subtree).
	wantServer := []string{`.spec.components[name="prefill"].template.spec.containers`}
	if diff := cmp.Diff(wantServer, filterBySuffix(serverLeaves, "containers")); diff != "" {
		t.Errorf("server field set for the container list (-want +got):\n%s", diff)
	}
	// The embedded schema must agree with the server on that ownership
	// granularity.
	if diff := cmp.Diff(serverLeaves, embeddedLeaves); diff != "" {
		t.Errorf("field sets diverge for spec.components[0].template (-server +embedded):\n%s", diff)
	}
}

// TestServerParityV1alpha1ComponentsAtomicity checks the list-type
// contract for v1alpha1 InstanceSpec.components: the CRD has no
// x-kubernetes-list-type, so the API server treats the list as atomic
// (replaced as a whole, one field-set leaf). The embedded schema must
// not turn it into a merge-by-name (associative) list.
func TestServerParityV1alpha1ComponentsAtomicity(t *testing.T) {
	server, _ := serverParity(t)
	embedded, _ := embeddedConverter(t)

	gvk := schema.GroupVersionKind{Group: "workloads.x-k8s.io", Version: "v1alpha1", Kind: "Instance"}
	content := map[string]interface{}{
		"spec": map[string]interface{}{
			"components": []interface{}{
				map[string]interface{}{"name": "c0", "size": 1},
			},
		},
	}
	obj := &unstructured.Unstructured{Object: content}
	obj.SetGroupVersionKind(gvk)

	serverSet, err := fieldSetOf(server, obj)
	if err != nil {
		t.Fatalf("server converter: %v", err)
	}
	embeddedSet, err := fieldSetOf(embedded, obj)
	if err != nil {
		t.Fatalf("embedded converter: %v", err)
	}

	serverLeaves := filterByContains(leaves(serverSet), "components")
	if diff := cmp.Diff([]string{".spec.components"}, serverLeaves); diff != "" {
		t.Errorf("server tracks v1alpha1 spec.components atomically (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(serverLeaves, filterByContains(leaves(embeddedSet), "components")); diff != "" {
		t.Errorf("embedded schema diverges from the server on v1alpha1 spec.components (-server +embedded):\n%s", diff)
	}
}

func filterBySuffix(paths []string, suffix string) []string {
	var out []string
	for _, p := range paths {
		if hasPathSuffix(p, suffix) {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

func filterByContains(paths []string, fragment string) []string {
	var out []string
	for _, p := range paths {
		if strings.Contains(p, fragment) {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

func hasPathSuffix(path, suffix string) bool {
	n := len(suffix)
	return len(path) >= n && path[len(path)-n:] == suffix
}
