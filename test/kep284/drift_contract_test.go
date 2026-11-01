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

package kep284

import (
	"testing"

	"sigs.k8s.io/rbgs/pkg/utils"
)

// Round-2 contract tests for the revised KEP-284 (transform design).
//
// The KEP ("Marker and Backfill") specifies that the marker is never written
// into a workload template AND, as a guard, that FilterSystemLabels ignores
// the rbg.workloads.x-k8s.io/managed key ONLY, with these contract tests as
// the implementation regression suite. On this docs-only head the production
// code is unchanged, so the first test is RED by design; it must flip GREEN
// when the implementation PR lands the key-scoped exemption.

// TestFilterSystemLabelsIgnoresManagedMarker is a CONTRACT test: the marker
// key alone must not register as label drift (KEP guarantee: adding the
// marker must not roll existing Pods). RED at the reviewed head f8d67fa
// (production exemption not yet implemented — that is implementation-PR work
// the KEP now specifies). A green result here with the rest of the guard
// tests green is the merge gate for the implementation PR.
func TestFilterSystemLabelsIgnoresManagedMarker(t *testing.T) {
	base := map[string]string{
		kepGroupNameLabel: "g1",
		"app":             "x",
	}
	withMarker := map[string]string{
		kepGroupNameLabel: "g1",
		"app":             "x",
		kepManagedLabel:   "true",
	}
	got := utils.FilterSystemLabels(withMarker)
	if _, ok := got[kepManagedLabel]; ok {
		t.Fatalf("FilterSystemLabels kept the managed marker; KEP-284 requires the %q key to be exempt from drift comparison (got %v)", kepManagedLabel, got)
	}
	if got[kepGroupNameLabel] != "g1" || got["app"] != "x" {
		t.Fatalf("FilterSystemLabels dropped unrelated labels: %v", got)
	}
	_ = base
}

// TestGroupNameDriftStillDetected is the scope guard for the exemption
// (CONTRACT, must stay GREEN on every head): the exemption covers ONLY the
// managed key; group-name drift is real drift and must still be reported.
// Stripping the whole rbg.workloads.x-k8s.io/ prefix would wrongly flip this
// test red.
func TestGroupNameDriftStillDetected(t *testing.T) {
	oldLabels := map[string]string{kepGroupNameLabel: "g1", "app": "x"}
	newLabels := map[string]string{kepGroupNameLabel: "g2", "app": "x"}
	fo := utils.FilterSystemLabels(oldLabels)
	if fo[kepGroupNameLabel] == "" || newLabels[kepGroupNameLabel] == "" {
		t.Fatalf("FilterSystemLabels wrongly stripped the group-name key: old=%v new=%v", fo, newLabels)
	}
	if fo[kepGroupNameLabel] == newLabels[kepGroupNameLabel] {
		t.Fatalf("group-name drift was hidden: old=%v new=%v", fo, newLabels)
	}
}
