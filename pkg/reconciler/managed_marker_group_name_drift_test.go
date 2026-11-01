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

package reconciler

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	constants "sigs.k8s.io/rbgs/api/workloads/constants"
)

// F1-guard — KEP-284 no-roll guarantee, drift-detection guard (contract test).
//
// KEP-284 (revision 2, "Marker and Backfill") specifies the no-roll mechanism
// as: (1) the marker is never written into a workload template, and (2) as a
// guard, FilterSystemLabels ignores the rbg.workloads.x-k8s.io/managed key
// "and only that key: ignoring the whole prefix would hide group-name,
// role-name, and group-uid drift", alongside "a test that group-name drift is
// still detected".
//
// This is that test. When the implementation PR lands the key-scoped
// exemption, this must stay green: a template diff that changes group-name
// (or role-name) must still be reported as drift, so the workload reconcilers
// keep patching real label drift while ignoring the managed marker.
//
// Polarity: contract. GREEN today and after the marker exemption; a flip to
// RED means the exemption was implemented too broadly (whole-prefix strip).
func TestGroupNameDriftStillDetected(t *testing.T) {
	base := func() corev1.PodTemplateSpec {
		return corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					constants.GroupNameLabelKey: "rbg-a",
					constants.RoleNameLabelKey:  "role-0",
				},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "c", Image: "img:1"}},
			},
		}
	}

	cases := []struct {
		name   string
		mutate func(*corev1.PodTemplateSpec)
	}{
		{
			name: "group-name value change is drift",
			mutate: func(ts *corev1.PodTemplateSpec) {
				ts.Labels[constants.GroupNameLabelKey] = "rbg-b"
			},
		},
		{
			name: "group-name removed is drift",
			mutate: func(ts *corev1.PodTemplateSpec) {
				delete(ts.Labels, constants.GroupNameLabelKey)
			},
		},
		{
			name: "role-name value change is drift",
			mutate: func(ts *corev1.PodTemplateSpec) {
				ts.Labels[constants.RoleNameLabelKey] = "role-1"
			},
		},
	}
	for _, tc := range cases {
		oldTemplate := base()
		newTemplate := base()
		tc.mutate(&newTemplate)

		if equal, err := podTemplateSpecEqual(oldTemplate, newTemplate); equal {
			t.Errorf(
				"%s: podTemplateSpecEqual reported templates that differ by a real RBG label as equal "+
					"(%v); a whole-prefix rbg.workloads.x-k8s.io/ exemption would silently hide "+
					"group-name/role-name drift, exactly what KEP-284's guard forbids",
				tc.name, err,
			)
		}
	}

	// objectMetaEqual directly: a managed-marker-only diff must be invisible
	// once the guard lands, while a group-name diff stays visible. Today only
	// the second half holds; the first is the F1 pair's contract.
	metaA := metav1.ObjectMeta{Labels: map[string]string{constants.GroupNameLabelKey: "rbg-a"}}
	metaB := metav1.ObjectMeta{Labels: map[string]string{constants.GroupNameLabelKey: "rbg-b"}}
	if equal, _ := objectMetaEqual(metaA, metaB); equal {
		t.Error("objectMetaEqual reported a group-name-only diff as equal; group-name drift must stay detectable")
	}
}
