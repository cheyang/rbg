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

// Reviewer-private verification harness for PR #416 -- finding F8.
//
// F8 is the design-level consequence of three independently innocuous links:
//
//  1. v1alpha1 RoleSpec.Workload carries a CRD-level default of
//     {apiVersion: apps/v1, kind: StatefulSet}
//     (api/workloads/v1alpha1/rolebasedgroup_types.go:341). The API SERVER applies it, so
//     `src.Workload` is never empty for any object that was actually submitted. Proven
//     against a live k8s v1.36.1 API server -- see
//     docs/verification/pr416-api-compat-toggle/results/l3-v1alpha1-defaulting.txt
//  2. convertRoleV1alpha1ToV2 unconditionally copies that into the
//     RoleWorkloadTypeAnnotationKey annotation
//     (api/workloads/v1alpha1/rolebasedgroup_conversion.go:142-148).
//  3. validateNoLegacyWorkloads rejects exactly that annotation value.
//
// Therefore `compatibility.v1alpha1.enabled=false` does not merely "restrict v1alpha1-era
// workload types" as the PR describes -- it makes the ENTIRE v1alpha1 API unusable, including
// for objects that never mentioned a workload type at all.
package v1alpha1

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	v2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// TestVerifyPR416_F8_DisablingCompatKillsTheWholeV1alpha1API is a CONTRACT test.
//
// Claim: a plain v1alpha1 RoleBasedGroup -- one whose author never chose a workload type --
// is rejected outright when compatibility is disabled, because the CRD default puts
// StatefulSet in the field, conversion turns that into the legacy annotation, and the new
// validator rejects the annotation.
//
// Intended behaviour (asserted here): disabling a *workload-type* restriction should not
// silently amount to switching off an entire API version. Either the flag should not apply to
// workload types that were injected by defaulting rather than chosen by the user, or the PR
// should state plainly that it disables v1alpha1 and be gated behind the migration path its
// own description defers to a follow-up.
//
// Expected on the PR head: RED.
func TestVerifyPR416_F8_DisablingCompatKillsTheWholeV1alpha1API(t *testing.T) {
	// Link 1: exactly what a live API server returns for a v1alpha1 role with no `workload`
	// field. Hard-coded here because CRD defaulting is done by the API server, not by Go; the
	// live proof is captured in results/l3-v1alpha1-defaulting.txt.
	src := &RoleSpec{
		Name:     "worker",
		Replicas: ptr.To(int32(1)),
		Workload: WorkloadSpec{APIVersion: "apps/v1", Kind: "StatefulSet"},
	}

	// Link 2: the project's real conversion function.
	dst := &v2.RoleSpec{}
	if err := convertRoleV1alpha1ToV2(src, dst); err != nil {
		t.Fatalf("conversion failed: %v", err)
	}
	got := dst.Annotations[constants.RoleWorkloadTypeAnnotationKey]
	t.Logf("conversion produced %s=%q", constants.RoleWorkloadTypeAnnotationKey, got)
	if got != constants.StatefulSetWorkloadType {
		t.Fatalf("HARNESS PROBLEM: expected conversion to stamp %q, got %q -- the premise of F8"+
			" (defaulted workload becomes a legacy annotation) no longer holds",
			constants.StatefulSetWorkloadType, got)
	}

	// Link 3: the real validator from this PR, with a real Client so its own nil-client guard
	// does not fire.
	rbg := &v2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "converted-from-v1alpha1", Namespace: "default"},
		Spec:       v2.RoleBasedGroupSpec{Roles: []v2.RoleSpec{*dst}},
	}
	v := &v2.RoleBasedGroupValidator{
		Client:                        fake.NewClientBuilder().Build(),
		EnableDeprecatedWorkloadTypes: false,
	}
	_, err := v.ValidateCreate(context.Background(), rbg)

	if err != nil {
		t.Errorf("F8 REPRODUCED: a v1alpha1 RoleBasedGroup whose author never chose a workload"+
			" type is rejected when compatibility is disabled. The CRD default injected"+
			" StatefulSet, conversion stamped it as %s=%q, and the validator refused it: %v."+
			" So compatibility.v1alpha1.enabled=false does not 'restrict v1alpha1-era workload"+
			" types' -- it disables the v1alpha1 API wholesale, which the PR description does"+
			" not say and which pre-empts the migration path that description defers to a"+
			" follow-up PR.",
			constants.RoleWorkloadTypeAnnotationKey, got, err)
		return
	}
	t.Logf("F8 FIXED: a defaulted v1alpha1 role is accepted with compatibility disabled")
}

// TestVerifyPR416_F8b_ControlsForF8 pins down the two boundaries that stop F8 being
// overstated, so the finding cannot be read as broader than it is.
func TestVerifyPR416_F8b_ControlsForF8(t *testing.T) {
	newV := func(disabled bool) *v2.RoleBasedGroupValidator {
		return &v2.RoleBasedGroupValidator{
			Client:                        fake.NewClientBuilder().Build(),
			EnableDeprecatedWorkloadTypes: !disabled,
		}
	}
	wrap := func(r *v2.RoleSpec) *v2.RoleBasedGroup {
		return &v2.RoleBasedGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "default"},
			Spec:       v2.RoleBasedGroupSpec{Roles: []v2.RoleSpec{*r}},
		}
	}

	// CONTROL 1: with compatibility ENABLED the very same converted object is accepted, so the
	// rejection in F8 is attributable to the flag and not to the conversion output being
	// malformed.
	t.Run("control_same_object_accepted_when_compat_enabled", func(t *testing.T) {
		src := &RoleSpec{
			Name: "worker", Replicas: ptr.To(int32(1)),
			Workload: WorkloadSpec{APIVersion: "apps/v1", Kind: "StatefulSet"},
		}
		dst := &v2.RoleSpec{}
		if err := convertRoleV1alpha1ToV2(src, dst); err != nil {
			t.Fatalf("conversion failed: %v", err)
		}
		if _, err := newV(false).ValidateCreate(context.Background(), wrap(dst)); err != nil {
			t.Errorf("CONTROL FAILED: compat-enabled validator rejected the converted object: %v", err)
		}
	})

	// CONTROL 2: there IS a way for a v1alpha1 object to survive -- explicitly naming the
	// v1alpha2 RoleInstanceSet type in the v1alpha1 `workload` field. This bounds F8 (v1alpha1
	// is not *unconditionally* dead) while showing how unreasonable the surviving path is as a
	// migration story: the user must write a v1alpha2 type into a v1alpha1 object.
	t.Run("control_v1alpha1_survives_only_by_naming_a_v1alpha2_type", func(t *testing.T) {
		src := &RoleSpec{
			Name: "worker", Replicas: ptr.To(int32(1)),
			Workload: WorkloadSpec{APIVersion: "workloads.x-k8s.io/v1alpha2", Kind: "RoleInstanceSet"},
		}
		dst := &v2.RoleSpec{}
		if err := convertRoleV1alpha1ToV2(src, dst); err != nil {
			t.Fatalf("conversion failed: %v", err)
		}
		if _, err := newV(true).ValidateCreate(context.Background(), wrap(dst)); err != nil {
			t.Errorf("CONTROL FAILED: even an explicitly RoleInstanceSet-typed v1alpha1 role was"+
				" rejected, which would make F8 unconditional: %v", err)
		} else {
			t.Logf("bounded: a v1alpha1 role survives only if the user explicitly writes the" +
				" v1alpha2 RoleInstanceSet type into the v1alpha1 workload field")
		}
	})
}
