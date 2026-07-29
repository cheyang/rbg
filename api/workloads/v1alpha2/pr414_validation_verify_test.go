/*
Copyright 2025 The RBG Authors.

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

// Reviewer verification harness for PR sgl-project/rbg#414, head 66a2500a.
// See docs/verification/pr414-v1alpha1-compat-flag/README.md.
package v1alpha2

import (
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/rbgs/api/workloads/constants"
)

func p414Role(name, wt string) RoleSpec {
	r := RoleSpec{Name: name}
	if wt != "" {
		r.Annotations = map[string]string{constants.RoleWorkloadTypeAnnotationKey: wt}
	}
	return r
}

func p414RBG(name string, roles ...RoleSpec) *RoleBasedGroup {
	return &RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       RoleBasedGroupSpec{Roles: roles},
	}
}

// ---------------------------------------------------------------------------
// F10 [CANARY] -- ValidateWorkloadTypesUpdate grandfathers by ROLE NAME, not by
// (role name, workload type), so an already-legacy role can be switched to a
// DIFFERENT legacy type while compat is disabled.
//
// The stated intent is "existing roles that already had legacy types in the old
// spec are allowed to remain so users can edit the RBG to migrate away from
// them". Switching StatefulSet -> Deployment is not migrating away from
// anything: it asks the controller for a workload kind the operator has just
// declared unsupported, and it is accepted.
//
// Practical consequence is mild (the role was already unmanaged either way),
// but it means the admission rule does not enforce monotonic progress toward
// RoleInstanceSet, which is the only reason to allow the update at all.
//
// PASSES on 66a2500a. FLIPS if the check is tightened to compare per-role
// workload types -- invert then.
// ---------------------------------------------------------------------------
func TestVerifyPR414_F10_LegacyTypeSwapAllowedOnGrandfatheredRole_Canary(t *testing.T) {
	cases := []struct {
		name     string
		from, to string
	}{
		{"statefulset_to_deployment", constants.StatefulSetWorkloadType, constants.DeploymentWorkloadType},
		{"deployment_to_lws", constants.DeploymentWorkloadType, constants.LeaderWorkerSetWorkloadType},
		{"lws_to_statefulset", constants.LeaderWorkerSetWorkloadType, constants.StatefulSetWorkloadType},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			old := p414RBG("rbg", p414Role("worker", tc.from))
			new := p414RBG("rbg", p414Role("worker", tc.to))

			if err := ValidateWorkloadTypesUpdate(old, new); err != nil {
				t.Fatalf("CANARY FLIPPED: swapping a grandfathered role from %s to %s is now "+
					"rejected (%v). The rule enforces per-role workload types -- invert this test.",
					tc.from, tc.to, err)
			}
			t.Logf("observed: role %q switched %s -> %s while compat is disabled, accepted",
				"worker", tc.from, tc.to)
		})
	}

	// Control: the check must still bite for a genuinely new legacy role, and
	// must still allow migration to RoleInstanceSet.
	t.Run("control_newLegacyRoleRejected", func(t *testing.T) {
		old := p414RBG("rbg", p414Role("worker", constants.StatefulSetWorkloadType))
		new := p414RBG("rbg",
			p414Role("worker", constants.StatefulSetWorkloadType),
			p414Role("extra", constants.DeploymentWorkloadType))
		if err := ValidateWorkloadTypesUpdate(old, new); err == nil {
			t.Fatal("HARNESS DOES NOT BITE: a brand new legacy role was accepted")
		}
	})
	t.Run("control_migrationToRoleInstanceSetAllowed", func(t *testing.T) {
		old := p414RBG("rbg", p414Role("worker", constants.StatefulSetWorkloadType))
		new := p414RBG("rbg", p414Role("worker", ""))
		if err := ValidateWorkloadTypesUpdate(old, new); err != nil {
			t.Fatalf("migration to RoleInstanceSet must stay allowed: %v", err)
		}
	})
}

// ---------------------------------------------------------------------------
// F8b [CONTRACT] -- third copy of the legacy-type list (this package) must agree
// with the reviewer's independent list. Pairs with F8 in the controller package.
// Expected GREEN today; exists so a fourth legacy type cannot be added to one
// copy only.
// ---------------------------------------------------------------------------
func TestVerifyPR414_F8b_ValidationLegacyListMatches(t *testing.T) {
	legacy := []string{
		constants.DeploymentWorkloadType,
		constants.StatefulSetWorkloadType,
		constants.LeaderWorkerSetWorkloadType,
	}
	notLegacy := []string{
		constants.RoleInstanceSetWorkloadType,
		"apps/v1/DaemonSet",
		"batch/v1/Job",
		"",
	}

	for _, wt := range legacy {
		err := ValidateWorkloadTypes(p414RBG("rbg", p414Role("r", wt)))
		if err == nil {
			t.Errorf("DRIFT: %q is a v1alpha1 indirect type but ValidateWorkloadTypes accepted it", wt)
			continue
		}
		if !strings.Contains(err.Error(), "not supported when v1alpha1 compat is disabled") {
			t.Errorf("unexpected message for %q: %v", wt, err)
		}
	}
	for _, wt := range notLegacy {
		if err := ValidateWorkloadTypes(p414RBG("rbg", p414Role("r", wt))); err != nil {
			t.Errorf("DRIFT: %q is not a v1alpha1 indirect type but was rejected: %v", wt, err)
		}
	}
}

// ---------------------------------------------------------------------------
// F7b [CONTRACT] -- the error a rejected child RoleBasedGroup actually produces.
//
// This is the input the F7 test in internal/controller/workloads stands in for.
// Asserting it here keeps that stand-in honest: if the validator's behavior
// changes, this goes red rather than F7 silently testing a fiction.
// ---------------------------------------------------------------------------
func TestVerifyPR414_F7b_ValidatorRejectsLegacyChildRBG(t *testing.T) {
	v := &RoleBasedGroupValidator{EnableV1Alpha1Compat: false}
	child := p414RBG("svc-0", p414Role("worker", constants.StatefulSetWorkloadType))

	_, err := v.ValidateCreate(nil, child) //nolint:staticcheck // nil ctx is fine, validator ignores it
	if err == nil {
		t.Fatal("expected ValidateCreate to reject a legacy-typed RBG when compat is disabled; " +
			"if this changed, the F7 RBGSet test's admission stand-in is no longer accurate")
	}
	if !strings.Contains(err.Error(), "not supported when v1alpha1 compat is disabled") {
		t.Fatalf("unexpected rejection message (F7's stand-in copies this text): %v", err)
	}
	t.Logf("observed rejection: %v", err)

	// Control: compat enabled must accept the same object.
	vOn := &RoleBasedGroupValidator{EnableV1Alpha1Compat: true}
	if _, err := vOn.ValidateCreate(nil, p414RBG("svc-0", p414Role("worker", constants.StatefulSetWorkloadType))); err != nil {
		t.Fatalf("HARNESS DOES NOT BITE: compat=true also rejected the object (%v), so the "+
			"rejection above is not attributable to the flag", err)
	}
}
