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

// Verification harness for sgl-project/rbg PR #512 (reviewer: codex).
// Additive only: no production code is modified by this file.
//
// These tests intentionally avoid referencing hasOrphanPod / getExistingPod so
// the SAME file compiles on the base branch and on the PR head, which is what
// makes the premise (P0) and the fix decidable by a single test run:
//
//   base (a0b11e6): TestVerifyForeignSameNamePodYieldsRetrySignal FAILS
//                   (Scale returns (false, 0, nil) -> no retry -> issue #511)
//   head (a8ef35f): the same test PASSES (Scale returns an error ->
//                   controller-runtime retries with backoff)
//
// See docs/verification/roleinstance-orphan-pod-retry/README.md.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	instanceutil "sigs.k8s.io/rbgs/pkg/reconciler/roleinstance/utils"
)

func verifyScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))
	return scheme
}

func verifyInstance(name, ns string, uid types.UID, size int32, templateType constants.RoleTemplateType) *workloadsv1alpha2.RoleInstance {
	labels := map[string]string{}
	if templateType != "" {
		labels[constants.RoleTypeLabelKey] = string(templateType)
	}
	return &workloadsv1alpha2.RoleInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			UID:       uid,
			Labels:    labels,
		},
		Spec: workloadsv1alpha2.RoleInstanceSpec{
			Components: []workloadsv1alpha2.RoleInstanceComponent{{
				Name: "role",
				Size: ptr.To(int32(size)),
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{{Name: "main", Image: "rbg:latest"}},
					},
				},
			}},
		},
	}
}

func verifyForeignPod(name, ns string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			UID:       "foreign-pod-uid",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "apps/v1",
				Kind:       "StatefulSet",
				Name:       "legacy-role",
				UID:        "old-statefulset-uid",
				Controller: ptr.To(true),
			}},
		},
	}
}

func verifyControl(scheme *runtime.Scheme, objs ...client.Object) (*realControl, client.Client) {
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	return &realControl{Client: fakeClient, recorder: record.NewFakeRecorder(16)}, fakeClient
}

func verifyRevision() *apps.ControllerRevision {
	return &apps.ControllerRevision{ObjectMeta: metav1.ObjectMeta{Name: "revision-1"}}
}

// TestVerifyForeignSameNamePodYieldsRetrySignal is the CONTRACT test for the
// premise and the fix (P0 / F-fix): when a same-name Pod is owned by a
// different controller (the StatefulSet -> RoleInstanceSet migration GC
// window), Scale must surface an error so controller-runtime retries, and must
// create the replacement once the foreign Pod is gone.
//
// Polarity: contract. FAILS on the base branch (premise reproduction),
// PASSES on the PR head (fix verified).
func TestVerifyForeignSameNamePodYieldsRetrySignal(t *testing.T) {
	ctx := context.Background()
	scheme := verifyScheme(t)

	instance := verifyInstance("migrate-role-0", "default", "new-ri-uid", 1, constants.PodTemplateTemplateType)
	foreign := verifyForeignPod("migrate-role-0", "default")

	ctrl, fakeClient := verifyControl(scheme, foreign)
	rev := verifyRevision()

	scaling, requeue, err := ctrl.Scale(ctx, instance, rev, rev, nil, nil, nil)
	assert.ErrorContains(t, err, "is not controlled by RoleInstance",
		"a foreign-owned same-name Pod must yield an error so controller-runtime retries (issue #511)")
	assert.False(t, scaling)
	assert.Zero(t, requeue, "no fixed requeue interval; the error return drives exponential backoff")

	podList := &corev1.PodList{}
	require.NoError(t, fakeClient.List(ctx, podList, client.InNamespace("default")))
	require.Len(t, podList.Items, 1)
	assert.Equal(t, types.UID("foreign-pod-uid"), podList.Items[0].UID,
		"the foreign Pod must not be adopted, deleted, or replaced while it exists")

	// After GC removes the foreign Pod, the retried reconcile must create the
	// replacement Pod owned by the new RoleInstance.
	require.NoError(t, fakeClient.Delete(ctx, foreign))
	scaling, requeue, err = ctrl.Scale(ctx, instance, rev, rev, nil, nil, nil)
	assert.NoError(t, err)
	assert.True(t, scaling)
	assert.Zero(t, requeue)

	require.NoError(t, fakeClient.List(ctx, podList, client.InNamespace("default")))
	require.Len(t, podList.Items, 1, "exactly one replacement Pod after the foreign Pod is gone")
	assert.Equal(t, "migrate-role-0", podList.Items[0].Name)
	assert.True(t, metav1.IsControlledBy(&podList.Items[0], instance),
		"replacement Pod must be controlled by the new RoleInstance")
}

