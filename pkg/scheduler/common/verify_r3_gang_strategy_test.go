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

// Reviewer verification harness, round 3 (PR head bd9ee4dd).
//
// Round 2 found ten defects (F1-F10) in the original KEP-430 implementation.
// The PR was then force-pushed with a full rework. Every test below is a
// CONTRACT test keyed to a round-2 finding: it asserts the intended, fixed
// behavior, so it must PASS on the reworked head. A failure means the fix
// regressed ("still broken"). See docs/verification/pr434-gang-scheduling/.

package common

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func verifyR3Scheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))
	return scheme
}

func verifyR3Policy(name string, rules ...workloadsv1alpha2.CoordinatedPolicyRule) *workloadsv1alpha2.CoordinatedPolicy {
	return &workloadsv1alpha2.CoordinatedPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       workloadsv1alpha2.CoordinatedPolicySpec{Policies: rules},
	}
}

func verifyR3GangRule(name string, roles []string, minReplicas map[string]int32) workloadsv1alpha2.CoordinatedPolicyRule {
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

func verifyR3Rbg(name string, annotation string, roles ...workloadsv1alpha2.RoleSpec) *workloadsv1alpha2.RoleBasedGroup {
	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       workloadsv1alpha2.RoleBasedGroupSpec{Roles: roles},
	}
	if annotation != "" {
		rbg.Annotations = map[string]string{constants.GangSchedulingAnnotationKey: annotation}
	}
	return rbg
}

func verifyR3Role(name string, replicas int32) workloadsv1alpha2.RoleSpec {
	return workloadsv1alpha2.RoleSpec{
		Name:     name,
		Replicas: ptr.To(replicas),
		Pattern: workloadsv1alpha2.Pattern{
			StandalonePattern: &workloadsv1alpha2.StandalonePattern{},
		},
	}
}

// TestVerifyR3_F2_UnknownRoleNameIsRejectedNotCollapsedToZero covers round-2 F2/F2b.
// A minReplicas key that names no role (typo, rename) used to collapse minMember
// to 0 and silently switch gang scheduling off. The fix must reject it with an
// IncompatibleGangConfigError instead.
func TestVerifyR3_F2_UnknownRoleNameIsRejectedNotCollapsedToZero(t *testing.T) {
	rbg := verifyR3Rbg("rbg", "", verifyR3Role("prefill", 4))

	// Merge level: the typo'd key is outside its own rule's roles list, so no
	// per-role minimum survives; that must be reported, not dropped.
	policy := verifyR3Policy("rbg", verifyR3GangRule("r1", []string{"prefill"}, map[string]int32{"prefil": 2}))
	strategy, err := MergeGangStrategies(policy)
	require.Error(t, err)
	assert.True(t, IsIncompatibleGangConfig(err), "typo'd role must surface as IncompatibleGangConfig, got %v", err)
	assert.Nil(t, strategy)

	// Size level: a strategy covering a role the RBG does not have must never
	// compute a number; GangSize must refuse.
	ghost := &GangStrategy{Roles: sets.New("ghost")}
	size, err := GangSize(rbg, ghost)
	require.Error(t, err)
	assert.True(t, IsIncompatibleGangConfig(err))
	assert.Zero(t, size)
	assert.ElementsMatch(t, []string{"ghost"}, UnknownGangRoles(rbg, ghost))
}

// TestVerifyR3_F6_RuleScopeIsHonored covers round-2 F6. GetGangStrategy used to
// discard CoordinatedPolicyRule.Roles and apply every rule group-wide. A minimum
// declared for a role outside the declaring rule's scope must be dropped.
func TestVerifyR3_F6_RuleScopeIsHonored(t *testing.T) {
	policy := verifyR3Policy("rbg",
		verifyR3GangRule("scoped", []string{"prefill"}, map[string]int32{"prefill": 2, "decode": 1}),
	)
	strategy, err := MergeGangStrategies(policy)
	require.NoError(t, err)
	require.NotNil(t, strategy)
	assert.Equal(t, sets.New("prefill"), strategy.Roles, "decode is not in the rule's roles and must not be covered")
	assert.Equal(t, map[string]int32{"prefill": 2}, strategy.MinReplicas, "the out-of-scope decode minimum must be dropped")
}

