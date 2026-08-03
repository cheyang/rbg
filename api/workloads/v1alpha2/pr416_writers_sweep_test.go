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

// Reviewer-private harness for PR #416 -- cross-file sweep results (F9, F10, F8b).
//
// The sweep looked for more instances of the shape that produced F8 and P1: several
// individually reasonable decisions, in different files, composing into a breaking outcome that
// no single-file review would catch.
//
// It found one systemic instance. This PR adds an UPDATE rule to the rolebasedgroups
// validating webhook (verbs=create;update) without auditing who writes that resource. THREE
// separate controllers do, and every one of them is denied for a legacy-typed RBG once
// compatibility is disabled:
//
//  1. internal/controller/workloads/rolebasedgroup_controller.go:362
//     ensureDiscoveryConfigMode -> client.Patch(rbg)          [filed as F2a]
//  2. internal/controller/workloads/rolebasedgroupset_controller.go:465
//     RBGSet syncing groupTemplate onto a child -> client.Update(latestRBG)   [F9]
//  3. internal/controller/workloads/rolebasedgroupscalingadapter_controller.go:511
//     updateRoleReplicas -> client.Update(rbg)  -- the HPA / scale path       [F10]
//
// None of the three sets a condition, and all three retry forever.
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

// legacyRole is the role shape that v1alpha1 conversion produces for every v1alpha1 object
// (the CRD default is apps/v1/StatefulSet -- see the F8 chain).
func pr416SweepLegacyRole(name string) RoleSpec {
	return RoleSpec{
		Name:        name,
		Replicas:    ptr.To(int32(1)),
		Annotations: map[string]string{constants.RoleWorkloadTypeAnnotationKey: constants.StatefulSetWorkloadType},
	}
}

func pr416SweepValidator(disabled bool) *RoleBasedGroupValidator {
	return &RoleBasedGroupValidator{
		Client:                        fake.NewClientBuilder().Build(),
		EnableDeprecatedWorkloadTypes: !disabled,
	}
}

// TestVerifyPR416_F9F10_ThreeControllersAreDeniedByTheNewUpdateRule is a CONTRACT test.
//
// Each subtest reproduces the exact object mutation one production writer performs, and
// asserts the intended behaviour: a controller's own housekeeping write on an existing object
// should not be rejected by a rule aimed at user intent. On the PR head all three are RED.
func TestVerifyPR416_F9F10_ThreeControllersAreDeniedByTheNewUpdateRule(t *testing.T) {
	ctx := context.Background()

	base := &RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "preexisting-legacy", Namespace: "default"},
		Spec:       RoleBasedGroupSpec{Roles: []RoleSpec{pr416SweepLegacyRole("worker")}},
	}

	writers := []struct {
		id       string
		site     string
		what     string
		mutate   func(*RoleBasedGroup)
		blastRad string
	}{
		{
			id:   "F2a",
			site: "rolebasedgroup_controller.go:362",
			what: "ensureDiscoveryConfigMode patches an annotation onto the main resource",
			mutate: func(r *RoleBasedGroup) {
				if r.Annotations == nil {
					r.Annotations = map[string]string{}
				}
				r.Annotations["rbg.workloads.x-k8s.io/discovery-config-mode"] = "refine"
			},
			blastRad: "the RBG can never be reconciled at all",
		},
		{
			id:   "F9",
			site: "rolebasedgroupset_controller.go:465",
			what: "RBGSet copies groupTemplate.spec.roles onto the child and Updates it",
			mutate: func(r *RoleBasedGroup) {
				// Identical roles -- the sync is idempotent, yet still rejected.
				r.Spec.Roles = []RoleSpec{pr416SweepLegacyRole("worker")}
			},
			blastRad: "a pre-existing RBGSet with a legacy template error-loops forever;" +
				" this is PR#414's F7 recurring, because the new RBGSet validator only" +
				" guards NEW RBGSets, not ones already in the cluster",
		},
		{
			id:   "F10",
			site: "rolebasedgroupscalingadapter_controller.go:511",
			what: "updateRoleReplicas sets role.Replicas and Updates -- the HPA/scale path",
			mutate: func(r *RoleBasedGroup) {
				r.Spec.Roles[0].Replicas = ptr.To(int32(5))
			},
			blastRad: "HPA-driven autoscaling of a legacy RBG fails; note retry.RetryOnConflict" +
				" only retries Conflict, so an admission Forbidden propagates straight out",
		},
	}

	for _, w := range writers {
		t.Run(w.id+"_"+w.site, func(t *testing.T) {
			oldRBG := base.DeepCopy()
			newRBG := base.DeepCopy()
			w.mutate(newRBG)

			_, err := pr416SweepValidator(true).ValidateUpdate(ctx, oldRBG, newRBG)

			// Control: the identical write is accepted when compatibility is enabled, so the
			// denial is attributable to the flag and not to the mutation being invalid.
			if _, ctlErr := pr416SweepValidator(false).ValidateUpdate(
				ctx, base.DeepCopy(), newRBG.DeepCopy(),
			); ctlErr != nil {
				t.Fatalf("CONTROL FAILED for %s: compat-enabled validator also rejected the write"+
					" (%v); this subtest cannot attribute anything to the flag", w.id, ctlErr)
			}

			if err != nil {
				t.Errorf("%s REPRODUCED at %s: %s, and the new UPDATE rule denies it (%v)."+
					" Consequence: %s.",
					w.id, w.site, w.what, err, w.blastRad)
				return
			}
			t.Logf("%s FIXED: the write at %s is permitted", w.id, w.site)
		})
	}
}

