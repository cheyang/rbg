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

package reconciler

// Reviewer A (Claude) verification harness for PR #588 / issue #518.
//
// Layer: L1 (deterministic unit) against ConstructPodTemplateSpecApplyConfiguration.
// Docs: docs/verification/exclusive-topology-claude/README.md
//
// P0 (premise): on the merge-base the injected required podAffinity selects
// constants.GroupUniqueHashLabelKey, which no v1alpha2 code path sets on Pods
// (Pods carry constants.GroupUIDLabelKey from GetCommonLabelsFromRole). The
// Kubernetes scheduler only lets the first Pod of a self-affine group schedule
// when the Pod matches its own term (interpodaffinity filtering.go: "This pod
// may be the first pod in a series that have affinity to themselves"), so the
// group stays Pending. TestVerifyClaude_ExclusiveTopologyPodMatchesItsOwnAffinity
// is a contract test that is RED on the merge-base (premise reproduced) and
// must be GREEN on the PR head.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	wrappersv2 "sigs.k8s.io/rbgs/test/wrappers/v1alpha2"
)

func verifyClaudeNewPodReconciler(t *testing.T) *PodReconciler {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))
	reconciler := NewPodReconciler(scheme, fake.NewClientBuilder().WithScheme(scheme).Build())
	reconciler.SetInjectors([]string{})
	return reconciler
}

func verifyClaudeExclusiveRBG() *workloadsv1alpha2.RoleBasedGroup {
	rbg := wrappersv2.BuildBasicRoleBasedGroup("verify-group", "verify-ns").Obj()
	rbg.Annotations = map[string]string{constants.GroupExclusiveTopologyKey: corev1.LabelHostname}
	return rbg
}

// verifyClaudeRoundTrip converts the apply configuration into a plain
// PodTemplateSpec the same way the workload controllers persist it.
func verifyClaudeRoundTrip(
	t *testing.T, result interface{},
) corev1.PodTemplateSpec {
	t.Helper()
	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(result)
	require.NoError(t, err)
	var pod corev1.PodTemplateSpec
	require.NoError(t, runtime.DefaultUnstructuredConverter.FromUnstructured(obj, &pod))
	return pod
}

// TestVerifyClaude_ExclusiveTopologyPodMatchesItsOwnAffinity is the premise
// check (P0, runsAgainst: base) and the core regression guard.
//
// Contract (L1, polarity: contract): for a RoleBasedGroup annotated with
// group-exclusive-topology, the injected required podAffinity selector must
// match the labels of the Pod template it is injected into, and it must select
// constants.GroupUIDLabelKey (the key every v1alpha2 Pod carries).
//
// On the merge-base: the selector selects GroupUniqueHashLabelKey, which no
// v1alpha2 Pod carries, so the first Pod of the group can never satisfy its
// own affinity and stays Pending (issue #518 symptom) — the test goes RED.
func TestVerifyClaude_ExclusiveTopologyPodMatchesItsOwnAffinity(t *testing.T) {
	reconciler := verifyClaudeNewPodReconciler(t)
	rbg := verifyClaudeExclusiveRBG()
	role := &rbg.Spec.Roles[0]

	result, err := reconciler.ConstructPodTemplateSpecApplyConfiguration(
		context.Background(), rbg, role, rbg.GetCommonLabelsFromRole(role))
	require.NoError(t, err)

	pod := verifyClaudeRoundTrip(t, result)

	require.NotNil(t, pod.Spec.Affinity, "exclusive topology must inject affinity")
	require.NotNil(t, pod.Spec.Affinity.PodAffinity)
	terms := pod.Spec.Affinity.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution
	require.NotEmpty(t, terms)

	for _, term := range terms {
		require.Equal(t, corev1.LabelHostname, term.TopologyKey)
		require.NotNil(t, term.LabelSelector)
		require.NotEmpty(t, term.LabelSelector.MatchExpressions)
		for _, expr := range term.LabelSelector.MatchExpressions {
			// Regression guard: the selector key must be the label Pods carry.
			require.Equal(t, constants.GroupUIDLabelKey, expr.Key,
				"affinity selector key must match the label carried by v1alpha2 Pods")
		}
		selector, err := metav1.LabelSelectorAsSelector(term.LabelSelector)
		require.NoError(t, err)
		require.True(t, selector.Matches(labels.Set(pod.Labels)),
			"P0 premise: required podAffinity %q does not match the pod's own labels %v; "+
				"the first Pod of the group can never satisfy its own affinity and stays Pending",
			selector, pod.Labels)
	}

	// The Pod template must actually carry the identity label the selector uses.
	require.Equal(t, rbg.GenGroupUniqueKey(), pod.Labels[constants.GroupUIDLabelKey])
}

