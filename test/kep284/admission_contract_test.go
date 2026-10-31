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

// Package kep284 holds the reviewer harness for KEP-284 (PR #530,
// keps/284-pod-cache-filter). The PR is docs-only: these contract tests pin
// the KEP's claims about the *current* codebase so the future implementation
// PR and any label refactoring trip a red test instead of silently drifting
// from the approved design.
//
// All tests are CONTRACT polarity: they assert the intended/correct behavior
// of the design against the code as it exists at the reviewed head.
package kep284

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	workloadscontroller "sigs.k8s.io/rbgs/internal/controller/workloads"
	instanceutil "sigs.k8s.io/rbgs/pkg/reconciler/roleinstance/utils"
)

// KEP-284 admission table (README.md "Admission Rule"). The proposed
// `rbg.workloads.x-k8s.io/managed` key does not exist in the codebase yet; it
// is spelled out here exactly as the KEP writes it so the implementation must
// match the approved design.
const (
	kepGroupNameLabel       = "rbg.workloads.x-k8s.io/group-name"
	kepRoleInstanceLabel    = "rbg.workloads.x-k8s.io/role-instance-name"
	kepWarmupLabel          = "workloads.x-k8s.io/warmup-name"
	kepManagedLabel         = "rbg.workloads.x-k8s.io/managed"
	kepManagedAdmittedValue = "true"
)

// kepAdmissionSelectors returns the four selectors of the KEP admission rule,
// one per List/Watch source: three key-exists selectors plus managed=true.
func kepAdmissionSelectors(t *testing.T) map[string]labels.Selector {
	t.Helper()
	exists := func(key string) labels.Selector {
		req, err := labels.NewRequirement(key, selection.Exists, nil)
		if err != nil {
			t.Fatalf("build exists requirement for %q: %v", key, err)
		}
		return labels.NewSelector().Add(*req)
	}
	eq, err := labels.NewRequirement(kepManagedLabel, selection.Equals, []string{kepManagedAdmittedValue})
	if err != nil {
		t.Fatalf("build managed=true requirement: %v", err)
	}
	return map[string]labels.Selector{
		"group-name":         exists(kepGroupNameLabel),
		"role-instance-name": exists(kepRoleInstanceLabel),
		"warmup-name":        exists(kepWarmupLabel),
		"managed":            labels.NewSelector().Add(*eq),
	}
}

func admittedByUnion(sels map[string]labels.Selector, set labels.Set) (string, bool) {
	for name, sel := range sels {
		if sel.Matches(set) {
			return name, true
		}
	}
	return "", false
}

// TestAdmissionRuleTable pins every row of the KEP admission table plus the
// explicitly documented edge cases (managed=false with legacy labels is still
// admitted; managed!=true alone is not).
func TestAdmissionRuleTable(t *testing.T) {
	sels := kepAdmissionSelectors(t)
	cases := []struct {
		name   string
		labels labels.Set
		admit  bool
	}{
		{"group-name key exists (RBG business pod)", labels.Set{kepGroupNameLabel: "g"}, true},
		{"role-instance-name key exists (standalone RoleInstance pod)", labels.Set{kepRoleInstanceLabel: "ri"}, true},
		{"warmup-name key exists (Warmup pod)", labels.Set{kepWarmupLabel: "w"}, true},
		{"managed=true only (new creation path)", labels.Set{kepManagedLabel: "true"}, true},
		{"legacy label with managed=false still admitted", labels.Set{kepGroupNameLabel: "g", kepManagedLabel: "false"}, true},
		{"managed=false alone is NOT admitted", labels.Set{kepManagedLabel: "false"}, false},
		{"managed=TRUE wrong case is NOT admitted", labels.Set{kepManagedLabel: "True"}, false},
		{"managed=1 is NOT admitted", labels.Set{kepManagedLabel: "1"}, false},
		{"unrelated pod (no admission labels)", labels.Set{"app": "x"}, false},
		{"completely unlabeled pod", labels.Set{}, false},
	}
	for _, tc := range cases {
		source, got := admittedByUnion(sels, tc.labels)
		if got != tc.admit {
			t.Errorf("%s: admitted=%v (source %q), want %v", tc.name, got, source, tc.admit)
		}
	}
}

