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
// controller must scale the StatefulSet back to at least role.Replicas.
//
// Round 2 (head c768d5cd, "restore surge when scaling back an in-flight
// rollout") deliberately changed the recovery contract to fix the F7 stall:
// the partition is now kept unchanged (no longer clamped to the live count)
// and replicas include surge (wantReplicas(roleUnreadyReplicas)) while a
// rollout is in flight (partition > 0); an idle scale-up (partition 0) still
// returns exactly role.Replicas. The L2 expectations below pin the NEW
// contract; on the previous head b28c5478 they observe the old (1,4) values
// and fail — that red/green pair is the differential evidence for the change.
//
// All tests below are CONTRACT tests (they assert intended correct behavior):
// they FAIL on the base branch (panic / wrong result) and PASS on the fixed
// head, except the negative control, which must pass on both. Layers: L1 =
// pure function, L2 = fake-client integration.

import (
	"context"
	"fmt"
	"strconv"
	"strings"
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
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	wrappersv2 "sigs.k8s.io/rbgs/test/wrappers/v1alpha2"
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

	// New contract (c768d5cd): a rollout is in flight (partition 2 > 0), so the
	// partition is kept and surge is restored — live 1 with 4 desired and 1
	// maxSurge bursts to 5 while the partition stays at 2. Never below desired.
	partition, replicas, err := r.rollingUpdateParameters(context.Background(), role, sts, false, nil)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, replicas, int32(4), "must scale the workload back to at least the desired count")
	assert.Equal(t, int32(5), replicas, "in-flight rollout restores surge (wantReplicas)")
	assert.Equal(t, int32(2), partition, "partition must be kept unchanged during drift recovery")
}

// TestVerify_L2_IdleScaleUp_ReturnsDesiredCount pins the idle half of the
// round-2 contract: with no rollout in flight (partition 0), a shrunken live
// StatefulSet scales back to exactly role.Replicas with no surge.
func TestVerify_L2_IdleScaleUp_ReturnsDesiredCount(t *testing.T) {
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
				RollingUpdate: &appsv1.RollingUpdateStatefulSetStrategy{Partition: ptr.To(int32(0))},
			},
		},
	}
	r := NewStatefulSetReconciler(
		scheme, fake.NewClientBuilder().WithScheme(scheme).WithObjects(sts, revision).Build())

	partition, replicas, err := r.rollingUpdateParameters(context.Background(), role, sts, false, nil)
	require.NoError(t, err)
	assert.Equal(t, int32(4), replicas, "idle scale-up returns exactly the desired count")
	assert.Equal(t, int32(0), partition)
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
	// Round-2 contract: in-flight rollout (partition 2) -> apply restores surge
	// (5) and keeps the partition; never at or below the shrunken count.
	assert.Equal(t, int32(5), *got.Spec.Replicas, "live StatefulSet must be scaled back up with surge")
	assert.GreaterOrEqual(t, *got.Spec.Replicas, int32(4), "never left at or below the shrunken count")
	assert.Equal(t, int32(2), *got.Spec.UpdateStrategy.RollingUpdate.Partition, "partition kept during recovery")
}

