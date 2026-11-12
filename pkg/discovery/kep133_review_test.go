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

// Review verification harness for PR #570 (KEP-133 compact declarative
// discovery ConfigMap). ADDITIVE ONLY: production code is untouched.
//
// The PR under review is documentation-only, so the "claims under test" are
// statements in keps/133-configmap-refine/README.md. Two kinds of tests live
// here:
//
//   - canary pins (pass today): record what the CURRENT code actually does, as
//     evidence for/against the KEP's description of the status quo.
//   - contract tests: encode the behavior the KEP's own text specifies, and
//     check it against ground truth (KEP-499 membership semantics as
//     implemented by api/workloads/ordinals, or the current builder output the
//     KEP promises to reproduce "field-by-field"). A RED contract test is the
//     reproduction of the design defect.
package discovery

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	"sigs.k8s.io/rbgs/api/workloads/ordinals"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/yaml"
)

func kep133Scheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := workloadsv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return scheme
}

func kep133Client(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().WithScheme(kep133Scheme(t)).WithObjects(objs...).Build()
}

func lwpRole(name string, replicas, size int32) workloadsv1alpha2.RoleSpec {
	return workloadsv1alpha2.RoleSpec{
		Name:     name,
		Replicas: ptr.To(replicas),
		Pattern: workloadsv1alpha2.Pattern{
			LeaderWorkerPattern: &workloadsv1alpha2.LeaderWorkerPattern{Size: ptr.To(size)},
		},
	}
}

// kep133RenderOrdinals transcribes the membership rule documented in KEP-133
// (PR #570), "Refine Format" + "Excluded Ordinals" sections:
//
//	roles[].replicas: "Declared instance count; role_index in [0, replicas)"
//	roles[].excludedOrdinals: "subset of [0, replicas)"
//	"Ordinals listed here are not declared members: the renderer skips them,
//	 so no address is emitted for them."
//
// i.e. rendered membership = dense [0, replicas) minus excludedOrdinals.
// If the KEP text is corrected, update this transcription (Harness-update).
func kep133RenderOrdinals(replicas int, excluded []int) []int {
	skip := map[int]bool{}
	for _, e := range excluded {
		skip[e] = true
	}
	var out []int
	for i := 0; i < replicas; i++ {
		if !skip[i] {
			out = append(out, i)
		}
	}
	return out
}

func kep133Expand(t *testing.T, replicas int, excluded string) []int {
	t.Helper()
	e, err := ordinals.Parse(excluded)
	if err != nil {
		t.Fatalf("parse %q: %v", excluded, err)
	}
	return kep133RenderOrdinals(replicas, flattenOrdinals(t, e, replicas))
}

func flattenOrdinals(t *testing.T, r ordinals.Ranges, ceiling int) []int {
	t.Helper()
	var out []int
	for i := 0; i < ceiling; i++ {
		if r.Contains(i) {
			out = append(out, i)
		}
	}
	return out
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	sort.Ints(a)
	sort.Ints(b)
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// --- P0: premise -------------------------------------------------------------
// KEP-133's core claim: the stored discovery content "lists one entry per pod"
// and "with a 2000-instance sibling role the ConfigMap exceeds the 1 MiB
// etcd/ConfigMap hard limit". Reproduce the size growth with the real builder.
// CONTRACT (premise check): PASSES on base == premise confirmed.
func TestP0_Premise_StoredContentGrowsLinearlyWithReplicas(t *testing.T) {
	build := func(replicas int32) int {
		rbg := &workloadsv1alpha2.RoleBasedGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "pd-inference", Namespace: "default"},
			Spec: workloadsv1alpha2.RoleBasedGroupSpec{
				Roles: []workloadsv1alpha2.RoleSpec{lwpRole("prefill", replicas, 5)},
			},
		}
		builder := NewConfigBuilder(kep133Client(t, rbg), rbg, nil)
		data, err := builder.Build()
		if err != nil {
			t.Fatalf("Build() error: %v", err)
		}
		return len(data)
	}
	small := build(10)
	large := build(500)
	huge := build(4000) // 4000 instances x (1 leader + 4 workers) = 20000 entries
	t.Logf("serialized size: replicas=10 -> %d bytes, replicas=500 -> %d bytes, replicas=4000 -> %d bytes", small, large, huge)
	if large < 40*small {
		t.Fatalf("premise expects ~linear growth with replicas: 50x replicas only grew %d -> %d", small, large)
	}
	const configMapHardLimit = 1024 * 1024 // 1 MiB etcd/ConfigMap limit cited by the KEP
	if huge <= configMapHardLimit {
		t.Fatalf("premise expects the 1 MiB hard limit to be reachable at KEP-plausible scale: "+
			"4000 instances x 5 pods serialized to %d bytes (<= 1 MiB)", huge)
	}
	t.Logf("premise CONFIRMED: content grows linearly and crosses the 1 MiB limit at ~%d instances x 5 pods",
		4000*configMapHardLimit/huge)
}

