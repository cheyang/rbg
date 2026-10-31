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

// Verification harness for PR #474 round 6 (reviewer additive tests; production code
// untouched).
//
// R6PARTITION (#521): the /scale subresource bypasses this webhook, so scaling below a
// stored partition is accepted; the partition<=replicas admission bound then locked
// EVERY later update (even a label change) until someone fixed the partition. The
// bound is dropped (the controller clamps), so:
//   - create/update with partition > replicas is admitted,
//   - a metadata-only update of such an object is admitted (the #521 lock-out),
//   - negative integers and >100% percentages are still rejected.
//
// Polarity: CONTRACT - fails on the round-5 head 4a9043c (bound present), passes at head.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

func r6VerifySet(partition intstr.IntOrString, replicas int32) *RoleBasedGroupSet {
	return &RoleBasedGroupSet{
		ObjectMeta: metav1.ObjectMeta{Name: "r6-partition", Namespace: "default"},
		Spec: RoleBasedGroupSetSpec{
			Replicas: ptr.To(replicas),
			GroupTemplate: RoleBasedGroupTemplateSpec{
				Spec: RoleBasedGroupSpec{
					Roles: []RoleSpec{{
						Name:     "worker",
						Replicas: ptr.To(int32(1)),
					}},
				},
			},
			RolloutStrategy: &GroupSetRolloutStrategy{
				Type:      RecreateStrategyType,
				Partition: &partition,
			},
		},
	}
}

func TestVerify_R6_PartitionAboveReplicasAdmitted_Contract(t *testing.T) {
	ctx := context.Background()
	v := &RoleBasedGroupSetValidator{}

	// The object as it exists after "kubectl scale --replicas=1" with a stored
	// partition of 3: admission must accept it on create and on update.
	set := r6VerifySet(intstr.FromInt32(3), 1)
	_, err := v.ValidateCreate(ctx, set)
	assert.NoError(t, err, "partition > replicas must be admitted; the controller clamps")

	// The #521 lock-out: a metadata-only update of the scaled-down object.
	updated := set.DeepCopy()
	updated.Labels = map[string]string{"review": "r6"}
	_, err = v.ValidateUpdate(ctx, set, updated)
	assert.NoError(t, err, "a metadata-only update must not fail on the stored partition")

	// The bound's removal must not weaken the remaining validations.
	_, err = v.ValidateCreate(ctx, r6VerifySet(intstr.FromInt32(-1), 3))
	assert.Error(t, err, "negative partition is still rejected")
	_, err = v.ValidateCreate(ctx, r6VerifySet(intstr.FromString("150%"), 3))
	assert.Error(t, err, "partition percentages above 100%% are still rejected")
	_, err = v.ValidateCreate(ctx, r6VerifySet(intstr.FromString("-10%"), 3))
	assert.Error(t, err, "negative partition percentages are still rejected")
	require.NotNil(t, set.Spec.RolloutStrategy)
}
