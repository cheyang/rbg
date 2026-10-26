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

// Reviewer verification harness for PR #512 (Reviewer A, Claude).
//
// Contract tests for the orphan/foreign-pod retry behavior in createPods.
// PR #512 replaced hasOrphanPod's silent skip with an error return so that
// controller-runtime retries with exponential backoff.
//
// TEST POLARITY (contract): each test asserts the INTENDED behavior and must
// PASS on the PR head. On the merge-base (a0b11e6) the non-gang foreign-pod
// path returned (false, nil) — no error, no requeue — so
// TestOrphanPodContractForeignOwner, TestOrphanPodContractBarePodCollision and
// TestOrphanPodContractMultiComponentAbort FAIL there: that failure is the
// reproduction of issue #511 at the unit layer.
// TestOrphanPodContractGangStillErrors and TestOrphanPodContractOwnedPodSkipped
// pass on both head and base and act as controls / regression guards.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
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

func newOrphanContractInstance(name string, templateType constants.RoleTemplateType, components []workloadsv1alpha2.RoleInstanceComponent) *workloadsv1alpha2.RoleInstance {
	return &workloadsv1alpha2.RoleInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			UID:       "new-role-instance",
			Labels: map[string]string{
				constants.RoleTypeLabelKey: string(templateType),
			},
		},
		Spec: workloadsv1alpha2.RoleInstanceSpec{
			Components: components,
		},
	}
}

func orphanContractComponent(name string) workloadsv1alpha2.RoleInstanceComponent {
	return workloadsv1alpha2.RoleInstanceComponent{
		Name: name,
		Size: ptr.To(int32(1)),
		Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "main", Image: "rbg:latest"}},
			},
		},
	}
}

func foreignOwnedPod(name, namespace string, ownerUID string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "apps/v1",
				Kind:       "StatefulSet",
				Name:       "legacy-set",
				UID:        types.UID(ownerUID),
				Controller: ptr.To(true),
			}},
		},
	}
}

func newOrphanContractControl(objects ...client.Object) (*realControl, client.Client) {
	fakeClient := fake.NewClientBuilder().
		WithScheme(orphanContractScheme()).
		WithObjects(objects...).
		Build()
	return &realControl{
		Client:   fakeClient,
		recorder: record.NewFakeRecorder(10),
	}, fakeClient
}

func orphanContractScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		panic(err)
	}
	if err := workloadsv1alpha2.AddToScheme(scheme); err != nil {
		panic(err)
	}
	if err := apps.AddToScheme(scheme); err != nil {
		panic(err)
	}
	return scheme
}

// P0 premise: a same-name Pod controlled by another workload (the deleted
// StatefulSet of a migration) must surface as a Scale error, which is the only
// signal that schedules the retry reconcile. On the merge-base this returns
// (false, nil) and the RoleInstance stalls until a manual reconcile — the bug
// reported in issue #511.
func TestOrphanPodContractForeignOwner(t *testing.T) {
	ctx := context.Background()
	instance := newOrphanContractInstance("role-0", constants.PodTemplateTemplateType,
		[]workloadsv1alpha2.RoleInstanceComponent{orphanContractComponent("role")})

	ctrl, fakeClient := newOrphanContractControl(foreignOwnedPod("role-0", "default", "old-statefulset"))
	revision := &apps.ControllerRevision{ObjectMeta: metav1.ObjectMeta{Name: "revision-1"}}

	scaling, requeue, err := ctrl.Scale(ctx, instance, revision, revision, nil, nil, nil)
	assert.ErrorContains(t, err, "is not controlled by RoleInstance")
	assert.False(t, scaling)
	assert.Zero(t, requeue,
		"controller-runtime supplies exponential backoff for the returned error; no fixed requeue interval is needed")

	podList := &corev1.PodList{}
	assert.NoError(t, fakeClient.List(ctx, podList, client.InNamespace("default")))
	assert.Len(t, podList.Items, 1, "the foreign same-name Pod must not be adopted, replaced, or deleted")

	// After GC removes the foreign Pod, the next reconcile must create the
	// replacement Pod owned by the new RoleInstance.
	assert.NoError(t, fakeClient.Delete(ctx, foreignOwnedPod("role-0", "default", "old-statefulset")))
	scaling, requeue, err = ctrl.Scale(ctx, instance, revision, revision, nil, nil, nil)
	assert.NoError(t, err)
	assert.True(t, scaling)
	assert.Zero(t, requeue)

	assert.NoError(t, fakeClient.List(ctx, podList, client.InNamespace("default")))
	assert.Len(t, podList.Items, 1)
	assert.Equal(t, "role-0", podList.Items[0].Name)
	assert.True(t, metav1.IsControlledBy(&podList.Items[0], instance))
}

