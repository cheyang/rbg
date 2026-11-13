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

// Review-verification harness for PR #586 (docs: v1alpha1->v1alpha2 migration
// guides). These tests pin the *code claims* the new guides make about the
// 0.9.0 compatibility gate, so the docs cannot silently drift away from the
// implementation. All tests are CONTRACT polarity: they assert the behavior
// the guides document, and must keep passing for the guides to stay true.
// Additive only; no production code is touched.

package v1alpha2

import (
	"strings"
	"testing"

	"sigs.k8s.io/rbgs/api/workloads/constants"
)

// roleWithWorkloadType builds a minimal RoleSpec carrying the given
// role-workload-type annotation value.
func roleWithWorkloadType(name, workloadType string) RoleSpec {
	return RoleSpec{
		Name:        name,
		Annotations: map[string]string{constants.RoleWorkloadTypeAnnotationKey: workloadType},
	}
}

// Guide claim (doc/migration-v1alpha1-to-v1alpha2.md, "Prerequisites"):
// "Compatibility is type-exact. An existing role keeps its own deprecated
// workload type." Contract: a role whose deprecated type is unchanged vs the
// old object must pass the compatibility gate.
func TestDocClaimGateKeepsExistingDeprecatedRole(t *testing.T) {
	old := []RoleSpec{roleWithWorkloadType("worker", constants.StatefulSetWorkloadType)}
	newRoles := []RoleSpec{roleWithWorkloadType("worker", constants.StatefulSetWorkloadType)}
	if err := validateNoNewDeprecatedWorkloadTypes("spec.roles", newRoles, old); err != nil {
		t.Fatalf("guide claims an existing role keeps its own deprecated type, but gate rejected unchanged role: %v", err)
	}
}

// Guide claim: "renaming the role ... is a new use and is rejected".
func TestDocClaimGateRejectsRenamedRole(t *testing.T) {
	old := []RoleSpec{roleWithWorkloadType("old-name", constants.StatefulSetWorkloadType)}
	newRoles := []RoleSpec{roleWithWorkloadType("new-name", constants.StatefulSetWorkloadType)}
	err := validateNoNewDeprecatedWorkloadTypes("spec.roles", newRoles, old)
	if err == nil || !strings.Contains(err.Error(), "new uses are not accepted") {
		t.Fatalf("guide claims renaming a deprecated role is rejected; got err=%v", err)
	}
}

// Guide claim: "switching a role from one deprecated type to another is a new
// use and is rejected".
func TestDocClaimGateRejectsDeprecatedTypeSwitch(t *testing.T) {
	old := []RoleSpec{roleWithWorkloadType("worker", constants.StatefulSetWorkloadType)}
	newRoles := []RoleSpec{roleWithWorkloadType("worker", constants.DeploymentWorkloadType)}
	if err := validateNoNewDeprecatedWorkloadTypes("spec.roles", newRoles, old); err == nil {
		t.Fatal("guide claims switching between deprecated types is rejected; gate allowed it")
	}
}

// Guide claim ("Rollback"): "Once a role has switched to RoleInstanceSet,
// restoring the saved Deployment, StatefulSet, LeaderWorkerSet, or InstanceSet
// selector is a new deprecated use. The validator rejects it even while
// controller.deprecatedWorkloadTypes.enabled=true".
func TestDocClaimGateRejectsRollbackToDeprecatedType(t *testing.T) {
	// old object: role already migrated (RoleInstanceSet annotation or none).
	old := []RoleSpec{roleWithWorkloadType("worker", constants.RoleInstanceSetWorkloadType)}
	// attempted rollback: re-add the StatefulSet selector.
	newRoles := []RoleSpec{roleWithWorkloadType("worker", constants.StatefulSetWorkloadType)}
	if err := validateNoNewDeprecatedWorkloadTypes("spec.roles", newRoles, old); err == nil {
		t.Fatal("guide claims rollback to a deprecated selector is rejected in compatibility mode; gate allowed it")
	}
}

// Guide claim ("v1alpha1 InstanceSet roles"): "A v1alpha1 InstanceSet selector
// is not covered by the compatibility toggle." The deprecation gate only knows
// Deployment/StatefulSet/LeaderWorkerSet; an InstanceSet selector is not a
// *deprecated-gated* type at all (it fails later, at controller preflight).
func TestDocClaimInstanceSetIsNotGateProtected(t *testing.T) {
	if isDeprecatedWorkloadType("workloads.x-k8s.io/v1alpha1/InstanceSet") {
		t.Fatal("guide claims InstanceSet is outside the compatibility gate, but the gate treats it as deprecated")
	}
	newRoles := []RoleSpec{roleWithWorkloadType("legacy", "workloads.x-k8s.io/v1alpha1/InstanceSet")}
	if err := validateNoNewDeprecatedWorkloadTypes("spec.roles", newRoles, nil); err != nil {
		t.Fatalf("guide claims the compatibility gate does not reject InstanceSet; got: %v", err)
	}
}

// Guide claim (inventory table): a role with no annotation uses the v1alpha2
// default, RoleInstanceSet.
func TestDocClaimV1alpha2DefaultWorkloadIsRoleInstanceSet(t *testing.T) {
	r := &RoleSpec{Name: "plain"}
	if got := r.GetWorkloadType(); got != constants.RoleInstanceSetWorkloadType {
		t.Fatalf("guide claims RoleInstanceSet is the v1alpha2 default; got %q", got)
	}
}
