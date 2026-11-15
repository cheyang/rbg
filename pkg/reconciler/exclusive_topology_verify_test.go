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

// Reviewer-verification harness for PR #588 (fixes #518).
// Additive only: does not modify production code or existing tests.
//
// Polarity: all tests in this file are CONTRACT tests — they assert the
// intended correct behavior. On the merge-base (1fc69f70, before the fix)
// TestVerifyExclusiveTopologyPodSelfMatch and
// TestVerifyExclusiveTopologyLegacySelectorGetsLabel FAIL, reproducing the
// issue-#518 mechanism; on the PR head they must PASS.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
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

func newVerifyPodReconciler(t *testing.T) *PodReconciler {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))
	r := NewPodReconciler(scheme, fake.NewClientBuilder().WithScheme(scheme).Build())
	r.SetInjectors([]string{})
	return r
}

func renderVerifyTemplate(
	t *testing.T, r *PodReconciler, rbg *workloadsv1alpha2.RoleBasedGroup,
	role *workloadsv1alpha2.RoleSpec, podLabels map[string]string,
) *corev1.PodTemplateSpec {
	t.Helper()
	result, err := r.ConstructPodTemplateSpecApplyConfiguration(
		context.Background(), rbg, role, podLabels,
	)
	require.NoError(t, err)
	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(result)
	require.NoError(t, err)
	var pod corev1.PodTemplateSpec
	require.NoError(t, runtime.DefaultUnstructuredConverter.FromUnstructured(obj, &pod))
	return &pod
}

// TestVerifyExclusiveTopologyPodSelfMatch is the core contract from issue
// #518: the pod template rendered for an exclusive-topology group must carry
// the very label that its required podAffinity selects, otherwise the first
// Pod of the group can never satisfy its own affinity and stays Pending.
func TestVerifyExclusiveTopologyPodSelfMatch(t *testing.T) {
	reconciler := newVerifyPodReconciler(t)
	rbg := wrappersv2.BuildBasicRoleBasedGroup("group-a", "test-ns").Obj()
	rbg.Annotations = map[string]string{constants.GroupExclusiveTopologyKey: "kubernetes.io/hostname"}
	role := &rbg.Spec.Roles[0]

	pod := renderVerifyTemplate(t, reconciler, rbg, role, rbg.GetCommonLabelsFromRole(role))

	require.NotNil(t, pod.Spec.Affinity, "exclusive topology must inject affinity")
	require.NotNil(t, pod.Spec.Affinity.PodAffinity)
	require.NotNil(t, pod.Spec.Affinity.PodAntiAffinity)
	require.Len(t, pod.Spec.Affinity.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution, 1)
	require.Len(t, pod.Spec.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution, 1)

	affinityTerm := pod.Spec.Affinity.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution[0]
	antiTerm := pod.Spec.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution[0]
	assert.Equal(t, "kubernetes.io/hostname", affinityTerm.TopologyKey)
	assert.Equal(t, affinityTerm.TopologyKey, antiTerm.TopologyKey)

	ownSelector, err := metav1.LabelSelectorAsSelector(affinityTerm.LabelSelector)
	require.NoError(t, err)
	otherSelector, err := metav1.LabelSelectorAsSelector(antiTerm.LabelSelector)
	require.NoError(t, err)

	// THE contract from issue #518: the first pod must match its own required
	// affinity, or it can never schedule (kube-scheduler self-match rule).
	assert.True(t, ownSelector.Matches(labels.Set(pod.Labels)),
		"first pod must carry the label its required affinity selects; selector=%v labels=%v",
		ownSelector, pod.Labels)
	// The label the affinity selects must be the group identity label.
	assert.Equal(t, rbg.GenGroupUniqueKey(), pod.Labels[constants.GroupUIDLabelKey])

	// Anti-affinity must exclude other groups but never the pod's own group.
	assert.False(t, otherSelector.Matches(labels.Set(pod.Labels)), "own group must not match anti-affinity")
	other := wrappersv2.BuildBasicRoleBasedGroup("group-b", "test-ns").Obj()
	otherLabels := other.GetCommonLabelsFromRole(&other.Spec.Roles[0])
	assert.True(t, otherSelector.Matches(labels.Set(otherLabels)),
		"another group's pods must match anti-affinity so the node stays exclusive")
	assert.False(t, otherSelector.Matches(labels.Set{"app": "unrelated"}),
		"pods without the group label must not be excluded")
}

