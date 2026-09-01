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

// Reviewer verification harness, round 3 (PR head bd9ee4dd). Volcano layer.
// Contract tests keyed to round-2 findings: they assert the fixed behavior and
// must PASS on the reworked head. See docs/verification/pr434-gang-scheduling/.

package volcano

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	coreapplyv1 "k8s.io/client-go/applyconfigurations/core/v1"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/pkg/scheduler/common"
)

func verifyR3PolicyForVolcano(rules ...workloadsv1alpha2.CoordinatedPolicyRule) *workloadsv1alpha2.CoordinatedPolicy {
	return &workloadsv1alpha2.CoordinatedPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "rbg", Namespace: "default"},
		Spec:       workloadsv1alpha2.CoordinatedPolicySpec{Policies: rules},
	}
}

func verifyR3GangRuleForVolcano(name string, roles []string, minReplicas map[string]int32) workloadsv1alpha2.CoordinatedPolicyRule {
	return workloadsv1alpha2.CoordinatedPolicyRule{
		Name:  name,
		Roles: roles,
		Strategy: workloadsv1alpha2.CoordinatedPolicyStrategy{
			Scheduling: &workloadsv1alpha2.SchedulingCoordinationStrategy{
				Gang: &workloadsv1alpha2.GangSchedulingStrategy{MinReplicas: minReplicas},
			},
		},
	}
}

// TestVerifyR3_F1_NonRoleInstanceSetBackingIsRejected covers round-2 F1. The old
// code emitted matchLabelKeys=[role-instance-name] for every workload type even
// though only RoleInstanceSet writes that label, describing a partition that does
// not exist. The fix rejects the configuration instead.
func TestVerifyR3_F1_NonRoleInstanceSetBackingIsRejected(t *testing.T) {
	for _, workloadType := range []string{
		constants.StatefulSetWorkloadType,
		constants.DeploymentWorkloadType,
	} {
		t.Run(workloadType, func(t *testing.T) {
			rbg := rbgWithRoles(standaloneRole("prefill", 4, workloadType))
			strategy := &common.GangStrategy{
				Roles:       nil, // whole-group style coverage over the only role
				MinReplicas: map[string]int32{"prefill": 2},
			}
			minMember, policies, err := buildGangSpec(rbg, strategy)
			require.Error(t, err, "a workload that never writes the per-instance label must be rejected")
			assert.True(t, common.IsIncompatibleGangConfig(err))
			assert.ErrorContains(t, err, "pod label is needed to partition")
			assert.Zero(t, minMember)
			assert.Empty(t, policies)
		})
	}
}

// TestVerifyR3_F3_ExcludedRoleGetsSchedulerNameButNoPodGroupAnnotation covers
// round-2 F3/F3b. InjectPodSchedulingFields used to enroll every role in the
// PodGroup regardless of the strategy's scope. Now only covered roles carry the
// gang annotation, while excluded roles keep the gang scheduler's schedulerName
// so one scheduler places the whole group.
func TestVerifyR3_F3_ExcludedRoleGetsSchedulerNameButNoPodGroupAnnotation(t *testing.T) {
	rbg := rbgWithRoles(standaloneRole("prefill", 2, ""), standaloneRole("sidecar", 1, ""))
	// Roles must be scoped the way MergeGangStrategies produces them: an empty
	// Roles set means whole-group coverage, which would legitimately enroll the
	// sidecar. The merged strategy lists exactly the covered roles.
	strategy := &common.GangStrategy{
		Roles:       sets.New("prefill"),
		MinReplicas: map[string]int32{"prefill": 1},
	}

	inGang := rbg.Spec.Roles[0]
	outOfGang := rbg.Spec.Roles[1]

	m := New(nil)

	ptsIn := coreapplyv1.PodTemplateSpec()
	m.InjectPodSchedulingFields(rbg, &inGang, strategy, ptsIn)
	require.NotNil(t, ptsIn.Spec)
	assert.Equal(t, SchedulerName, *ptsIn.Spec.SchedulerName)
	assert.Equal(t, "rbg", ptsAnnotations(ptsIn)[AnnotationKey], "a covered role must be enrolled in the PodGroup")

	ptsOut := coreapplyv1.PodTemplateSpec()
	m.InjectPodSchedulingFields(rbg, &outOfGang, strategy, ptsOut)
	require.NotNil(t, ptsOut.Spec)
	assert.Equal(t, SchedulerName, *ptsOut.Spec.SchedulerName, "an excluded role is still placed by the gang scheduler")
	_, enrolled := ptsAnnotations(ptsOut)[AnnotationKey]
	assert.False(t, enrolled, "an excluded role must not be counted against the gang minimum")
}

