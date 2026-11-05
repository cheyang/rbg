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

package reconciler

// Reviewer verification harness for PR #553 / issue #547.
//
// Claim P0 (premise): on the base branch, a StatefulSet whose spec.replicas
// sits far below role.Replicas (role-size annotation unchanged, rollout not
// completed, no template/revision change, so stsUpdated=false) drives case 6
// of rollingUpdateParameters into rollingUpdatePartition with a partition
// index larger than len(states), causing an index-out-of-range panic.
//
// Claim F1 (fix): on the PR head, the same inputs must not panic; the
// controller must scale the StatefulSet back to role.Replicas.
//
// All tests below are CONTRACT tests (they assert intended correct behavior):
// they FAIL on the base branch (panic / wrong result) and PASS on the fixed
// head, except the negative control, which must pass on both. Layers: L1 =
// pure function, L2 = fake-client integration.

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/rbgs/api/workloads/constants"
)

// TestVerify_L1_RollingUpdatePartition_NoPanicWhenLiveBelowDesired is the
// issue #547 repro, verbatim numbers: desired 4, maxUnavailable 1, maxSurge 1,
// live 1, no continuously ready replica -> rollingStep -2, index 3, len(states) 1.
func TestVerify_L1_RollingUpdatePartition_NoPanicWhenLiveBelowDesired(t *testing.T) {
	got := rollingUpdatePartition(context.Background(), make([]replicaState, 1), 1, -2, 0)
	assert.Equal(t, int32(0), got)

	// A live count of 0 previously panicked at states[0].
	got = rollingUpdatePartition(context.Background(), nil, 0, -3, 2)
	assert.Equal(t, int32(0), got)
}

// TestVerify_L2_RollingUpdateParameters_LiveBelowDesired_NoPanic walks the
// full parameter computation for the #547 fixture through a fake client:
// role replicas 4, live spec.replicas 1, role-size annotation "4",
// partition 2, rollout not completed, no template change (stsUpdated=false).
func TestVerify_L2_RollingUpdateParameters_LiveBelowDesired_NoPanic(t *testing.T) {
	scheme := newRolloutErrScheme(t)
	rbg, role := newRolloutErrRBG(intstr.FromInt32(0))
	selector := rbg.GetCommonLabelsFromRole(role)
	revision := &appsv1.ControllerRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "rev-1", Namespace: "default", Labels: selector},
		Revision:   1,
	}
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:        rbg.GetWorkloadName(role),
			Namespace:   "default",
			UID:         "sts-uid",
			Annotations: map[string]string{constants.RoleSizeAnnotationKey: "4"},
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: ptr.To(int32(1)),
			Selector: &metav1.LabelSelector{MatchLabels: selector},
			UpdateStrategy: appsv1.StatefulSetUpdateStrategy{
				Type:          appsv1.RollingUpdateStatefulSetStrategyType,
				RollingUpdate: &appsv1.RollingUpdateStatefulSetStrategy{Partition: ptr.To(int32(2))},
			},
		},
	}
	r := NewStatefulSetReconciler(
		scheme, fake.NewClientBuilder().WithScheme(scheme).WithObjects(sts, revision).Build())

	partition, replicas, err := r.rollingUpdateParameters(context.Background(), role, sts, false, nil)
	require.NoError(t, err)
	assert.Equal(t, int32(4), replicas, "must scale the workload back to the desired count")
	assert.Equal(t, int32(1), partition, "partition must be clamped to the live replica count")
}