// TestVerifyR3_F7_MultipleGangRulesAreMergedNotDropped covers round-2 F7. The old
// code returned on the first gang rule and silently dropped every later one. All
// rules must be merged, taking the maximum minimum per role.
func TestVerifyR3_F7_MultipleGangRulesAreMergedNotDropped(t *testing.T) {
	policy := verifyR3Policy("rbg",
		verifyR3GangRule("a", []string{"prefill"}, map[string]int32{"prefill": 1}),
		verifyR3GangRule("b", []string{"prefill", "decode"}, map[string]int32{"prefill": 3, "decode": 2}),
	)
	strategy, err := MergeGangStrategies(policy)
	require.NoError(t, err)
	require.NotNil(t, strategy)
	assert.Equal(t, sets.New("prefill", "decode"), strategy.Roles)
	assert.Equal(t, map[string]int32{"prefill": 3, "decode": 2}, strategy.MinReplicas,
		"both rules must contribute; the larger prefill minimum wins")
}

// TestVerifyR3_N1_AllOrNothingRuleDoesNotAbsorbOtherRulesMinimums pins the exact
// cross-rule scenario raised in review: rule A is all-or-nothing over prefill,
// rule B gives decode a minimum of 2. Decode's minimum must survive; only a role
// named by the all-or-nothing rule itself is subsumed.
func TestVerifyR3_N1_AllOrNothingRuleDoesNotAbsorbOtherRulesMinimums(t *testing.T) {
	policy := verifyR3Policy("rbg",
		verifyR3GangRule("a", []string{"prefill"}, nil),
		verifyR3GangRule("b", []string{"decode"}, map[string]int32{"decode": 2}),
	)
	strategy, err := MergeGangStrategies(policy)
	require.NoError(t, err)
	require.NotNil(t, strategy)
	assert.Equal(t, sets.New("prefill", "decode"), strategy.Roles)
	assert.Equal(t, map[string]int32{"decode": 2}, strategy.MinReplicas,
		"decode keeps its minimum; prefill participates in full with no entry")

	// Downstream contract: GangMinimumReplicas must demand all of prefill's
	// replicas and exactly 2 of decode, which is what the scaling guard and the
	// PodGroup budget rely on.
	rbg := verifyR3Rbg("rbg", "", verifyR3Role("prefill", 4), verifyR3Role("decode", 6))
	assert.Equal(t, map[string]int32{"prefill": 4, "decode": 2}, GangMinimumReplicas(rbg, strategy))
	size, err := GangSize(rbg, strategy)
	require.NoError(t, err)
	assert.Equal(t, int32(4+6), size, "whole-group size counts both covered roles")
}

// TestVerifyR3_F8_PolicyReadErrorPropagates covers round-2 F8. A read failure on
// the CoordinatedPolicy used to be indistinguishable from absence (err==nil
// guard), silently degrading the scheduling guarantee. It must propagate.
func TestVerifyR3_F8_PolicyReadErrorPropagates(t *testing.T) {
	scheme := verifyR3Scheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(
		interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				return apierrors.NewInternalError(errors.New("etcd unavailable"))
			},
		},
	).Build()

	rbg := verifyR3Rbg("rbg", "true", verifyR3Role("prefill", 2))
	strategy, err := GetGangStrategy(context.Background(), c, rbg)
	require.Error(t, err, "a transient API failure must not be swallowed")
	assert.ErrorContains(t, err, "get CoordinatedPolicy default/rbg")
	assert.Nil(t, strategy, "an error must not degrade to either gang-on or gang-off")

	// NotFound is still absence: the legacy annotation decides.
	cNoPolicy := fake.NewClientBuilder().WithScheme(scheme).Build()
	strategy, err = GetGangStrategy(context.Background(), cNoPolicy, rbg)
	require.NoError(t, err)
	require.NotNil(t, strategy, "annotation=true with no policy must enable the whole-group gang")
	assert.Empty(t, strategy.Roles)
	assert.Empty(t, strategy.MinReplicas)
}

