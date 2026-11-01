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

import (
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	"sigs.k8s.io/rbgs/api/workloads/ordinals"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// F3 (reviewer A harness, PR #538): surge allocation must not place a
// transient surge instance on an ordinal the user excluded.
//
// Contract: while a sparse set (committed members [0,2,4], record "1,3") is
// mid-rollout with maxSurge=1, the surge ordinal chosen by computeTopology
// must not be an ordinal in the role's current exclusion input.
//
// The committed record only encodes holes BELOW the last member, so a user
// exclusion above the last member (E="1,3,5") is invisible to surge
// allocation: skips.Members(replicas+maxSurge) yields [0,2,4,5] and the
// surge instance lands exactly on the excluded ordinal 5.
//
// polarity: contract — FAILS on the PR head (07ae1194) as captured in
// results/unit-head.out; PASSES once surge allocation also consults the
// user's exclusion input (or the record covers exclusions above the last
// member).
func TestClaudeSurgeOrdinalAvoidsUserExcludedOrdinals(t *testing.T) {
	set := buildSet("claude-surge", 3, nil, nil)
	set.Annotations = map[string]string{constants.CommittedSkipOrdinalsAnnotationKey: "1,3"}
	set.Spec.UpdateStrategy.MaxSurge = ptr.To(intstr.FromInt32(1))
	set.Spec.UpdateStrategy.Type = workloadsv1alpha2.RecreatePodUpdateStrategyType

	instances := []*workloadsv1alpha2.RoleInstance{
		buildInst(set.Name, 0, "rev-1", true, true),
		buildInst(set.Name, 2, "rev-1", true, true),
		buildInst(set.Name, 4, "rev-1", true, true),
	}

	// The user's exclusion input at commit time: Select keeps the fixed
	// membership [0,2,4] and Record stores only "1,3", so ordinal 5 is
	// excluded by the user but absent from the persisted record.
	excluded, err := ordinals.Parse("1,3,5")
	require.NoError(t, err)

	// Rollout in progress: base members are stale, not paused.
	topo, err := computeTopology(set, instances, "rev-1", "rev-2", false)
	require.NoError(t, err)
	require.Greater(t, topo.endOrdinal, topo.replicas, "rollout must hold a surge slot")

	surgeOrdinal := topo.ordinal(topo.replicas)
	require.False(t, excluded.Contains(surgeOrdinal),
		"surge instance would be created at ordinal %d, which the user excluded (E=%q); "+
			"surge allocation consults only the committed gap record, not the exclusion input",
		surgeOrdinal, "1,3,5")

	// Positive control: without ordinal 5 in the exclusion input the surge
	// ordinal 5 is legal and the current behavior is correct.
	control, err := ordinals.Parse("1,3")
	require.NoError(t, err)
	require.False(t, control.Contains(topo.ordinal(topo.replicas)))
}
