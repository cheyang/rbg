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

// TestVerifyF1GangOnlyPlanInjectionMatchesLegacy is a CONTRACT test (round 2
// conversion of the former F1-diff canary). The F1 fix routes gang-only plans back
// through the legacy KEP-430 injector, so the placement injector and the legacy
// injector must produce identical scheduling fields for every role — covered roles
// get schedulerName + the rbg.Name PodGroup annotation, uncovered roles get
// schedulerName only. PASSES on a fixed head; red means the backends diverged again.
func TestVerifyF1GangOnlyPlanInjectionMatchesLegacy(t *testing.T) {
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
	require.NotNil(t, gangStrategy)

	plan, err := common.ResolvePlacementPlan(ctx, c, rbg, gangStrategy)
	require.NoError(t, err)
	require.False(t, plan.HasTopology())

	m := New(nil)
	readAnnotation := func(pts *coreapplyv1.PodTemplateSpecApplyConfiguration) string {
		if pts.ObjectMetaApplyConfiguration == nil {
			return ""
		}
		return pts.Annotations[AnnotationKey]
	}
	for i := range rbg.Spec.Roles {
		placement := &coreapplyv1.PodTemplateSpecApplyConfiguration{}
		m.InjectPlacementSchedulingFields(rbg, &rbg.Spec.Roles[i], plan, placement)

		legacy := &coreapplyv1.PodTemplateSpecApplyConfiguration{}
		m.InjectPodSchedulingFields(rbg, &rbg.Spec.Roles[i], gangStrategy, legacy)

		require.NotNil(t, placement.Spec, "role %s: placement injector set nothing", rbg.Spec.Roles[i].Name)
		require.NotNil(t, placement.Spec.SchedulerName,
			"role %s: placement injector lost schedulerName", rbg.Spec.Roles[i].Name)
		require.Equal(t, SchedulerName, *placement.Spec.SchedulerName,
			"role %s: placement injector must send every role to Volcano", rbg.Spec.Roles[i].Name)
		require.NotNil(t, legacy.Spec.SchedulerName,
			"role %s: legacy injector set nothing", rbg.Spec.Roles[i].Name)
		require.Equal(t, *legacy.Spec.SchedulerName, *placement.Spec.SchedulerName,
			"role %s: placement and legacy injectors disagree on schedulerName", rbg.Spec.Roles[i].Name)
		require.Equal(t, readAnnotation(legacy), readAnnotation(placement),
			"role %s: placement and legacy injectors disagree on the PodGroup annotation",
			rbg.Spec.Roles[i].Name)
	}
}

