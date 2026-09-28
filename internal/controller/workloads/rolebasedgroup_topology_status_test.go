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

package workloads

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	gangcommon "sigs.k8s.io/rbgs/pkg/scheduler/common"
)

func TestSetPlacementConditionsRecordsClassifiedResolutionFailure(t *testing.T) {
	testScheme := runtime.NewScheme()
	if err := workloadsv1alpha2.AddToScheme(testScheme); err != nil {
		t.Fatal(err)
	}
	if err := scheme.AddToScheme(testScheme); err != nil {
		t.Fatal(err)
	}

	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "rbg", Namespace: "default"},
	}
	client := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(rbg).
		WithStatusSubresource(&workloadsv1alpha2.RoleBasedGroup{}).
		Build()

	r := &RoleBasedGroupReconciler{
		client:   client,
		recorder: record.NewFakeRecorder(10),
	}
	cause := gangcommon.NewTopologyTranslationError("unknown topology role")
	if err := r.setPlacementConditions(context.Background(), rbg, nil, cause, nil); err != nil {
		t.Fatal(err)
	}

	updated := &workloadsv1alpha2.RoleBasedGroup{}
	if err := client.Get(context.Background(), types.NamespacedName{Name: rbg.Name, Namespace: rbg.Namespace}, updated); err != nil {
		t.Fatal(err)
	}
	planReady := findCondition(updated.Status.Conditions, string(workloadsv1alpha2.RoleBasedGroupPlacementPlanReady))
	if planReady == nil || planReady.Status != metav1.ConditionFalse {
		t.Fatalf("expected PlacementPlanReady=False, got %#v", planReady)
	}
	topologyTranslated := findCondition(updated.Status.Conditions, string(workloadsv1alpha2.RoleBasedGroupTopologyTranslated))
	if topologyTranslated == nil || topologyTranslated.Status != metav1.ConditionFalse {
		t.Fatalf("expected TopologyTranslated=False, got %#v", topologyTranslated)
	}
}

func findCondition(conditions []metav1.Condition, conditionType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}

func TestValidateTopologyImmutabilityFromStatusRejectsTopologyRemoval(t *testing.T) {
	r := &RoleBasedGroupReconciler{}
	rbg := &workloadsv1alpha2.RoleBasedGroup{
		Status: workloadsv1alpha2.RoleBasedGroupStatus{Conditions: []metav1.Condition{{
			Type:               string(workloadsv1alpha2.RoleBasedGroupTopologyConstraintActive),
			Status:             metav1.ConditionTrue,
			LastTransitionTime: metav1.Now(),
			Reason:             "TopologyConstraintActive",
			Message:            "Topology constraints are active; declarations=" + topologyTestPlan("prefill").TopologyDeclarationRecord(),
		}}},
	}

	err := r.validateTopologyImmutabilityFromStatus(rbg, nil)
	if err == nil {
		t.Fatal("expected topology removal after placement became active to be rejected")
	}
}

func TestValidateTopologyImmutabilityFromStatusAllowsAddedDeclaration(t *testing.T) {
	r := &RoleBasedGroupReconciler{}
	oldPlan := topologyTestPlan("prefill")
	rbg := &workloadsv1alpha2.RoleBasedGroup{
		Status: workloadsv1alpha2.RoleBasedGroupStatus{Conditions: []metav1.Condition{{
			Type:               string(workloadsv1alpha2.RoleBasedGroupTopologyConstraintActive),
			Status:             metav1.ConditionTrue,
			LastTransitionTime: metav1.Now(),
			Reason:             "TopologyConstraintActive",
			Message:            "Topology constraints are active; declarations=" + oldPlan.TopologyDeclarationRecord(),
		}}},
	}
	newPlan := &gangcommon.PlacementPlan{Root: &gangcommon.PlacementGroup{
		Children: []*gangcommon.PlacementGroup{
			topologyTestGroup("prefill"),
			topologyTestGroup("decode"),
		},
	}}

	if err := r.validateTopologyImmutabilityFromStatus(rbg, newPlan); err != nil {
		t.Fatalf("expected a newly added topology declaration to be allowed, got %v", err)
	}
}

