package v1alpha2

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"sigs.k8s.io/rbgs/api/workloads/constants"
)

// Round-2 harness for PR #416.
//
// Round 1 found that turning the deprecated workload types off stranded every
// object that already used one: the operator's own writes were denied, forever,
// with nothing on the object to say why (F2a, F2c, F9, F10). Round 2 narrowed the
// UPDATE path to a delta check -- validateNoNewDeprecatedWorkloadTypes -- so a role
// that already carries a deprecated type stays writable.
//
// This file does two things:
//
//  1. REGRESSION GUARDS for what the delta check now gets right, so a later round
//     cannot quietly regress it (TestPR416R2_Grandfathering_*).
//  2. A CONTRACT test for the path the delta check does NOT cover: the
//     RoleBasedGroupSet controller CREATEs its child RoleBasedGroups
//     (internal/controller/workloads/rolebasedgroupset_controller.go:231), and
//     CREATE still runs the strict whole-object check. Grandfathering an RBGSet's
//     own UPDATE therefore does not let that RBGSet make a new child
//     (TestPR416R2_ChildRBGCreateIsStillDenied).
//
// Every denial assertion is paired with an ENABLED control proving the same
// operation is accepted when the deprecated types are on, so no test can pass
// vacuously. The validator is always built with a real fake client -- it has a
// nil-client guard that would otherwise contaminate the result.

func r2Validator(enabled bool) *RoleBasedGroupValidator {
	return &RoleBasedGroupValidator{
		Client:                        fake.NewClientBuilder().Build(),
		EnableDeprecatedWorkloadTypes: enabled,
	}
}

func r2SetValidator(enabled bool) *RoleBasedGroupSetValidator {
	return &RoleBasedGroupSetValidator{EnableDeprecatedWorkloadTypes: enabled}
}

// r2Role builds a role whose effective workload type is wt, using the
// role-workload-type annotation the v1alpha1 conversion webhook writes. This is
// the shape a pre-existing object actually has on disk.
func r2Role(name, wt string) RoleSpec {
	return RoleSpec{
		Name:        name,
		Replicas:    ptr.To(int32(1)),
		Annotations: map[string]string{constants.RoleWorkloadTypeAnnotationKey: wt},
	}
}

func r2RBG(roles ...RoleSpec) *RoleBasedGroup {
	return &RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "rbg", Namespace: "default"},
		Spec:       RoleBasedGroupSpec{Roles: roles},
	}
}

func r2RBGSet(roles ...RoleSpec) *RoleBasedGroupSet {
	return &RoleBasedGroupSet{
		ObjectMeta: metav1.ObjectMeta{Name: "rbgset", Namespace: "default"},
		Spec: RoleBasedGroupSetSpec{
			GroupTemplate: RoleBasedGroupTemplateSpec{
				Spec: RoleBasedGroupSpec{Roles: roles},
			},
		},
	}
}

var r2DeprecatedTypes = []string{
	constants.DeploymentWorkloadType,
	constants.StatefulSetWorkloadType,
	constants.LeaderWorkerSetWorkloadType,
}

// TestPR416R2_Grandfathering_ExistingRoleStaysWritable is a REGRESSION GUARD for
// the round-1 blockers F2a/F2c/F10. An update that leaves a pre-existing
// deprecated role's workload type alone must be accepted, because every one of the
// operator's own housekeeping writes (the discovery-mode annotation patch, the
// RBGSet template sync, the ScalingAdapter replica update) rewrites roles it does
// not mean to change. Round 1: RED. Must stay GREEN.
func TestPR416R2_Grandfathering_ExistingRoleStaysWritable(t *testing.T) {
	r3RetireGrandfatheringAssertion(t)
	for _, wt := range r2DeprecatedTypes {
		t.Run(wt, func(t *testing.T) {
			old := r2RBG(r2Role("worker", wt))

			// (a) a no-op update (what an annotation patch looks like to admission)
			if _, err := r2Validator(false).ValidateUpdate(context.Background(), old.DeepCopy(), old.DeepCopy()); err != nil {
				t.Fatalf("REGRESSION (F2a): no-op update of a pre-existing %s role was rejected: %v", wt, err)
			}

			// (b) a scale, which is what F2c was about
			scaled := old.DeepCopy()
			scaled.Spec.Roles[0].Replicas = ptr.To(int32(5))
			if _, err := r2Validator(false).ValidateUpdate(context.Background(), old.DeepCopy(), scaled); err != nil {
				t.Fatalf("REGRESSION (F2c): scaling a pre-existing %s role was rejected: %v", wt, err)
			}
		})
	}

	// CONTROL: the strict create-time check still rejects the same role, so the
	// acceptances above are the delta check at work and not a dead code path.
	for _, wt := range r2DeprecatedTypes {
		if _, err := r2Validator(false).ValidateCreate(context.Background(), r2RBG(r2Role("worker", wt))); err == nil {
			t.Fatalf("HARNESS PROBLEM: create of a %s role was also accepted, so the"+
				" update acceptances above prove nothing about grandfathering", wt)
		}
	}
}

