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

// Reviewer harness for PR #474 — webhook validation of spec.rolloutStrategy.
//
// C5 (canary): the both-zero rule is spelling-sensitive. The integer spelling
//      (maxUnavailable: 0, maxSurge: 0) is rejected by the webhook, while the
//      percentage spelling ("0%" / "0%") is accepted and silently falls back
//      to maxUnavailable=1 at runtime — the author's own test
//      ("zero percentages resolve to zero at runtime and fall back to
//      maxUnavailable=1") pins that as intended. This canary documents the
//      resulting UX inconsistency: two spellings of the same resolved budget
//      take opposite paths, and the percent one changes the user's requested
//      availability contract without any event or condition. If the project
//      later aligns the spellings (reject both, or document the fallback in
//      the CRD description), this test flips to RED and must be inverted.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

func verifyStrategySpec(maxUnavailable, maxSurge *intstr.IntOrString) *RoleBasedGroupSetSpec {
	return &RoleBasedGroupSetSpec{
		Replicas: ptr.To(int32(3)),
		RolloutStrategy: &GroupSetRolloutStrategy{
			Type:           RecreateStrategyType,
			MaxUnavailable: maxUnavailable,
			MaxSurge:       maxSurge,
		},
	}
}

// TestVerifyPR474_Webhook_ZeroBudgetIsSpellingSensitive is canary-polarity and
// documents how a resolved 0-unavailable/0-surge budget behaves per spelling.
func TestVerifyPR474_Webhook_ZeroBudgetIsSpellingSensitive(t *testing.T) {
	intZero := validateGroupSetRolloutStrategy(
		verifyStrategySpec(ptr.To(intstr.FromInt32(0)), ptr.To(intstr.FromInt32(0))))
	assert.Error(t, intZero, "integer spelling of the unprogressable budget is rejected")

	percentZero := validateGroupSetRolloutStrategy(
		verifyStrategySpec(ptr.To(intstr.FromString("0%")), ptr.To(intstr.FromString("0%"))))
	assert.NoError(t, percentZero,
		"canary: the percent spelling of the same resolved budget is accepted and silently degrades at runtime")

	// The no-downtime shape must stay valid in both spellings.
	assert.NoError(t, validateGroupSetRolloutStrategy(
		verifyStrategySpec(ptr.To(intstr.FromInt32(0)), ptr.To(intstr.FromInt32(1)))))
	assert.NoError(t, validateGroupSetRolloutStrategy(
		verifyStrategySpec(ptr.To(intstr.FromString("0%")), ptr.To(intstr.FromInt32(1)))))
}