// TestVerifyOwnedSameNamePodIsSkippedWithoutError is a regression CONTRACT: a
// same-name Pod already owned by THIS RoleInstance (e.g. observed twice due to
// cache lag) must be skipped quietly, not reported as an error.
func TestVerifyOwnedSameNamePodIsSkippedWithoutError(t *testing.T) {
	ctx := context.Background()
	scheme := verifyScheme(t)

	instance := verifyInstance("owned-ri-0", "default", "ri-uid-1", 1, constants.PodTemplateTemplateType)
	owned := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "owned-ri-0",
			Namespace: "default",
			UID:       "owned-pod-uid",
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(instance, instanceutil.ControllerKind),
			},
		},
	}

	ctrl, fakeClient := verifyControl(scheme, owned)
	rev := verifyRevision()

	_, _, err := ctrl.Scale(ctx, instance, rev, rev, nil, nil, nil)
	assert.NoError(t, err, "an already-owned same-name Pod must not produce a retry storm")

	podList := &corev1.PodList{}
	require.NoError(t, fakeClient.List(ctx, podList, client.InNamespace("default")))
	assert.Len(t, podList.Items, 1, "no duplicate Pod may be created")
	assert.Equal(t, types.UID("owned-pod-uid"), podList.Items[0].UID)
}

// TestVerifyOwnedTerminatingPodIsSkippedWithoutError is a regression CONTRACT:
// a same-name Pod owned by this RoleInstance that is still terminating must
// not produce an error either — its (owned) deletion event re-triggers the
// reconcile, so no error-driven retry is needed.
func TestVerifyOwnedTerminatingPodIsSkippedWithoutError(t *testing.T) {
	ctx := context.Background()
	scheme := verifyScheme(t)

	instance := verifyInstance("term-ri-0", "default", "ri-uid-2", 1, constants.PodTemplateTemplateType)
	terminating := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "term-ri-0",
			Namespace:  "default",
			UID:        "terminating-pod-uid",
			Finalizers: []string{"verify/hold"},
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(instance, instanceutil.ControllerKind),
			},
		},
	}

	ctrl, fakeClient := verifyControl(scheme, terminating)
	rev := verifyRevision()

	// Put the Pod into Terminating state.
	require.NoError(t, fakeClient.Delete(ctx, terminating))

	_, _, err := ctrl.Scale(ctx, instance, rev, rev, nil, nil, nil)
	assert.NoError(t, err, "an owned terminating Pod must be awaited via its deletion event, not via error retry")

	podList := &corev1.PodList{}
	require.NoError(t, fakeClient.List(ctx, podList, client.InNamespace("default")))
	assert.Len(t, podList.Items, 1, "the terminating Pod must remain untouched")
	assert.Equal(t, types.UID("terminating-pod-uid"), podList.Items[0].UID)
}

// TestVerifyPartialForeignCollisionBlocksWholeBatch is a CANARY documenting a
// behavior change of PR #512 that is NOT obviously correct: with a size-2
// component where only ONE ordinal collides with a foreign Pod, the head code
// aborts the entire batch (no sibling is created) and returns an error, while
// the base code created the non-colliding sibling and silently skipped the
// colliding one.
//
// Polarity: canary. It asserts the CURRENT head behavior; if the maintainers
// prefer "create what is creatable, still return the collision error", flip
// this test's expectations.
func TestVerifyPartialForeignCollisionBlocksWholeBatch(t *testing.T) {
	ctx := context.Background()
	scheme := verifyScheme(t)

	instance := verifyInstance("partial-ri", "default", "ri-uid-3", 2, constants.ComponentsTemplateType)
	// Pod names for ComponentsTemplateType: <instance>-<component>-<id>.
	foreign := verifyForeignPod("partial-ri-role-0", "default")

	ctrl, fakeClient := verifyControl(scheme, foreign)
	rev := verifyRevision()

	_, _, err := ctrl.Scale(ctx, instance, rev, rev, nil, nil, nil)
	assert.ErrorContains(t, err, "is not controlled by RoleInstance")

	podList := &corev1.PodList{}
	require.NoError(t, fakeClient.List(ctx, podList, client.InNamespace("default")))
	created := 0
	for _, p := range podList.Items {
		if p.UID != "foreign-pod-uid" {
			created++
		}
	}
	assert.Zero(t, created,
		"head behavior: a single foreign name collision aborts creation of the whole batch (base created the siblings)")
}
