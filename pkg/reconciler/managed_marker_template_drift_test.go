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

// managedLabelKey is the marker proposed by KEP-284 (keps/284-pod-cache-filter).
const managedLabelKey = constants.RBGPrefix + "managed"

// F1b — KEP-284 no-roll guarantee, end-to-end through the drift detector
// (contract test).
//
// podTemplateSpecEqual is the comparison used by the workload reconcilers
// (StatefulSet at pkg/reconciler/sts_reconciler.go, LWS templates, and the
// direct pod-template path) to decide whether an existing workload must be
// patched. If two templates that differ ONLY by the managed marker compare
// unequal, applying KEP-284's creation-path rule to existing workloads rolls
// their Pods, contradicting the KEP's non-goal "Rolling workloads in order to
// add labels".
//
// Polarity: contract. RED == finding F1 confirmed end to end.
func TestPodTemplateSpecEqualIgnoresManagedMarker(t *testing.T) {
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

	oldTemplate := base()
	newTemplate := base()
	newTemplate.Labels[managedLabelKey] = "true"

	if equal, err := podTemplateSpecEqual(oldTemplate, newTemplate); !equal {
		t.Errorf(
			"F1 confirmed: podTemplateSpecEqual reports templates that differ only by %s as unequal "+
				"(%v); the workload apply path will patch the object and the native controller will "+
				"roll every Pod, contradicting KEP-284's no-rolling-update guarantee",
			managedLabelKey, err,
		)
	}
}
