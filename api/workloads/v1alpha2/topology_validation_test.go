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

package v1alpha2

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/rbgs/api/workloads/constants"
)

func TestValidateRoleTopologyConstraintsRejectsEmptyLevel(t *testing.T) {
	rbg := &RoleBasedGroup{Spec: RoleBasedGroupSpec{Roles: []RoleSpec{{
		Name:                       "prefill",
		InstanceTopologyConstraint: &TopologyConstraint{Pack: &TopologyPackConstraint{Required: ptrString("")}},
	}}}}
	err := ValidateRoleTopologyConstraints(rbg)
	if err == nil || !strings.Contains(err.Error(), "must not be empty") {
		t.Fatalf("expected empty level error, got %v", err)
	}
}

func TestValidateRoleTopologyImmutabilityAfterPlacement(t *testing.T) {
	old := &RoleBasedGroup{
		Status: StatusWithTopologyConditionActive(),
		Spec: RoleBasedGroupSpec{Roles: []RoleSpec{{
			Name:                       "prefill",
			InstanceTopologyConstraint: &TopologyConstraint{Pack: &TopologyPackConstraint{Required: ptrString("rack")}},
		}}},
	}
	changed := old.DeepCopy()
	changed.Spec.Roles[0].InstanceTopologyConstraint.Pack.Required = ptrString("block")
	if err := ValidateRoleTopologyImmutability(old, changed); err == nil {
		t.Fatal("expected immutable topology error")
	}
}

func TestValidateCoordinatedPolicyTopologyRejectsRoleInTwoRules(t *testing.T) {
	policy := &CoordinatedPolicy{Spec: CoordinatedPolicySpec{Policies: []CoordinatedPolicyRule{
		{
			Name:  "a",
			Roles: []string{"prefill"},
			Strategy: CoordinatedPolicyStrategy{Scheduling: &SchedulingCoordinationStrategy{
				TopologyConstraint: &TopologyConstraint{Pack: &TopologyPackConstraint{Required: ptrString("rack")}},
			}},
		},
		{
			Name:  "b",
			Roles: []string{"prefill"},
			Strategy: CoordinatedPolicyStrategy{Scheduling: &SchedulingCoordinationStrategy{
				TopologyConstraint: &TopologyConstraint{Pack: &TopologyPackConstraint{Required: ptrString("block")}},
			}},
		},
	}}}
	err := ValidateCoordinatedPolicyTopology(policy)
	if err == nil || !strings.Contains(err.Error(), "at most one topology-bearing rule") {
		t.Fatalf("expected overlap error, got %v", err)
	}
}

func StatusWithTopologyConditionActive() RoleBasedGroupStatus {
	return RoleBasedGroupStatus{Conditions: []metav1.Condition{{
		Type:               string(RoleBasedGroupTopologyConstraintActive),
		Status:             metav1.ConditionTrue,
		LastTransitionTime: metav1.Now(),
		Reason:             "TopologyConstraintActive",
	}}}
}

func ptrString(v string) *string { return &v }

func TestValidateRoleTopologyConstraintsForRBGSet(t *testing.T) {
	rbgs := &RoleBasedGroupSet{
		Spec: RoleBasedGroupSetSpec{
			GroupTemplate: RoleBasedGroupTemplateSpec{
				Spec: RoleBasedGroupSpec{
					Roles: []RoleSpec{{
						Name:                       "prefill",
						InstanceTopologyConstraint: &TopologyConstraint{Pack: &TopologyPackConstraint{Required: ptrString("")}},
					}},
				},
			},
		},
	}
	err := validateRoleTopologyConstraints("spec.groupTemplate.spec.roles", rbgs.Spec.GroupTemplate.Spec.Roles)
	if err == nil || !strings.Contains(err.Error(), "spec.groupTemplate.spec.roles[0].instanceTopologyConstraint") {
		t.Fatalf("expected path-aware topology error, got %v", err)
	}
}

func TestCoordinatedPolicyTopologyImmutabilityRejectsRuleRemoval(t *testing.T) {
	old := activeTopologyPolicy("pd", []string{"prefill", "decode"})
	newPolicy := old.DeepCopy()
	newPolicy.Spec.Policies = nil
	if err := ValidateCoordinatedPolicyTopologyImmutability(old, newPolicy); err == nil {
		t.Fatal("expected rule removal to be rejected")
	}
}

func TestCoordinatedPolicyTopologyImmutabilityRejectsRoleChange(t *testing.T) {
	old := activeTopologyPolicy("pd", []string{"prefill", "decode"})
	newPolicy := old.DeepCopy()
	newPolicy.Spec.Policies[0].Roles = []string{"prefill"}
	if err := ValidateCoordinatedPolicyTopologyImmutability(old, newPolicy); err == nil {
		t.Fatal("expected role membership change to be rejected")
	}
}