// A Pod already controlled by this RoleInstance must be skipped without error
// (no duplicate creation attempt, no spurious retry). Regression guard that
// passes on both base and head.
func TestOrphanPodContractOwnedPodSkipped(t *testing.T) {
	ctx := context.Background()
	instance := newOrphanContractInstance("role-0", constants.PodTemplateTemplateType,
		[]workloadsv1alpha2.RoleInstanceComponent{orphanContractComponent("role")})

	ownedPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "role-0",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: workloadsv1alpha2.GroupVersion.String(),
				Kind:       "RoleInstance",
				Name:       instance.Name,
				UID:        instance.UID,
				Controller: ptr.To(true),
			}},
		},
	}

	ctrl, fakeClient := newOrphanContractControl(ownedPod)
	revision := &apps.ControllerRevision{ObjectMeta: metav1.ObjectMeta{Name: "revision-1"}}

	scaling, requeue, err := ctrl.Scale(ctx, instance, revision, revision, nil, nil, nil)
	assert.NoError(t, err)
	assert.False(t, scaling, "nothing to create: the owned Pod already exists")
	assert.Zero(t, requeue)

	podList := &corev1.PodList{}
	assert.NoError(t, fakeClient.List(ctx, podList, client.InNamespace("default")))
	assert.Len(t, podList.Items, 1, "no duplicate Pod may be created for an already-owned name")
}

// A Pod with no controller ownerRef (a bare, manually created Pod squatting on
// the stable name) is a permanent collision: it must also surface as a Scale
// error so the FailedScale condition and the retry loop make it visible.
// On base the non-gang path silently skipped it.
func TestOrphanPodContractBarePodCollision(t *testing.T) {
	ctx := context.Background()
	instance := newOrphanContractInstance("role-0", constants.PodTemplateTemplateType,
		[]workloadsv1alpha2.RoleInstanceComponent{orphanContractComponent("role")})

	barePod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "role-0", Namespace: "default"},
	}

	ctrl, _ := newOrphanContractControl(barePod)
	revision := &apps.ControllerRevision{ObjectMeta: metav1.ObjectMeta{Name: "revision-1"}}

	_, _, err := ctrl.Scale(ctx, instance, revision, revision, nil, nil, nil)
	assert.ErrorContains(t, err, "is not controlled by RoleInstance",
		"a controller-less same-name Pod is a permanent collision and must be reported, not silently skipped")
}

// Gang scheduling previously had its own orphan error; the unified check must
// keep returning an error for that path. Passes on both base and head (control).
func TestOrphanPodContractGangStillErrors(t *testing.T) {
	ctx := context.Background()
	instance := newOrphanContractInstance("role-0", constants.PodTemplateTemplateType,
		[]workloadsv1alpha2.RoleInstanceComponent{orphanContractComponent("role")})
	instance.Annotations = map[string]string{constants.RoleInstanceGangSchedulingAnnotationKey: "true"}

	ctrl, _ := newOrphanContractControl(foreignOwnedPod("role-0", "default", "old-statefulset"))
	revision := &apps.ControllerRevision{ObjectMeta: metav1.ObjectMeta{Name: "revision-1"}}

	_, _, err := ctrl.Scale(ctx, instance, revision, revision, nil, nil, nil)
	assert.Error(t, err)
}

// Behavior-change contract (documents PR #512's new abort semantics): when one
// component's stable Pod name collides with a foreign Pod, creation of the
// OTHER components' Pods is aborted too and retried on the next reconcile.
// On base, the sibling Pod was created immediately while the colliding name
// was skipped. The new all-or-nothing behavior is intentional (the PR's fix
// returns on the first collision); this test pins it so any future change is
// deliberate.
func TestOrphanPodContractMultiComponentAbort(t *testing.T) {
	ctx := context.Background()
	instance := newOrphanContractInstance("multi-0", constants.ComponentsTemplateType,
		[]workloadsv1alpha2.RoleInstanceComponent{orphanContractComponent("c1"), orphanContractComponent("c2")})

	// Foreign Pod squats on multi-0-c1-0 only; multi-0-c2-0 is free.
	ctrl, fakeClient := newOrphanContractControl(foreignOwnedPod("multi-0-c1-0", "default", "old-statefulset"))
	revision := &apps.ControllerRevision{ObjectMeta: metav1.ObjectMeta{Name: "revision-1"}}

	_, _, err := ctrl.Scale(ctx, instance, revision, revision, nil, nil, nil)
	assert.ErrorContains(t, err, "multi-0-c1-0 is not controlled by RoleInstance")

	podList := &corev1.PodList{}
	assert.NoError(t, fakeClient.List(ctx, podList, client.InNamespace("default")))
	names := []string{}
	for _, p := range podList.Items {
		names = append(names, p.Name)
	}
	assert.NotContains(t, names, "multi-0-c2-0",
		"head aborts sibling creation until the collision resolves (retry creates it later)")

	// Once the collision is gone, both components' Pods are created.
	assert.NoError(t, fakeClient.Delete(ctx, foreignOwnedPod("multi-0-c1-0", "default", "old-statefulset")))
	scaling, _, err := ctrl.Scale(ctx, instance, revision, revision, nil, nil, nil)
	assert.NoError(t, err)
	assert.True(t, scaling)
	assert.NoError(t, fakeClient.List(ctx, podList, client.InNamespace("default")))
	names = []string{}
	for _, p := range podList.Items {
		names = append(names, p.Name)
	}
	assert.Contains(t, names, "multi-0-c1-0")
	assert.Contains(t, names, "multi-0-c2-0")
}
