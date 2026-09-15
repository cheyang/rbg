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

// Package workloads *_test.go companion: harness for PR #469's behavioral premise.
//
// PR #469 records, in test/e2e/upgrade/snapshot.go, that the legacy-set child
// RoleBasedGroup is healed from "Recreate" to "RecreatePod" on the upgraded
// controller's first reconcile, and phase 3 asserts child.Type == RecreatePod.
// The cited mechanism is the RBGS controller re-applying the child from its
// normalized groupTemplate via updateExistingRBGs.
//
// updateExistingRBGs is only reached for children where needsUpdate == true, and
// needsUpdate delegates to rolesEqual, which normalizes BOTH parent and child
// before DeepEqual (#463's convergence fix). On the upgrade path the child was
// written by v0.7.0 with "Recreate" copied verbatim from a legacy template, so
// parent and child are spelling-identical and normalize to the same value:
// needsUpdate returns false, the child is never re-applied, and its stored spec
// stays "Recreate". The PR's recorded heal and phase-3 assertion therefore do
// not reflect what the reconciler does.
//
// These tests are the unit-level proof. They do not touch production code.
package workloads

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// legacyRoleWithStrategy builds a single role carrying the v1alpha1 "Recreate"
// spelling of the rolling-update strategy type, the shape v0.7.0 stored on both
// the legacy-set GroupTemplate and the child it stamped out of it.
func legacyRoleWithStrategy(name string) workloadsv1alpha2.RoleSpec {
	return workloadsv1alpha2.RoleSpec{
		Name: name,
		RolloutStrategy: &workloadsv1alpha2.RolloutStrategy{
			RollingUpdate: &workloadsv1alpha2.RollingUpdate{
				Type: workloadsv1alpha2.LegacyRecreateUpdateStrategyType,
			},
		},
	}
}

// TestPR469_LegacyChildNotReapplied_RefutesPremise is the contract: on the upgrade
// path (parent template "Recreate", existing child "Recreate") needsUpdate is
// false, so updateExistingRBGs is never called for the legacy child and its
// stored strategy type is NOT healed to "RecreatePod" via the RBGS re-apply path
// the PR cites. A true result here would mean the child is re-applied; the code
// returns false, which is the refutation of the PR's recorded premise.
func TestPR469_LegacyChildNotReapplied_RefutesPremise(t *testing.T) {
	r := &RoleBasedGroupSetReconciler{}
	rbgset := &workloadsv1alpha2.RoleBasedGroupSet{
		ObjectMeta: metav1.ObjectMeta{Name: "up-legacy-set"},
		Spec: workloadsv1alpha2.RoleBasedGroupSetSpec{
			GroupTemplate: workloadsv1alpha2.RoleBasedGroupTemplateSpec{
				Spec: workloadsv1alpha2.RoleBasedGroupSpec{
					Roles: []workloadsv1alpha2.RoleSpec{legacyRoleWithStrategy("role-1")},
				},
			},
		},
	}
	// Existing child as v0.7.0 wrote it: the legacy "Recreate" spelling copied
	// verbatim from the template. Labels/annotations are in sync (v0.7.0 set them
	// from the same template), so the only field in play is the strategy spelling.
	child := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "up-legacy-set-0",
			Namespace: "default",
			Labels: map[string]string{
				constants.GroupSetNameLabelKey:  "up-legacy-set",
				constants.GroupSetIndexLabelKey: "0",
			},
		},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{
			Roles: []workloadsv1alpha2.RoleSpec{legacyRoleWithStrategy("role-1")},
		},
	}

	assert.False(t, r.needsUpdate(rbgset, child),
		"needsUpdate must be false for a legacy child under a legacy parent: "+
			"rolesEqual normalizes both sides, so the RBGS reconciler does not re-apply "+
			"the child and its stored 'Recreate' is not healed to 'RecreatePod' on upgrade")
}

// TestPR469_NormalizedGroupTemplateDoesHeal is the canary: it confirms the normalize
// helper itself would produce 'RecreatePod', so the only thing standing between a
// legacy child and the healed value is the needsUpdate gate above — not a missing
// normalize. This isolates the cause: the heal does not happen because the gate is
// closed, not because normalization is absent.
func TestPR469_NormalizedGroupTemplateDoesHeal(t *testing.T) {
	rbgset := &workloadsv1alpha2.RoleBasedGroupSet{
		Spec: workloadsv1alpha2.RoleBasedGroupSetSpec{
			GroupTemplate: workloadsv1alpha2.RoleBasedGroupTemplateSpec{
				Spec: workloadsv1alpha2.RoleBasedGroupSpec{
					Roles: []workloadsv1alpha2.RoleSpec{legacyRoleWithStrategy("role-1")},
				},
			},
		},
	}
	roles := normalizedGroupTemplateRoles(rbgset)
	if assert.Len(t, roles, 1) {
		assert.Equal(t, workloadsv1alpha2.RecreatePodUpdateStrategyType,
			roles[0].RolloutStrategy.RollingUpdate.Type)
	}
	// And the source object is left untouched (deep copy), so the parent template
	// keeps its legacy spelling — matching the PR's own claim that the set is
	// never healed.
	assert.Equal(t, workloadsv1alpha2.LegacyRecreateUpdateStrategyType,
		rbgset.Spec.GroupTemplate.Spec.Roles[0].RolloutStrategy.RollingUpdate.Type)
}
