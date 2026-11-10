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

// F3 canary (documents current behavior; flips red once surge allocation
// consults the role's exclusion input): with committed members [0,2,4] (record
// "1,3") and maxSurge=1, a rollout places the transient surge instance at
// ordinal 5 — even when the role's excluded-ordinals input is "1,3,5", i.e. the
// user asked for ordinal 5 never to be used. The child RIS never carries the
// exclusion input (filteredRoleAnnotations strips it) and the committed record
// encodes only holes below the last member, so computeTopology cannot see E.
// Verified still present on head f3d44874 after the ce03814f surge rework:
// materializeSurge allocates via skips.Next over the committed record only.
func TestVerifyF3SurgeLandsOnUserExcludedOrdinal(t *testing.T) {
	userExclusions, err := ordinals.Parse("1,3,5")
	require.NoError(t, err)

	h := newRollbackTestHarness(t, "verify-f3-surge", 3, 0, 1)
	h.set.Annotations = map[string]string{constants.CommittedSkipOrdinalsAnnotationKey: "1,3"}
	instances := []*workloadsv1alpha2.RoleInstance{
		h.instance(0, h.current.Name, true),
		h.instance(2, h.current.Name, true),
		h.instance(4, h.current.Name, true),
	}
	topo, err := computeTopology(h.set, instances, h.current.Name, h.intermediate.Name, false)
	require.NoError(t, err)
	require.Equal(t, 1, topo.activeSurge, "rollout with 3 healthy old base members and maxUnavailable=0 should surge")

	surgeOrdinal := topo.ordinal(3)
	t.Logf("committed members [0,2,4], user excluded %q, surge ordinal = %d", "1,3,5", surgeOrdinal)
	require.True(t, userExclusions.Contains(surgeOrdinal),
		"F3 appears fixed: surge ordinal %d no longer falls on a user-excluded ordinal; invert this canary", surgeOrdinal)
	require.Equal(t, 5, surgeOrdinal)
}
