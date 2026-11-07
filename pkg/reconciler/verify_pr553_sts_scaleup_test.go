/*
Verification harness for PR #553 (issue #547) — Reviewer B (Codex).
Additive only; does not modify production code.

Claim under test (contract polarity — FAILS on the buggy base, PASSES on the fix):
  When the live StatefulSet replica count sits far below the role's desired
  count (e.g. after `kubectl scale`), the rolling-update parameter computation
  must not panic and must scale the StatefulSet back to (at least) the desired
  count.

CONTRACT CHANGE (round 2, head c768d5cd, commit "restore surge when scaling
back an in-flight rollout"): the drift-recovery branch no longer clamps the
partition to the live count and no longer returns exactly roleReplicas. It now
KEEPS the live partition unchanged and, while a rollout is in flight
(partition > 0), returns wantReplicas(roleUnreadyReplicas) (surge restored) so
the case-6 fixed point found in round 1 (F7, Copilot C4483274801) cannot be
reached. Expectations below encode the round-2 contract:

  partition == 0 (no rollout in flight)  -> (0, roleReplicas)
  partition  > 0 (rollout in flight)     -> (partition, wantReplicas(unready))
*/

package reconciler

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	wrappersv2 "sigs.k8s.io/rbgs/test/wrappers/v1alpha2"
)

// L1 (pure function): rollingUpdatePartition must not index past len(states),
// no matter how large the computed rollingStepPartition is.
//
// Issue #547 inputs: desired 4, maxUnavailable 1, maxSurge 1, live 1 with no
// continuously ready replica => case 6 computes rollingStep=-2 and, on the
// base code, indexes states[1] and states[2] on a length-1 slice (panic).
func TestVerifyPR553RollingUpdatePartitionLiveFarBelowDesired(t *testing.T) {
	// Case A: live 1, rollingStep -2. Base: panic (index out of range).
	got := rollingUpdatePartition(context.Background(), make([]replicaState, 1), 1, -2, 0)
	if got != 0 {
		t.Fatalf("live=1 rollingStep=-2: expected partition 0, got %d", got)
	}

	// Case B: live 0 (scaled to zero), rollingStep -3, stale partition 2.
	// Base: panic at states[0]. Fix must clamp to the empty slice.
	got = rollingUpdatePartition(context.Background(), nil, 0, -3, 2)
	if got != 0 {
		t.Fatalf("live=0 rollingStep -3: expected partition 0, got %d", got)
	}
}

// L2 (fake client, real caller chain): rollingUpdateParameters must recover
// from a shrunken live StatefulSet without panicking in case 6.
// Round-2 contract (head c768d5cd): the partition is KEPT (not clamped), and
// replicas restore surge via wantReplicas(roleUnreadyReplicas) while a rollout
// is in flight (partition > 0); with partition == 0 the plain desired count is
// returned.
//
// Role: replicas 4, maxUnavailable 1, maxSurge 1 (same as issue #547), so
// burstReplicas = 5 and roleUnreadyReplicas for live 0/1 with no pods is 4
// (> maxSurge) => wantReplicas = 5.
func TestVerifyPR553RollingUpdateParametersScalesBackUp(t *testing.T) {
	cases := []struct {
		name          string
		liveReplicas  int32
		livePartition int32
		wantReplicas  int32
		wantPartition int32
	}{
		{name: "live 1 of 4, partition 2 (in-flight)", liveReplicas: 1, livePartition: 2, wantReplicas: 5, wantPartition: 2},
		{name: "live 0 of 4, partition 2 (in-flight)", liveReplicas: 0, livePartition: 2, wantReplicas: 5, wantPartition: 2},
		{name: "live 3 of 4, partition 0 (idle scale-up)", liveReplicas: 3, livePartition: 0, wantReplicas: 4, wantPartition: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
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
					Replicas: ptr.To(tc.liveReplicas),
					Selector: &metav1.LabelSelector{MatchLabels: selector},
					UpdateStrategy: appsv1.StatefulSetUpdateStrategy{
						Type:          appsv1.RollingUpdateStatefulSetStrategyType,
						RollingUpdate: &appsv1.RollingUpdateStatefulSetStrategy{Partition: ptr.To(tc.livePartition)},
					},
				},
			}
			r := NewStatefulSetReconciler(
				scheme, fake.NewClientBuilder().WithScheme(scheme).WithObjects(sts, revision).Build())

			partition, replicas, err := r.rollingUpdateParameters(context.Background(), role, sts, false, nil)
			if err != nil {
				t.Fatalf("rollingUpdateParameters returned error: %v", err)
			}
			if replicas != tc.wantReplicas {
				t.Fatalf("expected replicas=%d, got %d", tc.wantReplicas, replicas)
			}
			if partition != tc.wantPartition {
				t.Fatalf("expected partition=%d, got %d", tc.wantPartition, partition)
			}
		})
	}
}

