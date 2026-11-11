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

package statefulmode

// Reviewer harness for PR #538 (verify/selective-scale-down-codex). Additive
// only; touches no production code. See
// docs/verification/selective-scale-down-codex/README.md.

import (
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	"sigs.k8s.io/rbgs/api/workloads/ordinals"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// F3 contract (was a canary on heads <= f3d44874; the fix in 5fc8c972 mirrors
// the role's exclusion input onto the child RIS and consults it in
// computeTopology via nextSurgeOrdinal): with committed members [0,2,4]
// (record "1,3"), mirrored exclusion input "1,3,5" on the child, and
// maxSurge=1, a rollout must NOT place the transient surge instance on
// user-excluded ordinal 5; it must skip to 6.
func TestVerifyF3SurgeSkipsMirroredExclusions(t *testing.T) {
	userExclusions, err := ordinals.Parse("1,3,5")
	require.NoError(t, err)

	h := newRollbackTestHarness(t, "verify-f3-surge", 3, 0, 1)
	h.set.Annotations = map[string]string{
		constants.CommittedSkipOrdinalsAnnotationKey: "1,3",
		constants.ExcludedOrdinalsAnnotationKey:      "1,3,5", // mirrored by the conditional writer
	}
	instances := []*workloadsv1alpha2.RoleInstance{
		h.instance(0, h.current.Name, true),
		h.instance(2, h.current.Name, true),
		h.instance(4, h.current.Name, true),
	}
	topo, err := computeTopology(h.set, instances, h.current.Name, h.intermediate.Name, false)
	require.NoError(t, err)
	require.Equal(t, 1, topo.activeSurge, "rollout with 3 healthy old base members and maxUnavailable=0 should surge")

	surgeOrdinal := topo.ordinal(3)
	require.False(t, userExclusions.Contains(surgeOrdinal),
		"surge ordinal %d must not fall on a user-excluded ordinal", surgeOrdinal)
	require.Equal(t, 6, surgeOrdinal)
}

// F3 characterization of the residual upgrade window: a child RIS last written
// by a pre-mirror controller carries the committed record but no mirrored
// exclusion input, so surge allocation can only consult the record and still
// picks ordinal 5. The conditional writer mirrors E on its first sync after
// upgrade, so this shape is transient; the test pins the documented behavior
// so any change is deliberate.
func TestVerifyF3LegacyChildWithoutMirror(t *testing.T) {
	h := newRollbackTestHarness(t, "verify-f3-legacy", 3, 0, 1)
	h.set.Annotations = map[string]string{
		constants.CommittedSkipOrdinalsAnnotationKey: "1,3",
	}
	instances := []*workloadsv1alpha2.RoleInstance{
		h.instance(0, h.current.Name, true),
		h.instance(2, h.current.Name, true),
		h.instance(4, h.current.Name, true),
	}
	topo, err := computeTopology(h.set, instances, h.current.Name, h.intermediate.Name, false)
	require.NoError(t, err)
	require.Equal(t, 1, topo.activeSurge)
	require.Equal(t, 5, topo.ordinal(3), "without the mirrored input the allocator can only consult the committed record")
}
