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
// Round 2 (head 9e423e86): the both-zero rule was made spelling-insensitive
// (F2 fixed — the round-1 canary TestVerifyPR474_Webhook_ZeroBudgetIsSpellingSensitive
// flipped RED on this head, which is the fix evidence), and InPlaceUpdate was
// added with its own admission rules. Both are pinned here as contract tests
// so a future regression turns them RED.

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

func verifyInPlaceSpec(maxUnavailable, maxSurge *intstr.IntOrString) *RoleBasedGroupSetSpec {
	spec := verifyStrategySpec(maxUnavailable, maxSurge)
	spec.RolloutStrategy.Type = InPlaceUpdateStrategyType
	return spec
}

// TestVerifyPR474_Webhook_ZeroBudgetRejectedSymmetrically (contract; was canary
// TestVerifyPR474_Webhook_ZeroBudgetIsSpellingSensitive in round 1). The
// unprogressable Recreate budget must be rejected in both spellings: at
// 44241903 only the integer spelling was rejected while "0%" slipped through
// and silently degraded to maxUnavailable=1 at runtime; at 9e423e86 the
// percent spelling is rejected too.
func TestVerifyPR474_Webhook_ZeroBudgetRejectedSymmetrically(t *testing.T) {
	assert.Error(t, validateGroupSetRolloutStrategy(
		verifyStrategySpec(ptr.To(intstr.FromInt32(0)), ptr.To(intstr.FromInt32(0)))),
		"integer spelling of the unprogressable budget must be rejected")
	assert.Error(t, validateGroupSetRolloutStrategy(
		verifyStrategySpec(ptr.To(intstr.FromString("0%")), ptr.To(intstr.FromString("0%")))),
		"percent spelling must be rejected symmetrically")
	assert.Error(t, validateGroupSetRolloutStrategy(
		verifyStrategySpec(ptr.To(intstr.FromInt32(0)), ptr.To(intstr.FromString("0%")))),
		"mixed spellings of the same resolved budget must be rejected too")

	// The no-downtime shapes must stay valid in both spellings.
	assert.NoError(t, validateGroupSetRolloutStrategy(
		verifyStrategySpec(ptr.To(intstr.FromInt32(0)), ptr.To(intstr.FromInt32(1)))))
	assert.NoError(t, validateGroupSetRolloutStrategy(
		verifyStrategySpec(ptr.To(intstr.FromString("0%")), ptr.To(intstr.FromInt32(1)))))
	// A positive percentage that rounds down to zero is admitted and handled
	// by the controller fallback (documented in the MaxUnavailable CRD text).
	assert.NoError(t, validateGroupSetRolloutStrategy(
		verifyStrategySpec(ptr.To(intstr.FromString("1%")), ptr.To(intstr.FromInt32(0)))))
}

// TestVerifyPR474_Webhook_InPlaceUpdateRules (contract): InPlaceUpdate rejects
// maxSurge > 0 (surge is Recreate-only) and a declared-zero maxUnavailable;
// it accepts the normal shapes.
func TestVerifyPR474_Webhook_InPlaceUpdateRules(t *testing.T) {
	assert.Error(t, validateGroupSetRolloutStrategy(
		verifyInPlaceSpec(ptr.To(intstr.FromInt32(1)), ptr.To(intstr.FromInt32(1)))),
		"InPlaceUpdate must reject a positive maxSurge")
	assert.Error(t, validateGroupSetRolloutStrategy(
		verifyInPlaceSpec(ptr.To(intstr.FromInt32(0)), nil)),
		"InPlaceUpdate must reject maxUnavailable: 0")
	assert.Error(t, validateGroupSetRolloutStrategy(
		verifyInPlaceSpec(ptr.To(intstr.FromString("0%")), nil)),
		"InPlaceUpdate must reject maxUnavailable: 0%")
	assert.NoError(t, validateGroupSetRolloutStrategy(
		verifyInPlaceSpec(ptr.To(intstr.FromInt32(1)), nil)))
	assert.NoError(t, validateGroupSetRolloutStrategy(
		verifyInPlaceSpec(ptr.To(intstr.FromString("1%")), nil)),
		"a positive percentage that may round down to zero is admitted")

	// An omitted type (defaulted to InPlaceUpdate by the CRD/webhook) follows
	// the InPlaceUpdate rules, not the Recreate rules.
	emptyType := verifyInPlaceSpec(nil, nil)
	emptyType.RolloutStrategy.Type = ""
	assert.Error(t, validateGroupSetRolloutStrategy(
		func() *RoleBasedGroupSetSpec {
			s := emptyType.DeepCopy()
			s.RolloutStrategy.MaxUnavailable = ptr.To(intstr.FromInt32(0))
			return s
		}()),
		"empty type (default InPlaceUpdate) must reject a zero maxUnavailable")
}
