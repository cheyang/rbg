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

// Verification harness for PR #536 (sgl-project/rbg#534 Task 2 premise).
// Polarity: CONTRACT tests — they assert the intended behavior.
// Expected RED on base 4a0765f7 (bug present) and GREEN on the PR head.

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

// P0a: entering PreparingDelete with markPodNotReady=true on an instance that
// lacks the custom readiness gate must still block the instance: the gate is
// initialized and a RoleInstanceCustomReady=False condition exists.
func TestVerifyP0GatelessInstanceBecomesBlocked(t *testing.T) {
	instance := &api.RoleInstance{ObjectMeta: metav1.ObjectMeta{Name: "instance", Namespace: "default"}}
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(instance).WithStatusSubresource(instance).Build()
	_, _, err := New(c).UpdateRoleInstanceLifecycle(instance, constants.RoleInstanceLifecycleStatePreparingDelete, true)
	require.NoError(t, err)
	got := &api.RoleInstance{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(instance), got))
	require.True(t, readiness.ContainsReadinessGate(got),
		"P0a: the custom readiness gate must be initialized for existing instances")
	condition := readiness.GetReadinessCondition(got)
	require.NotNil(t, condition, "P0a: a custom readiness condition must be written")
	require.Equal(t, corev1.ConditionFalse, condition.Status,
		"P0a: the custom readiness condition must be False while the hook blocks")
}

// P0b: with the gate pre-declared, the first AddNotReadyKey must write a
// condition with a valid Status=False, not an empty status.
func TestVerifyP0FirstConditionHasFalseStatus(t *testing.T) {
	instance := &api.RoleInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "instance", Namespace: "default"},
		Spec: api.RoleInstanceSpec{ReadinessGates: []api.RoleInstanceReadinessGate{
			{ConditionType: api.RoleInstanceCustomReady},
		}},
	}
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(instance).WithStatusSubresource(instance).Build()
	require.NoError(t, readiness.New(c).AddNotReadyKey(instance, readiness.Message{UserAgent: "Lifecycle", Key: "preDeleteHook"}))
	got := &api.RoleInstance{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(instance), got))
	condition := readiness.GetReadinessCondition(got)
	require.NotNil(t, condition)
	require.Equal(t, corev1.ConditionFalse, condition.Status,
		"P0b: the first custom condition must be created with Status=False")
}