func activeTopologyPolicy(name string, roles []string) *CoordinatedPolicy {
	return &CoordinatedPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "policy", Namespace: "default"},
		Status: CoordinatedPolicyStatus{Conditions: []metav1.Condition{{
			Type:               CoordinatedPolicyTopologyConstraintActive,
			Status:             metav1.ConditionTrue,
			LastTransitionTime: metav1.Now(),
			Reason:             "TopologyConstraintActive",
		}}},
		Spec: CoordinatedPolicySpec{Policies: []CoordinatedPolicyRule{{
			Name:  name,
			Roles: roles,
			Strategy: CoordinatedPolicyStrategy{Scheduling: &SchedulingCoordinationStrategy{
				TopologyConstraint: &TopologyConstraint{Pack: &TopologyPackConstraint{Required: ptrString("block")}},
			}},
		}}},
	}
}

func TestRoleTopologyImmutabilityAllowsNewRoleWithoutTopology(t *testing.T) {
	old := &RoleBasedGroup{
		Status: StatusWithTopologyConditionActive(),
		Spec: RoleBasedGroupSpec{Roles: []RoleSpec{{
			Name:                       "prefill",
			InstanceTopologyConstraint: &TopologyConstraint{Pack: &TopologyPackConstraint{Required: ptrString("rack")}},
		}}},
	}
	updated := old.DeepCopy()
	updated.Spec.Roles = append(updated.Spec.Roles, RoleSpec{Name: "router"})
	if err := ValidateRoleTopologyImmutability(old, updated); err != nil {
		t.Fatalf("expected unrelated role addition to be allowed, got %v", err)
	}
}

func TestValidateCoordinatedPolicyTopologyRejectsDuplicateRuleNames(t *testing.T) {
	policy := &CoordinatedPolicy{Spec: CoordinatedPolicySpec{Policies: []CoordinatedPolicyRule{
		{
			Name:  "same",
			Roles: []string{"prefill"},
			Strategy: CoordinatedPolicyStrategy{Scheduling: &SchedulingCoordinationStrategy{
				TopologyConstraint: &TopologyConstraint{Pack: &TopologyPackConstraint{Required: ptrString("rack")}},
			}},
		},
		{
			Name:  "same",
			Roles: []string{"decode"},
			Strategy: CoordinatedPolicyStrategy{Scheduling: &SchedulingCoordinationStrategy{
				TopologyConstraint: &TopologyConstraint{Pack: &TopologyPackConstraint{Required: ptrString("rack")}},
			}},
		},
	}}}

	err := ValidateCoordinatedPolicyTopology(policy)
	if err == nil || !strings.Contains(err.Error(), "duplicate topology rule name") {
		t.Fatalf("expected duplicate rule-name error, got %v", err)
	}
}

func TestCoordinatedPolicyTopologyImmutabilityDetectsDuplicateNameRuleRemoval(t *testing.T) {
	old := activeTopologyPolicy("same", []string{"prefill"})
	old.Spec.Policies = append(old.Spec.Policies, CoordinatedPolicyRule{
		Name:  "same",
		Roles: []string{"decode"},
		Strategy: CoordinatedPolicyStrategy{Scheduling: &SchedulingCoordinationStrategy{
			TopologyConstraint: &TopologyConstraint{Pack: &TopologyPackConstraint{Required: ptrString("rack")}},
		}},
	})
	newPolicy := old.DeepCopy()
	newPolicy.Spec.Policies = newPolicy.Spec.Policies[:1]

	if err := ValidateCoordinatedPolicyTopologyImmutability(old, newPolicy); err == nil {
		t.Fatal("expected duplicate-name rule removal to be rejected")
	}
}

func TestRoleBasedGroupSetValidatorRejectsTopologyUpdateForActiveChild(t *testing.T) {
	testScheme := runtime.NewScheme()
	if err := AddToScheme(testScheme); err != nil {
		t.Fatal(err)
	}

	oldRBGS := &RoleBasedGroupSet{
		ObjectMeta: metav1.ObjectMeta{Name: "rbgs", Namespace: "default"},
		Spec: RoleBasedGroupSetSpec{
			GroupTemplate: RoleBasedGroupTemplateSpec{
				Spec: RoleBasedGroupSpec{Roles: []RoleSpec{{
					Name:                       "prefill",
					InstanceTopologyConstraint: &TopologyConstraint{Pack: &TopologyPackConstraint{Required: ptrString("rack")}},
				}}},
			},
		},
	}
	newRBGS := oldRBGS.DeepCopy()
	newRBGS.Spec.GroupTemplate.Spec.Roles[0].InstanceTopologyConstraint.Pack.Required = ptrString("block")

	child := &RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rbgs-0",
			Namespace: "default",
			Labels:    map[string]string{constants.GroupSetNameLabelKey: "rbgs"},
		},
		Status: StatusWithTopologyConditionActive(),
	}
	c := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(child).Build()
	v := &RoleBasedGroupSetValidator{
		Client:                        c,
		EnableDeprecatedWorkloadTypes: true,
	}

	_, err := v.ValidateUpdate(context.Background(), oldRBGS, newRBGS)
	if err == nil || !strings.Contains(err.Error(), "rbgs-0") {
		t.Fatalf("expected active-child topology immutability error, got %v", err)
	}
}
