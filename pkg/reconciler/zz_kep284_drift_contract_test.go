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
)

// Round-2 KEP-284 contract test, in-package because podTemplateSpecEqual is
// unexported. The KEP ("Marker and Backfill") guarantees that adding
// rbg.workloads.x-k8s.io/managed to a workload pod template is not label
// drift, so an operator upgrade must not roll existing Pods. RED at the
// reviewed head f8d67fa: the key-scoped FilterSystemLabels exemption the KEP
// now specifies is implementation-PR work.
func TestPodTemplateSpecEqualIgnoresManagedMarker(t *testing.T) {
	const managedKey = "rbg.workloads.x-k8s.io/managed"
	tmpl := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{
			"rbg.workloads.x-k8s.io/group-name": "g1",
			"rbg.workloads.x-k8s.io/role-name":  "r1",
		}},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: "i"}}},
	}
	withMarker := tmpl.DeepCopy()
	withMarker.Labels[managedKey] = "true"
	equal, err := podTemplateSpecEqual(tmpl, *withMarker)
	if err == nil && equal {
		return // exemption landed
	}
	t.Fatalf("managed marker in a pod template registers as drift (equal=%v, err=%v); KEP-284 requires a key-scoped exemption for %q so adding the marker cannot roll existing Pods", equal, err, managedKey)
}
