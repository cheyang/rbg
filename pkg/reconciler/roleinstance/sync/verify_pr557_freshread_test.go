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
// F-A, bug-canary polarity: before this PR, calculateDiffsWithExpectation only
// read the RoleInstance fresh from the API server for instances using
// RecreateRoleInstanceOnPodRestart, and a read failure was non-fatal. The PR
// makes the fresh read unconditional and fatal for every instance, including
// restartPolicy=None instances that can never carry a retiring list. This test
// pins the new behavior so a later fix (gating the read on the policy or on a
// non-empty retiring list) flips it.

import (
	"context"
	"errors"
	"testing"

	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
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

type pr557ErrReader struct{ err error }

func (r pr557ErrReader) Get(context.Context, client.ObjectKey, client.Object, ...client.GetOption) error {
	return r.err
}
func (r pr557ErrReader) List(context.Context, client.ObjectList, ...client.ListOption) error {
	return r.err
}

func TestPR557FreshReadBlocksNonPolicyInstance(t *testing.T) {
	instance := &workloadsv1alpha2.RoleInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name: "plain-0", Namespace: "default", UID: types.UID("plain-instance"), Generation: 1,
			Labels: map[string]string{constants.RoleTypeLabelKey: string(constants.ComponentsTemplateType)},
		},
		Spec: workloadsv1alpha2.RoleInstanceSpec{
			// No restart policy: this instance can never use retiringPods.
			RestartPolicyConfig: &workloadsv1alpha2.RestartPolicyConfig{Type: workloadsv1alpha2.RestartPolicyNone},
			Components: []workloadsv1alpha2.RoleInstanceComponent{{
				Name: "worker", Size: ptr.To(int32(1)),
				Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "main", Image: "rbg:latest"}},
				}},
			}},
		},
		Status: workloadsv1alpha2.RoleInstanceStatus{
			ObservedGeneration: 1, CurrentRevision: "rev-1", UpdateRevision: "rev-1",
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "plain-0-worker-0", Namespace: "default", UID: types.UID("plain-pod-0"),
			Labels: map[string]string{
				constants.ComponentNameLabelKey:    "worker",
				constants.ComponentIDLabelKey:      "0",
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
	ctrl := &realControl{
		Client:    c,
		apiReader: pr557ErrReader{err: errors.New("simulated transient apiserver read failure")},
		recorder:  record.NewFakeRecorder(10),
	}
	rev := &apps.ControllerRevision{ObjectMeta: metav1.ObjectMeta{Name: "rev-1"}}
	_, err := ctrl.calculateDiffsWithExpectation(context.Background(), instance, rev, rev,
		[]*apps.ControllerRevision{rev}, []*corev1.Pod{pod}, nil)
	if err == nil {
		t.Fatalf("bug-canary: non-policy instance tolerated an apiserver read failure (pre-PR behavior)")
	}
	t.Logf("observed PR-head behavior: scaling blocked for restartPolicy=None instance: %v", err)
}