// --- F1: excludedOrdinals semantics ------------------------------------------

// CANARY pin (passes today): the CURRENT builder publishes the KEP-499
// committed membership (child RIS committed ordinals), i.e. members survive in
// discovery even when their ordinal is listed in the exclusion annotation or
// lies outside [0, replicas). This is the behavior KEP-133 claims to inherit
// "verbatim".
func TestF1_CurrentBuilder_PublishesCommittedMembership(t *testing.T) {
	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "group", Namespace: "default", UID: "rbg-uid"},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{
			Roles: []workloadsv1alpha2.RoleSpec{{
				Name:     "worker",
				Replicas: ptr.To(int32(3)),
				Annotations: map[string]string{
					constants.ExcludedOrdinalsAnnotationKey: "1,3",
				},
				Pattern: workloadsv1alpha2.Pattern{StandalonePattern: &workloadsv1alpha2.StandalonePattern{}},
			}},
		},
	}
	set := &workloadsv1alpha2.RoleInstanceSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rbg.GetWorkloadName(&rbg.Spec.Roles[0]),
			Namespace: rbg.Namespace,
			Labels:    map[string]string{constants.GroupNameLabelKey: rbg.Name},
			Annotations: map[string]string{
				// Committed state after "replicas 5 -> 3 with E=[1,3]": M=[0,2,4].
				constants.CommittedSkipOrdinalsAnnotationKey: "1,3",
			},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: workloadsv1alpha2.GroupVersion.String(),
				Kind:       "RoleBasedGroup",
				Name:       rbg.Name,
				UID:        rbg.UID,
				Controller: ptr.To(true),
			}},
		},
		Spec: workloadsv1alpha2.RoleInstanceSetSpec{Replicas: ptr.To(int32(3))},
	}
	builder := NewConfigBuilder(kep133Client(t, rbg, set), rbg, nil)
	data, err := builder.Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	var cfg ClusterConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var got []string
	for _, inst := range cfg.Roles["worker"].Instances {
		got = append(got, inst.Address)
	}
	sort.Strings(got)
	// KEP-499 committed membership M=[0,2,4] (replicas=3, skips=[1,3]).
	want := []string{"group-worker-0.s-group-worker", "group-worker-2.s-group-worker", "group-worker-4.s-group-worker"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("current discovery membership = %v, want committed members %v", got, want)
	}
	t.Logf("current builder publishes committed members %v (incl. ordinal 4 >= replicas=3)", got)
}

