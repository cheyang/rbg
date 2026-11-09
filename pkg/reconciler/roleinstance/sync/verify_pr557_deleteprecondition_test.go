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

// Verification harness for PR #557 (reviewer: codex). Additive test file.
//
// F-B, bug-canary polarity: deletePods now issues every Delete with UID +
// ResourceVersion preconditions. On the restart path the pods are re-read
// fresh, but on the non-restart paths (scale-in, gang-recreate, Failed-pod
// cleanup) the pod objects come from the informer snapshot and can be stale.
// A stale resourceVersion makes the API server reject the Delete with 409
// Conflict; the reconcile then errors, emits a FailedDelete event, and retries
// on the next informer sync. This test pins that behavior at the smallest
// layer that enforces preconditions.

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

func TestPR557StaleSnapshotDeleteConflicts(t *testing.T) {
	instance := &workloadsv1alpha2.RoleInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name: "scalein-0", Namespace: "default", UID: types.UID("scalein-instance"), Generation: 1,
			Labels: map[string]string{constants.RoleTypeLabelKey: string(constants.ComponentsTemplateType)},
		},
		Spec: workloadsv1alpha2.RoleInstanceSpec{
			RestartPolicyConfig: &workloadsv1alpha2.RestartPolicyConfig{Type: workloadsv1alpha2.RestartPolicyNone},
			Components: []workloadsv1alpha2.RoleInstanceComponent{{
				Name: "worker", Size: ptr.To(int32(1)),
				Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "main", Image: "rbg:latest"}},
				}},
			}},
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "scalein-0-worker-1", Namespace: "default", UID: types.UID("excess-pod"),
			Labels: map[string]string{
				constants.ComponentNameLabelKey:    "worker",
				constants.ComponentIDLabelKey:      "1",
				constants.RoleInstanceNameLabelKey: instance.Name,
			},
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(instance, workloadsv1alpha2.GroupVersion.WithKind("RoleInstance"))},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := workloadsv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(instance, pod).Build()
	ctx := context.Background()

	// Simulate informer staleness: the pod object changes (e.g. a kubelet
	// status update bumps resourceVersion) after the snapshot was taken.
	stale := &corev1.Pod{}
	if err := c.Get(ctx, client.ObjectKeyFromObject(pod), stale); err != nil {
		t.Fatal(err)
	}
	live := stale.DeepCopy()
	live.Labels["kubelet"] = "status-write"
	if err := c.Update(ctx, live); err != nil {
		t.Fatal(err)
	}
	if stale.ResourceVersion == live.ResourceVersion {
		t.Skip("fake client did not bump resourceVersion; precondition not exercised")
	}

	ctrl := &realControl{Client: c, apiReader: c, recorder: record.NewFakeRecorder(10)}
	// Non-restart path: instance has no retiring list, podsToDelete carries the
	// stale informer object (scale-in shape).
	_, err := ctrl.deletePods(ctx, instance.DeepCopy(), []*corev1.Pod{stale})
	if err == nil {
		t.Fatalf("bug-canary: stale-snapshot delete succeeded without preconditions (pre-PR behavior)")
	}
	if !apierrors.IsConflict(err) {
		t.Fatalf("expected a 409 Conflict from the resourceVersion precondition, got: %v", err)
	}
	t.Logf("observed PR-head behavior: stale informer pod delete rejected with conflict: %v", err)
}
