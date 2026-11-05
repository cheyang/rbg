/*
Verification harness for PR #553 (issue #547) — Reviewer B (Codex).
Additive only; does not modify production code.

Claim under test (contract polarity — FAILS on the buggy base, PASSES on the fix):
  When the live StatefulSet replica count sits far below the role's desired
  count (e.g. after `kubectl scale`), the rolling-update parameter computation
  must not panic and must scale the StatefulSet back to the desired count.
*/

package reconciler

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"sigs.k8s.io/rbgs/api/workloads/constants"
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
// from a shrunken live StatefulSet by returning replicas=roleReplicas with the
// partition clamped to the live count, instead of panicking in case 6.
func TestVerifyPR553RollingUpdateParametersScalesBackUp(t *testing.T) {
	cases := []struct {
		name            string
		liveReplicas    int32
		livePartition   int32
		wantReplicas    int32
		wantPartition   int32
	}{
		{name: "live 1 of 4, partition 2", liveReplicas: 1, livePartition: 2, wantReplicas: 4, wantPartition: 1},
		{name: "live 0 of 4, partition 2", liveReplicas: 0, livePartition: 2, wantReplicas: 4, wantPartition: 0},
		{name: "live 3 of 4, partition 0", liveReplicas: 3, livePartition: 0, wantReplicas: 4, wantPartition: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scheme := newRolloutErrScheme(t)
			// role: replicas 4, maxUnavailable 1, maxSurge 1 (same as issue #547).
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
				t.Fatalf("expected replicas=%d (scale back to desired), got %d", tc.wantReplicas, replicas)
			}
			if partition != tc.wantPartition {
				t.Fatalf("expected partition=%d (clamped to live), got %d", tc.wantPartition, partition)
			}
		})
	}
}
