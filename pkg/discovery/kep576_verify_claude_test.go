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

package discovery

// KEP-576 (PR #578) review harness — Reviewer A (Claude).
//
// PR #578 is documentation-only (adds keps/576-compact-discovery-configmap/);
// base and head production code are identical. These tests therefore pin the
// *codebase facts* the KEP's design and compatibility gate are written against.
// Polarity (see docs/verification/kep576-compact-discovery-claude/README.md):
//   - P0 / F-claim canaries: assert what the shipped code DOES today; a green
//     run establishes the fact. They contradict the KEP wherever noted.
//   - The component-indexing test is a contract test FOR the KEP's design rule
//     (`component_index = offset + j`): green means the KEP's core mechanism
//     is faithful to the shipped builder.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/yaml"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func kep576Scheme() *runtime.Scheme {
	schema := runtime.NewScheme()
	_ = corev1.AddToScheme(schema)
	_ = workloadsv1alpha2.AddToScheme(schema)
	return schema
}

// P0 (premise): the shipped `refine` enumeration really is O(pods) and, with
// the KEP's own example shape (CCP leader size 1 + worker size 4, ports
// http+grpc, names pd-inference/prefill), a 4000-replica role crosses the
// 1 MiB ConfigMap limit. Also records the measured per-entry size so the
// Summary's "roughly 43 bytes per pod" claim can be checked.
func TestKEP576Claude_P0_EnumerationSizeGrowsWithPodCount(t *testing.T) {
	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "pd-inference", Namespace: "default"},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{
			Roles: []workloadsv1alpha2.RoleSpec{{
				Name:     "prefill",
				Replicas: ptr.To(int32(3)), // small sample; extrapolate below
				ServicePorts: []corev1.ServicePort{
					{Name: "http", Port: 8080},
					{Name: "grpc", Port: 50000},
				},
				Pattern: workloadsv1alpha2.Pattern{
					CustomComponentsPattern: &workloadsv1alpha2.CustomComponentsPattern{
						Components: []workloadsv1alpha2.InstanceComponent{
							{Name: "leader", Size: ptr.To(int32(1))},
							{Name: "worker", Size: ptr.To(int32(4))},
						},
					},
				},
			}},
		},
	}

	builder := NewConfigBuilder(
		fake.NewClientBuilder().WithScheme(kep576Scheme()).Build(), rbg, nil)
	data, err := builder.Build()
	require.NoError(t, err)

	entryCount := 3 * 5 // replicas x (1 leader + 4 workers)
	require.Greater(t, len(data), 0)
	perEntry := len(data) / entryCount
	t.Logf("shipped enumeration: %d bytes for %d entries => ~%d bytes/entry",
		len(data), entryCount, perEntry)

	// The premise holds at the KEP's example shape: 4000 replicas x 5 pods
	// crosses the 1 MiB etcd object limit.
	total := perEntry * 4000 * 5
	require.Greater(t, total, 1024*1024,
		"premise: 4000-replica role should exceed 1 MiB; measured %d bytes/entry => %d bytes total", perEntry, total)

	// ...but the Summary's "roughly 43 bytes per pod" does NOT cross the limit
	// (43 x 20000 = 860 KB < 1 MiB): the Summary's own arithmetic contradicts
	// its claim. Pin that so the doc gets one consistent number.
	require.Greater(t, perEntry, 43,
		"KEP Summary claims ~43 bytes/pod crosses 1 MiB at 20k pods; it does not (860 KB)")
}