// TestVerifyExclusiveTopologyLegacySelectorGetsLabel covers workloads whose
// persisted selector predates group-uid in the common labels (deploy/sts
// selector reuse): the pod template must still gain the group identity label,
// or the fixed affinity would select nothing.
func TestVerifyExclusiveTopologyLegacySelectorGetsLabel(t *testing.T) {
	reconciler := newVerifyPodReconciler(t)
	rbg := wrappersv2.BuildBasicRoleBasedGroup("group-legacy", "test-ns").Obj()
	rbg.Annotations = map[string]string{constants.GroupExclusiveTopologyKey: "kubernetes.io/hostname"}
	role := &rbg.Spec.Roles[0]
	legacySelector := map[string]string{"legacy-selector": "preserved"}

	result, err := reconciler.ConstructPodTemplateSpecApplyConfiguration(
		context.Background(), rbg, role, legacySelector,
	)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"legacy-selector": "preserved"}, legacySelector,
		"caller-owned selector map must not be mutated")
	assert.Equal(t, rbg.GenGroupUniqueKey(), result.Labels[constants.GroupUIDLabelKey],
		"pod template must gain the group identity label when the selector lacks it")
}

// TestVerifyExclusiveTopologyConflictReported covers the new guard: a
// workload selector that pins a *different* group-uid must be reported as an
// error instead of silently rendering an unsatisfiable affinity.
func TestVerifyExclusiveTopologyConflictReported(t *testing.T) {
	reconciler := newVerifyPodReconciler(t)
	rbg := wrappersv2.BuildBasicRoleBasedGroup("group-conflict", "test-ns").Obj()
	rbg.Annotations = map[string]string{constants.GroupExclusiveTopologyKey: "kubernetes.io/hostname"}

	result, err := reconciler.ConstructPodTemplateSpecApplyConfiguration(
		context.Background(), rbg, &rbg.Spec.Roles[0],
		map[string]string{constants.GroupUIDLabelKey: "foreign-value"},
	)
	require.Error(t, err, "conflicting group-uid in the workload selector must be reported")
	assert.Contains(t, err.Error(), constants.GroupUIDLabelKey)
	assert.Nil(t, result)
}

// TestVerifyExclusiveTopologyAffinityGateRepairsLegacy is the contract for the
// round-2 follow-up (S1): an existing Deployment/StatefulSet/LeaderWorkerSet
// whose stored pod template still carries the broken group-unique-hash
// exclusive affinity must be detected as "not equal" so its reconcile is not
// skipped and the affinity is repaired to group-uid after upgrade. On the
// previous head (3ae9d70b) podTemplateSpecEqual never compared Affinity, so
// the first assertion FAILS there; on the current head it must PASS. The
// idempotence and gate-scope assertions hold on every revision.
func TestVerifyExclusiveTopologyAffinityGateRepairsLegacy(t *testing.T) {
	const topologyKey = corev1.LabelHostname

	templateWithAffinity := func(selectorKey string) corev1.PodTemplateSpec {
		var tmpl corev1.PodTemplateSpec
		tmpl.Annotations = map[string]string{constants.GroupExclusiveTopologyKey: topologyKey}
		require.NoError(t, setExclusiveAffinities(&tmpl, "group-key", topologyKey, selectorKey))
		return tmpl
	}

	storedLegacy := templateWithAffinity(constants.GroupUniqueHashLabelKey) // pre-fix render
	desired := templateWithAffinity(constants.GroupUIDLabelKey)             // fixed render

	// The legacy stored template must be judged NOT equal so the reconcile
	// proceeds and rewrites the affinity. Red on 3ae9d70b, green at head.
	equal, err := podTemplateSpecEqual(storedLegacy, desired)
	assert.False(t, equal, "legacy group-unique-hash affinity must trigger repair, got equal=true (err=%v)", err)

	// After the repair the same comparison must be equal again: no perpetual
	// update loop.
	equal, err = podTemplateSpecEqual(desired, templateWithAffinity(constants.GroupUIDLabelKey))
	assert.True(t, equal, "repaired template must be stable, got err=%v", err)

	// Gate scope: with no exclusive-topology annotation on either template, an
	// affinity mismatch stays ignored (pre-existing podSpecEqual semantics for
	// non-exclusive workloads) — the repair gate is deliberately narrow.
	storedPlain := templateWithAffinity(constants.GroupUniqueHashLabelKey)
	delete(storedPlain.Annotations, constants.GroupExclusiveTopologyKey)
	desiredPlain := templateWithAffinity(constants.GroupUIDLabelKey)
	delete(desiredPlain.Annotations, constants.GroupExclusiveTopologyKey)
	equal, err = podTemplateSpecEqual(storedPlain, desiredPlain)
	assert.True(t, equal, "affinity drift without the annotation must stay ignored, got err=%v", err)
}
