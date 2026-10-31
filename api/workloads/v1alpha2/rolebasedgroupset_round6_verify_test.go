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

// Reviewer harness for PR #474 — round 6, the #521 contract.
//
// 13cbcab4 dropped the partition <= replicas admission bound: the scale
// subresource bypasses the webhook, so a set scaled below a stored partition
// must not become unwritable (every later full-object update was rejected
// until somebody lowered the partition). The controller clamps the effective
// partition instead, matching StatefulSet semantics.
//
// Polarity: contract — FAILs if the bound is reintroduced.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

// TestVerifyPR474_Webhook_PartitionNotBoundedByReplicas: admission accepts an
// integer partition above replicas (the controller clamps), while the other
// partition rules (percentage range, non-negative) still apply.
func TestVerifyPR474_Webhook_PartitionNotBoundedByReplicas(t *testing.T) {
	spec := func(partition int32) *RoleBasedGroupSetSpec {
		return &RoleBasedGroupSetSpec{
			Replicas: ptr.To(int32(1)),
			RolloutStrategy: &GroupSetRolloutStrategy{
				Type:           RecreateStrategyType,
				Partition:      ptr.To(intstr.FromInt32(partition)),
				MaxUnavailable: ptr.To(intstr.FromInt32(1)),
				MaxSurge:       ptr.To(intstr.FromInt32(1)),
			},
		}
	}

	// The #521 shape: an autoscaler scaled the set to one group while the
	// partition still holds four back.
	assert.NoError(t, validateGroupSetRolloutStrategy(spec(4)),
		"partition above replicas must be admitted; the controller clamps it")
	assert.NoError(t, validateGroupSetRolloutStrategy(spec(0)),
		"partition zero stays valid")

	// The remaining partition rules still apply.
	percent := spec(0)
	percent.RolloutStrategy.Partition = ptr.To(intstr.FromString("101%"))
	assert.ErrorContains(t, validateGroupSetRolloutStrategy(percent),
		"partition: percentage must be between 0% and 100%")

	negative := spec(-1)
	assert.ErrorContains(t, validateGroupSetRolloutStrategy(negative), "partition")
}

// TestVerifyPR474_Webhook_ScaledDownSetStillAcceptsMetadataWrites: the object
// shape #521 left stuck (replicas=1, partition=4) must keep passing admission
// for ordinary full-object updates, not just for strategy changes.
func TestVerifyPR474_Webhook_ScaledDownSetStillAcceptsMetadataWrites(t *testing.T) {
	scaled := &RoleBasedGroupSet{
		Spec: RoleBasedGroupSetSpec{
			Replicas: ptr.To(int32(1)),
			RolloutStrategy: &GroupSetRolloutStrategy{
				Type:           RecreateStrategyType,
				Partition:      ptr.To(intstr.FromInt32(4)),
				MaxUnavailable: ptr.To(intstr.FromInt32(1)),
				MaxSurge:       ptr.To(intstr.FromInt32(1)),
			},
			GroupTemplate: RoleBasedGroupTemplateSpec{Spec: RoleBasedGroupSpec{
				Roles: []RoleSpec{{Name: "worker", Replicas: ptr.To(int32(1))}},
			}},
		},
	}
	// A metadata-only write (kubectl label) of the same stuck object.
	assert.NoError(t, validateGroupSetRolloutStrategy(&scaled.Spec),
		"a set scaled below its stored partition must remain writable (#521)")
}