// F1 canary (contract for the *shipped* format): the serialized `roles` map is
// emitted with alphabetically sorted keys, NOT spec declaration order, while
// `group.roles` IS spec declaration order. KEP-576 Shipped Baseline item 6
// ("roles in spec order") and the Compact Format table ("rendering preserves
// the shipped enumeration order") are wrong for the `roles` map: a renderer
// that emits roles in spec order produces a different file than today for any
// RBG whose role names are not already alphabetical.
func TestKEP576Claude_ShippedRolesMapOrderIsSortedNotSpecOrder(t *testing.T) {
	r5 := int32(5)
	r7 := int32(7)
	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "g", Namespace: "default"},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{
			Roles: []workloadsv1alpha2.RoleSpec{
				{Name: "prefill", Replicas: &r5}, // spec order: prefill first
				{Name: "decode", Replicas: &r7},  // decode second
			},
		},
	}

	builder := NewConfigBuilder(
		fake.NewClientBuilder().WithScheme(kep576Scheme()).Build(), rbg, nil)
	data, err := builder.Build()
	require.NoError(t, err)

	// Order of keys under `roles:` as serialized (2-space indent).
	var roleMapOrder []string
	inRoles := false
	for _, line := range strings.Split(string(data), "\n") {
		if line == "roles:" {
			inRoles = true
			continue
		}
		if inRoles {
			// exactly 2-space indent = role map keys; deeper indent = role fields
			if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") && strings.HasSuffix(line, ":") {
				roleMapOrder = append(roleMapOrder, strings.TrimSpace(strings.TrimSuffix(line, ":")))
			} else if line != "" && !strings.HasPrefix(line, " ") {
				break
			}
		}
	}
	require.Equal(t, []string{"decode", "prefill"}, roleMapOrder,
		"shipped `roles` map is key-sorted, not spec order (spec order is [prefill decode])")

	// group.roles is a slice and IS spec order.
	var cfg struct {
		Group struct {
			Roles []string `json:"roles"`
		} `json:"group"`
	}
	require.NoError(t, yaml.Unmarshal(data, &cfg))
	require.Equal(t, []string{"prefill", "decode"}, cfg.Group.Roles,
		"group.roles is spec order — the shipped file mixes two role orderings")
}

// Design-validation contract: the KEP's single indexing rule
// `component_index = component.offset + j` with the documented per-pattern
// offsets reproduces the shipped builder's address list exactly (values and
// order) for CustomComponentsPattern (incl. ServiceName override) and
// LeaderWorkerPattern on RoleInstanceSet. Green = KEP claim CONFIRMED.
func TestKEP576Claude_ComponentIndexRuleReproducesShippedAddresses(t *testing.T) {
	type component struct {
		name      string
		size      int32
		offset    int32 // KEP: offset of the component's first component_index
		inAddress bool  // KEP: CCP bases contain the component name; LWP bases do not
	}

	cases := []struct {
		name       string
		role       workloadsv1alpha2.RoleSpec
		components []component
	}{
		{
			name: "CustomComponentsPattern with ServiceName override",
			role: workloadsv1alpha2.RoleSpec{
				Name:     "r",
				Replicas: ptr.To(int32(3)),
				Pattern: workloadsv1alpha2.Pattern{
					CustomComponentsPattern: &workloadsv1alpha2.CustomComponentsPattern{
						Components: []workloadsv1alpha2.InstanceComponent{
							{Name: "a", Size: ptr.To(int32(2))},
							{Name: "b", Size: ptr.To(int32(3)), ServiceName: "custom-svc"},
						},
					},
				},
			},
			// KEP pattern mapping: `<rbg>-<role>-{role_index}-<component>-{component_index}.<svc>`, offset 0
			components: []component{
				{name: "a", size: 2, offset: 0, inAddress: true},
				{name: "b", size: 3, offset: 0, inAddress: true},
			},
		},
		{
			name: "LeaderWorkerPattern on RoleInstanceSet",
			role: workloadsv1alpha2.RoleSpec{
				Name:     "r",
				Replicas: ptr.To(int32(3)),
				Pattern: workloadsv1alpha2.Pattern{
					LeaderWorkerPattern: &workloadsv1alpha2.LeaderWorkerPattern{
						Size: ptr.To(int32(4)),
					},
				},
			},
			// KEP pattern mapping: `<rbg>-<role>-{role_index}-{component_index}.<svc>`;
			// leader offset 0 size 1, worker offset 1 size size-1
			components: []component{
				{name: "leader", size: 1, offset: 0, inAddress: false},
				{name: "worker", size: 3, offset: 1, inAddress: false},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rbg := &workloadsv1alpha2.RoleBasedGroup{
				ObjectMeta: metav1.ObjectMeta{Name: "g", Namespace: "default"},
				Spec:       workloadsv1alpha2.RoleBasedGroupSpec{Roles: []workloadsv1alpha2.RoleSpec{tc.role}},
			}
			builder := NewConfigBuilder(
				fake.NewClientBuilder().WithScheme(kep576Scheme()).Build(), rbg, nil)
			shipped, err := builder.buildInstances(&tc.role)
			require.NoError(t, err)

			// Re-derive per the KEP: members = dense [0, replicas) (no child,
			// no exclusion input), addresses in component spec order.
			workload := rbg.GetWorkloadName(&tc.role)
			var want []Instance
			for i := int32(0); i < *tc.role.Replicas; i++ {
				for _, comp := range tc.components {
					svc := "s-g-r"
					if tc.name == "CustomComponentsPattern with ServiceName override" && comp.name == "b" {
						svc = "custom-svc" // component ServiceName override, controller-precomputed
					}
					for j := int32(0); j < comp.size; j++ {
						idx := comp.offset + j // KEP: component_index = offset + j
						var addr string
						if comp.inAddress {
							addr = fmt.Sprintf("%s-%d-%s-%d.%s", workload, i, comp.name, idx, svc)
						} else {
							addr = fmt.Sprintf("%s-%d-%d.%s", workload, i, idx, svc)
						}
						want = append(want, Instance{Address: addr})
					}
				}
			}
			require.Equal(t, want, shipped)
		})
	}
}

