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

// Verification harness for PR #474 review (Reviewer B / Codex). Additive only.
// Contract tests fail while the finding is present and pass once it is fixed.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

func verifyRolloutSet(name string, replicas int32, ru *GroupSetRolloutStrategy) *RoleBasedGroupSet {
	return &RoleBasedGroupSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: RoleBasedGroupSetSpec{
			Replicas: ptr.To(replicas),
			GroupTemplate: RoleBasedGroupTemplateSpec{
				Spec: RoleBasedGroupSpec{
					Roles: []RoleSpec{{Name: "worker", Replicas: ptr.To(int32(1))}},
				},
			},
			RolloutStrategy: ru,
		},
	}
}

// F1 (contract): negative rollout budgets must be rejected. intstr.GetScaledValueFromIntOrPercent
// accepts negative ints and percents, and an IntOrString CRD field cannot express a minimum, so
// this validating webhook is the only gate. Today maxUnavailable: -1 with maxSurge: 1 is
// admitted, and the controller then computes budget = -1 + readySurge, which never lets a
// serving group be deleted: the rollout wedges with Rolling=True forever (see the F1 mechanism
// test in internal/controller/workloads).
func TestVerify_F1_NegativeRolloutBudgetsRejected(t *testing.T) {
	v := &RoleBasedGroupSetValidator{EnableDeprecatedWorkloadTypes: true}

	tests := []struct {
		name string
		ru   *GroupSetRolloutStrategy
	}{
		{
			name: "negative maxUnavailable with surge budget",
			ru: &GroupSetRolloutStrategy{
				MaxUnavailable: ptr.To(intstr.FromInt32(-1)),
				MaxSurge:       ptr.To(intstr.FromInt32(1)),
			},
		},
		{
			name: "negative maxUnavailable percentage",
			ru: &GroupSetRolloutStrategy{
				MaxUnavailable: ptr.To(intstr.FromString("-10%")),
				MaxSurge:       ptr.To(intstr.FromInt32(1)),
			},
		},
		{
			name: "negative maxSurge",
			ru: &GroupSetRolloutStrategy{
				MaxSurge: ptr.To(intstr.FromInt32(-2)),
			},
		},
		{
			name: "negative partition",
			ru: &GroupSetRolloutStrategy{
				Partition: ptr.To(intstr.FromInt32(-1)),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			set := verifyRolloutSet("neg", 3, tt.ru)
			_, err := v.ValidateCreate(context.Background(), set)
			require.Error(t, err, "negative rollout budget must be rejected: %+v", tt.ru)
		})
	}
}

// F1 (regression pin, passes before and after the fix): valid non-negative budgets keep
// being admitted.
func TestVerify_F1_ValidBudgetsStillAdmitted(t *testing.T) {
	v := &RoleBasedGroupSetValidator{EnableDeprecatedWorkloadTypes: true}
	set := verifyRolloutSet("ok", 3, &GroupSetRolloutStrategy{
		MaxUnavailable: ptr.To(intstr.FromInt32(0)),
		MaxSurge:       ptr.To(intstr.FromInt32(1)),
		Partition:      ptr.To(intstr.FromString("50%")),
	})
	_, err := v.ValidateCreate(context.Background(), set)
	require.NoError(t, err)
}

// F3 (contract): validateNoRoleScalingAdapter runs on create only, so an update can NEWLY
// enable a ScalingAdapter in the group template. That opens exactly the non-converging fight
// the rule exists to prevent (the set controller owns spec.roles of every child, the adapter
// controller writes replicas back, the child validator rejects the propagation). Upgrade
// compatibility only requires grandfathering sets that ALREADY carry the adapter — not
// letting clean sets grow one. The fix is an old-vs-new comparison in ValidateUpdate.
func TestVerify_F3_ScalingAdapterNewlyEnabledOnUpdateRejected(t *testing.T) {
	v := &RoleBasedGroupSetValidator{EnableDeprecatedWorkloadTypes: true}

	oldSet := verifyRolloutSet("sa", 1, nil)
	newSet := oldSet.DeepCopy()
	newSet.Spec.GroupTemplate.Spec.Roles[0].ScalingAdapter = &ScalingAdapter{Enable: true}

	_, err := v.ValidateUpdate(context.Background(), oldSet, newSet)
	require.Error(t, err,
		"an update that newly enables scalingAdapter in the group template must be rejected")
	assert.Contains(t, err.Error(), "scalingAdapter")
}

// F3 (regression pin, passes before and after the fix): a set that already carries the
// adapter — the pre-existing object from the upgrade scenario — must stay updatable.
func TestVerify_F3_ScalingAdapterGrandfatheredOnUpdate(t *testing.T) {
	v := &RoleBasedGroupSetValidator{EnableDeprecatedWorkloadTypes: true}

	oldSet := verifyRolloutSet("sa", 1, nil)
	oldSet.Spec.GroupTemplate.Spec.Roles[0].ScalingAdapter = &ScalingAdapter{Enable: true}
	newSet := oldSet.DeepCopy()
	newSet.Spec.Replicas = ptr.To(int32(2))

	_, err := v.ValidateUpdate(context.Background(), oldSet, newSet)
	require.NoError(t, err, "a pre-existing set with the adapter must stay updatable")
}
