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

// Verification harness for PR #558 review (Reviewer B / Codex).
// Additive test file only; production code is untouched.
//
// F2 (contract): entering PreparingDelete with MarkNotReady=true must succeed
// in a single UpdateRoleInstanceLifecycle call. The PR changed the patch to an
// optimistic-lock merge patch built from the stale cached object, while
// executeRoleInstanceNotReadyPolicy writes instance status FIRST (bumping the
// server-side resourceVersion). The subsequent patch therefore always carries
// a stale resourceVersion and conflicts on the first attempt.
//   - base (fd160cc): PASS  (raw merge patch, no resourceVersion precondition)
//   - head (4d82f84): FAIL  (conflict on the first attempt; state never set)
//
// F2-bound (contract): the failure is transient — a retry with a fresh read
// succeeds. Green on both base and head; it bounds F2's blast radius to one
// failed reconcile per hooked deletion and guards against a fix that makes
// things worse.
//
// Property (contract, new in this PR): a lifecycle patch built from a stale
// cached object conflicts once the consumer has concurrently released the
// hook, so the controller retries instead of clobbering the release.
//   - base: FAIL (raw patch clobbers silently)
//   - head: PASS

package lifecycle

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func newHookedInstanceWithGate(name string) *workloadsv1alpha2.RoleInstance {
	return &workloadsv1alpha2.RoleInstance{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: metav1.NamespaceDefault,
			Name:      name,
			Labels:    map[string]string{"consumer.example/held": "true"},
		},
		Spec: workloadsv1alpha2.RoleInstanceSpec{
			ReadinessGates: []workloadsv1alpha2.RoleInstanceReadinessGate{
				{ConditionType: workloadsv1alpha2.RoleInstanceCustomReady},
			},
		},
	}
}

func newLifecycleFakeClient(objs ...client.Object) client.Client {
	_ = workloadsv1alpha2.AddToScheme(clientgoscheme.Scheme)
	builder := fake.NewClientBuilder().WithScheme(clientgoscheme.Scheme).WithObjects(objs...)
	for _, o := range objs {
		builder = builder.WithStatusSubresource(o)
	}
	return builder.Build()
}

func getStored(t *testing.T, c client.Client, name string) *workloadsv1alpha2.RoleInstance {
	t.Helper()
	stored := &workloadsv1alpha2.RoleInstance{}
	if err := c.Get(context.TODO(), types.NamespacedName{Namespace: metav1.NamespaceDefault, Name: name}, stored); err != nil {
		t.Fatal(err)
	}
	return stored
}

// F2 contract: fails on the PR head, passes on base and on a fixed head.
func TestVerifyPreDeleteMarkNotReadySingleCallSucceeds(t *testing.T) {
	instance := newHookedInstanceWithGate("hooked-0")
	c := newLifecycleFakeClient(instance)
	lc := New(c)

	// what the controller holds: a cached copy, as delivered by the informer
	cached := getStored(t, c, "hooked-0")

	if _, _, err := lc.UpdateRoleInstanceLifecycle(cached, constants.RoleInstanceLifecycleStatePreparingDelete, true); err != nil {
		t.Fatalf("first UpdateRoleInstanceLifecycle(markNotReady=true) returned error: %v", err)
	}
	if got := GetRoleInstanceLifecycleState(getStored(t, c, "hooked-0")); got != constants.RoleInstanceLifecycleStatePreparingDelete {
		t.Fatalf("lifecycle state after first call = %q, want %q", got, constants.RoleInstanceLifecycleStatePreparingDelete)
	}
}

// F2 bound: green on both base and head while the failure stays transient.
func TestVerifyPreDeleteMarkNotReadyHealsWithinTwoAttempts(t *testing.T) {
	instance := newHookedInstanceWithGate("hooked-1")
	c := newLifecycleFakeClient(instance)
	lc := New(c)

	var lastErr error
	for attempt := 1; attempt <= 2; attempt++ {
		cached := getStored(t, c, "hooked-1")
		if _, _, lastErr = lc.UpdateRoleInstanceLifecycle(cached, constants.RoleInstanceLifecycleStatePreparingDelete, true); lastErr == nil {
			break
		}
		t.Logf("attempt %d failed (transient conflict expected on PR head): %v", attempt, lastErr)
	}
	if lastErr != nil {
		t.Fatalf("PreparingDelete transition did not succeed within 2 attempts: %v", lastErr)
	}
	if got := GetRoleInstanceLifecycleState(getStored(t, c, "hooked-1")); got != constants.RoleInstanceLifecycleStatePreparingDelete {
		t.Fatalf("state after retries = %q, want PreparingDelete", got)
	}
}

// Property: passes on head (optimistic lock), fails on base (raw patch).
func TestVerifyOptimisticPatchConflictsAfterConsumerUpdate(t *testing.T) {
	instance := newHookedInstanceWithGate("hooked-2")
	c := newLifecycleFakeClient(instance)
	lc := New(c)

	stale := getStored(t, c, "hooked-2")

	// consumer concurrently releases the hook
	consumer := getStored(t, c, "hooked-2")
	delete(consumer.Labels, "consumer.example/held")
	if err := c.Update(context.TODO(), consumer); err != nil {
		t.Fatal(err)
	}

	if _, _, err := lc.UpdateRoleInstanceLifecycle(stale, constants.RoleInstanceLifecycleStatePreparingDelete, false); err == nil {
		t.Fatalf("expected conflict patching from a stale resourceVersion, got nil")
	} else if !strings.Contains(err.Error(), "modified") && !strings.Contains(err.Error(), "onflict") {
		t.Fatalf("expected a conflict-style error, got: %v", err)
	}
	// consumer's release must be intact
	if _, ok := getStored(t, c, "hooked-2").Labels["consumer.example/held"]; ok {
		t.Fatalf("consumer hook release was clobbered")
	}
}
