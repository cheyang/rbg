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

// Reviewer A (Claude) verification harness for PR #588, round 2 (head 29fa5faa
// "fix: migrate exclusive topology affinity on upgrade").
//
// Docs: docs/verification/exclusive-topology-claude/README.md
//
// Round-2 claim (S1-fix, polarity: contract, layer: L1): a Deployment or
// StatefulSet created by the pre-#588 controller carries the exclusive
// affinity on the LEGACY selector key constants.GroupUniqueHashLabelKey. On
// the previous head (3ae9d70b) the shared podTemplateSpecEqual never compared
// Affinity, so deploy/sts/lws reconcile skipped the apply as "unchanged" and
// the legacy affinity persisted after the controller upgrade (S1). Head
// 29fa5faa adds the affinity comparison in podTemplateSpecEqual, so the same
// reconcile must now rewrite the stored affinity to the group-uid selector.
//
// The tests below drive the REAL DeploymentReconciler / StatefulSetReconciler
// (fake client) through the full Reconciler entry point: create, corrupt the
// stored affinity to the legacy form, reconcile again, assert the repair and
// the idempotence (a further reconcile must not touch the object). On the
// previous head 3ae9d70b the repair assertions are RED (the skip path fires);
// on the PR head they must be GREEN.
//
// Annotation-removal direction (guards the asymmetry of the new guard, which
// only checks template2's group-exclusive-topology annotation): removing the
// annotation from the RBG must drop the injected affinity from the stored
// workload. objectMetaEqual (which compares annotations, rbg.* not filtered)
// is what catches this; it held on both heads.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	wrappersv2 "sigs.k8s.io/rbgs/test/wrappers/v1alpha2"
)

const verifyClaudeRound2RevisionHash = "round2-revision-hash"

func verifyClaudeRound2Scheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))
	return scheme
}

// verifyClaudeLegacyAffinity returns the affinity shape the pre-#588
// controller persisted: identical terms, but on GroupUniqueHashLabelKey.
func verifyClaudeLegacyAffinity(rbg *workloadsv1alpha2.RoleBasedGroup) *corev1.Affinity {
	group := []string{rbg.GenGroupUniqueKey()}
	return &corev1.Affinity{
		PodAffinity: &corev1.PodAffinity{RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{{
			TopologyKey:   corev1.LabelHostname,
			LabelSelector: &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{Key: constants.GroupUniqueHashLabelKey, Operator: metav1.LabelSelectorOpIn, Values: group}}},
		}}},
		PodAntiAffinity: &corev1.PodAntiAffinity{RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{{
			TopologyKey: corev1.LabelHostname,
			LabelSelector: &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{
				{Key: constants.GroupUniqueHashLabelKey, Operator: metav1.LabelSelectorOpExists},
				{Key: constants.GroupUniqueHashLabelKey, Operator: metav1.LabelSelectorOpNotIn, Values: group},
			}},
		}}},
	}
}

// TestVerifyClaude_LegacyAffinityDeploymentRepair is the S1-fix contract for
// the Deployment skip path (deploy_reconciler.go: semanticallyEqual &&
// revisionHashEqual && controllerRef present => skip).
//
// RED on previous head 3ae9d70b (affinity-blind equality => legacy affinity
// kept), must be GREEN on the PR head.
func TestVerifyClaude_LegacyAffinityDeploymentRepair(t *testing.T) {
	ctx := context.Background()
	scheme := verifyClaudeRound2Scheme(t)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	reconciler := NewDeploymentReconciler(scheme, fakeClient)

	rbg := wrappersv2.BuildBasicRoleBasedGroup("round2-deploy", "default").
		WithAnnotations(map[string]string{constants.GroupExclusiveTopologyKey: corev1.LabelHostname}).Obj()
	role := &rbg.Spec.Roles[0]
	role.Annotations = map[string]string{constants.RoleWorkloadTypeAnnotationKey: "apps/v1/Deployment"}
	key := types.NamespacedName{Namespace: rbg.Namespace, Name: rbg.GetWorkloadName(role)}

	// 1. Fresh create: the workload gets the group-uid affinity.
	require.NoError(t, reconciler.Reconciler(ctx, rbg, role, nil, verifyClaudeRound2RevisionHash))
	deploy := &appsv1.Deployment{}
	require.NoError(t, fakeClient.Get(ctx, key, deploy))
	require.NotNil(t, deploy.Spec.Template.Spec.Affinity, "fresh exclusive deployment must carry injected affinity")

	// 2. Simulate the pre-#588 controller's persisted state: same owner,
	// same selector, same revision-hash label, LEGACY affinity selector.
	deploy.UID = "round2-stored-deployment-uid"
	deploy.Spec.Template.Spec.Affinity = verifyClaudeLegacyAffinity(rbg)
	require.NoError(t, fakeClient.Update(ctx, deploy))

	// 3. Reconcile again with the same revision hash: the only base->head
	// diff is the affinity selector, exactly the S1 scenario.
	require.NoError(t, reconciler.Reconciler(ctx, rbg, role, nil, verifyClaudeRound2RevisionHash))
	require.NoError(t, fakeClient.Get(ctx, key, deploy))

	fixed := deploy.Spec.Template.Spec.Affinity
	require.NotNil(t, fixed, "the reconcile must not silently drop the injected affinity")
	require.NotNil(t, fixed.PodAffinity)
	terms := fixed.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution
	require.NotEmpty(t, terms)
	for _, term := range terms {
		require.Equal(t, corev1.LabelHostname, term.TopologyKey)
		for _, expr := range term.LabelSelector.MatchExpressions {
			require.Equal(t, constants.GroupUIDLabelKey, expr.Key,
				"S1: the stored legacy group-unique-hash affinity must be migrated to group-uid")
		}
		require.Equal(t, rbg.GenGroupUniqueKey(), term.LabelSelector.MatchExpressions[0].Values[0])
	}

	// 4. Idempotence: a further reconcile must not touch the workload.
	resourceVersion := deploy.ResourceVersion
	require.NoError(t, reconciler.Reconciler(ctx, rbg, role, nil, verifyClaudeRound2RevisionHash))
	require.NoError(t, fakeClient.Get(ctx, key, deploy))
	require.Equal(t, resourceVersion, deploy.ResourceVersion,
		"after the migration a further reconcile must take the skip path (no churn)")
}