// TestVerifyR3_F4_MinReplicasAboveReplicasRejected covers round-2 F4: a minimum
// larger than the role's replicas makes the gang permanently unsatisfiable and
// must be reported when the PodGroup is built.
func TestVerifyR3_F4_MinReplicasAboveReplicasRejected(t *testing.T) {
	rbg := rbgWithRoles(standaloneRole("prefill", 2, ""))
	strategy := &common.GangStrategy{MinReplicas: map[string]int32{"prefill": 5}}
	_, _, err := buildGangSpec(rbg, strategy)
	require.Error(t, err)
	assert.True(t, common.IsIncompatibleGangConfig(err))
	assert.ErrorContains(t, err, "can never be satisfied")
}

// TestVerifyR3_F5_MinReplicasBelowOneRejected covers round-2 F5 at the runtime
// layer (admission is checked separately in api/workloads/v1alpha2): zero and
// negative minimums must not reach the PodGroup.
func TestVerifyR3_F5_MinReplicasBelowOneRejected(t *testing.T) {
	for _, minimum := range []int32{0, -3} {
		rbg := rbgWithRoles(standaloneRole("prefill", 4, ""))
		strategy := &common.GangStrategy{MinReplicas: map[string]int32{"prefill": minimum}}
		_, _, err := buildGangSpec(rbg, strategy)
		require.Error(t, err, "minReplicas=%d must be rejected", minimum)
		assert.True(t, common.IsIncompatibleGangConfig(err))
		assert.ErrorContains(t, err, "must be at least 1")
	}
}

// TestVerifyR3_N5_MergedPolicyEndToEndToPodGroupSpec pins the full path for the
// mixed-strategy shape this round introduced: rule A all-or-nothing over prefill,
// rule B minimum 2 for decode. The merged strategy must produce minMember =
// 4 (prefill in full) + 2 (decode at its minimum) and one subGroupPolicy entry
// per covered role with the per-instance partitioning label.
func TestVerifyR3_N5_MergedPolicyEndToEndToPodGroupSpec(t *testing.T) {
	policy := verifyR3PolicyForVolcano(
		verifyR3GangRuleForVolcano("a", []string{"prefill"}, nil),
		verifyR3GangRuleForVolcano("b", []string{"decode"}, map[string]int32{"decode": 2}),
	)
	strategy, err := common.MergeGangStrategies(policy)
	require.NoError(t, err)
	require.NotNil(t, strategy)

	rbg := rbgWithRoles(standaloneRole("prefill", 4, ""), standaloneRole("decode", 6, ""))
	minMember, policies, err := buildGangSpec(rbg, strategy)
	require.NoError(t, err)
	assert.Equal(t, int32(6), minMember, "prefill participates in full (4) plus decode's minimum (2)")

	require.Len(t, policies, 2)
	byName := map[string]int32{}
	for _, p := range policies {
		byName[p.Name] = *p.MinSubGroups
		assert.EqualValues(t, 1, *p.SubGroupSize, "standalone instances produce one pod each")
		assert.Equal(t, []string{constants.RoleInstanceNameLabelKey}, p.MatchLabelKeys)
		require.NotNil(t, p.LabelSelector)
		assert.Equal(t, "rbg", p.LabelSelector.MatchLabels[constants.GroupNameLabelKey])
		assert.Equal(t, p.Name, p.LabelSelector.MatchLabels[constants.RoleNameLabelKey])
	}
	assert.Equal(t, int32(4), byName["prefill"], "the all-or-nothing role is held to all its replicas")
	assert.Equal(t, int32(2), byName["decode"], "the minimum role is held to its minimum")
}

// TestVerifyR3_Baseline_HappyPathSubGroupPolicy is the harness-bites check for
// this layer: the plain per-role-minimums path computes minMember and one entry
// per role. If this were red, the reds above could be harness bugs.
func TestVerifyR3_Baseline_HappyPathSubGroupPolicy(t *testing.T) {
	rbg := rbgWithRoles(standaloneRole("prefill", 4, ""), standaloneRole("decode", 6, ""))
	strategy := &common.GangStrategy{MinReplicas: map[string]int32{"prefill": 2, "decode": 3}}
	minMember, policies, err := buildGangSpec(rbg, strategy)
	require.NoError(t, err)
	assert.Equal(t, int32(5), minMember)
	require.Len(t, policies, 2)
}
