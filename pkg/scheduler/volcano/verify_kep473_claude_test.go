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

// Reviewer harness for PR #492 (KEP-473 topology-aware scheduling).
// File: docs/verification/kep473-topology/ maps each test to a finding and polarity.
package volcano

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	coreapplyv1 "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/pkg/scheduler/common"
	volcanoschedulingv1beta1 "volcano.sh/apis/pkg/apis/scheduling/v1beta1"

	"github.com/stretchr/testify/require"
)

// verifyScheme can read RBG, CoordinatedPolicy, CRDs, and Volcano PodGroups.
func verifyScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	sch := runtime.NewScheme()
	require.NoError(t, workloadsv1alpha2.AddToScheme(sch))
	require.NoError(t, apiextensionsv1.AddToScheme(sch))
	require.NoError(t, volcanoschedulingv1beta1.AddToScheme(sch))
	return sch
}

func verifyPolicy(rules ...workloadsv1alpha2.CoordinatedPolicyRule) *workloadsv1alpha2.CoordinatedPolicy {
	return &workloadsv1alpha2.CoordinatedPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "rbg", Namespace: "default"},
		Spec:       workloadsv1alpha2.CoordinatedPolicySpec{Policies: rules},
	}
}

func verifyTopologyConstraint(level string) *workloadsv1alpha2.TopologyConstraint {
	return &workloadsv1alpha2.TopologyConstraint{
		Pack: &workloadsv1alpha2.TopologyPackConstraint{Required: ptr.To(level)},
	}
}

// TestVerifyF1GangSubsetPlanKeepsVolcanoSchedulerForUncoveredRole is a CONTRACT test
// for finding F1 (reviewer: Claude, PR #492).
//
// Claim: under a gang strategy scoped to a strict subset of the RBG's roles
// (CoordinatedPolicy spec.policies[].roles = [prefill], RBG roles = prefill+router),
// every role's pod template must carry spec.schedulerName=volcano, matching the
// KEP-430 contract the base branch enforced ("schedulerName is injected for every
// role: Volcano must own the scheduling decision for the whole group, otherwise
// excluded roles would be placed by a different scheduler and could starve the gang
// of resources").
//
// On the PR head this FAILS (red = reproduction): the placement injector skips
// schedulerName for the uncovered role. On a fixed head it must go green.
func TestVerifyF1GangSubsetPlanKeepsVolcanoSchedulerForUncoveredRole(t *testing.T) {
	rbg := rbgWithRoles(
		standaloneRole("prefill", 2, constants.RoleInstanceSetWorkloadType),
		standaloneRole("router", 1, constants.RoleInstanceSetWorkloadType),
	)
	rbg.UID = "rbg-uid"
	policy := verifyPolicy(workloadsv1alpha2.CoordinatedPolicyRule{
		Name:  "gang-prefill",
		Roles: []string{"prefill"},
		Strategy: workloadsv1alpha2.CoordinatedPolicyStrategy{
			Scheduling: &workloadsv1alpha2.SchedulingCoordinationStrategy{
				Gang: &workloadsv1alpha2.GangSchedulingStrategy{},
			},
		},
	})
	c := fake.NewClientBuilder().WithScheme(verifyScheme(t)).WithObjects(rbg, policy).Build()
	ctx := context.Background()

	gangStrategy, err := common.GetGangStrategy(ctx, c, rbg)
	require.NoError(t, err)
	require.NotNil(t, gangStrategy, "expected a gang strategy from the policy rule")
	require.True(t, gangStrategy.Roles.Has("prefill"), "gang should cover prefill only")

	plan, err := common.ResolvePlacementPlan(ctx, c, rbg, gangStrategy)
	require.NoError(t, err)
	require.NotNil(t, plan, "expected a gang-only plan")
	require.False(t, plan.HasTopology())
	require.Len(t, plan.TopLevelGroups(), 1)
	require.Equal(t, []string{"prefill"}, plan.TopLevelGroups()[0].Scope.Roles)

	m := New(c)

	// Covered role: must get schedulerName + the legacy rbg.Name PodGroup annotation.
	covered := &coreapplyv1.PodTemplateSpecApplyConfiguration{}
	m.InjectPlacementSchedulingFields(rbg, &rbg.Spec.Roles[0], plan, covered)
	require.NotNil(t, covered.Spec)
	require.NotNil(t, covered.Spec.SchedulerName)
	require.Equal(t, SchedulerName, *covered.Spec.SchedulerName)
	require.Equal(t, rbg.Name, covered.Annotations[AnnotationKey])

	// Uncovered role: KEP-430 contract requires schedulerName even outside the gang scope.
	uncovered := &coreapplyv1.PodTemplateSpecApplyConfiguration{}
	m.InjectPlacementSchedulingFields(rbg, &rbg.Spec.Roles[1], plan, uncovered)
	if uncovered.Spec == nil || uncovered.Spec.SchedulerName == nil || *uncovered.Spec.SchedulerName != SchedulerName {
		t.Errorf("F1 REPRODUCED: role %q outside the gang scope lost schedulerName=%q; got %#v",
			rbg.Spec.Roles[1].Name, SchedulerName, uncovered.Spec)
	}
	if uncovered.ObjectMetaApplyConfiguration != nil && uncovered.Annotations[AnnotationKey] != "" {
		t.Errorf("uncovered role must not be enrolled in the gang PodGroup, got annotation %q",
			uncovered.Annotations[AnnotationKey])
	}
}