// TestVerifyClaude_LegacyAffinityStatefulSetRepair is the same S1-fix
// contract for the StatefulSet skip path (sts_reconciler.go:153).
func TestVerifyClaude_LegacyAffinityStatefulSetRepair(t *testing.T) {
	ctx := context.Background()
	scheme := verifyClaudeRound2Scheme(t)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	reconciler := NewStatefulSetReconciler(scheme, fakeClient)

	rbg := wrappersv2.BuildBasicRoleBasedGroup("round2-sts", "default").
		WithAnnotations(map[string]string{constants.GroupExclusiveTopologyKey: corev1.LabelHostname}).Obj()
	role := wrappersv2.BuildStandaloneRole("worker").WithWorkload("apps/v1", "StatefulSet").Obj()
	stsRole := &role
	rbg.Spec.Roles[0] = role
	key := types.NamespacedName{Namespace: rbg.Namespace, Name: rbg.GetWorkloadName(stsRole)}

	require.NoError(t, reconciler.Reconciler(ctx, rbg, stsRole, nil, verifyClaudeRound2RevisionHash))
	sts := &appsv1.StatefulSet{}
	require.NoError(t, fakeClient.Get(ctx, key, sts))
	require.NotNil(t, sts.Spec.Template.Spec.Affinity, "fresh exclusive statefulset must carry injected affinity")

	sts.UID = "round2-stored-sts-uid"
	sts.Spec.Template.Spec.Affinity = verifyClaudeLegacyAffinity(rbg)
	require.NoError(t, fakeClient.Update(ctx, sts))

	// The rolling-update parameters need at least one ControllerRevision
	// owned by the stored StatefulSet (the real STS controller maintains
	// these; the fake client has none).
	revision := &appsv1.ControllerRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sts.Name + "-round2-1",
			Namespace: sts.Namespace,
			Labels:    rbg.GetCommonLabelsFromRole(stsRole),
		},
		Data:     runtime.RawExtension{Raw: []byte(`{}`)},
		Revision: 1,
	}
	require.NoError(t, fakeClient.Create(ctx, revision))

	require.NoError(t, reconciler.Reconciler(ctx, rbg, stsRole, nil, verifyClaudeRound2RevisionHash))
	require.NoError(t, fakeClient.Get(ctx, key, sts))

	fixed := sts.Spec.Template.Spec.Affinity
	require.NotNil(t, fixed)
	require.NotNil(t, fixed.PodAffinity)
	terms := fixed.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution
	require.NotEmpty(t, terms)
	for _, term := range terms {
		require.Equal(t, corev1.LabelHostname, term.TopologyKey)
		for _, expr := range term.LabelSelector.MatchExpressions {
			require.Equal(t, constants.GroupUIDLabelKey, expr.Key,
				"S1: the stored legacy group-unique-hash affinity must be migrated to group-uid")
		}
		require.Equal(t, rbg.GenGroupUniqueKey(), term.LabelSelector.MatchExpressions[0].Values[0])
	}

	resourceVersion := sts.ResourceVersion
	require.NoError(t, reconciler.Reconciler(ctx, rbg, stsRole, nil, verifyClaudeRound2RevisionHash))
	require.NoError(t, fakeClient.Get(ctx, key, sts))
	require.Equal(t, resourceVersion, sts.ResourceVersion,
		"after the migration a further reconcile must take the skip path (no churn)")
}