// TestVerifyF2CrossRoleTopologyChildRendersFlatSubGroupPolicy is a CONTRACT test
// (round 2 promotion of the former F2 canary). The head now renders the containment
// case — a whole-group gang parent with a cross-role topology child — instead of
// rejecting it as SchedulerUnsupported: the gang parent keeps the KEP-430
// rbg.Name PodGroup, and the topology child is rendered as one flat subGroupPolicy
// entry selecting the child roles with a role-name In expression and carrying the
// child networkTopology. Red means the rendering regressed back to rejection.
func TestVerifyF2CrossRoleTopologyChildRendersFlatSubGroupPolicy(t *testing.T) {
	rbg := rbgWithRoles(
		standaloneRole("prefill", 2, constants.RoleInstanceSetWorkloadType),
		standaloneRole("decode", 2, constants.RoleInstanceSetWorkloadType),
		standaloneRole("router", 1, constants.RoleInstanceSetWorkloadType),
	)
	rbg.UID = "rbg-uid"
	policy := verifyPolicy(workloadsv1alpha2.CoordinatedPolicyRule{
		Name:  "topo-pd",
		Roles: []string{"prefill", "decode"},
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

	gangStrategy := &common.GangStrategy{Roles: sets.New("prefill", "decode", "router")}
	plan, err := common.ResolvePlacementPlan(ctx, c, rbg, gangStrategy)
	require.NoError(t, err)
	require.True(t, plan.HasTopology())
	require.Len(t, plan.TopLevelGroups(), 1, "gang superset should contain the topology child")
	require.NotNil(t, plan.TopLevelGroups()[0].Children,
		"the topology rule must nest beneath the gang parent")
	require.Len(t, plan.TopLevelGroups()[0].Children, 1)

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
	require.NoError(t, err, "the cross-role containment case must render, not be rejected: %v", err)

	podGroups := &volcanoschedulingv1beta1.PodGroupList{}
	require.NoError(t, c.List(ctx, podGroups, &client.ListOptions{Namespace: rbg.Namespace}))
	require.Len(t, podGroups.Items, 1)
	rendered := podGroups.Items[0]
	require.Equal(t, rbg.Name, rendered.Name,
		"a gang parent without its own topology keeps the KEP-430 PodGroup name")
	require.NotEmpty(t, rendered.Spec.SubGroupPolicy,
		"the topology child must be rendered as a flat subGroupPolicy entry")
	found := false
	for _, sub := range rendered.Spec.SubGroupPolicy {
		if sub.Name != "policy-topo-pd" {
			continue
		}
		found = true
		require.NotNil(t, sub.LabelSelector, "cross-role subgroup must select pods")
		require.Len(t, sub.LabelSelector.MatchExpressions, 1)
		in := sub.LabelSelector.MatchExpressions[0]
		require.Equal(t, constants.RoleNameLabelKey, in.Key)
		require.Equal(t, metav1.LabelSelectorOpIn, in.Operator)
		require.ElementsMatch(t, []string{"prefill", "decode"}, in.Values)
		require.NotNil(t, sub.NetworkTopology, "child networkTopology must be carried")
		require.Equal(t, "rack", sub.NetworkTopology.HighestTierName)
	}
	require.True(t, found, "expected a policy-topo-pd subgroup, got %+v", rendered.Spec.SubGroupPolicy)
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

// TestVerifyF3TopologyContractStableAcrossNonTopologyRoleChanges is a CONTRACT test
// for finding F3 (round 2, head dab87262).
//
// Claim: admission validation explicitly allows changes to roles that never carry a
// topology constraint ("Ordinary changes to roles that never carry a topology
// constraint remain allowed" — ValidateRoleTopologyImmutability). But for an RBG
// whose placement plan combines a whole-group gang with a role-level topology
// constraint, TopologyContractID hashes the gang scope's full role set, so adding or
// removing ANY role changes the contract ID. The controller then reports
// TopologyPolicyLate ("delete and recreate the RoleBasedGroup") and gates role
// reconciliation — an admission-allowed operation bricks the RBG.
//
// On the head this FAILS (red = reproduction): the two contract IDs differ although
// the topology declarations are identical. A fixed head must go green.
func TestVerifyF3TopologyContractStableAcrossNonTopologyRoleChanges(t *testing.T) {
	rbg := rbgWithRoles(
		standaloneRole("prefill", 2, constants.RoleInstanceSetWorkloadType),
		standaloneRole("decode", 2, constants.RoleInstanceSetWorkloadType),
		standaloneRole("router", 1, constants.RoleInstanceSetWorkloadType),
	)
	rbg.UID = "rbg-uid"
	rbg.Spec.Roles[1].InstanceTopologyConstraint = verifyTopologyConstraint("rack")

	// No policy needed: the role-level constraint provides the topology, and an
	// empty-roles GangStrategy is the legacy whole-group gang (scope = all RBG roles).
	gangStrategy := &common.GangStrategy{}
	c := fake.NewClientBuilder().WithScheme(verifyScheme(t)).WithObjects(rbg).Build()
	ctx := context.Background()

	before, err := common.ResolvePlacementPlan(ctx, c, rbg, gangStrategy)
	require.NoError(t, err)
	require.True(t, before.HasTopology(), "role-level constraint must make the plan topology-bearing")

	// Operator removes the router role — a role with no topology constraint, so
	// admission validation allows it.
	rbgAfter := rbg.DeepCopy()
	rbgAfter.Spec.Roles = rbgAfter.Spec.Roles[:2]
	require.NoError(t, workloadsv1alpha2.ValidateRoleTopologyImmutability(rbg, rbgAfter),
		"admission allows removing a role that never carried a topology constraint")

	after, err := common.ResolvePlacementPlan(ctx, c, rbgAfter, gangStrategy)
	require.NoError(t, err)
	require.True(t, after.HasTopology())

	if before.TopologyContractID() != after.TopologyContractID() {
		t.Errorf("F3 REPRODUCED: the topology declarations are unchanged, but the contract ID changed "+
			"(%q -> %q) because the whole-group gang scope includes the removed role; "+
			"ensureTopologyContract will report TopologyPolicyLate and gate role reconciliation",
			before.TopologyContractID(), after.TopologyContractID())
	}
}

// TestVerifyF5TopologyContractEncodingIsAmbiguous is a CONTRACT test for finding F5
// (round 2, minor). TopologyContractID builds its preimage by concatenating fields
// with unescaped separators (';', ':' and empty delimiters), and topologyName is a
// free-form identifier (validateTopologyIdentifier only checks non-empty and length).
// A topologyName containing the literal separators can therefore alias a different
// plan structure, so two semantically different declarations can share one contract
// ID and a change between them escapes the freeze. On the head this FAILS (red).
func TestVerifyF5TopologyContractEncodingIsAmbiguous(t *testing.T) {
	weird := &common.PlacementPlan{Root: &common.PlacementGroup{
		Scope: common.PlacementScope{Roles: []string{"a"}},
		Topology: &workloadsv1alpha2.TopologyConstraint{
			// 47 chars, non-empty, under 253 — passes validateTopologyIdentifier.
			TopologyName: ptr.To("t;required:b;child:roles:b;partition:;gang:0;topology:"),
			Pack:         &workloadsv1alpha2.TopologyPackConstraint{Required: ptr.To("r")},
		},
	}}
	structured := &common.PlacementPlan{Root: &common.PlacementGroup{
		Scope:    common.PlacementScope{Roles: []string{"a"}},
		Topology: &workloadsv1alpha2.TopologyConstraint{TopologyName: ptr.To("t"), Pack: &workloadsv1alpha2.TopologyPackConstraint{Required: ptr.To("b")}},
		Children: []*common.PlacementGroup{{
			Scope:    common.PlacementScope{Roles: []string{"b"}},
			Topology: &workloadsv1alpha2.TopologyConstraint{Pack: &workloadsv1alpha2.TopologyPackConstraint{Required: ptr.To("r")}},
		}},
	}}
	if weird.TopologyContractID() == structured.TopologyContractID() {
		t.Errorf("F5 REPRODUCED: two semantically different plans share one contract ID (%q); "+
			"a change between them is invisible to the freeze", weird.TopologyContractID())
	}
}