// CONTRACT test (RED on the KEP as written == reproduction): apply the render
// rule documented in KEP-133 to membership states that KEP-499 (as implemented
// by api/workloads/ordinals) actually produces. The documented rule must
// reproduce the true membership; it does not.
func TestF1_KEP133DocumentedRule_ReproducesCommittedMembership(t *testing.T) {
	scenarios := []struct {
		name           string
		replicas       int
		excluded       string
		committedTruth []int // membership per KEP-499 semantics (ordinals.Select / Members)
	}{
		{
			// KEP-499: "N of 3 and E of [1,3] produce initial M of [0,2,4]."
			name:           "initial creation with exclusions",
			replicas:       3,
			excluded:       "1,3",
			committedTruth: []int{0, 2, 4},
		},
		{
			// KEP-499: "Edit E without changing replicas -> Keep M unchanged."
			// M stays [0,1,2] after annotating E=[1]; member 1 is still serving.
			name:           "exclusion edit without replica change keeps members",
			replicas:       3,
			excluded:       "1",
			committedTruth: []int{0, 1, 2},
		},
		{
			// KEP-499 scale-down prefers E members but removes only as many as
			// needed: M=[0,1,2,3,4] -> replicas=4 with E=[1,2,3] removes one E
			// member (ordinal 3 first: the existing scale-down order applies
			// within the same priority) and keeps the surviving E-listed
			// members 1 and 2 serving. Verified against ordinals.Select.
			name:           "partial selective scale-down keeps surviving excluded members",
			replicas:       4,
			excluded:       "1,2,3",
			committedTruth: []int{0, 1, 2, 4},
		},
	}
	for _, tt := range scenarios {
		t.Run(tt.name, func(t *testing.T) {
			// Ground truth from the real KEP-499 implementation.
			e, err := ordinals.Parse(tt.excluded)
			if err != nil {
				t.Fatal(err)
			}
			var truth []int
			if strings.Contains(tt.name, "partial") {
				truth, err = ordinals.Select([]int{0, 1, 2, 3, 4}, tt.replicas, e)
			} else if strings.Contains(tt.name, "initial") {
				truth, err = ordinals.Select(nil, tt.replicas, e)
			} else {
				truth = tt.committedTruth // "keep M unchanged" is identity
			}
			if err != nil {
				t.Fatal(err)
			}
			if !equalInts(append([]int(nil), truth...), append([]int(nil), tt.committedTruth...)) {
				t.Fatalf("test setup: ordinals package gave %v, want %v", truth, tt.committedTruth)
			}
			rendered := kep133Expand(t, tt.replicas, tt.excluded)
			if !equalInts(append([]int(nil), rendered...), append([]int(nil), truth...)) {
				t.Fatalf("KEP-133 documented rule renders %v, but KEP-499 committed membership is %v: "+
					"serving members %v would be dropped from discovery",
					rendered, truth, diffInts(truth, rendered))
			}
		})
	}
}

func diffInts(a, b []int) []int {
	inB := map[int]bool{}
	for _, x := range b {
		inB[x] = true
	}
	var out []int
	for _, x := range a {
		if !inB[x] {
			out = append(out, x)
		}
	}
	return out
}

// --- F2: address format ------------------------------------------------------

// CANARY pin (passes today): the current builder emits relative
// "<pod>.<service>" addresses - no namespace, no ".svc.cluster.local".
// KEP-133's Motivation example and its `base` templates
// ("...s-pd-inference-prefill.default.svc.cluster.local") describe FQDNs.
func TestF2_CurrentBuilder_EmitsRelativeAddresses(t *testing.T) {
	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "pd-inference", Namespace: "default"},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{
			Roles: []workloadsv1alpha2.RoleSpec{
				{
					Name:     "router",
					Replicas: ptr.To(int32(2)),
					Pattern:  workloadsv1alpha2.Pattern{StandalonePattern: &workloadsv1alpha2.StandalonePattern{}},
				},
				lwpRole("prefill", 1, 2),
				{
					Name:     "decode",
					Replicas: ptr.To(int32(1)),
					Pattern: workloadsv1alpha2.Pattern{CustomComponentsPattern: &workloadsv1alpha2.CustomComponentsPattern{
						Components: []workloadsv1alpha2.InstanceComponent{
							{Name: "leader", Size: ptr.To(int32(1))},
							{Name: "worker", Size: ptr.To(int32(1))},
						},
					}},
				},
			},
		},
	}
	builder := NewConfigBuilder(kep133Client(t, rbg), rbg, nil)
	data, err := builder.Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	var cfg ClusterConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var addrs []string
	for _, ri := range cfg.Roles {
		for _, inst := range ri.Instances {
			addrs = append(addrs, inst.Address)
		}
	}
	sort.Strings(addrs)
	for _, a := range addrs {
		if strings.Contains(a, "svc.cluster.local") || strings.Contains(a, ".default.") {
			t.Fatalf("current builder emitted FQDN-style address %q; expected relative <pod>.<service>", a)
		}
	}
	want := []string{
		"pd-inference-decode-0-leader-0.s-pd-inference-decode",
		"pd-inference-decode-0-worker-0.s-pd-inference-decode",
		"pd-inference-prefill-0-0.s-pd-inference-prefill",
		"pd-inference-prefill-0-1.s-pd-inference-prefill",
		"pd-inference-router-0.s-pd-inference-router",
		"pd-inference-router-1.s-pd-inference-router",
	}
	if strings.Join(addrs, "\n") != strings.Join(want, "\n") {
		t.Fatalf("current addresses:\n%s\nwant:\n%s", strings.Join(addrs, "\n"), strings.Join(want, "\n"))
	}
	t.Logf("current builder emits relative addresses, e.g. %q", addrs[0])
}