// TestVerifyR3_N2_ContextCacheDoesNotLeakAcrossRBGs guards the new
// resolve-once-and-carry-on-ctx design: the cached strategy is keyed by object,
// so a context from reconciling RBG A must never serve RBG B.
func TestVerifyR3_N2_ContextCacheDoesNotLeakAcrossRBGs(t *testing.T) {
	scheme := verifyR3Scheme(t)
	policyA := verifyR3Policy("rbg-a", verifyR3GangRule("r", []string{"prefill"}, map[string]int32{"prefill": 2}))
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(policyA).Build()

	rbgA := verifyR3Rbg("rbg-a", "", verifyR3Role("prefill", 4))
	rbgB := verifyR3Rbg("rbg-b", "", verifyR3Role("prefill", 4))

	ctxA, strategyA, err := ResolveGangStrategy(context.Background(), c, rbgA)
	require.NoError(t, err)
	require.NotNil(t, strategyA)

	// Same ctx, different RBG, no policy for it: must resolve fresh to nil.
	strategyB, err := GetGangStrategy(ctxA, c, rbgB)
	require.NoError(t, err)
	assert.Nil(t, strategyB, "the cached strategy of rbg-a must not leak into rbg-b")

	// And the original RBG still hits the cache.
	strategyAgain, err := GetGangStrategy(ctxA, c, rbgA)
	require.NoError(t, err)
	assert.Same(t, strategyA, strategyAgain)
}

// TestVerifyR3_N3_PolicyWithoutGangFallsBackToAnnotation pins the resolution
// order: a CoordinatedPolicy that declares no gang strategy must not mask the
// legacy annotation, and a policy that does declare one takes precedence.
func TestVerifyR3_N3_PolicyWithoutGangFallsBackToAnnotation(t *testing.T) {
	scheme := verifyR3Scheme(t)

	rollingOnly := verifyR3Policy("rbg", workloadsv1alpha2.CoordinatedPolicyRule{
		Name:  "rolling",
		Roles: []string{"prefill"},
		Strategy: workloadsv1alpha2.CoordinatedPolicyStrategy{
			RollingUpdate: &workloadsv1alpha2.RollingUpdateCoordinationStrategy{},
		},
	})
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(rollingOnly).Build()

	annotated := verifyR3Rbg("rbg", "true", verifyR3Role("prefill", 2))
	strategy, err := GetGangStrategy(context.Background(), c, annotated)
	require.NoError(t, err)
	require.NotNil(t, strategy, "policy without gang must fall back to the annotation")
	assert.Empty(t, strategy.Roles)

	plain := verifyR3Rbg("rbg", "", verifyR3Role("prefill", 2))
	strategy, err = GetGangStrategy(context.Background(), c, plain)
	require.NoError(t, err)
	assert.Nil(t, strategy, "no gang anywhere means gang scheduling is off")

	// Precedence: a policy with a gang rule wins over the annotation, and the
	// resolved strategy carries the policy's scope, not the whole group.
	ganged := verifyR3Policy("rbg2", verifyR3GangRule("r", []string{"prefill"}, map[string]int32{"prefill": 1}))
	c2 := fake.NewClientBuilder().WithScheme(scheme).WithObjects(ganged).Build()
	annotated2 := verifyR3Rbg("rbg2", "true", verifyR3Role("prefill", 2), verifyR3Role("decode", 2))
	strategy, err = GetGangStrategy(context.Background(), c2, annotated2)
	require.NoError(t, err)
	require.NotNil(t, strategy)
	assert.Equal(t, sets.New("prefill"), strategy.Roles, "the policy's scoped gang must win over the whole-group annotation")
}

// TestVerifyR3_F2b_AllZeroCoveredRolesRejected covers the zero-minMember guard:
// a gang whose covered roles are all scaled to zero provides no guarantee and
// must be reported rather than written as minMember 0.
func TestVerifyR3_F2b_AllZeroCoveredRolesRejected(t *testing.T) {
	rbg := verifyR3Rbg("rbg", "", verifyR3Role("prefill", 0))
	strategy := &GangStrategy{Roles: sets.New("prefill")}
	size, err := GangSize(rbg, strategy)
	require.Error(t, err)
	assert.True(t, IsIncompatibleGangConfig(err))
	assert.ErrorContains(t, err, "all scaled to zero")
	assert.Zero(t, size)
}

// TestVerifyR3_Baseline_WholeGroupGangSize is the harness-bites check: the
// legacy whole-group path computes the sum over every role.
func TestVerifyR3_Baseline_WholeGroupGangSize(t *testing.T) {
	rbg := verifyR3Rbg("rbg", "true", verifyR3Role("prefill", 4), verifyR3Role("decode", 6))
	size, err := GangSize(rbg, &GangStrategy{})
	require.NoError(t, err)
	assert.Equal(t, int32(10), size)
	assert.Equal(t, map[string]int32{"prefill": 4, "decode": 6}, GangMinimumReplicas(rbg, &GangStrategy{}))
}
