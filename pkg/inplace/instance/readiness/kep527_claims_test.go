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

package readiness

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func newKep527FakeClient(t *testing.T, objs ...*workloadsv1alpha2.RoleInstance) *fake.ClientBuilder {
	t.Helper()
	_ = workloadsv1alpha2.AddToScheme(clientgoscheme.Scheme)
	clientObjs := make([]client.Object, 0, len(objs))
	for _, o := range objs {
		clientObjs = append(clientObjs, o)
	}
	return fake.NewClientBuilder().WithScheme(clientgoscheme.Scheme).WithObjects(clientObjs...)
}

// KEP-527 (keps/527-roleinstance-lifecycle, "Ready Gating") claims:
//
//	"This gate reuses the existing `RoleInstanceCustomReady` condition. The RI's
//	 `spec.readinessGates` must include `RoleInstanceCustomReady`; otherwise, the
//	 existing readiness helper returns immediately and the gate has no effect."
//
// TestKep527ReadinessGatePrerequisite pins that claim against the current code.
// CONTRACT test for the documented behavior: passes while the claim is
// accurate; turns red if the helper semantics change, which would make the
// KEP's prerequisite text stale.
func TestKep527ReadinessGatePrerequisite(t *testing.T) {
	withoutGate := &workloadsv1alpha2.RoleInstance{
		ObjectMeta: metav1.ObjectMeta{Namespace: metav1.NamespaceDefault, Name: "kep527-no-gate"},
		Spec:       workloadsv1alpha2.RoleInstanceSpec{ReadinessGates: []workloadsv1alpha2.RoleInstanceReadinessGate{}},
	}
	withGate := &workloadsv1alpha2.RoleInstance{
		ObjectMeta: metav1.ObjectMeta{Namespace: metav1.NamespaceDefault, Name: "kep527-with-gate"},
		Spec: workloadsv1alpha2.RoleInstanceSpec{
			ReadinessGates: []workloadsv1alpha2.RoleInstanceReadinessGate{
				{ConditionType: workloadsv1alpha2.RoleInstanceCustomReady},
			},
		},
	}

	fakeClient := newKep527FakeClient(t, withoutGate, withGate).
		WithStatusSubresource(withoutGate, withGate).Build()

	ctrl := New(fakeClient)
	msg := Message{UserAgent: "Lifecycle", Key: "preUpdateHook"}

	// Without the readiness gate the helper must be a no-op: it returns nil and
	// writes no condition at all.
	if err := ctrl.AddNotReadyKey(withoutGate, msg); err != nil {
		t.Fatalf("AddNotReadyKey without gate should return nil, got %v", err)
	}
	got := &workloadsv1alpha2.RoleInstance{}
	if err := fakeClient.Get(context.TODO(), types.NamespacedName{Namespace: metav1.NamespaceDefault, Name: "kep527-no-gate"}, got); err != nil {
		t.Fatal(err)
	}
	if len(got.Status.Conditions) != 0 {
		t.Fatalf("AddNotReadyKey without readiness gate must not write conditions, got %v", got.Status.Conditions)
	}

	// With the gate registered the helper must record the not-ready key in the
	// RoleInstanceCustomReady condition (message payload).
	if err := ctrl.AddNotReadyKey(withGate, msg); err != nil {
		t.Fatalf("AddNotReadyKey with gate should succeed, got %v", err)
	}
	got = &workloadsv1alpha2.RoleInstance{}
	if err := fakeClient.Get(context.TODO(), types.NamespacedName{Namespace: metav1.NamespaceDefault, Name: "kep527-with-gate"}, got); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, cond := range got.Status.Conditions {
		if cond.Type == workloadsv1alpha2.RoleInstanceCustomReady {
			found = true
			if !alreadyHasKey(got, msg, workloadsv1alpha2.RoleInstanceCustomReady) {
				t.Fatalf("RoleInstanceCustomReady condition should carry message %v, got %q", msg, cond.Message)
			}
		}
	}
	if !found {
		t.Fatalf("AddNotReadyKey with readiness gate should write RoleInstanceCustomReady, got %v", got.Status.Conditions)
	}
}

// TestKep527CanaryCustomReadyFirstWriteOmitsStatus is a BUG-CANARY for a
// pre-existing defect surfaced while verifying the KEP claim above (not
// introduced by PR #546, which is documentation-only):
//
// addNotReadyKey creates the RoleInstanceCustomReady condition without setting
// Status, so the first write lands with status "" instead of "False"
// (api/workloads/v1alpha2 requires condition.status; consumers compare against
// "True"/"False"). The canary PASSES while the wrong behavior is present and
// FLIPS TO FAIL when it is fixed — invert the assertion when it flips.
func TestKep527CanaryCustomReadyFirstWriteOmitsStatus(t *testing.T) {
	withGate := &workloadsv1alpha2.RoleInstance{
		ObjectMeta: metav1.ObjectMeta{Namespace: metav1.NamespaceDefault, Name: "kep527-canary"},
		Spec: workloadsv1alpha2.RoleInstanceSpec{
			ReadinessGates: []workloadsv1alpha2.RoleInstanceReadinessGate{
				{ConditionType: workloadsv1alpha2.RoleInstanceCustomReady},
			},
		},
	}
	fakeClient := newKep527FakeClient(t, withGate).WithStatusSubresource(withGate).Build()

	ctrl := New(fakeClient)
	msg := Message{UserAgent: "Lifecycle", Key: "preUpdateHook"}
	if err := ctrl.AddNotReadyKey(withGate, msg); err != nil {
		t.Fatalf("AddNotReadyKey should succeed, got %v", err)
	}
	got := &workloadsv1alpha2.RoleInstance{}
	if err := fakeClient.Get(context.TODO(), types.NamespacedName{Namespace: metav1.NamespaceDefault, Name: "kep527-canary"}, got); err != nil {
		t.Fatal(err)
	}
	for _, cond := range got.Status.Conditions {
		if cond.Type == workloadsv1alpha2.RoleInstanceCustomReady {
			if cond.Status != "" {
				t.Fatalf("canary flipped: first write now sets Status=%q (expected the pre-existing empty status); invert this canary and promote it to a contract test", cond.Status)
			}
			return
		}
	}
	t.Fatalf("RoleInstanceCustomReady condition not written: %v", got.Status.Conditions)
}
