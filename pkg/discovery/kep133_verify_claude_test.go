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

// KEP-133 review harness (Reviewer A, Claude) — PR #570.
//
// These tests are codebase-fact tests: each one pins the *current base
// behavior* that the rewritten KEP-133 (keps/133-configmap-refine/README.md)
// makes claims about. They are not bug repros; the PR under review changes
// documentation only, so code behavior is identical on base and head. A green
// result here means "the codebase fact is established"; whenever that fact
// contradicts a KEP statement, the KEP claim is thereby refuted.
//
// Mapping to findings (see docs/verification/kep133-refine-claude/):
//   F2  TestKEP133Claude_AddressShapeIsShortForm
//   F3  TestKEP133Claude_DiscoveryFollowsCommittedTargetNotAnnotation
//   F4  TestKEP133Claude_CCPComponentOrderIsSpecOrder
//   F6  TestKEP133Claude_GroupSizeEqualsRoleCount
//   P0  TestKEP133Claude_EnumerationGrowsWithReplicas (premise: size claim)

import (
	"fmt"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"

	"sigs.k8s.io/yaml"
)

func kepScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := workloadsv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatalf("add workloads scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	return scheme
}

func kepRBG(name, ns string, roles ...workloadsv1alpha2.RoleSpec) *workloadsv1alpha2.RoleBasedGroup {
	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, UID: "rbg-test-uid"},
		Spec:       workloadsv1alpha2.RoleBasedGroupSpec{Roles: roles},
	}
	return rbg
}

func buildConfig(t *testing.T, rbg *workloadsv1alpha2.RoleBasedGroup, objs ...runtime.Object) ClusterConfig {
	t.Helper()
	client := fake.NewClientBuilder().WithScheme(kepScheme(t)).WithRuntimeObjects(objs...).Build()
	data, err := NewConfigBuilder(client, rbg, nil).Build()
	if err != nil {
		t.Fatalf("ConfigBuilder.Build() error: %v", err)
	}
	var cfg ClusterConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal built config: %v\nconfig:\n%s", err, string(data))
	}
	return cfg
}

// F2: the current discovery address is "<pod-name>.<service-name>" — a short
// form with NO ".<namespace>.svc.cluster.local" suffix. KEP-133's `base`
// examples and pattern table all embed the full FQDN suffix, so a renderer
// implemented from those templates produces addresses that differ
// field-by-field from today's enumeration.
func TestKEP133Claude_AddressShapeIsShortForm(t *testing.T) {
	const (
		rbgName = "epd-test"
		ns      = "default"
	)
	lwpRole := workloadsv1alpha2.RoleSpec{
		Name:     "prefill",
		Replicas: ptr.To(int32(1)),
	}
	lwpRole.LeaderWorkerPattern = &workloadsv1alpha2.LeaderWorkerPattern{Size: ptr.To(int32(2))}
	ccpRole := workloadsv1alpha2.RoleSpec{
		Name:     "decode",
		Replicas: ptr.To(int32(1)),
	}
	ccpRole.CustomComponentsPattern = &workloadsv1alpha2.CustomComponentsPattern{
		Components: []workloadsv1alpha2.InstanceComponent{
			{Name: "leader", Size: ptr.To(int32(1))},
			{Name: "worker", Size: ptr.To(int32(1))},
		},
	}
	rbg := kepRBG(rbgName, ns, lwpRole, ccpRole)
	cfg := buildConfig(t, rbg)

	cases := []struct {
		role  string
		want  []string
	}{
		{
			role: "prefill",
			want: []string{
				"epd-test-prefill-0-0.s-epd-test-prefill",
				"epd-test-prefill-0-1.s-epd-test-prefill",
			},
		},
		{
			role: "decode",
			want: []string{
				"epd-test-decode-0-leader-0.s-epd-test-decode",
				"epd-test-decode-0-worker-0.s-epd-test-decode",
			},
		},
	}
	for _, tc := range cases {
		ri, ok := cfg.Roles[tc.role]
		if !ok {
			t.Fatalf("role %s missing from config", tc.role)
		}
		if len(ri.Instances) != len(tc.want) {
			t.Fatalf("role %s: got %d instances, want %d", tc.role, len(ri.Instances), len(tc.want))
		}
		for i, inst := range ri.Instances {
			if inst.Address != tc.want[i] {
				t.Errorf("role %s instance %d address = %q, want %q", tc.role, i, inst.Address, tc.want[i])
			}
			if strings.Contains(inst.Address, ns) || strings.Contains(inst.Address, "svc.cluster.local") {
				t.Errorf("role %s instance %d address %q contains namespace/FQDN suffix; current format is short form",
					tc.role, i, inst.Address)
			}
		}
	}
}

// F4: legacy enumeration emits CustomComponentsPattern addresses in *spec
// order* of the components, not sorted by component name. KEP-133 sorts
// components by name ("components: array, sorted by name"), so the rendered
// instance list order diverges from the legacy enumeration whenever a spec
// declares components in non-alphabetical order.
func TestKEP133Claude_CCPComponentOrderIsSpecOrder(t *testing.T) {
	ccpRole := workloadsv1alpha2.RoleSpec{
		Name:     "role",
		Replicas: ptr.To(int32(1)),
	}
	ccpRole.CustomComponentsPattern = &workloadsv1alpha2.CustomComponentsPattern{
		Components: []workloadsv1alpha2.InstanceComponent{
			{Name: "zebra", Size: ptr.To(int32(1))},
			{Name: "alpha", Size: ptr.To(int32(1))},
		},
	}
	cfg := buildConfig(t, kepRBG("test", "default", ccpRole))

	ri := cfg.Roles["role"]
	got := make([]string, 0, len(ri.Instances))
	for _, inst := range ri.Instances {
		got = append(got, inst.Address)
	}
	want := []string{
		"test-role-0-zebra-0.s-test-role", // spec order: zebra before alpha
		"test-role-0-alpha-0.s-test-role",
	}
	if len(got) != len(want) {
		t.Fatalf("got instances %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("instance order is SPEC order, not name-sorted: got %v, want %v", got, want)
		}
	}
}