// TestVerify_L2_F7_ConvergenceAfterExternalShrink pins the fix for the F7
// stall (Copilot C4483274801): with maxSurge=1 and an effective maxUnavailable
// of 0 (10% of 4 replicas), an external shrink mid-rollout previously left the
// rollout parked one ordinal short forever (the (partition 1, replicas 4)
// fixed point on the previous head b28c5478). The round-2 surge restoration
// must let the loop converge to partition 0 / replicas == role.Replicas.
//
// The test simulates the reconcile loop against a fully-converged world each
// iteration: every pod at ordinal < replicas exists, is Running/Ready, and
// carries the old revision below the partition and the highest revision at or
// above it (exactly what the StatefulSet controller converges to between
// reconciles). Contract: converge within 24 iterations, never scale below the
// desired count, partition monotone non-increasing, no panic.
func TestVerify_L2_F7_ConvergenceAfterExternalShrink(t *testing.T) {
	scheme := newRolloutErrScheme(t)
	rbg := wrappersv2.BuildBasicRoleBasedGroup("test-rbg", "default").Obj()
	rbg.Spec.Roles[0].Replicas = ptr.To(int32(4))
	rbg.Spec.Roles[0].RolloutStrategy = &workloadsv1alpha2.RolloutStrategy{
		Type: workloadsv1alpha2.RollingUpdateStrategyType,
		RollingUpdate: &workloadsv1alpha2.RollingUpdate{
			// 10% of 4 rounds down to 0: the corner that produced the stall.
			MaxUnavailable: ptr.To(intstr.FromString("10%")),
			MaxSurge:       ptr.To(intstr.FromInt32(1)),
			Partition:      ptr.To(intstr.FromInt32(0)),
		},
	}
	role := &rbg.Spec.Roles[0]
	selector := rbg.GetCommonLabelsFromRole(role)
	ctx := context.Background()

	const oldRev, newRev = "rev-old", "rev-new"
	revisions := []client.Object{
		&appsv1.ControllerRevision{
			ObjectMeta: metav1.ObjectMeta{Name: oldRev, Namespace: "default", Labels: selector},
			Revision:   1,
		},
		&appsv1.ControllerRevision{
			ObjectMeta: metav1.ObjectMeta{Name: newRev, Namespace: "default", Labels: selector},
			Revision:   2,
		},
	}

	// world builds the pod set the StatefulSet controller converges to for
	// (replicas, partition): pods below the partition on the old revision,
	// pods at/above it on the highest revision, all Running and Ready.
	world := func(replicas int32, partition int32) []client.Object {
		objs := append([]client.Object{}, revisions...)
		for idx := int32(0); idx < replicas; idx++ {
			hash := oldRev
			if idx >= partition {
				hash = newRev
			}
			objs = append(objs, &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      rbg.GetWorkloadName(role) + "-" + strconv.Itoa(int(idx)),
					Namespace: "default",
					Labels: mergeLabels(selector, map[string]string{
						"controller-revision-hash":     hash,
						"apps.kubernetes.io/pod-index": strconv.Itoa(int(idx)),
					}),
				},
				Status: corev1.PodStatus{
					Phase:      corev1.PodRunning,
					Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
				},
			})
		}
		return objs
	}

	// Start: mid-rollout (partition 2), externally shrunk to live 1.
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

	var trace []string
	converged := false
	for iter := 0; iter < 24; iter++ {
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(world(*sts.Spec.Replicas, *sts.Spec.UpdateStrategy.RollingUpdate.Partition)...).Build()
		partition, replicas, err := NewStatefulSetReconciler(scheme, c).rollingUpdateParameters(ctx, role, sts, false, nil)
		require.NoError(t, err, "iteration %d must not fail", iter)
		assert.GreaterOrEqual(t, replicas, int32(4), "iteration %d: never scale below the desired count", iter)
		assert.LessOrEqual(t, partition, *sts.Spec.UpdateStrategy.RollingUpdate.Partition,
			"iteration %d: partition must be monotone non-increasing", iter)
		trace = append(trace, fmt.Sprintf("iter %d: (%d, %d)", iter, partition, replicas))

		if partition == 0 && replicas == 4 && *sts.Spec.Replicas == 4 {
			// One more reconcile from the settled state must be stable.
			converged = true
			break
		}
		sts.Spec.Replicas = ptr.To(replicas)
		sts.Spec.UpdateStrategy.RollingUpdate.Partition = ptr.To(partition)
	}
	assert.True(t, converged, "rollout must converge to (partition 0, replicas 4); trace:\n%s", strings.Join(trace, "\n"))
	t.Logf("convergence trace:\n%s", strings.Join(trace, "\n"))
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
