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

package utils

import (
	"testing"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	constants "sigs.k8s.io/rbgs/api/workloads/constants"
)

// These tests pin the Kubernetes label-selector semantics that KEP-284
// (keps/284-pod-cache-filter) builds its four-watch design on. They are
// expected to PASS on any Kubernetes client library: they document the
// premises of the design and the feasibility of the alternatives raised in
// review. Polarity: contract (green on both base and head; a flip would mean
// the vendored client changed semantics and the KEP's premises must be
// revisited).

var (
	warmupLabelKey = "workloads.x-k8s.io/warmup-name"
)

func existsSelector(t *testing.T, key string) labels.Selector {
	t.Helper()
	req, err := labels.NewRequirement(key, selection.Exists, nil)
	if err != nil {
		t.Fatalf("failed to build exists requirement for %s: %v", key, err)
	}
	return labels.NewSelector().Add(*req)
}

func notExistsSelector(t *testing.T, base labels.Selector, key string) labels.Selector {
	t.Helper()
	req, err := labels.NewRequirement(key, selection.DoesNotExist, nil)
	if err != nil {
		t.Fatalf("failed to build not-exists requirement for %s: %v", key, err)
	}
	return base.Add(*req)
}

// Premise mechanism: a single label selector is a conjunction, so no one
// selector covers Pods that carry any of the four admission keys. The four
// categories from the KEP admission table:
func TestKEP284NoSingleSelectorCoversTheUnion(t *testing.T) {
	sources := map[string]labels.Selector{
		"group-name":         existsSelector(t, constants.GroupNameLabelKey),
		"role-instance-name": existsSelector(t, constants.RoleInstanceNameLabelKey),
		"warmup-name":        existsSelector(t, warmupLabelKey),
		"managed=true":       existsSelector(t, managedLabelKey),
	}

	pods := map[string]labels.Set{
		"rbg native workload pod (group-name only)": {
			constants.GroupNameLabelKey: "rbg-a",
		},
		"standalone roleinstance pod (role-instance-name only)": {
			constants.RoleInstanceNameLabelKey: "instance-0",
		},
		"warmup pod (warmup-name only)": {
			warmupLabelKey: "warmup-0",
		},
		"new creation path pod (managed only)": {
			managedLabelKey: "true",
		},
	}

	// Every source fails to match at least one admitted category, and the
	// conjunction of all four sources matches nothing at all: OR across the
	// four keys is not expressible in one selector, which is the KEP's stated
	// reason for running four List/Watch streams.
	conjunction := labels.NewSelector()
	for _, selector := range sources {
		for podName, podLabels := range pods {
			if selector.Matches(podLabels) {
				t.Logf("source %q matches %q", "source", podName)
			}
		}
		reqs, selectable := selector.Requirements()
		if !selectable {
			t.Fatalf("selector is not convertible to requirements")
		}
		conjunction = conjunction.Add(reqs...)
	}
	for podName, podLabels := range pods {
		if conjunction.Matches(podLabels) {
			t.Errorf("conjunction of all four sources unexpectedly matches %q", podName)
		}
	}

	// Any single one of the four keys alone drops at least one category.
	for name, selector := range sources {
		matched := 0
		for _, podLabels := range pods {
			if selector.Matches(podLabels) {
				matched++
			}
		}
		if matched == len(pods) {
			t.Errorf("source %q alone covers every admitted category — the four-watch premise would be false", name)
		}
	}
}

// KEP admission-rule sanity: a Pod with legacy labels but managed=false stays
// admitted (the rule admits on key existence, not the marker's value).
func TestKEP284LegacyPodWithManagedFalseStaysAdmitted(t *testing.T) {
	groupName := existsSelector(t, constants.GroupNameLabelKey)
	podLabels := labels.Set{
		constants.GroupNameLabelKey: "rbg-a",
		managedLabelKey:             "false",
	}
	if !groupName.Matches(podLabels) {
		t.Errorf("pod with group-name and managed=false must stay admitted via the group-name source")
	}
}

// F4 feasibility: the disjoint partition suggested in review (group-name;
// role-instance-name,!group-name; ...) is expressible with DoesNotExist
// requirements, so per-Pod overlap (and the 2-4x duplicate delivery it causes
// for RBG-path RoleInstance Pods, which carry both group-name and
// role-instance-name) is a design choice, not a constraint of the API.
func TestKEP284DisjointPartitionIsExpressible(t *testing.T) {
	disjointRoleInstance := notExistsSelector(
		t, existsSelector(t, constants.RoleInstanceNameLabelKey), constants.GroupNameLabelKey)

	cases := []struct {
		name     string
		pod      labels.Set
		expected bool
	}{
		{
			name:     "standalone instance pod (role-instance-name only)",
			pod:      labels.Set{constants.RoleInstanceNameLabelKey: "instance-0"},
			expected: true,
		},
		{
			name: "rbg-path instance pod (both labels)",
			pod: labels.Set{
				constants.GroupNameLabelKey:        "rbg-a",
				constants.RoleInstanceNameLabelKey: "instance-0",
			},
			expected: false,
		},
	}
	for _, tc := range cases {
		if got := disjointRoleInstance.Matches(tc.pod); got != tc.expected {
			t.Errorf("%s: disjoint selector matched=%v, want %v", tc.name, got, tc.expected)
		}
	}
}
