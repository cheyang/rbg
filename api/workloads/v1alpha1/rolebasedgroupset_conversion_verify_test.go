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

package v1alpha1

// Reviewer harness for PR #474, round 2 — the v1alpha1 round-trip fix for the
// round-1 consensus blocker (a full-object write through the still-served
// v1alpha1 API silently dropped spec.rolloutStrategy and the v1alpha2-only
// rollout status fields, reverting a mid-rollout set to the un-paced static
// path). The fix stashes them in annotations on the v1alpha1 view.
//
// These tests are independent of the author's
// rolebasedgroupset_conversion_test.go and simulate the full client flow:
// hub -> v1alpha1 read view -> client edit -> v1alpha1 full-object write ->
// hub. The deployed CRD wires the conversion webhook for rolebasedgroupsets
// (deploy/kubectl/manifests.yaml, config/crd/patches/webhook_in_rolebasedgroupsets.yaml),
// so these conversion functions run on real clusters for every v1alpha1 read
// and write.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	v2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func verifyHubSet(strategyType v2.GroupUpdateStrategyType) *v2.RoleBasedGroupSet {
	return &v2.RoleBasedGroupSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "rbgs", Namespace: "ns",
			Labels:      map[string]string{"team": "infra"},
			Annotations: map[string]string{"note": "user-managed"},
		},
		Spec: v2.RoleBasedGroupSetSpec{
			Replicas: ptr.To(int32(3)),
			RolloutStrategy: &v2.GroupSetRolloutStrategy{
				Type:           strategyType,
				Partition:      ptr.To(intstr.FromInt32(1)),
				MaxUnavailable: ptr.To(intstr.FromInt32(1)),
				MaxSurge:       ptr.To(intstr.FromString("25%")),
			},
			GroupTemplate: v2.RoleBasedGroupTemplateSpec{
				Spec: v2.RoleBasedGroupSpec{
					Roles: []v2.RoleSpec{{Name: "worker", Replicas: ptr.To(int32(1))}},
				},
			},
		},
		Status: v2.RoleBasedGroupSetStatus{
			Replicas:                3,
			ReadyReplicas:           2,
			CurrentReplicas:         1,
			UpdatedReplicas:         2,
			UpdatedReadyReplicas:    2,
			ExpectedUpdatedReplicas: 2,
			CurrentRevision:         "rbgs-old",
			UpdateRevision:          "rbgs-new",
		},
	}
}

// TestVerifyPR474_V1alpha1RoundTrip_PreservesRolloutStrategy (contract): the
// exact blocker scenario — a mid-rollout set is read through v1alpha1 and
// written back as a full object; the strategy and the rollout status must
// survive, and the stash must not leak into the stored hub object.
func TestVerifyPR474_V1alpha1RoundTrip_PreservesRolloutStrategy(t *testing.T) {
	for _, strategyType := range []v2.GroupUpdateStrategyType{
		v2.RecreateStrategyType, v2.InPlaceUpdateStrategyType,
	} {
		t.Run(string(strategyType), func(t *testing.T) {
			hub := verifyHubSet(strategyType)
			// Read view: hub -> v1alpha1.
			view := &RoleBasedGroupSet{}
			require.NoError(t, view.ConvertFrom(hub))
			require.NotNil(t, view.Annotations)
			assert.Contains(t, view.Annotations, annotationV1alpha2RolloutStrategy)
			assert.Contains(t, view.Annotations, annotationV1alpha2RolloutStatus)
			assert.Equal(t, "user-managed", view.Annotations["note"],
				"user-managed annotations must survive alongside the stash")

			// A v1alpha1 client edits an unrelated field and writes the whole
			// object back (kubectl edit / apply semantics).
			view.Labels["synced"] = "true"

			// Write-back: v1alpha1 -> hub (what the conversion webhook does).
			roundTripped := &v2.RoleBasedGroupSet{}
			require.NoError(t, view.ConvertTo(roundTripped))

			assert.Equal(t, hub.Spec.RolloutStrategy, roundTripped.Spec.RolloutStrategy,
				"spec.rolloutStrategy must survive a v1alpha1 full-object write mid-rollout")
			assert.Equal(t, hub.Status.CurrentReplicas, roundTripped.Status.CurrentReplicas)
			assert.Equal(t, hub.Status.UpdatedReplicas, roundTripped.Status.UpdatedReplicas)
			assert.Equal(t, hub.Status.UpdatedReadyReplicas, roundTripped.Status.UpdatedReadyReplicas)
			assert.Equal(t, hub.Status.ExpectedUpdatedReplicas, roundTripped.Status.ExpectedUpdatedReplicas)
			assert.Equal(t, hub.Status.CurrentRevision, roundTripped.Status.CurrentRevision)
			assert.Equal(t, hub.Status.UpdateRevision, roundTripped.Status.UpdateRevision)
			assert.Equal(t, "true", roundTripped.Labels["synced"])

			// The conversion-only keys must not leak into storage.
			assert.NotContains(t, roundTripped.Annotations, annotationV1alpha2RolloutStrategy)
			assert.NotContains(t, roundTripped.Annotations, annotationV1alpha2RolloutStatus)
		})
	}
}