// TestVerify_L2_ReconcileStatefulSet_LiveBelowDesired_ScalesUp is the
// user-visible contract: reconcile a role whose live StatefulSet was scaled
// externally below the desired count; the reconcile must not panic and the
// StatefulSet must end up back at role.Replicas.
func TestVerify_L2_ReconcileStatefulSet_LiveBelowDesired_ScalesUp(t *testing.T) {
	scheme := newRolloutErrScheme(t)
	rbg, role := newRolloutErrRBG(intstr.FromInt32(0))
	ctx := context.Background()
	const revisionKey = "rev-1"

	// Seed a live STS that is semantically equal to what the controller would
	// build (so stsUpdated=false), then externally scale it below the desired
	// count with a mid-rollout partition.
	seeder := NewStatefulSetReconciler(scheme, fake.NewClientBuilder().WithScheme(scheme).Build())
	applyCfg, err := seeder.constructStatefulSetApplyConfiguration(ctx, rbg, role, &appsv1.StatefulSet{}, revisionKey)
	require.NoError(t, err)
	u, err := runtime.DefaultUnstructuredConverter.ToUnstructured(applyCfg)
	require.NoError(t, err)
	live := &appsv1.StatefulSet{}
	require.NoError(t, runtime.DefaultUnstructuredConverter.FromUnstructured(u, live))
	live.UID = "sts-uid"
	live.Spec.Replicas = ptr.To(int32(1))
	live.Spec.UpdateStrategy = appsv1.StatefulSetUpdateStrategy{
		Type:          appsv1.RollingUpdateStatefulSetStrategyType,
		RollingUpdate: &appsv1.RollingUpdateStatefulSetStrategy{Partition: ptr.To(int32(2))},
	}
	revision := &appsv1.ControllerRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name: revisionKey, Namespace: "default", Labels: live.Spec.Selector.MatchLabels,
		},
		Revision: 1,
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(live, revision).Build()
	err = NewStatefulSetReconciler(scheme, c).reconcileStatefulSet(ctx, rbg, role, nil, revisionKey)
	require.NoError(t, err, "reconcile must not fail or panic on a shrunken live StatefulSet")

	got := &appsv1.StatefulSet{}
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: live.Name, Namespace: live.Namespace}, got))
	assert.Equal(t, int32(4), *got.Spec.Replicas, "live StatefulSet must be scaled back to the desired count")
}

// TestVerify_L2_NegativeControl_NormalCase6Unchanged guards against the fix
// changing the normal in-flight rollout computation: a mid-rollout
// StatefulSet at exactly the desired replica count must compute identical
// parameters on base and head. Contract: expected values were captured from
// the BASE branch behavior (unchanged path, not a bug).
func TestVerify_L2_NegativeControl_NormalCase6Unchanged(t *testing.T) {
	scheme := newRolloutErrScheme(t)
	rbg, role := newRolloutErrRBG(intstr.FromInt32(0))
	selector := rbg.GetCommonLabelsFromRole(role)
	revision := &appsv1.ControllerRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "rev-1", Namespace: "default", Labels: selector},
		Revision:   1,
	}
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:        rbg.GetWorkloadName(role),
			Namespace:   "default",
			UID:         "sts-uid",
			Annotations: map[string]string{constants.RoleSizeAnnotationKey: "4"},
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: ptr.To(int32(4)),
			Selector: &metav1.LabelSelector{MatchLabels: selector},
			UpdateStrategy: appsv1.StatefulSetUpdateStrategy{
				Type:          appsv1.RollingUpdateStatefulSetStrategyType,
				RollingUpdate: &appsv1.RollingUpdateStatefulSetStrategy{Partition: ptr.To(int32(3))},
			},
		},
	}

	// Pods 0 and 1 are not ready; pods 2 and 3 are ready and updated.
	objects := []client.Object{sts, revision}
	for idx := 0; idx < 4; idx++ {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      rbg.GetWorkloadName(role) + "-" + strconv.Itoa(idx),
				Namespace: "default",
				Labels: mergeLabels(selector, map[string]string{
					"controller-revision-hash":     "rev-1",
					"apps.kubernetes.io/pod-index": strconv.Itoa(idx),
				}),
			},
		}
		if idx >= 2 {
			pod.Status = corev1.PodStatus{
				Phase: corev1.PodRunning,
				Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
			}
		}
		objects = append(objects, pod)
	}

	r := NewStatefulSetReconciler(
		scheme, fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build())
	partition, replicas, err := r.rollingUpdateParameters(context.Background(), role, sts, false, nil)
	require.NoError(t, err)
	// Two unready replicas > maxSurge(1): burst to 5. Partition: continuous
	// ready from tail = 2, rollingStep = 1 + 1 - (5-4) = 1,
	// rollingStepPartition = 4-2-1 = 1, unavailable(states[0]) = 1,
	// partition = 2 then reduced to 1 (states[1] not ready breaks at 1).
	assert.Equal(t, int32(5), replicas)
	assert.Equal(t, int32(1), partition)
}