// TestPR416R2_Grandfathering_StillRejectsNewAndSwapped is a REGRESSION GUARD for the
// other half: grandfathering must not become a hole. Adding a role that uses a
// deprecated type, or swapping one deprecated type for another, must still be
// rejected.
func TestPR416R2_Grandfathering_StillRejectsNewAndSwapped(t *testing.T) {
	r3RetireGrandfatheringAssertion(t)
	old := r2RBG(r2Role("worker", constants.StatefulSetWorkloadType))

	t.Run("newly added deprecated role is rejected", func(t *testing.T) {
		added := r2RBG(
			r2Role("worker", constants.StatefulSetWorkloadType),
			r2Role("extra", constants.DeploymentWorkloadType),
		)
		_, err := r2Validator(false).ValidateUpdate(context.Background(), old.DeepCopy(), added)
		if err == nil {
			t.Fatal("a newly added Deployment-backed role was accepted; grandfathering is too loose")
		}
		if !strings.Contains(err.Error(), "newly added role") {
			t.Errorf("error should say the role is newly added, got: %v", err)
		}
		// CONTROL: accepted when the deprecated types are enabled.
		if _, err := r2Validator(true).ValidateUpdate(context.Background(), old.DeepCopy(), added); err != nil {
			t.Fatalf("HARNESS PROBLEM: rejected even with the deprecated types ENABLED: %v", err)
		}
	})

	t.Run("swapping one deprecated type for another is rejected", func(t *testing.T) {
		swapped := r2RBG(r2Role("worker", constants.DeploymentWorkloadType))
		_, err := r2Validator(false).ValidateUpdate(context.Background(), old.DeepCopy(), swapped)
		if err == nil {
			t.Fatal("swapping StatefulSet -> Deployment was accepted; grandfathering is too loose")
		}
		if !strings.Contains(err.Error(), "cannot be changed") {
			t.Errorf("error should say the type cannot be changed, got: %v", err)
		}
		if _, err := r2Validator(true).ValidateUpdate(context.Background(), old.DeepCopy(), swapped); err != nil {
			t.Fatalf("HARNESS PROBLEM: rejected even with the deprecated types ENABLED: %v", err)
		}
	})
}

// TestPR416R2_Grandfathering_RoleRenameLosesTheExemption records a consequence of
// keying the exemption by role NAME: renaming a role that legitimately carries a
// deprecated type reads as "newly added" and is rejected, even though the object
// already used that type. Documented as a known edge rather than asserted as a
// defect -- a rename is arguably a new role. It matters because the RBGSet
// template is the source of the child's role names, so a template rename
// propagates here.
func TestPR416R2_Grandfathering_RoleRenameLosesTheExemption(t *testing.T) {
	old := r2RBG(r2Role("worker", constants.StatefulSetWorkloadType))
	renamed := r2RBG(r2Role("worker-v2", constants.StatefulSetWorkloadType))

	_, err := r2Validator(false).ValidateUpdate(context.Background(), old.DeepCopy(), renamed)
	if err == nil {
		t.Log("NOTE: renaming a pre-existing deprecated role is ACCEPTED (exemption survives a rename)")
		return
	}
	t.Logf("NOTE: renaming a pre-existing deprecated role is REJECTED, because the"+
		" exemption is keyed by role name: %v", err)
	if _, cErr := r2Validator(true).ValidateUpdate(context.Background(), old.DeepCopy(), renamed); cErr != nil {
		t.Fatalf("HARNESS PROBLEM: the rename is rejected even with the deprecated types ENABLED,"+
			" so the rejection above is not attributable to the toggle: %v", cErr)
	}
}

