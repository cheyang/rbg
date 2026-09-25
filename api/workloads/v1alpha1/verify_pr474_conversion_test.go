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

// Verification harness for the review of PR #474 (RoleBasedGroupSet rolling update).
// Additive only — no production code is touched by this file.
//
// Claim F1 (contract polarity): the v1alpha1 <-> v1alpha2 conversion of a
// RoleBasedGroupSet must round-trip spec.rolloutStrategy and the rollout status
// counters. v1alpha1 is still a served version, so any full-object write through
// it (kubectl replace, GitOps pinned to v1alpha1) rewrites the storage object via
// these two functions; anything they drop is silently deleted from the stored
// object, reverting a rolling set to the un-paced static behavior.
//
// On the PR head this test FAILS: ConvertFrom/ConvertTo carry neither the
// strategy nor the new counters. It goes green once the strategy is preserved
// (e.g. stashed in a conversion annotation, the same machinery already used for
// v1alpha1-only fields).

package v1alpha1

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	v2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// TestVerifyPR474_RolloutStrategySurvivesV1alpha1RoundTrip simulates a v1alpha1
// read-modify-write cycle of a set that carries a rolloutStrategy: the API server
// converts storage (v1alpha2) -> v1alpha1 for the read, and v1alpha1 -> v1alpha2
// for the write. The strategy must still be there afterwards.
func TestVerifyPR474_RolloutStrategySurvivesV1alpha1RoundTrip(t *testing.T) {
	stored := &v2.RoleBasedGroupSet{
		ObjectMeta: metav1.ObjectMeta{Name: "set", Namespace: "ns"},
		Spec: v2.RoleBasedGroupSetSpec{
			Replicas: ptr.To(int32(4)),
			RolloutStrategy: &v2.GroupSetRolloutStrategy{
				Type:           v2.RecreateStrategyType,
				MaxUnavailable: ptr.To(intstr.FromInt32(1)),
				MaxSurge:       ptr.To(intstr.FromString("25%")),
				Partition:      ptr.To(intstr.FromInt32(2)),
				Paused:         true,
			},
		},
		Status: v2.RoleBasedGroupSetStatus{
			Replicas:                5,
			ReadyReplicas:           4,
			CurrentReplicas:         2,
			UpdatedReplicas:         2,
			UpdatedReadyReplicas:    2,
			ExpectedUpdatedReplicas: 2,
		},
	}

	// storage -> v1alpha1 (what a v1alpha1 client is served)
	spoke := &RoleBasedGroupSet{}
	require.NoError(t, spoke.ConvertFrom(stored))

	// v1alpha1 -> storage (the client writes the object back, e.g. kubectl replace)
	roundTripped := &v2.RoleBasedGroupSet{}
	require.NoError(t, spoke.ConvertTo(roundTripped))

	require.NotNil(t, roundTripped.Spec.RolloutStrategy,
		"spec.rolloutStrategy was dropped by the v1alpha1 round trip")
	assert.Equal(t, v2.RecreateStrategyType, roundTripped.Spec.RolloutStrategy.Type)
	assert.Equal(t, intstr.FromInt32(1), *roundTripped.Spec.RolloutStrategy.MaxUnavailable)
	assert.Equal(t, intstr.FromString("25%"), *roundTripped.Spec.RolloutStrategy.MaxSurge)
	assert.Equal(t, intstr.FromInt32(2), *roundTripped.Spec.RolloutStrategy.Partition)
	assert.True(t, roundTripped.Spec.RolloutStrategy.Paused)

	// The rollout status counters are part of the same contract.
	assert.Equal(t, int32(2), roundTripped.Status.CurrentReplicas)
	assert.Equal(t, int32(2), roundTripped.Status.UpdatedReplicas)
	assert.Equal(t, int32(2), roundTripped.Status.UpdatedReadyReplicas)
	assert.Equal(t, int32(2), roundTripped.Status.ExpectedUpdatedReplicas)
}