// TestAdmissionRuleNotExpressibleAsOneSelector demonstrates the KEP's premise
// that "Kubernetes label selectors do not support OR across these different
// keys": requirements inside one selector are ANDed, so no single selector
// admits exactly the union.
func TestAdmissionRuleNotExpressibleAsOneSelector(t *testing.T) {
	sels := kepAdmissionSelectors(t)
	// A pod carrying ONLY group-name and a pod carrying ONLY managed=true must
	// both be admitted. Any single selector that matches both can only use
	// requirements satisfied by both label sets; with disjoint keys that means
	// no requirements at all, which would match everything (including a
	// foreign pod) — hence four sources.
	groupOnly := labels.Set{kepGroupNameLabel: "g"}
	managedOnly := labels.Set{kepManagedLabel: "true"}
	foreign := labels.Set{"app": "x"}

	matchAll := labels.Everything()
	if !matchAll.Matches(groupOnly) || !matchAll.Matches(managedOnly) || !matchAll.Matches(foreign) {
		t.Fatal("sanity: labels.Everything must match all sets")
	}
	// Any conjunction of requirements drawn from the admission keys that
	// matches both groupOnly and managedOnly cannot exist: a requirement on
	// the group-name key excludes managedOnly and vice versa. Verify by
	// trying every pair of the KEP selectors ANDed together.
	names := []string{"group-name", "role-instance-name", "warmup-name", "managed"}
	for _, a := range names {
		for _, b := range names {
			if a == b {
				continue
			}
			combined := andSelectors(sels[a], sels[b])
			if combined.Matches(groupOnly) && combined.Matches(managedOnly) {
				t.Errorf("AND of %q and %q unexpectedly admits both disjoint label sets", a, b)
			}
		}
	}
	// Sanity: each individual source still admits its own shape.
	if _, ok := admittedByUnion(sels, groupOnly); !ok {
		t.Error("union must admit group-name-only pod")
	}
	if _, ok := admittedByUnion(sels, managedOnly); !ok {
		t.Error("union must admit managed-only pod")
	}
	if _, ok := admittedByUnion(sels, foreign); ok {
		t.Error("union must NOT admit a foreign pod")
	}
}

func andSelectors(a, b labels.Selector) labels.Selector {
	reqsA, _ := a.Requirements()
	reqsB, _ := b.Requirements()
	out := labels.NewSelector()
	for _, r := range reqsA {
		r := r
		out = out.Add(r)
	}
	for _, r := range reqsB {
		r := r
		out = out.Add(r)
	}
	return out
}

// TestLegacyLabelKeysMatchKEPTable pins the exact production label keys the
// KEP admission table names. If a key is ever renamed, the cache filter and
// the KEP must move together.
func TestLegacyLabelKeysMatchKEPTable(t *testing.T) {
	if constants.GroupNameLabelKey != kepGroupNameLabel {
		t.Errorf("GroupNameLabelKey = %q, KEP table says %q", constants.GroupNameLabelKey, kepGroupNameLabel)
	}
	if constants.RoleInstanceNameLabelKey != kepRoleInstanceLabel {
		t.Errorf("RoleInstanceNameLabelKey = %q, KEP table says %q", constants.RoleInstanceNameLabelKey, kepRoleInstanceLabel)
	}
	if workloadscontroller.LabelWarmupName != kepWarmupLabel {
		t.Errorf("LabelWarmupName = %q, KEP table says %q", workloadscontroller.LabelWarmupName, kepWarmupLabel)
	}
}

// TestWorkloadPodTemplatesCarryGroupName pins the KEP's load-bearing
// compatibility claim for the RBG workload path: the labels merged into every
// Deployment/StatefulSet/LWS/RoleInstanceSet pod template
// (GetCommonLabelsFromRole, used as both selector and template labels)
// contain group-name, so pods of RBG-managed workloads are admitted before
// any backfill exists.
func TestWorkloadPodTemplatesCarryGroupName(t *testing.T) {
	rbg := &workloadsv1alpha2.RoleBasedGroup{ObjectMeta: metav1.ObjectMeta{Name: "g", Namespace: "ns"}}
	role := &workloadsv1alpha2.RoleSpec{Name: "r"}
	merged := rbg.GetCommonLabelsFromRole(role)
	if _, ok := merged[constants.GroupNameLabelKey]; !ok {
		t.Errorf("workload pod template labels %v lack %q: pods of supported native workloads would be invisible to the union cache", merged, kepGroupNameLabel)
	}
}

// TestRoleInstancePodsCarryRoleInstanceName pins the claim for the
// RoleInstance creation path: pods built by InitComponentPodLabels always
// carry role-instance-name.
func TestRoleInstancePodsCarryRoleInstanceName(t *testing.T) {
	for _, tt := range []constants.RoleTemplateType{
		constants.PodTemplateTemplateType,
		constants.LeaderWorkerSetTemplateType,
		constants.ComponentsTemplateType,
	} {
		podLabels := instanceutil.InitComponentPodLabels("ri", "c", 0, tt)
		if _, ok := podLabels[constants.RoleInstanceNameLabelKey]; !ok {
			t.Errorf("template type %s: pod labels %v miss %q", tt, podLabels, kepRoleInstanceLabel)
		}
	}
}