// TestVerifyF1DifferentialLegacyInjectorStillInjects documents the differential for F1
// inside the same binary: the legacy KEP-430 injector (which the base branch used for
// this call site) still injects schedulerName for the uncovered role, while the new
// placement path used by the PR does not. The two code paths disagree on the head.
// PASSES on head; it exists as evidence, not as a regression guard.
func TestVerifyF1DifferentialLegacyInjectorStillInjects(t *testing.T) {
	rbg := rbgWithRoles(
		standaloneRole("prefill", 2, constants.RoleInstanceSetWorkloadType),
		standaloneRole("router", 1, constants.RoleInstanceSetWorkloadType),
	)
	gangStrategy := &common.GangStrategy{Roles: sets.New("prefill")}
	m := New(nil)

	legacy := &coreapplyv1.PodTemplateSpecApplyConfiguration{}
	m.InjectPodSchedulingFields(rbg, &rbg.Spec.Roles[1], gangStrategy, legacy)
	require.NotNil(t, legacy.Spec)
	require.NotNil(t, legacy.Spec.SchedulerName)
	require.Equal(t, SchedulerName, *legacy.Spec.SchedulerName)
	if legacy.ObjectMetaApplyConfiguration != nil && legacy.Annotations[AnnotationKey] != "" {
		t.Errorf("legacy path must not enroll uncovered roles in the PodGroup, got annotation %q",
			legacy.Annotations[AnnotationKey])
	}
}

// TestVerifyF2GangSupersetWithSubsetTopologyRuleRejected is a BUG-CANARY for finding F2.
//
// Claim: a gang covering {prefill, decode} plus a CoordinatedPolicy topology rule
// covering only {prefill} is rejected by the Volcano compiler as SchedulerUnsupported,
// which gates ALL role creation of the RBG (TopologyTranslated=False). This documents
// the current behavior: the KEP only promises nesting "when a backend can render it",
// and Volcano only renders per-instance single-role children.
//
// On the head it PASSES (documents behavior). If Volcano later learns to render
// nested cross-role scopes, this canary flips to fail and must be inverted into a
// contract test.
func TestVerifyF2GangSupersetWithSubsetTopologyRuleRejected(t *testing.T) {
	rbg := rbgWithRoles(
		standaloneRole("prefill", 2, constants.RoleInstanceSetWorkloadType),
		standaloneRole("decode", 2, constants.RoleInstanceSetWorkloadType),
	)
	rbg.UID = "rbg-uid"
	policy := verifyPolicy(workloadsv1alpha2.CoordinatedPolicyRule{
		Name:  "topo-prefill",
		Roles: []string{"prefill"},
		Strategy: workloadsv1alpha2.CoordinatedPolicyStrategy{
			Scheduling: &workloadsv1alpha2.SchedulingCoordinationStrategy{
				TopologyConstraint: verifyTopologyConstraint("rack"),
			},
		},
	})
	c := fake.NewClientBuilder().
		WithScheme(verifyScheme(t)).
		WithObjects(rbg, policy, hyperNodeCRD()).
		WithInterceptorFuncs(interceptor.Funcs{
			List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				if hyperNodes, ok := list.(*unstructured.UnstructuredList); ok {
					hyperNodes.SetAPIVersion("topology.volcano.sh/v1alpha1")
					hyperNodes.SetKind("HyperNodeList")
					hyperNodes.Items = []unstructured.Unstructured{testHyperNode("rack", "rack", 1)}
					return nil
				}
				return cl.List(ctx, list, opts...)
			},
		}).
		Build()
	ctx := context.Background()

	gangStrategy := &common.GangStrategy{Roles: sets.New("prefill", "decode")}
	plan, err := common.ResolvePlacementPlan(ctx, c, rbg, gangStrategy)
	require.NoError(t, err)
	require.True(t, plan.HasTopology())
	require.Len(t, plan.TopLevelGroups(), 1, "gang superset should contain the topology child")

	m := New(c)
	m.networkTopologySupported = true
	m.networkTopologyProbedAt = time.Now()
	m.topologySubGroupSupported = true
	m.topologySubGroupProbedAt = time.Now()
	watched := sync.Map{}
	watched.Store(CrdName, struct{}{})

	_, err = m.ReconcilePlacement(
		ctx, rbg, plan,
		&builder.TypedBuilder[reconcile.Request]{}, &watched, c,
	)
	require.Error(t, err, "F2: nested non-per-instance topology child must be rejected")
	require.True(t, common.IsSchedulerUnsupported(err), "expected SchedulerUnsupported, got %v", err)

	// The rejection message mislabels the single-role child as "cross-role"; this
	// documents the misleading wording (finding F2b, minor).
	require.True(t,
		strings.Contains(err.Error(), "cross-role topology child group roles [prefill]"),
		"expected the misleading cross-role wording for a single-role child, got: %v", err)

	podGroups := &volcanoschedulingv1beta1.PodGroupList{}
	require.NoError(t, c.List(ctx, podGroups, &client.ListOptions{Namespace: rbg.Namespace}))
	require.Empty(t, podGroups.Items, "no PodGroup may be rendered for a rejected plan")
}