// TestPR416R2_ChildRBGCreateIsStillDenied is a CONTRACT test for the gap the delta
// check leaves open, i.e. the unfixed remainder of round-1 blocker F9.
//
// A RoleBasedGroupSet does not own its children's roles in-place: its controller
// CREATEs one RoleBasedGroup per replica --
// internal/controller/workloads/rolebasedgroupset_controller.go:231
// (`r.client.Create(ctx, rbg)`), reached whenever a child is missing. The
// validating webhook covers verbs=create;update on rolebasedgroups, and
// ValidateCreate still runs the strict whole-object check with no delta exemption.
//
// So for a pre-existing RBGSet whose template uses a deprecated workload type:
//   - its own UPDATE is now grandfathered (proved below as a control), but
//   - it can no longer materialise a child.
//
// Concretely: scaling the RBGSet up, or any child being deleted (node drain,
// manual kubectl delete, GC), leaves a replica that can never come back, and the
// controller retries the denied CREATE forever. Round 1 flagged the update path;
// round 2 fixed that and left this one.
//
// This test asserts the INTENDED behaviour -- a child create for a template that
// was already accepted should not be denied -- so it is RED while the gap exists
// and turns GREEN when it is closed.
func TestPR416R2_ChildRBGCreateIsStillDenied(t *testing.T) {
	r3RetireGrandfatheringAssertion(t)
	for _, wt := range r2DeprecatedTypes {
		t.Run(wt, func(t *testing.T) {
			ctx := context.Background()
			template := r2Role("worker", wt)
			set := r2RBGSet(template)

			// PREMISE 1: the pre-existing RBGSet's own update is grandfathered.
			// If this failed, the finding below would be about the update path, not
			// about child creation.
			if _, err := r2SetValidator(false).ValidateUpdate(ctx, set.DeepCopy(), set.DeepCopy()); err != nil {
				t.Fatalf("HARNESS PROBLEM: the RBGSet's own update is still denied (%v);"+
					" this test is about the child CREATE and needs the update path to be exempt", err)
			}

			// PREMISE 2: the child create is accepted when the deprecated types are
			// enabled -- i.e. this object shape is otherwise valid.
			child := r2RBG(template)
			if _, err := r2Validator(true).ValidateCreate(ctx, child.DeepCopy()); err != nil {
				t.Fatalf("HARNESS PROBLEM: the child RBG is rejected even with the deprecated"+
					" types ENABLED, so any denial below is not attributable to the toggle: %v", err)
			}

			// THE CLAIM: with the toggle off, the child the RBGSet controller must
			// create is denied, even though the parent RBGSet is grandfathered.
			_, err := r2Validator(false).ValidateCreate(ctx, child.DeepCopy())
			if err == nil {
				t.Logf("R2-F13 FIXED: the child RoleBasedGroup create for a grandfathered"+
					" %s RBGSet template is now accepted", wt)
				return
			}
			t.Errorf("R2-F13 REPRODUCED: the RBGSet's own update is grandfathered, but the child"+
				" RoleBasedGroup it must CREATE (rolebasedgroupset_controller.go:231) is denied"+
				" for workload type %s, so a pre-existing RBGSet can no longer scale up or"+
				" replace a deleted child; the controller retries the denied create forever."+
				" Denial: %v", wt, err)
		})
	}
}

// r3RetireGrandfatheringAssertion retires a round-2 assertion that the design
// reversal at aac6056d made meaningless.
//
// Round 2 narrowed ValidateUpdate to a delta check
// (validateNoNewDeprecatedWorkloadTypes) so a pre-existing deprecated role stayed
// writable. Commit aac6056d deletes that function and restores the strict
// whole-object check on both create and update, for RoleBasedGroup and
// RoleBasedGroupSet alike.
//
// So these tests are NOT evidence of a regression -- the contract they encode no
// longer exists, deliberately. The replacement pins for the current design are the
// TestPR416R3_* tests in api/workloads/v1alpha2/pr416_r3_design_test.go. The
// question the reversal actually raises -- whether the "fresh installation only"
// premise that justifies it is enforced anywhere (it is not) -- is R3-F22, proved by
// docs/verification/pr416-api-compat-toggle/scripts/10-fresh-install-invariant.sh.
func r3RetireGrandfatheringAssertion(t *testing.T) {
	t.Helper()
	t.Skip("superseded: grandfathering removed at aac6056d; see TestPR416R3_* pins and R3-F22")
}
