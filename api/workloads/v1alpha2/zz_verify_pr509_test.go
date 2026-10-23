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

// Verification harness for PR #509 review (verify/pr-509-deprecation-window-codex).
// These tests are additive review evidence; they do not change production code.
//
// Polarity: all tests in this file are CONTRACT tests — they assert the behavior
// the PR documents ("existing roles continue to reconcile" during the
// compatibility window). A failing contract test reproduces the finding.

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/rbgs/api/workloads/constants"
)

// legacyStatefulSetRole mirrors a role that was written while the deprecated
// workload types were still accepted (pre-0.9.0), or via v1alpha1 defaults.
func legacyStatefulSetRole(name string) RoleSpec {
	return RoleSpec{
		Name:     name,
		Replicas: ptr.To(int32(1)),
		Annotations: map[string]string{
			constants.RoleWorkloadTypeAnnotationKey: constants.StatefulSetWorkloadType,
		},
	}
}

// TestPR509Contract_ChildRBGFromLegacyTemplateIsAdmittedOnCreate is the core
// regression check for the compatibility window. The RoleBasedGroupSet
// controller creates child RoleBasedGroups through the API server
// (rolebasedgroupset_controller.go scaleUp -> client.Create). For a set that
// already exists with a legacy workload template, scale-up (or re-creating a
// deleted child) must succeed during the migration window, because the PR
// promises "existing roles continue to reconcile". The child carries the
// template roles verbatim (normalizedGroupTemplateRoles deep-copies them,
// annotations included), so the create goes through
// RoleBasedGroupValidator.ValidateCreate with no old object.
//
// Expected (per the PR's own contract): admitted.
// Suspected actual: rejected as a "new use" of a deprecated workload type.
func TestPR509Contract_ChildRBGFromLegacyTemplateIsAdmittedOnCreate(t *testing.T) {
	// Existing set, created before the upgrade: template still carries the
	// legacy StatefulSet workload annotation.
	rbgset := &RoleBasedGroupSet{
		ObjectMeta: metav1.ObjectMeta{Name: "legacy-set", Namespace: "default"},
		Spec: RoleBasedGroupSetSpec{
			Replicas: ptr.To(int32(1)),
			GroupTemplate: RoleBasedGroupTemplateSpec{
				Spec: RoleBasedGroupSpec{
					Roles: []RoleSpec{legacyStatefulSetRole("worker")},
				},
			},
		},
	}

	// Child exactly as newRBGForSet builds it: template roles deep-copied,
	// annotations (including the workload-type annotation) preserved.
	child := &RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "legacy-set-1", Namespace: rbgset.Namespace},
		Spec: RoleBasedGroupSpec{
			Roles: []RoleSpec{*rbgset.Spec.GroupTemplate.Spec.Roles[0].DeepCopy()},
		},
	}

	validator := &RoleBasedGroupValidator{EnableDeprecatedWorkloadTypes: true}
	_, err := validator.ValidateCreate(context.Background(), child)
	assert.NoError(t, err,
		"compat window must admit controller-created children of an existing legacy RoleBasedGroupSet; "+
			"otherwise scale-up and self-heal of such sets are broken by upgrading (PR #509 F1)")
}

// TestPR509Contract_CompatModeRejectionDoesNotAdviseReenablingTheFlag covers the
// error text users see when the compatibility gate rejects a new use. The
// shared hint tells the user to "re-enable the deprecated workload types
// (--enable-deprecated-workload-types=true)" — but in compat mode the flag is
// already true, so the advice is a no-op and masks the real fix (migrate the
// role to RoleInstanceSet).
func TestPR509Contract_CompatModeRejectionDoesNotAdviseReenablingTheFlag(t *testing.T) {
	validator := &RoleBasedGroupValidator{EnableDeprecatedWorkloadTypes: true}
	rbg := &RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "new-rbg", Namespace: "default"},
		Spec:       RoleBasedGroupSpec{Roles: []RoleSpec{legacyStatefulSetRole("worker")}},
	}
	_, err := validator.ValidateCreate(context.Background(), rbg)
	if assert.Error(t, err) {
		assert.NotContains(t, err.Error(), "re-enable the deprecated workload types",
			"in compat mode the flag is already true; advising the user to re-enable it is misleading (PR #509 F4)")
		assert.False(t, strings.Contains(err.Error(), "enable-deprecated-workload-types=true"),
			"compat-mode rejection must not recommend the flag value that is already in effect")
	}
}