// TestVerifyClaude_ExclusiveTopologyAntiAffinitySemantics verifies the
// anti-affinity half of the fix at the unit layer.
//
// Contract (L1, polarity: contract): the injected required podAntiAffinity
// must NOT match Pods of the same group, must match Pods of another group
// that carries group-uid (whether or not that group opts into exclusive
// topology), and must not match unlabeled Pods.
func TestVerifyClaude_ExclusiveTopologyAntiAffinitySemantics(t *testing.T) {
	reconciler := verifyClaudeNewPodReconciler(t)
	rbg := verifyClaudeExclusiveRBG()
	role := &rbg.Spec.Roles[0]

	result, err := reconciler.ConstructPodTemplateSpecApplyConfiguration(
		context.Background(), rbg, role, rbg.GetCommonLabelsFromRole(role))
	require.NoError(t, err)

	pod := verifyClaudeRoundTrip(t, result)
	require.NotNil(t, pod.Spec.Affinity.PodAntiAffinity)
	antiTerms := pod.Spec.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution
	require.NotEmpty(t, antiTerms)

	other := wrappersv2.BuildBasicRoleBasedGroup("verify-other", "verify-ns").Obj()
	otherRole := &other.Spec.Roles[0]
	otherLabels := other.GetCommonLabelsFromRole(otherRole)
	// otherLabels carries group-uid of a different group; it does not opt into
	// exclusive topology itself.

	var antiSelector labels.Selector
	for _, term := range antiTerms {
		require.Equal(t, corev1.LabelHostname, term.TopologyKey)
		selector, err := metav1.LabelSelectorAsSelector(term.LabelSelector)
		require.NoError(t, err)
		antiSelector = selector
		require.False(t, selector.Matches(labels.Set(pod.Labels)),
			"anti-affinity must not match the group's own Pods")
		require.True(t, selector.Matches(labels.Set(otherLabels)),
			"anti-affinity must match Pods of another group carrying group-uid "+
				"(even when that group does not opt into exclusive topology)")
		require.False(t, selector.Matches(labels.Set{"app": "unrelated"}),
			"anti-affinity must not match unlabeled Pods")
	}
	require.NotNil(t, antiSelector, "at least one anti-affinity term is expected")
}

// TestVerifyClaude_ExclusiveTopologyOptOut (contract, L1): a role annotated
// with role-disable-exclusive keeps the identity label in its pod labels but
// receives no injected affinity, so its Pods remain valid affinity targets
// for the enabled roles of the same group.
func TestVerifyClaude_ExclusiveTopologyOptOut(t *testing.T) {
	reconciler := verifyClaudeNewPodReconciler(t)
	rbg := verifyClaudeExclusiveRBG()
	role := &rbg.Spec.Roles[0]
	role.GetTemplate().Annotations = map[string]string{
		constants.DisableExclusiveKeyAnnotationKey: "true",
	}

	result, err := reconciler.ConstructPodTemplateSpecApplyConfiguration(
		context.Background(), rbg, role, rbg.GetCommonLabelsFromRole(role))
	require.NoError(t, err)

	pod := verifyClaudeRoundTrip(t, result)
	require.Equal(t, rbg.GenGroupUniqueKey(), pod.Labels[constants.GroupUIDLabelKey],
		"a disabled role keeps the group identity label")
	require.Nil(t, pod.Spec.Affinity, "a disabled role must not get injected affinity")
}

// TestVerifyClaude_ExclusiveTopologyConflictSelectorErrors is a bug-canary
// (L1): the PR deliberately hard-errors when an existing workload selector
// carries a group-uid that differs from the group identity, instead of
// silently generating an unsatisfiable affinity. If the maintainers decide
// this case should be auto-corrected or warned instead of erroring, this
// canary must be inverted.
func TestVerifyClaude_ExclusiveTopologyConflictSelectorErrors(t *testing.T) {
	reconciler := verifyClaudeNewPodReconciler(t)
	rbg := verifyClaudeExclusiveRBG()
	role := &rbg.Spec.Roles[0]

	result, err := reconciler.ConstructPodTemplateSpecApplyConfiguration(
		context.Background(), rbg, role,
		map[string]string{constants.GroupUIDLabelKey: "someone-elses-uid"},
	)
	require.ErrorContains(t, err, constants.GroupUIDLabelKey)
	require.Nil(t, result)
}

// TestVerifyClaude_ExclusiveTopologyLegacySelectorIsAugmented (contract, L1):
// when the workload selector passed in does not carry group-uid at all (an
// older controller created the workload), the constructed template must gain
// the identity label so the first Pod still matches its own affinity, while
// the caller's selector keys are preserved verbatim.
func TestVerifyClaude_ExclusiveTopologyLegacySelectorIsAugmented(t *testing.T) {
	reconciler := verifyClaudeNewPodReconciler(t)
	rbg := verifyClaudeExclusiveRBG()
	role := &rbg.Spec.Roles[0]
	legacySelector := map[string]string{"legacy-selector": "preserved"}

	result, err := reconciler.ConstructPodTemplateSpecApplyConfiguration(
		context.Background(), rbg, role, legacySelector)
	require.NoError(t, err)

	pod := verifyClaudeRoundTrip(t, result)
	require.Equal(t, "preserved", pod.Labels["legacy-selector"])
	require.Equal(t, rbg.GenGroupUniqueKey(), pod.Labels[constants.GroupUIDLabelKey])

	require.NotNil(t, pod.Spec.Affinity.PodAffinity)
	term := pod.Spec.Affinity.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution[0]
	selector, err := metav1.LabelSelectorAsSelector(term.LabelSelector)
	require.NoError(t, err)
	require.True(t, selector.Matches(labels.Set(pod.Labels)),
		"the first Pod created from a legacy workload selector must still match its own affinity")
}
