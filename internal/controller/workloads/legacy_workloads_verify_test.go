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

// Verification harness for review findings on PR sgl-project/rbg#413
// ("--enable-legacy-workloads"). Head under test: 59e384d5.
//
// This file is a REVIEWER ARTIFACT. It adds no production code and is not
// intended to be merged as-is. Polarity is declared per test:
//
//	[CONTRACT] asserts the intended-correct behavior -> RED on buggy code,
//	           GREEN once fixed.
//	[CANARY]   asserts the current (suspected-wrong) behavior -> GREEN on
//	           buggy code, FLIPS TO RED once fixed (then invert it).
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
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/pkg/reconciler"
)

func vfyScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatalf("add clientgo scheme: %v", err)
	}
	if err := workloadsv1alpha2.AddToScheme(s); err != nil {
		t.Fatalf("add rbg scheme: %v", err)
	}
	return s
}

func vfyCtx() context.Context {
	return ctrl.LoggerInto(context.Background(), zap.New().WithValues("env", "pr413-verify"))
}

// vfyRBG builds a minimal RBG whose roles carry the given workload-type
// annotations. An empty annotation means "default" (RoleInstanceSet).
func vfyRBG(name, ns string, roleWorkloadTypes map[string]string) *workloadsv1alpha2.RoleBasedGroup {
	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			UID:       types.UID("uid-" + name),
		},
	}
	for roleName, wt := range roleWorkloadTypes {
		role := workloadsv1alpha2.RoleSpec{Name: roleName}
		if wt != "" {
			role.Annotations = map[string]string{constants.RoleWorkloadTypeAnnotationKey: wt}
		}
		rbg.Spec.Roles = append(rbg.Spec.Roles, role)
	}
	return rbg
}

func vfyReconciler(t *testing.T, enableLegacy bool, objs ...client.Object) *RoleBasedGroupReconciler {
	t.Helper()
	s := vfyScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).Build()
	return &RoleBasedGroupReconciler{
		client:                c,
		apiReader:             c,
		scheme:                s,
		recorder:              record.NewFakeRecorder(200),
		workloadReconciler:    make(map[string]reconciler.WorkloadReconciler),
		enableLegacyWorkloads: enableLegacy,
	}
}

// ---------------------------------------------------------------------------
// F3a [CONTRACT] -- the PR's own guard. Legacy workload kinds must be refused
// when --enable-legacy-workloads=false, and non-legacy kinds must still work.
// Expected GREEN on head 59e384d5 (this is the fix the author added after the
// first review round); it is kept as a regression guard.
// ---------------------------------------------------------------------------
func TestVerifyPR413_F3a_LegacyKindsRejectedWhenDisabled(t *testing.T) {
	legacy := []string{
		constants.DeploymentWorkloadType,
		constants.StatefulSetWorkloadType,
		constants.LeaderWorkerSetWorkloadType,
	}
	for _, wt := range legacy {
		t.Run("reject_"+wt, func(t *testing.T) {
			r := vfyReconciler(t, false)
			role := workloadsv1alpha2.RoleSpec{
				Name:        "r",
				Annotations: map[string]string{constants.RoleWorkloadTypeAnnotationKey: wt},
			}
			rec, err := r.getOrCreateWorkloadReconciler(vfyCtx(), role.GetWorkloadSpec())
			if err == nil {
				t.Fatalf("expected legacy kind %q to be rejected when legacy workloads are disabled, got reconciler %T", wt, rec)
			}
			if !strings.Contains(err.Error(), "not supported when legacy workloads are disabled") {
				t.Errorf("unexpected rejection reason for %q: %v", wt, err)
			}
		})
	}

	t.Run("allow_RoleInstanceSet", func(t *testing.T) {
		r := vfyReconciler(t, false)
		role := workloadsv1alpha2.RoleSpec{Name: "r"} // default = RoleInstanceSet
		if _, err := r.getOrCreateWorkloadReconciler(vfyCtx(), role.GetWorkloadSpec()); err != nil {
			t.Fatalf("RoleInstanceSet must still be reconcilable when legacy workloads are disabled: %v", err)
		}
	})
}

