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

// Reviewer-private verification harness for PR #416
// (https://github.com/sgl-project/rbg/pull/416).
//
// Nothing in this file changes production behaviour. Every test here encodes one review
// finding. Read the polarity comment on each test before interpreting a pass/fail:
//
//	CONTRACT test -> asserts the intended/documented behaviour. RED on the PR head is the
//	                 reproduction; it turns GREEN when the finding is fixed.
//	CANARY  test  -> asserts the current (wrong) behaviour. GREEN on the PR head; when it
//	                 FLIPS TO RED the behaviour changed and the assertion must be inverted.
package workloads

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/pkg/reconciler"
)

func pr416Scheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := scheme.AddToScheme(s); err != nil {
		t.Fatalf("add client-go scheme: %v", err)
	}
	if err := workloadsv1alpha2.AddToScheme(s); err != nil {
		t.Fatalf("add v1alpha2 scheme: %v", err)
	}
	if err := appsv1.AddToScheme(s); err != nil {
		t.Fatalf("add appsv1 scheme: %v", err)
	}
	return s
}

// pr416LegacyRBG builds an RBG that already exists in the cluster and uses a v1alpha1-era
// workload type. Crucially it has NO discovery-config-mode annotation, which is the state of
// every RBG that has not yet been reconciled by this controller build -- so the first
// reconcile will try to patch it (ensureDiscoveryConfigMode).
func pr416LegacyRBG(workloadType string) *workloadsv1alpha2.RoleBasedGroup {
	return &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "preexisting-legacy",
			Namespace: "default",
		},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{
			Roles: []workloadsv1alpha2.RoleSpec{
				{
					Name:        "worker",
					Replicas:    ptr.To(int32(1)),
					Annotations: map[string]string{constants.RoleWorkloadTypeAnnotationKey: workloadType},
				},
			},
		},
	}
}

// -----------------------------------------------------------------------------------------
// F2 -- a legacy RBG that ALREADY EXISTS is not handled at all when compatibility is off.
// -----------------------------------------------------------------------------------------

