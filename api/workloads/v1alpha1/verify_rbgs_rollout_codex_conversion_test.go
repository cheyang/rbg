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

// Verification harness for PR #474 review (Reviewer B / Codex). Additive only.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	v2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// F2 (contract): spec.rolloutStrategy exists only in v1alpha2, while v1alpha1 is still a
// served version and the CRD converts via webhook with v1alpha2 as storage. A client doing a
// full-object read-modify-write through v1alpha1 (kubectl edit, older tooling, a GitOps
// pipeline pinned to v1alpha1) round-trips the object through ConvertFrom/ConvertTo, and the
// strategy is silently dropped: the controller then reverts to the un-paced static update the
// user opted out of. The repo already solves exactly this for v1alpha1-only fields with
// preserveV1alpha1Fields annotations; the same treatment (or refusal) is needed here.
func TestVerify_F2_RolloutStrategySurvivesV1alpha1RoundTrip(t *testing.T) {
	src := &v2.RoleBasedGroupSet{
		ObjectMeta: metav1.ObjectMeta{Name: "rs", Namespace: "default"},
		Spec: v2.RoleBasedGroupSetSpec{
			Replicas: ptr.To(int32(4)),
			GroupTemplate: v2.RoleBasedGroupTemplateSpec{
				Spec: v2.RoleBasedGroupSpec{
					Roles: []v2.RoleSpec{{Name: "worker", Replicas: ptr.To(int32(1))}},
				},
			},
			RolloutStrategy: &v2.GroupSetRolloutStrategy{
				Type:           v2.RecreateStrategyType,
				MaxUnavailable: ptr.To(intstr.FromString("25%")),
				MaxSurge:       ptr.To(intstr.FromInt32(1)),
				Partition:      ptr.To(intstr.FromInt32(1)),
				Paused:         true,
			},
		},
	}

	// v1alpha2 (storage) -> v1alpha1 (what a v1alpha1 client sees) ...
	var viaV1 RoleBasedGroupSet
	require.NoError(t, viaV1.ConvertFrom(src))

	// ... -> v1alpha2 (what gets stored again after any full-object v1alpha1 write).
	roundTripped := &v2.RoleBasedGroupSet{}
	require.NoError(t, viaV1.ConvertTo(roundTripped))

	require.NotNil(t, roundTripped.Spec.RolloutStrategy,
		"spec.rolloutStrategy is silently dropped by a v1alpha1 read-modify-write round trip")
	assert.Equal(t, src.Spec.RolloutStrategy, roundTripped.Spec.RolloutStrategy,
		"the rollout strategy must survive a v1alpha1 round trip unchanged")
}