// ---------------------------------------------------------------------------
// F3b [CANARY] -- granularity of the refusal.
//
// A single legacy role makes preCheck fail for the WHOLE RoleBasedGroup.
// Reconcile calls preCheck first (rolebasedgroup_controller.go:183) and returns
// on error, so every other role in that RBG -- including healthy
// RoleInstanceSet roles -- stops being reconciled, status is never updated, and
// deleteOrphanRoles is never reached.
//
// This test PASSES on head 59e384d5, documenting the fail-closed-whole-group
// behavior. It FLIPS TO RED if the author moves to per-role degradation (or
// rejects legacy roles at admission instead) -- at which point invert it.
// ---------------------------------------------------------------------------
func TestVerifyPR413_F3b_OneLegacyRoleFailsEntireGroup_Canary(t *testing.T) {
	mixed := vfyRBG("mixed", "default", map[string]string{
		"modern": "", // RoleInstanceSet
		"legacy": constants.DeploymentWorkloadType,
	})

	r := vfyReconciler(t, false, mixed)
	err := r.preCheck(vfyCtx(), mixed)
	if err == nil {
		t.Fatalf("CANARY FLIPPED: preCheck no longer fails the whole RBG for a single legacy role. " +
			"Per-role degradation may now be implemented -- re-read the code and invert this test.")
	}
	if !strings.Contains(err.Error(), "legacy") {
		t.Fatalf("preCheck failed for an unrelated reason, harness does not bite: %v", err)
	}
	// The healthy role is collateral damage: the aggregate error aborts Reconcile
	// before any role is reconciled.
	t.Logf("observed: whole-group preCheck failure caused by one legacy role: %v", err)
}

// ---------------------------------------------------------------------------
// F4 [CONTRACT] -- orphan cleanup must survive disabling legacy workloads.
//
// deleteOrphanRoles skips CleanupOrphanedWorkloads for Deployment/StatefulSet/
// LWS when the flag is off (rolebasedgroup_controller.go:703). An RBG-owned
// Deployment left over from a role that has since been migrated to
// RoleInstanceSet therefore leaks: it is owner-referenced by the RBG so
// Kubernetes GC will not collect it until the whole RBG is deleted, and its
// pods keep running alongside the new RoleInstanceSet pods.
//
// The RBG here contains ONLY a RoleInstanceSet role, so F3b's whole-group
// refusal does not apply -- this isolates F4.
//
// Expected RED on head 59e384d5. The enableLegacyWorkloads=true subtest is the
// control that proves the harness bites.
// ---------------------------------------------------------------------------
func TestVerifyPR413_F4_OrphanedLegacyWorkloadLeaksWhenDisabled(t *testing.T) {
	const ns = "default"

	build := func() (*workloadsv1alpha2.RoleBasedGroup, *appsv1.Deployment) {
		// Post-migration RBG: the only role is now a RoleInstanceSet role.
		rbg := vfyRBG("mig", ns, map[string]string{"worker": ""})
		// Left over from when "worker" (or a since-removed role) was a Deployment.
		orphan := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "mig-oldrole",
				Namespace: ns,
				Labels:    map[string]string{constants.GroupNameLabelKey: rbg.Name},
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: workloadsv1alpha2.GroupVersion.String(),
					Kind:       "RoleBasedGroup",
					Name:       rbg.Name,
					UID:        rbg.UID,
					Controller: ptrBool(true),
				}},
			},
		}
		return rbg, orphan
	}

	orphanStillExists := func(t *testing.T, enableLegacy bool) bool {
		t.Helper()
		rbg, orphan := build()
		r := vfyReconciler(t, enableLegacy, rbg, orphan)
		if err := r.deleteOrphanRoles(vfyCtx(), rbg); err != nil {
			// RoleInstanceSet/scaling-adapter cleanup may complain under the fake
			// client; that is not what we are asserting on.
			t.Logf("deleteOrphanRoles returned (non-fatal for this assertion): %v", err)
		}
		got := &appsv1.Deployment{}
		err := r.client.Get(vfyCtx(), types.NamespacedName{Name: orphan.Name, Namespace: ns}, got)
		if apierrors.IsNotFound(err) {
			return false
		}
		if err != nil {
			t.Fatalf("unexpected error reading orphan Deployment: %v", err)
		}
		return true
	}

	t.Run("control_legacyEnabled_orphanIsCleanedUp", func(t *testing.T) {
		if orphanStillExists(t, true) {
			t.Fatal("HARNESS DOES NOT BITE: orphaned Deployment survived even with " +
				"enableLegacyWorkloads=true, so this test is not exercising CleanupOrphanedWorkloads")
		}
	})

	t.Run("legacyDisabled_orphanMustStillBeCleanedUp", func(t *testing.T) {
		if orphanStillExists(t, false) {
			t.Fatal("F4 REPRODUCED: RBG-owned orphaned Deployment was NOT deleted when " +
				"--enable-legacy-workloads=false. It is owner-referenced by the RBG, so GC will " +
				"not collect it until the RBG itself is deleted; its pods keep running alongside " +
				"the new RoleInstanceSet pods. Cleanup of already-created legacy workloads must " +
				"remain enabled regardless of the flag.")
		}
	})
}

func ptrBool(b bool) *bool { return &b }

// vfySilenceUnused keeps corev1 imported for future scenarios without tripping
// the compiler if a subtest is removed.
var _ = corev1.Pod{}
