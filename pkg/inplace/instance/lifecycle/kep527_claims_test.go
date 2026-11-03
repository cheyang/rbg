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

package lifecycle

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// KEP-527 (keps/527-roleinstance-lifecycle, "Relationship to Existing
// Mechanisms") and issue #527 claim the existing RIS lifecycle coordinates
// PreDelete / InPlaceUpdate hooks through labelsHandler labels and
// finalizersHandler finalizers written onto the RoleInstance, with the
// external consumer releasing them.
//
// TestKep527ExistingCoordinationModel is a CONTRACT test for that premise: it
// pins the current mechanism the KEP builds on. It passes while the premise is
// accurate and turns red if the existing protocol changes shape, which would
// make the KEP's "builds on the coordination model of the existing RIS
// labelsHandler" statement stale.
func TestKep527ExistingCoordinationModel(t *testing.T) {
	instance := &workloadsv1alpha2.RoleInstance{
		ObjectMeta: metav1.ObjectMeta{Namespace: metav1.NamespaceDefault, Name: "kep527-instance"},
	}

	_ = workloadsv1alpha2.AddToScheme(clientgoscheme.Scheme)
	fakeClient := fake.NewClientBuilder().
		WithScheme(clientgoscheme.Scheme).
		WithObjects(instance).
		Build()

	hook := &workloadsv1alpha2.RoleInstanceSetLifecycleHook{
		LabelsHandler:     map[string]string{"example.com/update-block": "true"},
		FinalizersHandler: []string{"example.com/fd"},
	}

	ctrl := New(fakeClient)

	// Before the hook is materialized, the RI is not hooked.
	if IsRoleInstanceHooked(hook, instance) {
		t.Fatal("fresh instance must not be hooked")
	}

	// The controller materializes the hook: it writes the labelsHandler
	// markers and finalizersHandler finalizers onto the RI together with the
	// lifecycle state.
	updated, got, err := ctrl.UpdateRoleInstanceLifecycleWithHandler(instance, constants.RoleInstanceLifecycleStatePreparingUpdate, hook)
	if err != nil || !updated {
		t.Fatalf("UpdateRoleInstanceLifecycleWithHandler should succeed and update, updated=%v err=%v", updated, err)
	}
	if got.Labels["example.com/update-block"] != "true" {
		t.Fatalf("controller must write the labelsHandler marker, labels=%v", got.Labels)
	}
	if !containsFinalizer(got, "example.com/fd") {
		t.Fatalf("controller must add the finalizersHandler finalizer, finalizers=%v", got.Finalizers)
	}
	if GetRoleInstanceLifecycleState(got) != constants.RoleInstanceLifecycleStatePreparingUpdate {
		t.Fatalf("controller must set lifecycle state, got %q", GetRoleInstanceLifecycleState(got))
	}
	if !IsRoleInstanceHooked(hook, got) || !IsRoleInstanceAllHooked(hook, got) {
		t.Fatal("instance with all markers present must be hooked")
	}

	// External release: removing the marker label un-blocks the wait — the
	// coordination direction the KEP's new protocol also relies on.
	released := got.DeepCopy()
	delete(released.Labels, "example.com/update-block")
	if err := fakeClient.Update(context.TODO(), released); err != nil {
		t.Fatal(err)
	}
	final := &workloadsv1alpha2.RoleInstance{}
	if err := fakeClient.Get(context.TODO(), types.NamespacedName{Namespace: metav1.NamespaceDefault, Name: "kep527-instance"}, final); err != nil {
		t.Fatal(err)
	}
	if IsRoleInstanceAllHooked(hook, final) {
		t.Fatal("removing the marker label must release the hook wait")
	}
	if !IsRoleInstanceHooked(hook, final) {
		t.Fatal("finalizer alone still counts as hooked (any-condition semantics)")
	}
}

func containsFinalizer(instance *workloadsv1alpha2.RoleInstance, finalizer string) bool {
	for _, f := range instance.Finalizers {
		if f == finalizer {
			return true
		}
	}
	return false
}
