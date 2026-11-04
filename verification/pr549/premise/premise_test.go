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

// Premise check (P0) for PR #549, written against the merge-base and the PR
// head. Contract: the field-managed fake clientset (fake.NewClientset, the
// replacement for the deprecated NewSimpleClientset) must accept Create,
// Update and Apply of RBG objects.
//
// Polarity: contract test. On the merge-base (98d3c4be) it must FAIL with
// "no type found matching", proving the claimed problem exists; on the PR
// head it must PASS, proving the fix works at the basic level.
package premise

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	applyv1alpha2 "sigs.k8s.io/rbgs/client-go/applyconfiguration/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/client-go/clientset/versioned/fake"
)

func TestFakeClientsetWritesRoleBasedGroups(t *testing.T) {
	ctx := context.Background()
	clientset := fake.NewClientset()
	client := clientset.WorkloadsV1alpha2().RoleBasedGroups("default")

	if _, err := client.Create(ctx, &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "created"},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{
			Roles: []workloadsv1alpha2.RoleSpec{{Name: "prefill", Replicas: ptr.To[int32](1)}},
		},
	}, metav1.CreateOptions{FieldManager: "creator"}); err != nil {
		t.Fatalf("Create via fake.NewClientset failed: %v", err)
	}

	got, err := client.Get(ctx, "created", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	got.Spec.Roles[0].Replicas = ptr.To[int32](2)
	if _, err := client.Update(ctx, got, metav1.UpdateOptions{FieldManager: "updater"}); err != nil {
		t.Fatalf("Update via fake.NewClientset failed: %v", err)
	}

	// Apply of an object that does not exist yet must create it, as the API
	// server does.
	if _, err := client.Apply(ctx, applyv1alpha2.RoleBasedGroup("applied", "default").
		WithSpec(applyv1alpha2.RoleBasedGroupSpec().
			WithRoles(applyv1alpha2.RoleSpec().WithName("decode").WithReplicas(1))),
		metav1.ApplyOptions{FieldManager: "applier"}); err != nil {
		t.Fatalf("Apply via fake.NewClientset failed: %v", err)
	}
}