// Baseline claim 5 canary (CONFIRMED): committed membership publishes members
// with ordinal >= replicas (KEP-499 example: replicas 3, committed skips
// [1,3] => members [0,2,4]).
func TestKEP576Claude_CommittedMembershipIncludesOrdinalsAboveReplicas(t *testing.T) {
	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: "g", Namespace: "default", UID: "rbg-uid",
		},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{
			Roles: []workloadsv1alpha2.RoleSpec{{Name: "r", Replicas: ptr.To(int32(3))}},
		},
	}
	role := &rbg.Spec.Roles[0]

	set := &workloadsv1alpha2.RoleInstanceSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rbg.GetWorkloadName(role),
			Namespace: rbg.Namespace,
			Annotations: map[string]string{
				constants.CommittedSkipOrdinalsAnnotationKey: "1,3",
			},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: rbg.APIVersion,
				Kind:       rbg.Kind,
				Name:       rbg.Name,
				UID:        rbg.GetUID(),
				Controller: ptr.To(true),
			}},
		},
		Spec: workloadsv1alpha2.RoleInstanceSetSpec{Replicas: ptr.To(int32(3))},
	}

	builder := NewConfigBuilder(
		fake.NewClientBuilder().WithScheme(kep576Scheme()).WithRuntimeObjects(set).Build(),
		rbg, nil)
	members, err := builder.memberOrdinals(role)
	require.NoError(t, err)
	require.Equal(t, []int{0, 2, 4}, members)
}

// F3 canary (contract for the shipped behavior): before the child exists, the
// shipped builder PROJECTS the user exclusion annotation (Select over the
// annotation), it does not fall back to dense membership. KEP-576's
// Committed Membership section says the compact format "does not project the
// user-facing excluded-ordinals annotation" and lists dense
// `[0, replicas)` as the pre-child source of truth — in this window that
// diverges from the shipped output the KEP promises to reproduce.
func TestKEP576Claude_PreChildExclusionWindowProjectsAnnotation(t *testing.T) {
	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "g", Namespace: "default"},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{
			Roles: []workloadsv1alpha2.RoleSpec{{
				Name:     "r",
				Replicas: ptr.To(int32(3)),
				Annotations: map[string]string{
					constants.ExcludedOrdinalsAnnotationKey: "1",
				},
			}},
		},
	}
	role := &rbg.Spec.Roles[0]
	require.Nil(t, workloadsv1alpha2.ValidateRoleExcludedOrdinals(rbg, role),
		"fixture must be valid input, else the freeze path fires")

	// No RoleInstanceSet exists yet (pre-child window).
	builder := NewConfigBuilder(
		fake.NewClientBuilder().WithScheme(kep576Scheme()).Build(), rbg, nil)
	members, err := builder.memberOrdinals(role)
	require.NoError(t, err)

	require.Equal(t, []int{0, 2, 3}, members,
		"shipped pre-child behavior projects the exclusion annotation (not dense [0 1 2])")
	require.NotContains(t, members, 1)
}

// Unused import guards / helpers referenced by future layers.
var _ = types.NamespacedName{}