// CONTRACT test (RED on the KEP as written == reproduction): expanding the
// KEP's documented `base` template for CustomComponentsPattern must reproduce
// the current builder output ("field-by-field equal", the KEP's own e2e
// acceptance criterion). The documented template bakes in
// ".{namespace}.svc.cluster.local", so it cannot match.
func TestF2_KEP133BaseTemplate_MatchesCurrentEnumeration(t *testing.T) {
	// Transcribed from KEP-133 "Refine Format" example:
	//   base: "pd-inference-prefill-{role_index}-leader-{component_index}.s-pd-inference-prefill.default.svc.cluster.local"
	const docBase = "pd-inference-decode-{role_index}-leader-{component_index}.s-pd-inference-decode.default.svc.cluster.local"
	rendered := strings.NewReplacer("{role_index}", "0", "{component_index}", "0").Replace(docBase)
	current := "pd-inference-decode-0-leader-0.s-pd-inference-decode" // proven by the pin test above
	if rendered != current {
		t.Fatalf("KEP-133 documented base template renders %q, current enumeration has %q: "+
			"the KEP's field-by-field equality acceptance criterion fails as written", rendered, current)
	}
}

// --- F7: motivation example semantics -----------------------------------------

// CANARY pin (passes today): group.size == number of roles, roles[].size ==
// number of published instance entries (pods), per the current builder. The
// KEP Motivation example shows "group: {size: 4, roles: [router, prefill,
// decode]}" (3 roles) and "prefill: size: 2500" for a 2500-instance role whose
// entries are per-pod.
func TestF7_CurrentBuilder_SizeFieldSemantics(t *testing.T) {
	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "pd-inference", Namespace: "default"},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{
			Roles: []workloadsv1alpha2.RoleSpec{
				{Name: "router", Replicas: ptr.To(int32(2)),
					Pattern: workloadsv1alpha2.Pattern{StandalonePattern: &workloadsv1alpha2.StandalonePattern{}}},
				lwpRole("prefill", 1, 2),
				{Name: "decode", Replicas: ptr.To(int32(1)),
					Pattern: workloadsv1alpha2.Pattern{StandalonePattern: &workloadsv1alpha2.StandalonePattern{}}},
			},
		},
	}
	builder := NewConfigBuilder(kep133Client(t, rbg), rbg, nil)
	data, err := builder.Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	var cfg ClusterConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg.Group.Size != len(rbg.Spec.Roles) {
		t.Fatalf("group.size = %d, want %d (number of roles)", cfg.Group.Size, len(rbg.Spec.Roles))
	}
	if cfg.Roles["prefill"].Size != 2 { // 1 replica x (leader+worker)
		t.Fatalf("roles[prefill].size = %d, want 2 (per-pod entries, not replicas)", cfg.Roles["prefill"].Size)
	}
	t.Logf("group.size=%d (role count), prefill.size=%d (per-pod entries)", cfg.Group.Size, cfg.Roles["prefill"].Size)
}

// Silence unused-import warnings for helpers kept for later rounds.
var _ = fmt.Sprintf
var _ = context.Background