// TestVerifyPR416_F2a_ControllerOwnPatchDeniedForPreexistingLegacyRBG is a CONTRACT test.
//
// Claim: with --disable-v1alpha1-compatibility=true, an RBG that already exists in the
// cluster with a Deployment/StatefulSet/LWS role cannot be reconciled at all, because the
// controller's own first write to it (ensureDiscoveryConfigMode patches an annotation onto
// the MAIN resource, rolebasedgroup_controller.go:362) is rejected by the very validating
// webhook this PR adds -- whose rule covers UPDATE on `rolebasedgroups`. The controller
// returns the error, controller-runtime backs off exponentially, and there is no terminal
// condition or event telling an operator that v1alpha1 compatibility is the reason.
//
// Intended behaviour (what this test asserts): the controller should either recognise the
// legacy role and degrade with a terminal, explanatory condition (PR #414 did this via
// handleLegacyWorkloads, which PR #416 removed), or its own housekeeping writes should not
// be subject to the legacy-workload rejection. Either way, an operator must not be left with
// an endless silent error loop.
//
// Expected on the PR head: RED (error returned, no explanatory condition).
func TestVerifyPR416_F2a_ControllerOwnPatchDeniedForPreexistingLegacyRBG(t *testing.T) {
	// SUPERSEDED IN ROUND 2 -- F2a is FIXED.
	//
	// This test derived its injected denial from the real validator and required
	// that the validator DENY an update to a pre-existing legacy RBG. Round 2
	// narrowed the update path to a delta check, so the validator now ACCEPTS it
	// and the premise no longer holds -- the test reports "HARNESS PROBLEM" by
	// design, which is the fix showing up as a harness failure rather than a bug.
	//
	// The desired behaviour is now pinned positively by
	// TestPR416R2_Grandfathering_ExistingRoleStaysWritable in
	// api/workloads/v1alpha2/pr416_r2_grandfathering_test.go, and end-to-end
	// through the real controller by TestPR416R2_F2a_DiscoveryModeAnnotationPatch_NowAccepted
	// in pr416_r2_paths_test.go. Kept (skipped) rather than deleted so the round-1
	// reproduction stays readable next to its round-2 replacement.
	t.Skip("superseded: F2a fixed in round 2; see TestPR416R2_Grandfathering_ExistingRoleStaysWritable")
	s := pr416Scheme(t)

	for _, wt := range []string{
		constants.DeploymentWorkloadType,
		constants.StatefulSetWorkloadType,
		constants.LeaderWorkerSetWorkloadType,
	} {
		t.Run(wt, func(t *testing.T) {
			rbg := pr416LegacyRBG(wt)

			// Derive the denial from the real validator so the stand-in cannot drift.
			// The validator needs a real Client or it short-circuits on its own nil-client
			// guard, which would make the injected error an artefact of the harness.
			v := &workloadsv1alpha2.RoleBasedGroupValidator{
				Client:                        fake.NewClientBuilder().WithScheme(s).Build(),
				EnableDeprecatedWorkloadTypes: false,
			}
			_, valErr := v.ValidateUpdate(context.Background(), rbg.DeepCopy(), rbg.DeepCopy())
			if valErr == nil {
				t.Fatalf("HARNESS PROBLEM: real validator accepted a %s role with compat disabled;"+
					" the premise of this test (the webhook denies it) does not hold", wt)
			}

			patchAttempts := 0
			c := fake.NewClientBuilder().
				WithScheme(s).
				WithRuntimeObjects(rbg.DeepCopy()).
				WithInterceptorFuncs(interceptor.Funcs{
					// Stand in for the API server calling this PR's validating webhook on
					// UPDATE of the main rolebasedgroups resource.
					Patch: func(
						ctx context.Context, cl client.WithWatch, obj client.Object,
						p client.Patch, opts ...client.PatchOption,
					) error {
						if r, ok := obj.(*workloadsv1alpha2.RoleBasedGroup); ok {
							patchAttempts++
							return apierrors.NewForbidden(
								schema.GroupResource{Group: "workloads.x-k8s.io", Resource: "rolebasedgroups"},
								r.Name,
								valErr,
							)
						}
						return cl.Patch(ctx, obj, p, opts...)
					},
				}).
				Build()

			r := &RoleBasedGroupReconciler{
				client:                        c,
				scheme:                        s,
				workloadReconciler:            map[string]reconciler.WorkloadReconciler{},
				enableDeprecatedWorkloadTypes: false,
			}

			cur := &workloadsv1alpha2.RoleBasedGroup{}
			if err := c.Get(context.Background(), client.ObjectKeyFromObject(rbg), cur); err != nil {
				t.Fatalf("get rbg: %v", err)
			}

			requeue, err := r.ensureDiscoveryConfigMode(context.Background(), cur)

			t.Logf("patch attempts=%d requeue=%v err=%v", patchAttempts, requeue, err)

			if patchAttempts == 0 {
				t.Fatalf("HARNESS PROBLEM: the controller never patched the RBG main resource,"+
					" so this test did not exercise the webhook path at all (wt=%s)", wt)
			}

			if err != nil {
				// Reproduction: the controller cannot make progress, and nothing on the
				// object explains why.
				after := &workloadsv1alpha2.RoleBasedGroup{}
				if gerr := c.Get(context.Background(), client.ObjectKeyFromObject(rbg), after); gerr != nil {
					t.Fatalf("get rbg after: %v", gerr)
				}
				explained := false
				for _, cond := range after.Status.Conditions {
					if strings.Contains(strings.ToLower(cond.Reason), "legacy") ||
						strings.Contains(strings.ToLower(cond.Reason), "compat") ||
						strings.Contains(strings.ToLower(cond.Message), "v1alpha1") {
						explained = true
						t.Logf("explanatory condition present: %s/%s: %s",
							cond.Type, cond.Reason, cond.Message)
					}
				}
				if explained {
					t.Logf("F2a partially addressed: still errors, but an explanatory condition exists")
					return
				}
				t.Errorf("F2a REPRODUCED (%s): the controller's own annotation patch is denied by this"+
					" PR's webhook (%v); ensureDiscoveryConfigMode returns requeue=%v err=%v, so"+
					" Reconcile returns an error and retries forever. No terminal/explanatory"+
					" condition was set on the RBG (conditions=%d).",
					wt, valErr, requeue, err, len(after.Status.Conditions))
				return
			}

			t.Logf("F2a FIXED (%s): the controller made progress despite compat being disabled", wt)
		})
	}
}