// L2 convergence walk (contract polarity): the round-1 F7 scenario —
// maxUnavailable "10%" (scales to 0 for 4 replicas), maxSurge 1, mid-rollout
// (partition 2) externally shrunk to live 1 — must CONVERGE to partition 0 at
// the desired count, not stall one ordinal short.
//
// On the previous head (b28c5478) step 1 returned (1,4) and the follow-up
// reconciles sat at the (1,4) fixed point forever (verified in round 1). On
// base (98d3c4be) step 1 panics in rollingUpdatePartition. On the round-2 head
// (c768d5cd) step 1 must restore surge (5) and keep the partition (2), and the
// walk must reach (0,4) with all replicas updated.
func TestVerifyPR553InFlightRecoveryConvergesWithSurge(t *testing.T) {
	scheme := newRolloutErrScheme(t)
	rbg := wrappersv2.BuildBasicRoleBasedGroup("test-rbg", "default").Obj()
	rbg.Spec.Roles[0].Replicas = ptr.To(int32(4))
	rbg.Spec.Roles[0].RolloutStrategy = &workloadsv1alpha2.RolloutStrategy{
		Type: workloadsv1alpha2.RollingUpdateStrategyType,
		RollingUpdate: &workloadsv1alpha2.RollingUpdate{
			MaxUnavailable: ptr.To(intstr.FromString("10%")),
			MaxSurge:       ptr.To(intstr.FromInt32(1)),
			Partition:      ptr.To(intstr.FromInt32(0)),
		},
	}
	role := &rbg.Spec.Roles[0]
	selector := rbg.GetCommonLabelsFromRole(role)
	stsName := rbg.GetWorkloadName(role)

	oldRev := &appsv1.ControllerRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "oldRevision", Namespace: "default", Labels: selector}, Revision: 1}
	newRev := &appsv1.ControllerRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "newRevision", Namespace: "default", Labels: selector}, Revision: 2}

	mkPod := func(ordinal int, revision string) *corev1.Pod {
		l := map[string]string{}
		for k, v := range selector {
			l[k] = v
		}
		l["controller-revision-hash"] = revision
		l["apps.kubernetes.io/pod-index"] = string(rune('0' + ordinal))
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      stsName + "-" + string(rune('0'+ordinal)),
				Namespace: "default",
				Labels:    l,
			},
			Status: corev1.PodStatus{
				Phase:      corev1.PodRunning,
				Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
			},
		}
	}
	mkSts := func(replicas, partition int32) *appsv1.StatefulSet {
		return &appsv1.StatefulSet{
			ObjectMeta: metav1.ObjectMeta{
				Name: stsName, Namespace: "default", UID: "sts-uid",
				Annotations: map[string]string{constants.RoleSizeAnnotationKey: "4"},
			},
			Spec: appsv1.StatefulSetSpec{
				Replicas: ptr.To(replicas),
				Selector: &metav1.LabelSelector{MatchLabels: selector},
				UpdateStrategy: appsv1.StatefulSetUpdateStrategy{
					Type:          appsv1.RollingUpdateStatefulSetStrategyType,
					RollingUpdate: &appsv1.RollingUpdateStatefulSetStrategy{Partition: ptr.To(partition)},
				},
			},
		}
	}
	call := func(sts *appsv1.StatefulSet, pods ...*corev1.Pod) (int32, int32) {
		objs := []client.Object{sts, oldRev, newRev}
		for _, p := range pods {
			objs = append(objs, p)
		}
		r := NewStatefulSetReconciler(scheme, fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build())
		partition, replicas, err := r.rollingUpdateParameters(context.Background(), role, sts, false, nil)
		if err != nil {
			t.Fatalf("rollingUpdateParameters err: %v", err)
		}
		return partition, replicas
	}
	check := func(step string, sts *appsv1.StatefulSet, wantPartition, wantReplicas int32, pods ...*corev1.Pod) {
		p, r := call(sts, pods...)
		t.Logf("live=%d partition-in=%d -> partition=%d replicas=%d", *sts.Spec.Replicas, *sts.Spec.UpdateStrategy.RollingUpdate.Partition, p, r)
		if p != wantPartition || r != wantReplicas {
			t.Fatalf("%s: expected (%d,%d), got (%d,%d)", step, wantPartition, wantReplicas, p, r)
		}
	}

	// Step 1 (recovery decision): live 1 (pod 0 old+ready), sts partition 2.
	// Round-2 head must keep the partition and restore surge => (2,5).
	// Previous head returned (1,4) here, which led to the stall; base panics.
	check("step1 recovery", mkSts(1, 2), 2, 5, mkPod(0, "oldRevision"))

	// Step 2: scaled to 5, partition 2. StatefulSet controller creates pods 1-4:
	// ordinals >= 2 get the new template (2,3,4 new+ready), ordinal 1 stays old.
	// Case 6 must make progress: partition 2 -> 1, surge retained.
	check("step2 case6 progress", mkSts(5, 2), 1, 5,
		mkPod(0, "oldRevision"), mkPod(1, "oldRevision"),
		mkPod(2, "newRevision"), mkPod(3, "newRevision"), mkPod(4, "newRevision"))

	// Step 3: pod 1 updated (ordinal >= partition 1). pod 0 old+ready, 1-4 new+ready.
	// Case 6 must drop the partition to 0 and start reclaiming surge => (0,4).
	check("step3 case6 completes", mkSts(5, 1), 0, 4,
		mkPod(0, "oldRevision"),
		mkPod(1, "newRevision"), mkPod(2, "newRevision"), mkPod(3, "newRevision"), mkPod(4, "newRevision"))

	// Step 4: at rest — live 4, all updated, partition 0 => (0,4), converged.
	check("step4 at rest", mkSts(4, 0), 0, 4,
		mkPod(0, "newRevision"), mkPod(1, "newRevision"), mkPod(2, "newRevision"), mkPod(3, "newRevision"))
}
