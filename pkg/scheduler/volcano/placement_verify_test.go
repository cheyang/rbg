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

package volcano

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/pkg/scheduler/common"
)

func verifyScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := workloadsv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return scheme
}

// Reviewer verification harness for PR #473 (reviewer A / Claude).
//
// F-NAME-COLLISION (contract test): two sibling placement groups in one RBG whose
// derived placement names share the first 22 characters must render to DISTINCT
// physical PodGroup names. On the PR code this test FAILS, which is the
// reproduction: boundedPlacementPodGroupName truncates placementName to 22 chars and
// the RBG UID hash does not distinguish scopes within one RBG, so the two logical
// groups collapse onto one physical PodGroup and the second apply overwrites the
// first group's spec while pods of both groups share one membership annotation.
func TestVerifySiblingPlacementGroupsGetDistinctPodGroupNames(t *testing.T) {
	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "llama-serving", Namespace: "default", UID: "rbg-uid-1"},
	}

	// Two cross-role topology rules whose "p-"-prefixed rule names are distinct but
	// share their first 22 characters (both are 31 chars long).
	groupOne := &common.PlacementGroup{
		ID:   "scope-hash-aaaaaaaaaaaaaaaa",
		Name: "p-pd-disaggregation-group-one",
		Scope: common.PlacementScope{
			Roles: []string{"prefill", "decode"},
		},
		Topology: &workloadsv1alpha2.TopologyConstraint{},
	}
	groupTwo := &common.PlacementGroup{
		ID:   "scope-hash-bbbbbbbbbbbbbbbb",
		Name: "p-pd-disaggregation-group-two",
		Scope: common.PlacementScope{
			Roles: []string{"router"},
		},
		Topology: &workloadsv1alpha2.TopologyConstraint{},
	}

	nameOne := placementPodGroupName(rbg, groupOne, 0)
	nameTwo := placementPodGroupName(rbg, groupTwo, 1)

	if nameOne == nameTwo {
		t.Fatalf("F-NAME-COLLISION reproduced: sibling placement groups render to the same PodGroup name %q; "+
			"pods of scopes %v and %v would share one physical PodGroup and the second apply silently "+
			"overwrites the first group's spec (source names: %q vs %q)",
			nameOne, groupOne.Scope.Roles, groupTwo.Scope.Roles, groupOne.Name, groupTwo.Name)
	}
}

// F-NAME-COLLISION (boundary): short names under the truncation threshold must stay
// distinct, pinning that the collision only starts past the 22-character budget.
func TestVerifyShortPlacementNamesRemainDistinct(t *testing.T) {
	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "llama-serving", Namespace: "default", UID: "rbg-uid-1"},
	}
	groupOne := &common.PlacementGroup{
		ID:   "scope-hash-aaaaaaaaaaaaaaaa",
		Name: "p-pd-one",
		Scope: common.PlacementScope{
			Roles: []string{"prefill", "decode"},
		},
		Topology: &workloadsv1alpha2.TopologyConstraint{},
	}
	groupTwo := &common.PlacementGroup{
		ID:   "scope-hash-bbbbbbbbbbbbbbbb",
		Name: "p-router",
		Scope: common.PlacementScope{
			Roles: []string{"router"},
		},
		Topology: &workloadsv1alpha2.TopologyConstraint{},
	}
	if placementPodGroupName(rbg, groupOne, 0) == placementPodGroupName(rbg, groupTwo, 1) {
		t.Fatalf("short distinct placement names must not collide")
	}
}

// F-COMPOSITION (bug-canary, current-behavior pin): a whole-group gang scope with a
// cross-role topology rule covering a SUBSET of the roles forms a parent/child tree
// in the logical plan (contained scope), but the Volcano compiler rejects that tree
// with SchedulerUnsupported. This canary asserts the current rejection so a future
// fix that makes the composition renderable flips it to red. The PR body's claim
// "Gang and topology can compose on the same logical scope" holds today only when
// the two scopes are IDENTICAL; subset composition fails on the only implemented
// backend (Volcano).
func TestVerifyGangWholeGroupPlusSubsetTopologyRuleIsRejected(t *testing.T) {
	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "llama-serving", Namespace: "default"},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{
			Roles: []workloadsv1alpha2.RoleSpec{
				{Name: "prefill"},
				{Name: "decode"},
				{Name: "router"},
			},
		},
	}

	// Gang covers the whole group (legacy KEP-430 empty-roles expansion).
	gangStrategy := &common.GangStrategy{Roles: nil, MinReplicas: nil}
	policy := &workloadsv1alpha2.CoordinatedPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "llama-serving", Namespace: "default"},
	}
	// Topology rule covering a strict subset of the gang's roles.
	rule := workloadsv1alpha2.CoordinatedPolicyRule{
		Name:  "pd-block",
		Roles: []string{"prefill", "decode"},
	}
	rule.Strategy.Scheduling = &workloadsv1alpha2.SchedulingCoordinationStrategy{
		TopologyConstraint: &workloadsv1alpha2.TopologyConstraint{
			Pack: &workloadsv1alpha2.TopologyPackConstraint{Required: ptr.To("block")},
		},
	}
	policy.Spec.Policies = append(policy.Spec.Policies, rule)

	var c client.Client = fake.NewClientBuilder().WithScheme(verifyScheme(t)).WithObjects(policy).Build()
	plan, err := common.ResolvePlacementPlan(context.Background(), c, rbg, gangStrategy)
	if err != nil {
		t.Fatalf("logical plan must resolve for a contained scope: %v", err)
	}
	if plan == nil || !plan.HasTopology() {
		t.Fatalf("expected a topology-bearing plan")
	}
	groups := plan.TopLevelGroups()
	if len(groups) != 1 {
		t.Fatalf("expected one top-level gang root, got %d", len(groups))
	}
	if len(groups[0].Children) != 1 {
		t.Fatalf("expected the topology rule as a child of the gang root, got %d children", len(groups[0].Children))
	}

	// Canary: the Volcano compiler currently rejects this composition.
	_, _, compileErr := buildTopologySubGroups(rbg, groups[0])
	if compileErr == nil {
		t.Fatalf("F-COMPOSITION canary flipped: gang whole-group + subset topology rule now compiles; "+
			"invert this canary into a contract test for the rendered PodGroup")
	}
	if !common.IsSchedulerUnsupported(compileErr) {
		t.Fatalf("expected SchedulerUnsupported, got %v", compileErr)
	}
	if !strings.Contains(compileErr.Error(), "cannot render topology child group roles") {
		t.Fatalf("unexpected error message: %v", compileErr)
	}
}