// F3: current discovery membership for a RoleInstanceSet role follows the
// child's *committed* target (committed-skip-ordinals), NOT the raw KEP-499
// role annotation (excluded-ordinals). KEP-133 claims the refine format
// projects the annotation "with semantics inherited verbatim"; a renderer that
// skips annotation-listed ordinals produces a different address list than the
// current builder in the window between annotation edit and membership commit.
func TestKEP133Claude_DiscoveryFollowsCommittedTargetNotAnnotation(t *testing.T) {
	const (
		rbgName = "test-rbg"
		ns      = "default"
	)
	role := workloadsv1alpha2.RoleSpec{
		Name:     "test-role",
		Replicas: ptr.To(int32(3)),
		Annotations: map[string]string{
			// Early-drain hint for ordinal 1 (KEP-499 user input).
			constants.ExcludedOrdinalsAnnotationKey: "1",
		},
	}
	rbg := kepRBG(rbgName, ns, role)

	// Child RIS exists with committed membership that skips ordinal 0
	// (controller-owned committed-skip-ordinals), replicas 3.
	set := &workloadsv1alpha2.RoleInstanceSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rbg.GetWorkloadName(&rbg.Spec.Roles[0]),
			Namespace: ns,
			Annotations: map[string]string{
				constants.CommittedSkipOrdinalsAnnotationKey: "0",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "workloads.x-k8s.io/v1alpha2",
					Kind:       "RoleBasedGroup",
					Name:       rbgName,
					UID:        "rbg-test-uid",
					Controller: ptr.To(true),
				},
			},
		},
		Spec: workloadsv1alpha2.RoleInstanceSetSpec{Replicas: ptr.To(int32(3))},
	}

	cfg := buildConfig(t, rbg, set)
	ri := cfg.Roles["test-role"]

	var got []string
	for _, inst := range ri.Instances {
		got = append(got, inst.Address)
	}
	// Committed target = "replicas members skipping committed-skip ordinals"
	// = [1,2,3] (skip 0). It wins over the annotation's "1":
	//   - ordinal 1 IS still listed even though excluded-ordinals says "1"
	//     (the raw annotation is NOT projected into discovery),
	//   - ordinal 0 is omitted (committed skip), which the annotation does
	//     not know about.
	// A KEP-133 renderer that skips annotation-listed ordinals would emit
	// [0,2] (replicas=3 minus annotation "1") instead — a different address
	// list from the current builder for the very same RBG state.
	want := []string{
		"test-rbg-test-role-1.s-test-rbg-test-role",
		"test-rbg-test-role-2.s-test-rbg-test-role",
		"test-rbg-test-role-3.s-test-rbg-test-role",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("discovery must follow the committed child target, not the annotation:\ngot  %v\nwant %v", got, want)
	}
}

// F6: group.size in the current format is the number of *roles*, not a group
// instance count. The KEP's motivation example shows `size: 4` next to three
// roles (router, prefill, decode), which does not match the builder.
func TestKEP133Claude_GroupSizeEqualsRoleCount(t *testing.T) {
	roles := []workloadsv1alpha2.RoleSpec{
		{Name: "router", Replicas: ptr.To(int32(1))},
		{Name: "prefill", Replicas: ptr.To(int32(2500))},
		{Name: "decode", Replicas: ptr.To(int32(8))},
	}
	cfg := buildConfig(t, kepRBG("pd-inference", "default", roles...))
	if cfg.Group.Size != 3 {
		t.Fatalf("group.size = %d, want 3 (len(roles)); the KEP motivation example shows size: 4 for a 3-role group", cfg.Group.Size)
	}
}

// P0 (premise): the stored discovery ConfigMap content grows linearly with
// replica count today — the size problem KEP-133 targets is real.
func TestKEP133Claude_EnumerationGrowsWithReplicas(t *testing.T) {
	client := fake.NewClientBuilder().WithScheme(kepScheme(t)).Build()

	sizes := []int32{10, 100, 1000, 10000}
	byteLen := map[int32]int{}
	for _, n := range sizes {
		role := workloadsv1alpha2.RoleSpec{Name: "role", Replicas: ptr.To(n)}
		rbg := kepRBG("scale", "default", role)
		data, err := NewConfigBuilder(client, rbg, nil).Build()
		if err != nil {
			t.Fatalf("Build() at replicas=%d: %v", n, err)
		}
		byteLen[n] = len(data)
	}

	perInstance := func(n int32) float64 { return float64(byteLen[n]) / float64(n) }
	t.Logf("enumeration sizes: %v", byteLen)
	t.Logf("bytes per instance: 10=%.0f 100=%.0f 1000=%.0f 10000=%.0f",
		perInstance(10), perInstance(100), perInstance(1000), perInstance(10000))

	// Linear growth: per-instance cost at 10k replicas stays within 2x of the
	// per-instance cost at 100 (fixed headers amortize at small N).
	if perInstance(10000) > 2*perInstance(100) || perInstance(10000) < 0.5*perInstance(100) {
		t.Fatalf("stored content is not O(replicas): per-instance cost grew from %.1f (100 replicas) to %.1f (10000 replicas)",
			perInstance(100), perInstance(10000))
	}
	if byteLen[10000] < 100_000 {
		t.Fatalf("10k single-component replicas produce only %d bytes; expected O(instances) growth", byteLen[10000])
	}
}
