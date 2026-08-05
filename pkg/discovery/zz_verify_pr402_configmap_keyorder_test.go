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

// zz_verify_pr402_configmap_keyorder_test.go is a REVIEW VERIFICATION HARNESS for
// PR #402 finding F4.
// Topic: pr402-port-alloc-doc   Code under review: b412ed6c
// See docs/verification/pr402-port-alloc-doc/README.md for the polarity table.
package discovery

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// vfxPR402RBG builds the RBG from the PR #402 guide's worked example:
// rbg "pd-inference" with roles prefill(2) and decode(2), each exposing http:8000.
func vfxPR402RBG(prefillReplicas, decodeReplicas int32) *workloadsv1alpha2.RoleBasedGroup {
	return &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "pd-inference", Namespace: "default"},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{
			Roles: []workloadsv1alpha2.RoleSpec{
				{
					Name:     "prefill",
					Replicas: &prefillReplicas,
					ServicePorts: []corev1.ServicePort{
						{Name: "http", Port: 8000},
					},
				},
				{
					Name:     "decode",
					Replicas: &decodeReplicas,
					ServicePorts: []corev1.ServicePort{
						{Name: "http", Port: 8000},
					},
				},
			},
		},
	}
}

// yamlKeyOrderAtIndent returns, in document order, the mapping keys that appear at
// exactly the given indentation depth inside the block that starts at startLine.
func yamlKeyOrderAtIndent(lines []string, startLine, indent int) []string {
	var keys []string
	for i := startLine; i < len(lines); i++ {
		l := lines[i]
		if strings.TrimSpace(l) == "" {
			continue
		}
		lead := len(l) - len(strings.TrimLeft(l, " "))
		if lead < indent {
			break // left the block
		}
		if lead != indent {
			continue
		}
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "- ") {
			continue
		}
		if idx := strings.Index(t, ":"); idx > 0 {
			keys = append(keys, t[:idx])
		}
	}
	return keys
}