// TestVerifyPR416_F8b_V1alpha1RBGSetIsAlsoRejectedWholesale is a CONTRACT test.
//
// F8 established that disabling compatibility rejects every v1alpha1 RoleBasedGroup, because
// the CRD default puts StatefulSet in the workload field and conversion stamps it into the
// annotation. The sweep found the same chain reaches RoleBasedGroupSet: the v1alpha1
// rolebasedgroupsets schema carries the same default on spec.template.roles[].workload, and
// rolebasedgroupset_conversion.go:37 routes through the SAME convertRoleV1alpha1ToV2.
//
// This matters for the fix: a remedy confined to the RBG validator would leave RBGSet broken.
func TestVerifyPR416_F8b_V1alpha1RBGSetIsAlsoRejectedWholesale(t *testing.T) {
	ctx := context.Background()

	// What conversion produces for a v1alpha1 RBGSet whose template never mentioned `workload`.
	rbgset := &RoleBasedGroupSet{
		ObjectMeta: metav1.ObjectMeta{Name: "converted-from-v1alpha1", Namespace: "default"},
		Spec: RoleBasedGroupSetSpec{
			Replicas: ptr.To(int32(1)),
			GroupTemplate: RoleBasedGroupTemplateSpec{
				Spec: RoleBasedGroupSpec{Roles: []RoleSpec{pr416SweepLegacyRole("worker")}},
			},
		},
	}

	disabled := &RoleBasedGroupSetValidator{EnableDeprecatedWorkloadTypes: false}
	_, err := disabled.ValidateCreate(ctx, rbgset)

	// Control: accepted with compatibility enabled.
	enabled := &RoleBasedGroupSetValidator{EnableDeprecatedWorkloadTypes: true}
	if _, ctlErr := enabled.ValidateCreate(ctx, rbgset.DeepCopy()); ctlErr != nil {
		t.Fatalf("CONTROL FAILED: compat-enabled RBGSet validator rejected the object (%v);"+
			" nothing can be attributed to the flag", ctlErr)
	}

	if err != nil {
		if !strings.Contains(err.Error(), constants.StatefulSetWorkloadType) {
			t.Errorf("unexpected denial reason (expected it to name %s): %v",
				constants.StatefulSetWorkloadType, err)
		}
		t.Errorf("F8b REPRODUCED: a v1alpha1 RoleBasedGroupSet whose template never specified a"+
			" workload type is rejected wholesale when compatibility is disabled: %v."+
			" The defaulting chain reaches RBGSet through rolebasedgroupset_conversion.go:37,"+
			" so any fix must cover BOTH kinds, not just RoleBasedGroup.", err)
		return
	}
	t.Logf("F8b FIXED: a defaulted v1alpha1 RBGSet is accepted with compatibility disabled")
}
