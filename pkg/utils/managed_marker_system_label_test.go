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

	constants "sigs.k8s.io/rbgs/api/workloads/constants"
)

// managedLabelKey is the marker KEP-284 (keps/284-pod-cache-filter) proposes to
// add to Pod labels: rbg.workloads.x-k8s.io/managed=true.
const managedLabelKey = constants.RBGPrefix + "managed"

// F1a — KEP-284 no-roll guarantee (contract test).
//
// KEP-284 requires new Deployment/StatefulSet/LWS templates to carry the
// managed marker and states "do not trigger a template rolling update for this
// cache fix". The workload drift detectors compare pod-template labels through
// objectMetaEqual -> FilterSystemLabels. Only labels under app.kubernetes.io/
// and rolebasedgroup.workloads.x-k8s.io/ are treated as system labels today.
//
// Contract: for the KEP's guarantee to hold, the managed marker must be
// filtered as a system label (or the KEP must specify another exclusion
// mechanism). RED on the current code means an implementation following the
// KEP as written would flag drift on every existing workload of that type,
// apply the new template, and roll every Pod.
//
// Polarity: contract. RED == finding F1 confirmed.
func TestFilterSystemLabelsIgnoresManagedMarker(t *testing.T) {
	labels := map[string]string{
		managedLabelKey:             "true",
		constants.GroupNameLabelKey: "rbg-a",
		"app.kubernetes.io/name":    "kept-because-filtered",
		"user.example.com/team":     "kept-because-user-label",
	}

	filtered := FilterSystemLabels(labels)

	if _, ok := filtered[managedLabelKey]; ok {
		t.Errorf(
			"F1 confirmed: FilterSystemLabels keeps %q, so adding the managed marker to a workload "+
				"pod template is label drift for objectMetaEqual and triggers a workload update that "+
				"rolls existing Pods; expected the marker to be ignored like app.kubernetes.io/*",
			managedLabelKey,
		)
	}

	// Sanity: the two prefixes that must stay filtered.
	for _, key := range []string{"app.kubernetes.io/name"} {
		if _, ok := filtered[key]; ok {
			t.Errorf("FilterSystemLabels unexpectedly kept system label %q", key)
		}
	}
	if _, ok := filtered[constants.GroupNameLabelKey]; !ok {
		t.Errorf("FilterSystemLabels unexpectedly dropped managed label %q", constants.GroupNameLabelKey)
	}
	if _, ok := filtered["user.example.com/team"]; !ok {
		t.Errorf("FilterSystemLabels unexpectedly dropped user label")
	}
}