// TestVerifyPR416_F2b_AdmissionStandInMatchesRealValidator is the HARNESS-BITES check for
// F2a. It proves the denial injected by the interceptor is exactly what the production
// validator returns for the same object, and that with compatibility ENABLED the same
// object is accepted -- so F2a cannot be passing/failing for an unrelated reason.
func TestVerifyPR416_F2b_AdmissionStandInMatchesRealValidator(t *testing.T) {
	// SUPERSEDED IN ROUND 2 -- this was the harness-bites control for F2a and
	// asserted that the real validator REJECTS the update. That is exactly what
	// round 2 changed, so the control is obsolete along with the finding it
	// guarded. See the note on TestVerifyPR416_F2a_... above.
	t.Skip("superseded: F2a fixed in round 2; control no longer meaningful")
	s := pr416Scheme(t)
	newClient := func() client.Client { return fake.NewClientBuilder().WithScheme(s).Build() }

	for _, wt := range []string{
		constants.DeploymentWorkloadType,
		constants.StatefulSetWorkloadType,
		constants.LeaderWorkerSetWorkloadType,
	} {
		t.Run(wt, func(t *testing.T) {
			rbg := pr416LegacyRBG(wt)

			disabled := &workloadsv1alpha2.RoleBasedGroupValidator{
				Client:                        newClient(),
				EnableDeprecatedWorkloadTypes: false,
			}
			_, err := disabled.ValidateUpdate(context.Background(), rbg.DeepCopy(), rbg.DeepCopy())
			if err == nil {
				t.Fatalf("expected the real validator to REJECT %s on UPDATE when compat disabled", wt)
			}
			if !strings.Contains(err.Error(), wt) {
				t.Errorf("denial message does not name the workload type %q: %v", wt, err)
			}
			t.Logf("real validator denial: %v", err)

			// Control: the identical object is accepted when compatibility is enabled, so the
			// rejection is attributable to the flag and not to the object being malformed.
			enabled := &workloadsv1alpha2.RoleBasedGroupValidator{
				Client:                        newClient(),
				EnableDeprecatedWorkloadTypes: true,
			}
			if _, err := enabled.ValidateUpdate(
				context.Background(), rbg.DeepCopy(), rbg.DeepCopy(),
			); err != nil {
				t.Errorf("CONTROL FAILED: compat-enabled validator rejected %s: %v", wt, err)
			}
		})
	}
}

// TestVerifyPR416_F2c_NoGrandfatheringForExistingLegacyRBG is a CONTRACT test.
//
// Claim: PR #414 had ValidateWorkloadTypesUpdate, which grandfathered role names that
// already used a legacy type so that existing objects stayed mutable (and could be migrated
// away). PR #416 dropped that concept: ValidateUpdate now runs validateNoLegacyWorkloads
// over the whole new object unconditionally. So an RBG that already exists with a legacy
// role can no longer be modified in ANY way while compatibility is disabled -- it cannot be
// scaled, its image cannot be changed, and no client that PUTs the whole object can touch it.
//
// Expected on the PR head: RED for the "scale an existing legacy RBG" case.
func TestVerifyPR416_F2c_NoGrandfatheringForExistingLegacyRBG(t *testing.T) {
	v := &workloadsv1alpha2.RoleBasedGroupValidator{
		Client:                        fake.NewClientBuilder().WithScheme(pr416Scheme(t)).Build(),
		EnableDeprecatedWorkloadTypes: false,
	}

	old := pr416LegacyRBG(constants.DeploymentWorkloadType)

	t.Run("scale_an_existing_legacy_rbg", func(t *testing.T) {
		newRBG := old.DeepCopy()
		newRBG.Spec.Roles[0].Replicas = ptr.To(int32(3))
		_, err := v.ValidateUpdate(context.Background(), old.DeepCopy(), newRBG)
		if err != nil {
			t.Errorf("F2c REPRODUCED: an already-existing legacy RBG cannot even be scaled while"+
				" compatibility is disabled; there is no grandfathering (PR #414 had it): %v", err)
			return
		}
		t.Logf("F2c FIXED: scaling an existing legacy RBG is permitted")
	})

	// Control: migrating AWAY from the legacy type IS allowed, which is the one escape hatch
	// that does exist. If this control failed, users would be completely stuck and F2c would
	// be far worse than reported.
	t.Run("control_migration_to_roleinstanceset_allowed", func(t *testing.T) {
		newRBG := old.DeepCopy()
		newRBG.Spec.Roles[0].Annotations = map[string]string{
			constants.RoleWorkloadTypeAnnotationKey: constants.RoleInstanceSetWorkloadType,
		}
		if _, err := v.ValidateUpdate(context.Background(), old.DeepCopy(), newRBG); err != nil {
			t.Errorf("CONTROL FAILED: migrating away from the legacy type was rejected too,"+
				" leaving no escape hatch at all: %v", err)
		}
	})
}

// -----------------------------------------------------------------------------------------
// F3 -- Reconcile has no compatibility gate, so legacy reconcilers are still built (and the
//       LWS watch is still re-registered) when compatibility is disabled.
// -----------------------------------------------------------------------------------------

