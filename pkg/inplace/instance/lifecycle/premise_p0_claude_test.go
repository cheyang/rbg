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

// Reviewer harness (Claude) for sgl-project/rbg PR #536 — claim P0 (premise).
//
// POLARITY: bug-canary, runsAgainst=base.
//
// The premise of PR #536 (issue #534, Task 2) is that on the BASE branch a
// lifecycle hook with markPodNotReady=true does not make the RoleInstance (or
// its component Pods) NotReady when the instance lacks the custom readiness
// gate: addNotReadyKey returns early, no condition is written, and no gate is
// added.
//
// This canary asserts exactly that BASE behavior. It therefore:
//   - PASSES on the base (bug present -> premise CONFIRMED);
//   - FAILS on the PR head (bug fixed -> premise fix verified).
// After the fix, invert it into a contract test (see
// TestP0PremiseContractAfterFix below, which encodes the fixed behavior and is
// skipped while the canary documents the base).

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	api "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/pkg/inplace/instance/readiness"
)

func newP0Instance() *api.RoleInstance {
	return &api.RoleInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "instance", Namespace: "default", UID: "uid-p0"},
	}
}

// TestP0PremiseBaseCanary asserts the BASE (buggy) behavior: transitioning to
// PreparingDelete with markPodNotReady=true has no readiness effect on an
// instance without the custom readiness gate.
func TestP0PremiseBaseCanary(t *testing.T) {
	instance := newP0Instance()
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(instance).WithStatusSubresource(instance).Build()

	_, _, err := New(c).UpdateRoleInstanceLifecycle(instance, constants.RoleInstanceLifecycleStatePreparingDelete, true)
	require.NoError(t, err)

	got := &api.RoleInstance{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(instance), got))

	// Base behavior: no custom readiness gate was added ...
	require.False(t, readiness.ContainsReadinessGate(got),
		"BASE behavior expected: no RoleInstanceCustomReady gate injected")
	// ... and no custom ready condition was written.
	require.Nil(t, readiness.GetReadinessCondition(got),
		"BASE behavior expected: no RoleInstanceCustomReady condition written")
}

// TestP0PremiseContractAfterFix encodes the FIXED behavior for the same input.
// On the PR head this passes; on base it fails. Kept alongside the canary so
// the harness can be inverted in one place after the fix lands upstream.
func TestP0PremiseContractAfterFix(t *testing.T) {
	instance := newP0Instance()
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(instance).WithStatusSubresource(instance).Build()

	_, _, err := New(c).UpdateRoleInstanceLifecycle(instance, constants.RoleInstanceLifecycleStatePreparingDelete, true)
	require.NoError(t, err)

	got := &api.RoleInstance{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(instance), got))
	require.True(t, readiness.ContainsReadinessGate(got), "gate must be initialized for existing instances")
	condition := readiness.GetReadinessCondition(got)
	require.NotNil(t, condition)
	require.Equal(t, corev1.ConditionFalse, condition.Status)
}
