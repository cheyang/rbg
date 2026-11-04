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

// Package attribution holds the base-compilable attribution contract for PR
// #549 finding F1. It deliberately imports only packages that exist on the
// merge-base, so the same test runs there.
package attribution

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	applyv1alpha1 "sigs.k8s.io/rbgs/client-go/applyconfiguration/workloads/v1alpha1"
	"sigs.k8s.io/rbgs/client-go/clientset/versioned/fake"
)

// TestFakeRejectsDivergentAtomicListApply is the attribution contract for F1:
// applying a divergent v1alpha1 Instance components list as a second field
// manager must never silently succeed — an error (a conflict once fixed, a
// loud schema error on the merge-base) is safe, a silent merge is not. The
// server treats v1alpha1 InstanceSpec.Components as an atomic list (the CRD
// has no x-kubernetes-list-type), so the correct behavior is a conflict.
//
// Expected outcomes: merge-base PASS (fake errors loudly: no schema), reviewed
// head FAIL (fake silently merges: the embedded schema models the list as a
// list-map), fixed head PASS (fake returns a conflict).
func TestFakeRejectsDivergentAtomicListApply(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset().WorkloadsV1alpha1().Instances("default")
	apply := func(component, manager string) error {
		_, err := client.Apply(ctx, applyv1alpha1.Instance("parity", "default").
			WithSpec(applyv1alpha1.InstanceSpec().
				WithComponents(applyv1alpha1.InstanceComponent().WithName(component))),
			metav1.ApplyOptions{FieldManager: manager})
		return err
	}
	if err := apply("prefill", "manager-a"); err != nil {
		t.Logf("first apply failed (expected on the merge-base, where the fake has no schema): %v", err)
		return
	}
	if fakeErr := apply("decode", "manager-b"); fakeErr == nil {
		t.Errorf("fake silently merged a divergent atomic components list (server behavior: conflict)")
	} else {
		t.Logf("second apply rejected (safe): %v", fakeErr)
	}
}
