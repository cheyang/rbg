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
// Round 1 pinned an inconsistency (canary): integer maxUnavailable=0 + maxSurge=0 was
// rejected while the "0%"/"0%" spellings passed and were silently clamped by the
// controller. The 2026-10-10 head fixed the admission gap, so the flipped canary is
// promoted to CONTRACT tests below: declared 0/0 is rejected in every spelling for
// Recreate, and the InPlaceUpdate-specific rules hold. The controller-side clamp now
// only covers positive percentages that RESOLVE to 0 (e.g. 10% of 3) - the documented
// fallback, pinned as a contract too.

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
}

func TestVerify_WebhookRejectsPercentZeroZero(t *testing.T) {
	// Promoted from the round-1 canary: declared "0%"/"0%" must be rejected like 0/0.
	assert.Error(t, validateGroupSetRolloutStrategy(rolloutSpecForWebhook(3, "0%", "0%")),
		"declared 0%%/0%% can never make progress and must be rejected")
	assert.Error(t, validateGroupSetRolloutStrategy(rolloutSpecForWebhook(3, "0%", "0%")),
		"mixed spellings of 0/0 must be rejected")
	zero := intstr.FromInt(0)
	mixed := &RoleBasedGroupSetSpec{
		Replicas: ptr.To(int32(3)),
		RolloutStrategy: &GroupSetRolloutStrategy{
			Type: RecreateStrategyType, MaxUnavailable: &zero, MaxSurge: ptr.To(intstr.FromString("0%")),
		},
	}
	assert.Error(t, validateGroupSetRolloutStrategy(mixed), "mixed 0 + 0% must be rejected")
}

func TestVerify_WebhookInPlaceUpdateRules(t *testing.T) {
	mk := func(mu, ms string) *RoleBasedGroupSetSpec {
		spec := rolloutSpecForWebhook(3, mu, ms)
		spec.RolloutStrategy.Type = InPlaceUpdateStrategyType
		return spec
	}
	assert.Error(t, validateGroupSetRolloutStrategy(mk("0%", "0%")),
		"InPlaceUpdate must reject maxUnavailable 0/0% (no surge mechanism exists)")
	zero := intstr.FromInt(0)
	inPlaceZero := mk("1%", "0%")
	inPlaceZero.RolloutStrategy.MaxUnavailable = &zero
	assert.Error(t, validateGroupSetRolloutStrategy(inPlaceZero),
		"InPlaceUpdate must reject integer maxUnavailable=0")
	surge := mk("1%", "0%")
	surge.RolloutStrategy.MaxSurge = ptr.To(intstr.FromInt(1))
	assert.Error(t, validateGroupSetRolloutStrategy(surge),
		"InPlaceUpdate must reject maxSurge > 0")
	ok := mk("1%", "0%")
	ok.RolloutStrategy.MaxUnavailable = ptr.To(intstr.FromInt(1))
	assert.NoError(t, validateGroupSetRolloutStrategy(ok),
		"InPlaceUpdate allows maxSurge 0% (ignored) and maxUnavailable 1")
}