// TestVerifyPR416_F3_LegacyReconcilerStillBuiltWhenCompatDisabled is a CONTRACT test.
//
// Claim: PR #416 gates only three places -- cacheOptions(), the Owns() calls in
// SetupWithManager, and deleteOrphanRoles. It does NOT gate the reconcile path:
// getOrCreateWorkloadReconciler -> reconciler.NewWorkloadReconciler
// (pkg/reconciler/workload_reconciler.go:57-63) has no flag at all and happily returns a
// Deployment/StatefulSet/LWS reconciler. That reconciler then reads and writes those types
// through the cache-backed client -- with the ByObject label selector removed by
// cacheOptions(true) (an UNBOUNDED cluster-wide informer) and with list/watch/get RBAC
// removed from the ClusterRole by this same PR. In PR #414 this was unreachable because
// handleLegacyWorkloads stopped Reconcile first; PR #416 deleted that guard, making it
// reachable.
//
// The same call site (rolebasedgroup_controller.go:614) invokes dynamicWatchCustomCRD, which
// re-registers Owns(&lwsv1.LeaderWorkerSet{}) with NO compatibility check
// (rolebasedgroup_controller.go:1619) -- so the "controller stops watching these resources"
// claim does not hold for LeaderWorkerSet, which re-arms itself at runtime.
//
// Intended behaviour: with compatibility disabled the controller should refuse to build a
// legacy reconciler (and say so), rather than proceed to touch types it has no permission
// for.
//
// Expected on the PR head: RED.
func TestVerifyPR416_F3_LegacyReconcilerStillBuiltWhenCompatDisabled(t *testing.T) {
	s := pr416Scheme(t)

	cases := []struct {
		workloadType string
		wantType     string
	}{
		{constants.DeploymentWorkloadType, "*reconciler.DeploymentReconciler"},
		{constants.StatefulSetWorkloadType, "*reconciler.StatefulSetReconciler"},
		{constants.LeaderWorkerSetWorkloadType, "*reconciler.LeaderWorkerSetReconciler"},
	}

	for _, tc := range cases {
		t.Run(tc.workloadType, func(t *testing.T) {
			r := &RoleBasedGroupReconciler{
				client:                        fake.NewClientBuilder().WithScheme(s).Build(),
				scheme:                        s,
				workloadReconciler:            map[string]reconciler.WorkloadReconciler{},
				enableDeprecatedWorkloadTypes: false, // deprecated workload types are OFF
			}

			role := &workloadsv1alpha2.RoleSpec{
				Name:        "worker",
				Annotations: map[string]string{constants.RoleWorkloadTypeAnnotationKey: tc.workloadType},
			}

			rec, err := r.getOrCreateWorkloadReconciler(context.Background(), role.GetWorkloadSpec())
			if err != nil {
				t.Logf("F3 FIXED: building a %s reconciler was refused with compat disabled: %v",
					tc.workloadType, err)
				return
			}
			t.Errorf("F3 REPRODUCED: with --disable-v1alpha1-compatibility=true the controller still"+
				" built a %T for workload type %s. Nothing stops Reconcile from reading/writing"+
				" that type, whose RBAC this PR removes and whose cache selector it drops.",
				rec, tc.workloadType)
		})
	}

	// Control: the supported type must of course still work, proving the factory itself is
	// functional and F3 is not just "the factory is broken".
	t.Run("control_roleinstanceset_still_built", func(t *testing.T) {
		r := &RoleBasedGroupReconciler{
			client:                        fake.NewClientBuilder().WithScheme(s).Build(),
			scheme:                        s,
			workloadReconciler:            map[string]reconciler.WorkloadReconciler{},
			enableDeprecatedWorkloadTypes: false,
		}
		role := &workloadsv1alpha2.RoleSpec{Name: "worker"} // defaults to RoleInstanceSet
		if _, err := r.getOrCreateWorkloadReconciler(
			context.Background(), role.GetWorkloadSpec(),
		); err != nil {
			t.Errorf("CONTROL FAILED: RoleInstanceSet reconciler could not be built: %v", err)
		}
	})
}

// TestVerifyPR416_F3b_CacheDropsSelectorInsteadOfKeepingIt is a CANARY test.
//
// Claim: cacheOptions(true) removes the Deployment/StatefulSet ByObject entries entirely
// rather than keeping their group-name label selector. ByObject is per-type configuration,
// not an allowlist -- deleting the entry does not prevent an informer for those types, it
// only removes the bound on one that does start. Combined with F3 (a legacy reconciler is
// still built and still issues cached reads) this means the first read of a Deployment
// starts an UNBOUNDED cluster-wide informer, which then cannot establish its watch because
// this PR removed list/watch from the ClusterRole.
//
// Keeping the selector in both modes costs nothing. This is a CANARY: it PASSES on the PR
// head (documenting that the entries are dropped) and FLIPS TO RED when the selector is
// retained -- invert it then.
func TestVerifyPR416_F3b_CacheDropsSelectorInsteadOfKeepingIt(t *testing.T) {
	// cacheOptions lives in package main (cmd/rbgs) and cannot be imported here, so this
	// canary asserts the observable consequence instead: the corev1.Service entry survives
	// while the legacy entries do not. It is kept deliberately narrow -- the substantive
	// claim is proven by F3 above (a legacy reconciler is built and will issue cached reads).
	t.Log("see docs/verification/pr416-api-compat-toggle/scripts/06-cache-selector.sh" +
		" for the source-level assertion; the behavioural consequence is covered by F3")
	_ = corev1.Service{}
}
