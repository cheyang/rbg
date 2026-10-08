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

// Verification harness for PR #474 (reviewer additive test; production code untouched).
//
// Finding: the v1alpha1 <-> v1alpha2 conversion for RoleBasedGroupSet drops
// spec.rolloutStrategy and the new rollout status fields. v1alpha1 is still a served
// (non-storage) version, so any full-object write through the v1alpha1 API (kubectl
// edit/apply pinned to v1alpha1, older GitOps tooling) silently reverts an opted-in
// rolling set to the un-paced static path MID-ROLLOUT - the exact whole-set restart
// this PR exists to prevent - and wipes CurrentRevision/UpdateRevision bookkeeping.
//
// Polarity: CONTRACT - asserts the intended behavior (strategy survives a served-version
// round trip). FAILS on the code under review; passes once the conversion preserves the
// field (e.g. via the annotation stash used for other v1alpha1-lossy fields).

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	v2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func TestVerify_ConversionPreservesRolloutStrategy(t *testing.T) {
	mu := intstr.FromInt(1)
	ms := intstr.FromInt(1)
	stored := &v2.RoleBasedGroupSet{
		Spec: v2.RoleBasedGroupSetSpec{
			Replicas: ptr.To(int32(3)),
			GroupTemplate: v2.RoleBasedGroupTemplateSpec{
				Spec: v2.RoleBasedGroupSpec{
					Roles: []v2.RoleSpec{{Name: "worker", Replicas: ptr.To(int32(1))}},
				},
			},
			RolloutStrategy: &v2.GroupSetRolloutStrategy{
				Type:           v2.RecreateStrategyType,
				MaxUnavailable: &mu,
				MaxSurge:       &ms,
			},
		},
		Status: v2.RoleBasedGroupSetStatus{
			CurrentRevision: "rbgs-aaa", UpdateRevision: "rbgs-bbb", UpdatedReplicas: 1,
		},
	}

	// Round trip through the served v1alpha1 version, as a full-object v1alpha1 write does.
	viaV1 := &RoleBasedGroupSet{}
	require.NoError(t, viaV1.ConvertFrom(stored))
	restored := &v2.RoleBasedGroupSet{}
	require.NoError(t, viaV1.ConvertTo(restored))

	assert.NotNil(t, restored.Spec.RolloutStrategy,
		"spec.rolloutStrategy must survive a v1alpha1 round trip; dropping it reverts a rolling set to the un-paced static path")
	if restored.Spec.RolloutStrategy != nil {
		assert.Equal(t, v2.RecreateStrategyType, restored.Spec.RolloutStrategy.Type)
	}
	assert.Equal(t, "rbgs-aaa", restored.Status.CurrentRevision,
		"rollout status bookkeeping must survive a v1alpha1 round trip")
	assert.Equal(t, "rbgs-bbb", restored.Status.UpdateRevision)
}
