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
// Claim F2 (contract polarity): the scalingAdapter-in-groupTemplate guard must
// also fire on UPDATE when the update is what introduces the adapter. Today the
// rule is create-only, so the exact non-converging state the rule exists to
// prevent (set controller and ScalingAdapter controller overwriting each other's
// spec.roles) remains reachable by updating any pre-existing set. The compat
// property the create-only boundary was chosen for — sets that ALREADY carry the
// adapter must stay updatable — is pinned by the second test and must keep
// passing under any fix.
//
// Claim F5 (contract polarity): negative rollout budget values must be rejected.
// intstr.GetScaledValueFromIntOrPercent does not reject negative ints, the CRD
// cannot express a minimum on an IntOrString, so the webhook is the only gate.
// Today maxUnavailable=-1 is accepted and reaches the controller, where
// maxSurge>0 suppresses the floor-to-1 clamp and the delete budget becomes
// (-1 + readySurge) — with a single surge group the rollout can never start.
//
// On the PR head the "rejects" tests FAIL (the gap), the "allows" compat test
// PASSES (and must keep passing once F2 is fixed by rejecting only the
// nil/disabled -> enabled transition).

package v1alpha2

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

func verifyPR474Set(ru *GroupSetRolloutStrategy, adapter bool) *RoleBasedGroupSet {
	role := RoleSpec{Name: "worker", Replicas: ptr.To(int32(1))}
	if adapter {
		role.ScalingAdapter = &ScalingAdapter{Enable: true}
	}
	return &RoleBasedGroupSet{
		ObjectMeta: metav1.ObjectMeta{Name: "verify-pr474", Namespace: "default"},
		Spec: RoleBasedGroupSetSpec{
			Replicas:        ptr.To(int32(3)),
			RolloutStrategy: ru,
			GroupTemplate: RoleBasedGroupTemplateSpec{
				Spec: RoleBasedGroupSpec{Roles: []RoleSpec{role}},
			},
		},
	}
}

// F2: an update that newly enables a ScalingAdapter must be rejected, exactly
// like a create is.
func TestVerifyPR474_UpdateEnablingScalingAdapterRejected(t *testing.T) {
	v := &RoleBasedGroupSetValidator{EnableDeprecatedWorkloadTypes: true}
	oldSet := verifyPR474Set(nil, false)
	newSet := verifyPR474Set(nil, true)

	_, err := v.ValidateUpdate(context.Background(), oldSet, newSet)
	require.Error(t, err,
		"update that newly enables scalingAdapter must be rejected, not just create")
	assert.Contains(t, err.Error(), "scalingAdapter.enable is not supported")
}

// F2 compat: a set that already carries the adapter must stay updatable for
// unrelated changes (the upgrade-compat property the create-only boundary was
// chosen for). Passes today; must keep passing under the fix.
func TestVerifyPR474_UpdateOnSetAlreadyCarryingAdapterAllowed(t *testing.T) {
	v := &RoleBasedGroupSetValidator{EnableDeprecatedWorkloadTypes: true}
	oldSet := verifyPR474Set(nil, true)
	newSet := verifyPR474Set(nil, true)
	newSet.Spec.Replicas = ptr.To(int32(4))

	_, err := v.ValidateUpdate(context.Background(), oldSet, newSet)
	require.NoError(t, err,
		"a set that already carries the adapter must stay updatable")
}

// F5: negative rollout budget values must be rejected by the webhook.
func TestVerifyPR474_NegativeRolloutBudgetsRejected(t *testing.T) {
	v := &RoleBasedGroupSetValidator{EnableDeprecatedWorkloadTypes: true}

	cases := map[string]*GroupSetRolloutStrategy{
		"negative maxUnavailable with surge": {
			MaxUnavailable: ptr.To(intstr.FromInt32(-1)),
			MaxSurge:       ptr.To(intstr.FromInt32(1)),
		},
		"negative maxSurge": {
			MaxUnavailable: ptr.To(intstr.FromInt32(1)),
			MaxSurge:       ptr.To(intstr.FromInt32(-1)),
		},
		"negative partition": {
			Partition: ptr.To(intstr.FromInt32(-1)),
		},
	}
	for name, ru := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := v.ValidateCreate(context.Background(), verifyPR474Set(ru, false))
			require.Error(t, err, "negative rollout budget value must be rejected")
		})
	}
}