// TestVerifyPR474_V1alpha1RoundTrip_ClearsStaleStash (contract): an update that
// removes the strategy through v2 (rolloutStrategy deleted) must not resurrect
// it from a stale annotation left on the v1alpha1 view. The status stash is
// different: status fields exist independently of the strategy, so they keep
// round-tripping — that is faithful, not staleness.
func TestVerifyPR474_V1alpha1RoundTrip_ClearsStaleStash(t *testing.T) {
	hub := verifyHubSet(v2.RecreateStrategyType)
	view := &RoleBasedGroupSet{}
	require.NoError(t, view.ConvertFrom(hub))
	staleStrategy := view.Annotations[annotationV1alpha2RolloutStrategy]
	require.NotEmpty(t, staleStrategy)

	// The v2 object drops the strategy (user opts back out of rollouts) but
	// keeps whatever rollout status it had.
	hub.Spec.RolloutStrategy = nil
	updated := &RoleBasedGroupSet{}
	require.NoError(t, updated.ConvertFrom(hub))
	assert.Empty(t, updated.Annotations[annotationV1alpha2RolloutStrategy],
		"ConvertFrom must not carry a stale strategy stash when the hub has none")
	assert.NotEmpty(t, updated.Annotations[annotationV1alpha2RolloutStatus],
		"rollout status fields keep round-tripping independently of the strategy")

	// A write of the *stale* view (an earlier read) through v1alpha1 still
	// carries the old strategy — inherent to last-write-wins — but a fresh
	// read/write cycle is consistent, which is what matters for kubectl flows.
	roundTripped := &v2.RoleBasedGroupSet{}
	require.NoError(t, updated.ConvertTo(roundTripped))
	assert.Nil(t, roundTripped.Spec.RolloutStrategy)
	assert.Equal(t, hub.Status.CurrentReplicas, roundTripped.Status.CurrentReplicas)
}

// TestVerifyPR474_V1alpha1RoundTrip_GarbageStashFailsClosed (contract): a
// corrupted stash annotation must fail the conversion loudly instead of
// silently dropping the strategy.
func TestVerifyPR474_V1alpha1RoundTrip_GarbageStashFailsClosed(t *testing.T) {
	src := &RoleBasedGroupSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "rbgs", Namespace: "ns",
			Annotations: map[string]string{annotationV1alpha2RolloutStrategy: "{not json"},
		},
	}
	dst := &v2.RoleBasedGroupSet{}
	assert.Error(t, src.ConvertTo(dst),
		"a garbage strategy stash must fail conversion, not silently revert to the static path")

	src = &RoleBasedGroupSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "rbgs", Namespace: "ns",
			Annotations: map[string]string{annotationV1alpha2RolloutStatus: "{not json"},
		},
	}
	assert.Error(t, src.ConvertTo(dst), "a garbage status stash must fail conversion too")
}

// TestVerifyPR474_V1alpha1RoundTrip_DroppedStashLosesStrategy (canary): the
// residual limitation of the annotation-stash design — a v1alpha1 client that
// writes a *fresh* object (constructed from scratch, not from a read) drops
// the strategy with no error. kubectl edit/apply echo annotations back, so
// the common paths are safe; a programmatic client that rebuilds the object
// is not. Documents the boundary of the fix; flips RED only if the project
// adds a real v1alpha1 field or rejects stash-less v1alpha1 writes.
func TestVerifyPR474_V1alpha1RoundTrip_DroppedStashLosesStrategy(t *testing.T) {
	hub := verifyHubSet(v2.InPlaceUpdateStrategyType)
	view := &RoleBasedGroupSet{}
	require.NoError(t, view.ConvertFrom(hub))

	fresh := view.DeepCopy()
	fresh.Annotations = map[string]string{} // client dropped everything it does not know
	roundTripped := &v2.RoleBasedGroupSet{}
	require.NoError(t, fresh.ConvertTo(roundTripped))
	assert.Nil(t, roundTripped.Spec.RolloutStrategy,
		"canary: a stash-less v1alpha1 write silently reverts the set to the legacy static path")
}