// TestVerifyGangTopologyEqualScopeComposesOnePodGroup is a CONTRACT test guarding the
// KEP's core composition promise: gang and topology intents with the same scope merge
// into one PlacementGroup rendered as one Volcano PodGroup carrying both the gang
// minimum and the networkTopology. PASSES on head; keeps the composition from regressing.
func TestVerifyGangTopologyEqualScopeComposesOnePodGroup(t *testing.T) {
	rbg := rbgWithRoles(
		standaloneRole("prefill", 2, constants.RoleInstanceSetWorkloadType),
		standaloneRole("decode", 2, constants.RoleInstanceSetWorkloadType),
	)
	rbg.UID = "rbg-uid"
	policy := verifyPolicy(workloadsv1alpha2.CoordinatedPolicyRule{
		Name:  "pd",
		Roles: []string{"prefill", "decode"},
		Strategy: workloadsv1alpha2.CoordinatedPolicyStrategy{
			Scheduling: &workloadsv1alpha2.SchedulingCoordinationStrategy{
				Gang:              &workloadsv1alpha2.GangSchedulingStrategy{},
				TopologyConstraint: verifyTopologyConstraint("block"),
			},
		},
	})
	c := fake.NewClientBuilder().
		WithScheme(verifyScheme(t)).
		WithObjects(rbg, policy, hyperNodeCRD()).
		WithInterceptorFuncs(interceptor.Funcs{
			List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				if hyperNodes, ok := list.(*unstructured.UnstructuredList); ok {
					hyperNodes.SetAPIVersion("topology.volcano.sh/v1alpha1")
					hyperNodes.SetKind("HyperNodeList")
					hyperNodes.Items = []unstructured.Unstructured{testHyperNode("block", "block", 2)}
					return nil
				}
				return cl.List(ctx, list, opts...)
			},
		}).
		Build()
	ctx := context.Background()

	gangStrategy, err := common.GetGangStrategy(ctx, c, rbg)
	require.NoError(t, err)
	require.NotNil(t, gangStrategy)

	plan, err := common.ResolvePlacementPlan(ctx, c, rbg, gangStrategy)
	require.NoError(t, err)
	require.True(t, plan.HasTopology())
	require.Len(t, plan.TopLevelGroups(), 1)
	group := plan.TopLevelGroups()[0]
	require.NotNil(t, group.Gang)
	require.NotNil(t, group.Topology)

	m := New(c)
	m.networkTopologySupported = true
	m.networkTopologyProbedAt = time.Now()
	watched := sync.Map{}
	watched.Store(CrdName, struct{}{})

	result, err := m.ReconcilePlacement(
		ctx, rbg, plan,
		&builder.TypedBuilder[reconcile.Request]{}, &watched, c,
	)
	require.NoError(t, err)
	require.NotNil(t, result)

	podGroups := &volcanoschedulingv1beta1.PodGroupList{}
	require.NoError(t, c.List(ctx, podGroups, &client.ListOptions{Namespace: rbg.Namespace}))
	require.Len(t, podGroups.Items, 1)
	rendered := podGroups.Items[0]
	require.NotEqual(t, rbg.Name, rendered.Name, "gang+topology scope must not reuse the legacy gang PodGroup name")
	require.Greater(t, rendered.Spec.MinMember, int32(0), "gang minimum must be preserved on the composed PodGroup")
	require.NotNil(t, rendered.Spec.NetworkTopology)
	require.Equal(t, "block", rendered.Spec.NetworkTopology.HighestTierName)

	// Every covered role's pod template must be annotated with the rendered PodGroup name,
	// and the annotation must match the object the compiler created.
	for i := range rbg.Spec.Roles {
		pts := &coreapplyv1.PodTemplateSpecApplyConfiguration{}
		m.InjectPlacementSchedulingFields(rbg, &rbg.Spec.Roles[i], plan, pts)
		require.NotNil(t, pts.Spec.SchedulerName)
		require.Equal(t, SchedulerName, *pts.Spec.SchedulerName)
		require.Equal(t, rendered.Name, pts.Annotations[AnnotationKey],
			"pod annotation must reference the rendered PodGroup")
	}
}