func TestValidateTopologyImmutabilityFromStatusRejectsChangedDeclaration(t *testing.T) {
	r := &RoleBasedGroupReconciler{}
	oldPlan := topologyTestPlan("prefill")
	rbg := &workloadsv1alpha2.RoleBasedGroup{
		Status: workloadsv1alpha2.RoleBasedGroupStatus{Conditions: []metav1.Condition{{
			Type:               string(workloadsv1alpha2.RoleBasedGroupTopologyConstraintActive),
			Status:             metav1.ConditionTrue,
			LastTransitionTime: metav1.Now(),
			Reason:             "TopologyConstraintActive",
			Message:            "Topology constraints are active; declarations=" + oldPlan.TopologyDeclarationRecord(),
		}}},
	}
	changedPlan := topologyTestPlan("prefill")
	changedPlan.Root.Topology.Pack.Required = ptr.To("block")

	if err := r.validateTopologyImmutabilityFromStatus(rbg, changedPlan); err == nil {
		t.Fatal("expected an active topology declaration to be immutable")
	}
}

func topologyTestPlan(role string) *gangcommon.PlacementPlan {
	return &gangcommon.PlacementPlan{Root: topologyTestGroup(role)}
}

func topologyTestGroup(role string) *gangcommon.PlacementGroup {
	return &gangcommon.PlacementGroup{
		Scope: gangcommon.PlacementScope{Roles: []string{role}},
		Topology: &workloadsv1alpha2.TopologyConstraint{
			Pack: &workloadsv1alpha2.TopologyPackConstraint{Required: ptr.To("rack")},
		},
	}
}

func TestTopologyConstraintActiveMarkerRemainsWhenCoveredRoleScalesToZero(t *testing.T) {
	testScheme := runtime.NewScheme()
	if err := workloadsv1alpha2.AddToScheme(testScheme); err != nil {
		t.Fatal(err)
	}
	if err := scheme.AddToScheme(testScheme); err != nil {
		t.Fatal(err)
	}

	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "rbg", Namespace: "default"},
		Status: workloadsv1alpha2.RoleBasedGroupStatus{Conditions: []metav1.Condition{{
			Type:               string(workloadsv1alpha2.RoleBasedGroupTopologyConstraintActive),
			Status:             metav1.ConditionTrue,
			LastTransitionTime: metav1.Now(),
			Reason:             "TopologyConstraintActive",
			Message:            "Topology constraints are active; declarations=" + topologyTestPlan("prefill").TopologyDeclarationRecord(),
		}}},
	}
	c := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(rbg).
		WithStatusSubresource(&workloadsv1alpha2.RoleBasedGroup{}).
		Build()
	plan := &gangcommon.PlacementPlan{
		Root: &gangcommon.PlacementGroup{
			Scope: gangcommon.PlacementScope{Roles: []string{"prefill"}},
			Topology: &workloadsv1alpha2.TopologyConstraint{
				Pack: &workloadsv1alpha2.TopologyPackConstraint{Required: ptr.To("rack")},
			},
		},
	}
	r := &RoleBasedGroupReconciler{client: c, recorder: record.NewFakeRecorder(10)}

	if err := r.setTopologyConstraintActiveCondition(context.Background(), rbg, plan); err != nil {
		t.Fatal(err)
	}
	updated := &workloadsv1alpha2.RoleBasedGroup{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: rbg.Name, Namespace: rbg.Namespace}, updated); err != nil {
		t.Fatal(err)
	}
	condition := findCondition(updated.Status.Conditions, string(workloadsv1alpha2.RoleBasedGroupTopologyConstraintActive))
	if condition == nil || condition.Status != metav1.ConditionTrue {
		t.Fatalf("expected sticky topology-active marker, got %#v", condition)
	}
}