// TestVerifyClaude_ExclusiveTopologyAnnotationRemovalDropsDeployAffinity
// guards the asymmetry of the round-2 guard in podTemplateSpecEqual: it only
// inspects template2's (desired) group-exclusive-topology annotation, so the
// removal direction relies on objectMetaEqual catching the annotation
// difference. Contract: removing the annotation from the RBG must leave the
// stored Deployment without the injected affinity. Expected GREEN on both the
// previous and current heads.
func TestVerifyClaude_ExclusiveTopologyAnnotationRemovalDropsDeployAffinity(t *testing.T) {
	ctx := context.Background()
	scheme := verifyClaudeRound2Scheme(t)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	reconciler := NewDeploymentReconciler(scheme, fakeClient)

	rbg := wrappersv2.BuildBasicRoleBasedGroup("round2-remove", "default").
		WithAnnotations(map[string]string{constants.GroupExclusiveTopologyKey: corev1.LabelHostname}).Obj()
	role := &rbg.Spec.Roles[0]
	role.Annotations = map[string]string{constants.RoleWorkloadTypeAnnotationKey: "apps/v1/Deployment"}
	key := types.NamespacedName{Namespace: rbg.Namespace, Name: rbg.GetWorkloadName(role)}

	require.NoError(t, reconciler.Reconciler(ctx, rbg, role, nil, verifyClaudeRound2RevisionHash))
	deploy := &appsv1.Deployment{}
	require.NoError(t, fakeClient.Get(ctx, key, deploy))
	deploy.UID = "round2-removal-deployment-uid"
	require.NoError(t, fakeClient.Update(ctx, deploy))
	require.NotNil(t, deploy.Spec.Template.Spec.Affinity)

	// Remove the annotation, keep everything else identical.
	delete(rbg.Annotations, constants.GroupExclusiveTopologyKey)
	require.NoError(t, reconciler.Reconciler(ctx, rbg, role, nil, verifyClaudeRound2RevisionHash))
	require.NoError(t, fakeClient.Get(ctx, key, deploy))
	require.Nil(t, deploy.Spec.Template.Spec.Affinity,
		"removing the exclusive-topology annotation must drop the injected affinity from the stored workload")
}

// TestVerifyClaude_PodTemplateSpecEqualAffinityGuard drives the shared
// predicate directly for the three shapes that matter:
//
//   - legacy-vs-fixed affinity with the annotation present => NOT equal
//     (repair must fire) — RED on previous head, GREEN on this head;
//   - identical affinity with the annotation present => equal (no churn);
//   - annotation absent on the desired template => affinity is not compared
//     (documented asymmetry; removal is caught by objectMetaEqual).
func TestVerifyClaude_PodTemplateSpecEqualAffinityGuard(t *testing.T) {
	rbg := wrappersv2.BuildBasicRoleBasedGroup("round2-predicate", "default").Obj()
	base := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{constants.GroupExclusiveTopologyKey: corev1.LabelHostname},
		},
	}

	fixedTemplate := base.DeepCopy()
	require.NoError(t, setExclusiveAffinities(fixedTemplate, rbg.GenGroupUniqueKey(), corev1.LabelHostname, constants.GroupUIDLabelKey))

	legacyTemplate := base.DeepCopy()
	require.NoError(t, setExclusiveAffinities(legacyTemplate, rbg.GenGroupUniqueKey(), corev1.LabelHostname, constants.GroupUniqueHashLabelKey))

	equal, err := podTemplateSpecEqual(*legacyTemplate, *fixedTemplate)
	require.Error(t, err, "legacy group-unique-hash affinity vs fixed group-uid affinity must be reported as a difference")
	require.False(t, equal)

	equal, err = podTemplateSpecEqual(*fixedTemplate, *fixedTemplate.DeepCopy())
	require.NoError(t, err)
	require.True(t, equal, "identical templates with the annotation must compare equal (no reconcile churn)")

	// No annotation on the desired template: the guard is skipped and
	// affinity is (still) not compared by podSpecEqual. The removal case is
	// caught one level up by objectMetaEqual, covered by the dedicated test.
	noAnnotation := corev1.PodTemplateSpec{}
	noAnnotation.Spec.Affinity = verifyClaudeLegacyAffinity(rbg)
	equal, err = podTemplateSpecEqual(noAnnotation, corev1.PodTemplateSpec{})
	require.NoError(t, err)
	require.True(t, equal, "without the desired-template annotation the guard is inactive (removal relies on objectMetaEqual)")
}