// TestVerifyPR402_F4_ConfigMapYAMLKeysAreAlphabetical_Contract
//
// POLARITY: contract  (green on the code under review; it pins what the doc SHOULD show)
//
// CLAIM (F4, minor): PR #402's guide prints /etc/rbg/config.yaml with key order
// size -> roles (under group) and size -> instances (under each role)
// (zh:183-208 / en:184-209). ACTUALLY the ConfigMap payload is produced by
// sigs.k8s.io/yaml (config_builder.go:91 -> yaml.Marshal), which round-trips through
// JSON and therefore emits mapping keys in ALPHABETICAL order:
//
//	group: name -> roles -> size
//	roles.<role>: instances -> size
//	roles map itself: decode before prefill
//
// The same guide is self-contradictory: its scale-out verification step (zh:228-239)
// prints the CORRECT alphabetical order (instances before size).
//
// This is a DOCUMENTATION defect; the implementation is deterministic and correct.
// This test PASSES on the reviewed code and pins the exact key order the doc must
// copy, guarding against a future serializer swap that would silently change it.
func TestVerifyPR402_F4_ConfigMapYAMLKeysAreAlphabetical_Contract(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))

	cases := []struct {
		name                string
		prefill, decode     int32
		wantGroupKeys       []string
		wantRolesOrder      []string
		wantRoleBodyKeys    []string
		wantInstanceSubKeys []string
		// key order the PR #402 guide prints, for the observed-vs-expected record
		docGroupKeys    []string
		docRoleBodyKeys []string
	}{
		{
			name:                "guide step 1 shape: prefill=2 decode=2",
			prefill:             2,
			decode:              2,
			wantGroupKeys:       []string{"name", "roles", "size"},
			wantRolesOrder:      []string{"decode", "prefill"},
			wantRoleBodyKeys:    []string{"instances", "size"},
			wantInstanceSubKeys: []string{"address", "ports"},
			docGroupKeys:        []string{"name", "size", "roles"},
			docRoleBodyKeys:     []string{"size", "instances"},
		},
		{
			name:                "guide step 2 shape after scale-out: prefill=2 decode=3",
			prefill:             2,
			decode:              3,
			wantGroupKeys:       []string{"name", "roles", "size"},
			wantRolesOrder:      []string{"decode", "prefill"},
			wantRoleBodyKeys:    []string{"instances", "size"},
			wantInstanceSubKeys: []string{"address", "ports"},
			// zh:228-239 happens to print the CORRECT order here - self-contradiction.
			docGroupKeys:    []string{"name", "roles", "size"},
			docRoleBodyKeys: []string{"instances", "size"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rbg := vfxPR402RBG(tc.prefill, tc.decode)
			kclient := fake.NewClientBuilder().WithScheme(scheme).Build()
			b := NewConfigBuilder(kclient, rbg, &rbg.Spec.Roles[0])

			raw, err := b.Build()
			require.NoError(t, err)
			out := string(raw)
			t.Logf("config.yaml produced by config_builder.Build():\n%s", out)

			lines := strings.Split(out, "\n")

			// --- top level ---
			assert.Equal(t, []string{"group", "roles"}, yamlKeyOrderAtIndent(lines, 0, 0),
				"top-level keys must be alphabetical (group before roles)")

			// --- group block ---
			groupLine := -1
			for i, l := range lines {
				if l == "group:" {
					groupLine = i
					break
				}
			}
			require.GreaterOrEqual(t, groupLine, 0, "expected a top-level `group:` key")
			gotGroupKeys := yamlKeyOrderAtIndent(lines, groupLine+1, 2)
			t.Logf("group keys: impl=%v  doc-prints=%v", gotGroupKeys, tc.docGroupKeys)
			assert.Equal(t, tc.wantGroupKeys, gotGroupKeys,
				"sigs.k8s.io/yaml emits group keys alphabetically")

			// --- roles map ordering ---
			rolesLine := -1
			for i, l := range lines {
				if l == "roles:" {
					rolesLine = i
					break
				}
			}
			require.GreaterOrEqual(t, rolesLine, 0, "expected a top-level `roles:` key")
			gotRoles := yamlKeyOrderAtIndent(lines, rolesLine+1, 2)
			t.Logf("roles map order: impl=%v", gotRoles)
			assert.Equal(t, tc.wantRolesOrder, gotRoles,
				"map keys are sorted, so `decode` precedes `prefill` regardless of spec order")

			// --- per-role body ---
			for _, role := range tc.wantRolesOrder {
				roleLine := -1
				for i := rolesLine + 1; i < len(lines); i++ {
					if lines[i] == "  "+role+":" {
						roleLine = i
						break
					}
				}
				require.GreaterOrEqual(t, roleLine, 0, "expected role block %q", role)
				gotBody := yamlKeyOrderAtIndent(lines, roleLine+1, 4)
				t.Logf("role %q body keys: impl=%v  doc-prints(step1)=%v",
					role, gotBody, tc.docRoleBodyKeys)
				assert.Equal(t, tc.wantRoleBodyKeys, gotBody,
					"role body keys must be alphabetical: instances before size")
			}

			// --- instance entry sub-keys ---
			for i, l := range lines {
				if strings.HasPrefix(l, "    - address:") {
					sub := yamlKeyOrderAtIndent(lines, i+1, 6)
					assert.Equal(t, []string{"ports"}, sub,
						"instance entry: address (on the dash line) then ports")
					_ = tc.wantInstanceSubKeys
					break
				}
			}

			// --- textual proof: `instances:` precedes `size:` inside every role block ---
			for _, role := range tc.wantRolesOrder {
				blockStart := strings.Index(out, "  "+role+":\n")
				require.GreaterOrEqual(t, blockStart, 0)
				block := out[blockStart:]
				iInstances := strings.Index(block, "    instances:")
				iSize := strings.Index(block, "    size:")
				require.GreaterOrEqual(t, iInstances, 0, "role %q must have instances:", role)
				require.GreaterOrEqual(t, iSize, 0, "role %q must have size:", role)
				assert.Less(t, iInstances, iSize,
					"role %q: `instances:` must appear BEFORE `size:` (finding F4: the "+
						"guide's step-1 listing prints them the other way round)", role)
			}

			// The doc's step-1 order is NOT what the code emits - that is finding F4.
			if len(tc.docRoleBodyKeys) == 2 && tc.docRoleBodyKeys[0] == "size" {
				assert.NotEqual(t, tc.wantRoleBodyKeys, tc.docRoleBodyKeys,
					"F4: guide zh:183-208 prints size before instances")
			}
		})
	}
}

// TestVerifyPR402_F4_KeyOrderIsStableAcrossBuilds_Contract
//
// POLARITY: contract
//
// A doc can only legitimately print one key order if the serializer is deterministic.
// Ten builds of the same RBG must be byte-identical.
func TestVerifyPR402_F4_KeyOrderIsStableAcrossBuilds_Contract(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))

	var first string
	for i := 0; i < 10; i++ {
		rbg := vfxPR402RBG(2, 2)
		kclient := fake.NewClientBuilder().WithScheme(scheme).Build()
		raw, err := NewConfigBuilder(kclient, rbg, &rbg.Spec.Roles[0]).Build()
		require.NoError(t, err)
		if i == 0 {
			first = string(raw)
			continue
		}
		require.Equal(t, first, string(raw),
			"config.yaml serialization must be byte-stable across builds")
	}
	t.Logf("stable output:\n%s", first)
}