func TestCoordinatedPolicyTopologyMarkerClearsForRecreatedRBG(t *testing.T) {
	testScheme := runtime.NewScheme()
	if err := workloadsv1alpha2.AddToScheme(testScheme); err != nil {
		t.Fatal(err)
	}
	if err := scheme.AddToScheme(testScheme); err != nil {
		t.Fatal(err)
	}

	policy := &workloadsv1alpha2.CoordinatedPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "rbg", Namespace: "default"},
		Spec: workloadsv1alpha2.CoordinatedPolicySpec{Policies: []workloadsv1alpha2.CoordinatedPolicyRule{{
			Name:  "topology",
			Roles: []string{"prefill"},
			Strategy: workloadsv1alpha2.CoordinatedPolicyStrategy{Scheduling: &workloadsv1alpha2.SchedulingCoordinationStrategy{
				TopologyConstraint: &workloadsv1alpha2.TopologyConstraint{
					Pack: &workloadsv1alpha2.TopologyPackConstraint{Required: ptr.To("rack")},
				},
			}},
		}}},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rbg-prefill-0",
			Namespace: "default",
			Labels: map[string]string{
				constants.GroupNameLabelKey: "rbg",
				constants.RoleNameLabelKey:  "prefill",
			},
		},
	}
	oldRBG := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "rbg", Namespace: "default", UID: "old-uid"},
	}
	newRBG := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "rbg", Namespace: "default", UID: "new-uid"},
	}
	c := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(policy, pod).
		WithStatusSubresource(&workloadsv1alpha2.CoordinatedPolicy{}).
		Build()
	r := &RoleBasedGroupReconciler{client: c, recorder: record.NewFakeRecorder(10)}

	if err := r.setCoordinatedPolicyTopologyActiveCondition(context.Background(), oldRBG); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	if err := r.setCoordinatedPolicyTopologyActiveCondition(context.Background(), newRBG); err != nil {
		t.Fatal(err)
	}

	updated := &workloadsv1alpha2.CoordinatedPolicy{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: policy.Name, Namespace: policy.Namespace}, updated); err != nil {
		t.Fatal(err)
	}
	if findCondition(updated.Status.Conditions, workloadsv1alpha2.CoordinatedPolicyTopologyConstraintActive) != nil {
		t.Fatal("expected topology marker from the previous RBG lifecycle to be removed")
	}
}

func TestTopologyConstraintActiveMarkerAddsNewDeclarationAfterPod(t *testing.T) {
	testScheme := runtime.NewScheme()
	if err := workloadsv1alpha2.AddToScheme(testScheme); err != nil {
		t.Fatal(err)
	}
	if err := scheme.AddToScheme(testScheme); err != nil {
		t.Fatal(err)
	}

	oldPlan := topologyTestPlan("prefill")
	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "rbg", Namespace: "default"},
		Status: workloadsv1alpha2.RoleBasedGroupStatus{Conditions: []metav1.Condition{{
			Type:               string(workloadsv1alpha2.RoleBasedGroupTopologyConstraintActive),
			Status:             metav1.ConditionTrue,
			LastTransitionTime: metav1.Now(),
			Reason:             "TopologyConstraintActive",
			Message:            "Topology constraints are active; declarations=" + oldPlan.TopologyDeclarationRecord(),
		}}},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rbg-decode-0",
			Namespace: "default",
			Labels: map[string]string{
				constants.GroupNameLabelKey: "rbg",
				constants.RoleNameLabelKey:  "decode",
			},
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(rbg, pod).
		WithStatusSubresource(&workloadsv1alpha2.RoleBasedGroup{}).
		Build()
	newPlan := &gangcommon.PlacementPlan{Root: &gangcommon.PlacementGroup{
		Children: []*gangcommon.PlacementGroup{
			topologyTestGroup("prefill"),
			topologyTestGroup("decode"),
		},
	}}
	r := &RoleBasedGroupReconciler{client: c, recorder: record.NewFakeRecorder(10)}

	if err := r.setTopologyConstraintActiveCondition(context.Background(), rbg, newPlan); err != nil {
		t.Fatal(err)
	}
	updated := &workloadsv1alpha2.RoleBasedGroup{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: rbg.Name, Namespace: rbg.Namespace}, updated); err != nil {
		t.Fatal(err)
	}
	condition := findCondition(updated.Status.Conditions, string(workloadsv1alpha2.RoleBasedGroupTopologyConstraintActive))
	if condition == nil {
		t.Fatal("expected topology-active condition")
	}
	declarations, err := gangcommon.ParseTopologyDeclarationRecord(
		conditionMessageValue(condition.Message, "declarations"))
	if err != nil {
		t.Fatal(err)
	}
	if len(declarations) != len(newPlan.TopologyDeclarationSignatures()) {
		t.Fatalf("expected new declaration to be recorded, got %q", condition.Message)
	}
}
