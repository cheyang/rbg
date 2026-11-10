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

package sync

// Reviewer harness for PR #560 (maxGroupRestarts) — findings F2/F4.
// Independent of the PR's own test fixtures so it survives PR test edits.

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// r560Fixture builds a budgeted FromCreation RoleInstance with 2 owned worker
// pods (one Failed) entirely from harness-local code.
func r560Fixture(t *testing.T, cachedGeneration int64, liveGeneration int64, limit *int32) (*realControl, *workloadsv1alpha2.RoleInstance, *countingReader) {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, v1.AddToScheme(scheme))
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))

	liveInstance := &workloadsv1alpha2.RoleInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "r560-0", Namespace: "default", UID: "r560-uid", Generation: liveGeneration},
		Spec: workloadsv1alpha2.RoleInstanceSpec{
			RestartPolicyConfig: &workloadsv1alpha2.RestartPolicyConfig{Type: workloadsv1alpha2.RecreateRoleInstanceOnPodRestartFromCreation, MaxGroupRestarts: limit},
			Components:          []workloadsv1alpha2.RoleInstanceComponent{{Name: "worker", Size: ptr.To[int32](2), Template: v1.PodTemplateSpec{Spec: v1.PodSpec{Containers: []v1.Container{{Name: "worker", Image: "v1"}}}}}},
		},
		Status: workloadsv1alpha2.RoleInstanceStatus{ObservedGeneration: liveGeneration},
	}
	pods := make([]*v1.Pod, 2)
	objects := make([]client.Object, 0, 3)
	objects = append(objects, liveInstance)
	for i := range pods {
		pods[i] = &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("r560-0-worker-%d", i), Namespace: "default", UID: types.UID(fmt.Sprintf("r560-pod-%d", i)),
				Labels:          map[string]string{constants.RoleInstanceNameLabelKey: "r560-0", constants.ComponentNameLabelKey: "worker", constants.ComponentIDLabelKey: fmt.Sprint(i)},
				OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(liveInstance, workloadsv1alpha2.SchemeGroupVersion.WithKind("RoleInstance"))}},
			Status: v1.PodStatus{Phase: v1.PodRunning},
		}
		objects = append(objects, pods[i])
	}
	pods[0].Status.Phase = v1.PodFailed

	cli := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(liveInstance, &v1.Pod{}).WithObjects(objects...).Build()
	counter := &countingReader{Reader: cli}
	cached := liveInstance.DeepCopy()
	cached.Generation = cachedGeneration
	control := &realControl{Client: cli, apiReader: counter, recorder: record.NewFakeRecorder(100), bindings: NewNodeBindingStore()}
	control.ClearRestarting(cached)
	t.Cleanup(func() { control.ClearRestarting(cached) })
	return control, cached, counter
}

// countingReader wraps the fake client as an "uncached" APIReader and counts reads.
type countingReader struct {
	client.Reader
	gets  int
	lists int
}

func (r *countingReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	r.gets++
	return r.Reader.Get(ctx, key, obj, opts...)
}

func (r *countingReader) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	r.lists++
	return r.Reader.List(ctx, list, opts...)
}

// TestR560F4BudgetGenerationMismatchSurfacesAsBlockingError is a BUG-CANARY for
// finding F4: reconcileRestartBudget (driven from Scale) turns an informer-lag
// generation mismatch into a blocking error on the whole Scale call, which the
// reconciler surfaces as a FailedScale condition and an errored reconcile.
// At the PR head this test PASSES (the behavior is present). If the mismatch is
// instead handled as a benign requeue/skip, this canary must be inverted.
func TestR560F4BudgetGenerationMismatchSurfacesAsBlockingError(t *testing.T) {
	ctx := context.Background()
	// Cached (informer) view is one generation behind the live object.
	c, cached, _ := r560Fixture(t, 1, 2, ptr.To[int32](3))
	blocked, err := c.reconcileRestartBudget(ctx, cached)
	require.Error(t, err, "F4 canary: generation mismatch must currently surface as an error")
	require.Contains(t, err.Error(), "identity or generation changed")
	require.True(t, blocked)
}

// TestR560F2UncachedReadAmplificationPerScale is a BUG-CANARY for finding F2:
// a single Scale call on a budgeted recreate-policy instance with an eligible
// fault performs several uncached (APIReader) round trips even though the same
// information was just fetched. The exact count is not the contract; the canary
// documents that >1 uncached GET of the same RoleInstance happens per Scale.
// If the budget lifecycle is deduplicated to a single fresh read, tighten or
// invert this test.
func TestR560F2UncachedReadAmplificationPerScale(t *testing.T) {
	ctx := context.Background()
	c, cached, counter := r560Fixture(t, 1, 1, ptr.To[int32](3))
	// Pods known to the reconciler: reuse the live ones from the fake client.
	pods := make([]*v1.Pod, 2)
	for i := range pods {
		pods[i] = &v1.Pod{}
		require.NoError(t, c.Client.Get(ctx, client.ObjectKey{Namespace: "default", Name: fmt.Sprintf("r560-0-worker-%d", i)}, pods[i]))
	}
	pods[0].Status.Phase = v1.PodFailed

	_, _, err := c.Scale(ctx, cached, nil, nil, nil, pods, nil)
	require.NoError(t, err)
	require.Greater(t, counter.gets, 1,
		"F2 canary: expected more than one uncached RoleInstance read per Scale call (budget lifecycle re-reads); got %d GETs, %d LISTs", counter.gets, counter.lists)
	require.GreaterOrEqual(t, counter.lists, 1)
	t.Logf("F2 evidence: one Scale call on a budgeted instance issued %d uncached GETs and %d uncached LISTs", counter.gets, counter.lists)
}
