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

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/utils/ptr"
)

// TestComputeSubGroupSize_ClampsNegativeSizes is the contract test for the
// negative-size clamping added to ComputeSubGroupSize by PR #459.
//
// A non-positive InstanceComponent.Size must contribute zero pods per replica,
// matching what the RoleInstance controller actually creates (its pod-build
// loop is bounded by the raw size, so a negative size yields zero iterations).
// Before the clamp, ComputeSubGroupSize added the raw (negative) value, which
// could under-count and even go negative — corrupting both the new
// OrderScheduled expected-pod gate and the gang scheduler's subGroupSize.
//
// This test FAILS on the base branch (pre-#459) and PASSES on the PR head.
func TestComputeSubGroupSize_ClampsNegativeSizes(t *testing.T) {
	tests := []struct {
		name string
		role *RoleSpec
		want int32
	}{
		{
			name: "single negative component contributes zero",
			role: &RoleSpec{Pattern: Pattern{CustomComponentsPattern: &CustomComponentsPattern{
				Components: []InstanceComponent{{Name: "c0", Size: ptr.To(int32(-1))}},
			}}},
			want: 0,
		},
		{
			name: "positive and negative cancel to the positive remainder",
			role: &RoleSpec{Pattern: Pattern{CustomComponentsPattern: &CustomComponentsPattern{
				Components: []InstanceComponent{
					{Name: "c0", Size: ptr.To(int32(2))},
					{Name: "c1", Size: ptr.To(int32(-2))},
				},
			}}},
			// Base branch computes 2 + (-2) = 0 (wrong; the 2 positive pods are real).
			// PR head clamps the negative component to 0, leaving 2.
			want: 2,
		},
		{
			name: "negative component does not shrink a larger positive total",
			role: &RoleSpec{Pattern: Pattern{CustomComponentsPattern: &CustomComponentsPattern{
				Components: []InstanceComponent{
					{Name: "c0", Size: ptr.To(int32(5))},
					{Name: "c1", Size: ptr.To(int32(-1))},
				},
			}}},
			// Base branch: 5 + (-1) = 4 (under-counts by one pod).
			// PR head: 5 + 0 = 5, matching the five real pods created.
			want: 5,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ComputeSubGroupSize(tt.role))
		})
	}
}
