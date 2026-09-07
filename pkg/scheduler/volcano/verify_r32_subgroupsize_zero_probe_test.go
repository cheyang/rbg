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

// Reviewer verification harness, round 3.2 probe (PR head 11ea20c9).
//
// The commit "Address review feedback on component sizing" dropped the
// `max(total, 1)` floor in ComputeSubGroupSize, so a customComponentsPattern
// with an EMPTY components list now yields subGroupSize 0 instead of 1. The
// field is +optional with no MinItems validation and the RBG webhook does not
// reject an empty list, so the value is reachable. This probe pins what
// buildGangSpec does with it, to decide whether the delta introduces a real
// defect (a SubGroupPolicy with SubGroupSize=0) or is simply more honest.
//
// This is an OBSERVATIONAL probe: it records current behavior with t.Logf and
// light assertions, not a red/green contract.

package volcano

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/utils/ptr"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/pkg/scheduler/common"
)

// emptyCustomComponentsRole builds a RoleInstanceSet-backed role (the default
// workload type) whose customComponentsPattern lists NO components, i.e. a
// replica that produces zero pods. ComputeSubGroupSize now returns 0 for it.
func emptyCustomComponentsRole(name string, replicas int32) workloadsv1alpha2.RoleSpec {
	return workloadsv1alpha2.RoleSpec{
		Name:     name,
		Replicas: ptr.To(replicas),
		Pattern: workloadsv1alpha2.Pattern{
			CustomComponentsPattern: &workloadsv1alpha2.CustomComponentsPattern{},
		},
	}
}

// TestVerifyR32_EmptyCustomComponentsSubGroupSize confirms the helper itself now
// returns 0 for an empty components list (was 1 before 11ea20c9).
func TestVerifyR32_EmptyCustomComponentsSubGroupSize(t *testing.T) {
	role := emptyCustomComponentsRole("ghost", 2)
	got := workloadsv1alpha2.ComputeSubGroupSize(&role)
	t.Logf("ComputeSubGroupSize(empty customComponents) = %d", got)
	assert.EqualValues(t, 0, got, "11ea20c9 removed the max(total,1) floor")
}

// TestVerifyR32_Probe_SingleEmptyRoleGangIsRejected covers the case where the
// ONLY covered role has subGroupSize 0: minMember collapses to 0 and the
// existing `if minMember == 0` guard must reject it (same protection as F2b).
func TestVerifyR32_Probe_SingleEmptyRoleGangIsRejected(t *testing.T) {
	rbg := rbgWithRoles(emptyCustomComponentsRole("ghost", 2))
	strategy := &common.GangStrategy{MinReplicas: map[string]int32{"ghost": 1}}
	minMember, policies, err := buildGangSpec(rbg, strategy)
	t.Logf("single empty role: minMember=%d policies=%d err=%v", minMember, len(policies), err)
	require.Error(t, err, "an all-zero gang must be rejected, not built with minMember 0")
	assert.True(t, common.IsIncompatibleGangConfig(err))
	assert.ErrorContains(t, err, "minMember 0")
	assert.Empty(t, policies)
}

// TestVerifyR32_Probe_MixedEmptyRoleEmitsZeroSubGroup is the interesting case:
// one healthy covered role plus one empty-customComponents covered role. minMember
// stays > 0 (so the guard does NOT fire) and the empty role still gets a
// SubGroupPolicy entry -- with SubGroupSize 0. This records whether that
// degenerate entry is emitted.
func TestVerifyR32_Probe_MixedEmptyRoleEmitsZeroSubGroup(t *testing.T) {
	rbg := rbgWithRoles(
		standaloneRole("prefill", 2, ""),
		emptyCustomComponentsRole("ghost", 2),
	)
	strategy := &common.GangStrategy{MinReplicas: map[string]int32{"prefill": 1, "ghost": 1}}
	minMember, policies, err := buildGangSpec(rbg, strategy)
	require.NoError(t, err)
	t.Logf("mixed: minMember=%d, %d subGroupPolicy entries", minMember, len(policies))
	for _, p := range policies {
		t.Logf("  subGroupPolicy name=%q subGroupSize=%d minSubGroups=%d", p.Name, ptr.Deref(p.SubGroupSize, -1), ptr.Deref(p.MinSubGroups, -1))
	}

	// Observational: pin what the current head actually does.
	byName := map[string]int32{}
	for _, p := range policies {
		byName[p.Name] = ptr.Deref(p.SubGroupSize, -1)
	}
	assert.Equal(t, int32(1), minMember, "only prefill (1 pod x min 1) contributes; ghost adds 0")
	assert.EqualValues(t, 1, byName["prefill"])
	// The degenerate entry: does the empty role still emit a subGroupSize=0 policy?
	if _, ok := byName["ghost"]; ok {
		t.Logf("FINDING: empty-customComponents role emitted a SubGroupPolicy with subGroupSize=%d", byName["ghost"])
	}
}
