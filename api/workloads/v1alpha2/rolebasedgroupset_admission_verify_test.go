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

package v1alpha2

// Verification harness for PR #474 (reviewer additive test; production code untouched).
//
// Finding F-webhook-gap: validateGroupSetRolloutStrategy rejects integer
// maxUnavailable=0 + maxSurge=0 ("no way to make progress") but lets the percentage
// spellings "0%"/"0%" through, even though 0% of any replica count is 0 and the
// rollout can never progress. The controller then silently clamps maxUnavailable to 1
// (resolveGroupSetRollout), rewriting the explicit user intent instead of rejecting it.
//
// Polarity: BUG CANARY - the first two assertions pin the CURRENT (inconsistent)
// behavior; they PASS on the code under review and must be revisited (inverted or
// promoted to a contract) once the webhook handles the percentage case consistently.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

func rolloutSpecForWebhook(replicas int32, maxUnavailable, maxSurge string) *RoleBasedGroupSetSpec {
	mu := intstr.FromString(maxUnavailable)
	ms := intstr.FromString(maxSurge)
	return &RoleBasedGroupSetSpec{
		Replicas: ptr.To(replicas),
		RolloutStrategy: &GroupSetRolloutStrategy{
			Type:           RecreateStrategyType,
			MaxUnavailable: &mu,
			MaxSurge:       &ms,
		},
	}
}

func TestVerify_WebhookRejectsIntegerZeroZero(t *testing.T) {
	zero, zeroS := intstr.FromInt(0), intstr.FromInt(0)
	spec := &RoleBasedGroupSetSpec{
		Replicas: ptr.To(int32(3)),
		RolloutStrategy: &GroupSetRolloutStrategy{
			Type: RecreateStrategyType, MaxUnavailable: &zero, MaxSurge: &zeroS,
		},
	}
	assert.Error(t, validateGroupSetRolloutStrategy(spec), "integer 0/0 is rejected (no progress possible)")
	_ = zeroS
}

func TestVerify_WebhookAcceptsPercentZeroZero_Canary(t *testing.T) {
	// CANARY: "0%"/"0%" resolves to 0/0 for every replica count, yet the webhook accepts
	// it because the both-zero check only fires for integer spellings.
	spec := rolloutSpecForWebhook(3, "0%", "0%")
	assert.NoError(t, validateGroupSetRolloutStrategy(spec),
		"canary: percentage 0%%/0%% currently passes validation although it resolves to 0/0")
}
