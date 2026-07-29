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

// Verification harness for review findings on PR sgl-project/rbg#414
// ("--enable-v1alpha1-compat"). Head under test: 66a2500a.
//
// PR#414 shares its head branch (0727-lagacy) with the closed PR#413, so the
// F-numbers here are a fresh set; the mapping back to the PR#413 round is
// recorded in verify-manifest.json.
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
	"fmt"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/pkg/reconciler"
)

func p414Scheme(t *testing.T) *runtime.Scheme {
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

func p414Ctx() context.Context {
	return ctrl.LoggerInto(context.Background(), zap.New().WithValues("env", "pr414-verify"))
}

// p414RBG builds a minimal RBG whose roles carry the given workload-type
// annotations. An empty annotation means "default" (RoleInstanceSet).
// Role order is fixed so tests are deterministic.
func p414RBG(name, ns string, roles [][2]string) *workloadsv1alpha2.RoleBasedGroup {
	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Namespace:  ns,
			UID:        types.UID("uid-" + name),
			Generation: 1,
		},
	}
	for _, rw := range roles {
		role := workloadsv1alpha2.RoleSpec{Name: rw[0]}
		if rw[1] != "" {
			role.Annotations = map[string]string{constants.RoleWorkloadTypeAnnotationKey: rw[1]}
		}
		rbg.Spec.Roles = append(rbg.Spec.Roles, role)
	}
	return rbg
}

func p414Reconciler(t *testing.T, compat bool, objs ...client.Object) *RoleBasedGroupReconciler {
	t.Helper()
	s := p414Scheme(t)
	c := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(objs...).
		WithStatusSubresource(&workloadsv1alpha2.RoleBasedGroup{}).
		Build()
	return &RoleBasedGroupReconciler{
		client:               c,
		apiReader:            c,
		scheme:               s,
		recorder:             record.NewFakeRecorder(200),
		workloadReconciler:   make(map[string]reconciler.WorkloadReconciler),
		enableV1Alpha1Compat: compat,
	}
}

// ---------------------------------------------------------------------------
// F3 [CONTRACT] -- the flag's own guard, kept as a regression test.
// getOrCreateWorkloadReconciler must refuse v1alpha1 indirect kinds when
// compat is off, and must still serve RoleInstanceSet.
// Expected GREEN on 66a2500a.
// ---------------------------------------------------------------------------
func TestVerifyPR414_F3_LegacyKindsRejectedWhenCompatDisabled(t *testing.T) {
	for _, wt := range p414LegacyTypes() {
		t.Run("reject_"+wt, func(t *testing.T) {
			r := p414Reconciler(t, false)
			role := workloadsv1alpha2.RoleSpec{
				Name:        "r",
				Annotations: map[string]string{constants.RoleWorkloadTypeAnnotationKey: wt},
			}
			rec, err := r.getOrCreateWorkloadReconciler(p414Ctx(), role.GetWorkloadSpec())
			if err == nil {
				t.Fatalf("expected %q to be rejected when compat is disabled, got %T", wt, rec)
			}
			if !strings.Contains(err.Error(), "not supported when v1alpha1 compat is disabled") {
				t.Errorf("unexpected rejection reason for %q: %v", wt, err)
			}
		})
	}

	t.Run("allow_RoleInstanceSet", func(t *testing.T) {
		r := p414Reconciler(t, false)
		role := workloadsv1alpha2.RoleSpec{Name: "r"} // default = RoleInstanceSet
		if _, err := r.getOrCreateWorkloadReconciler(p414Ctx(), role.GetWorkloadSpec()); err != nil {
			t.Fatalf("RoleInstanceSet must remain reconcilable when compat is disabled: %v", err)
		}
	})
}

// ---------------------------------------------------------------------------
// F4 [CANARY] -- blast radius of handleLegacyWorkloads is the WHOLE RBG, and
// the stop is now TERMINAL.
//
// handleLegacyWorkloads runs at Step 0 of Reconcile, before preCheck and before
// any role is reconciled. One legacy role anywhere in .spec.roles makes it
// return stop=true, err=nil, so Reconcile returns ctrl.Result{}, nil -- no
// error, no RequeueAfter. Healthy RoleInstanceSet roles in the same RBG are
// never created, and nothing retries.
//
// Compared with the PR#413 round this is strictly worse for a mixed RBG: there
// the whole-group failure at least came back on the error backoff, so a later
// spec fix or a transient recovery would be picked up by the requeue. Here the
// only thing that can revive the group is a fresh watch event.
//
// PASSES on 66a2500a (documents the behavior). FLIPS TO RED when the author
// moves to per-role degradation -- invert it then.
// ---------------------------------------------------------------------------
func TestVerifyPR414_F4_MixedRBGTerminallyStoppedAsAWhole_Canary(t *testing.T) {
	mixed := p414RBG("mixed", "default", [][2]string{
		{"modern-a", ""}, // RoleInstanceSet
		{"modern-b", ""}, // RoleInstanceSet
		{"legacy", constants.DeploymentWorkloadType},
	})

	r := p414Reconciler(t, false, mixed)
	stop, err := r.handleLegacyWorkloads(p414Ctx(), mixed)

	if !stop {
		t.Fatalf("CANARY FLIPPED: handleLegacyWorkloads no longer stops the whole RBG for " +
			"a single legacy role. Per-role degradation may now be implemented -- re-read " +
			"the code and invert this test.")
	}
	if err != nil {
		t.Fatalf("expected a terminal stop with NO error (that is what makes it un-retried), got err=%v", err)
	}

	// stop=true + err=nil is exactly `return ctrl.Result{}, nil` in Reconcile:
	// controller-runtime treats that as "done, do not requeue".
	t.Logf("observed: 2 healthy RoleInstanceSet roles (%s) are abandoned because of 1 legacy role; "+
		"Reconcile returns Result{}=no-requeue, err=nil=no-backoff -> terminal for the whole group",
		"modern-a, modern-b")

	// And the condition blames the group, not the role.
	cond := apimeta.FindStatusCondition(mixed.Status.Conditions,
		string(workloadsv1alpha2.RoleBasedGroupReady))
	if cond == nil {
		t.Fatal("HARNESS DOES NOT BITE: no Ready condition was set at all")
	}
	if cond.Status != metav1.ConditionFalse || cond.Reason != LegacyWorkloadsDisabled {
		t.Fatalf("unexpected condition, harness may be measuring the wrong thing: %+v", cond)
	}
	t.Logf("observed condition: Ready=%s reason=%s msg=%q", cond.Status, cond.Reason, cond.Message)
}

// ---------------------------------------------------------------------------
// F5 [CANARY] -- .status.roleStatuses is frozen, not invalidated.
//
// handleLegacyWorkloads sets Ready=False/LegacyWorkloadsDisabled and then
// SSA-patches status via toRBGApplyConfigurationForStatus, which ALWAYS
// re-declares .status.roleStatuses from the object it was handed. For an RBG
// that was healthy before the operator flipped the flag off, that re-declares
// the last-known replica counts verbatim -- and because the stop is terminal,
// nothing ever updates them again.
//
// Net effect for an operator: `kubectl get rbg -o yaml` shows
// Ready=False(LegacyWorkloadsDisabled) sitting next to roleStatuses claiming
// 3/3 ready, forever, even after the underlying pods are gone.
//
// PASSES on 66a2500a. FLIPS when the author clears/marks the stale role
// statuses (or requeues) -- invert then.
// ---------------------------------------------------------------------------
func TestVerifyPR414_F5_StaleRoleStatusesFrozenNotInvalidated_Canary(t *testing.T) {
	rbg := p414RBG("frozen", "default", [][2]string{
		{"worker", constants.StatefulSetWorkloadType},
	})
	// Pretend this RBG was healthy under compat=true before the flag was flipped.
	rbg.Status.RoleStatuses = []workloadsv1alpha2.RoleStatus{
		{Name: "worker", Replicas: 3, ReadyReplicas: 3},
	}
	apimeta.SetStatusCondition(&rbg.Status.Conditions, metav1.Condition{
		Type:               string(workloadsv1alpha2.RoleBasedGroupReady),
		Status:             metav1.ConditionTrue,
		Reason:             "AllRolesReady",
		Message:            "All roles are ready",
		ObservedGeneration: 1,
		LastTransitionTime: metav1.Now(),
	})

	r := p414Reconciler(t, false, rbg)
	stop, err := r.handleLegacyWorkloads(p414Ctx(), rbg)
	if err != nil {
		t.Fatalf("handleLegacyWorkloads: %v", err)
	}
	if !stop {
		t.Fatal("HARNESS DOES NOT BITE: expected a stop for a legacy-only RBG with compat disabled")
	}

	cond := apimeta.FindStatusCondition(rbg.Status.Conditions,
		string(workloadsv1alpha2.RoleBasedGroupReady))
	if cond == nil || cond.Reason != LegacyWorkloadsDisabled {
		t.Fatalf("expected the terminal condition to be set, got %+v", cond)
	}

	if len(rbg.Status.RoleStatuses) != 1 {
		t.Fatalf("CANARY FLIPPED: roleStatuses is no longer carried through verbatim (%d entries); "+
			"the author may now be invalidating stale status -- re-read and invert this test.",
			len(rbg.Status.RoleStatuses))
	}
	rs := rbg.Status.RoleStatuses[0]
	if rs.Replicas != 3 || rs.ReadyReplicas != 3 {
		t.Fatalf("CANARY FLIPPED: stale roleStatuses were adjusted (%d/%d ready); invert this test.",
			rs.ReadyReplicas, rs.Replicas)
	}
	t.Logf("observed: Ready=False(%s) reported alongside stale roleStatuses %q=%d/%d ready, "+
		"and the terminal stop means these numbers are never refreshed",
		cond.Reason, rs.Name, rs.ReadyReplicas, rs.Replicas)
}

// ---------------------------------------------------------------------------
// F6 [CONTRACT] -- orphan cleanup must survive disabling compat.
//
// deleteOrphanRoles skips CleanupOrphanedWorkloads for Deployment/StatefulSet/
// LWS when compat is off. An RBG-owned Deployment left behind by a role that
// has since been migrated to RoleInstanceSet is therefore never removed by the
// controller: it is owner-referenced by the RBG, so Kubernetes GC will not
// collect it while the RBG lives, and its pods keep serving alongside the new
// RoleInstanceSet pods.
//
// The RBG here has ONLY a RoleInstanceSet role, so F4's whole-group stop does
// not apply -- this isolates the cleanup skip.
//
// Expected RED on 66a2500a. The compat=true subtest is the control that proves
// the harness bites.
//
// NOTE (carried from the PR#413 round): the LIVE arm of this finding was
// INCONCLUSIVE -- on a real cluster the orphan disappears in ~10s in both
// modes and even with no controller running at all, because Kubernetes GC is
// the actor. Only the code-level skip is proven; do not claim "leaks forever".
// ---------------------------------------------------------------------------
func TestVerifyPR414_F6_OrphanedLegacyWorkloadNotCleanedUpWhenCompatDisabled(t *testing.T) {
	const ns = "default"

	orphanStillExists := func(t *testing.T, compat bool) bool {
		t.Helper()
		rbg := p414RBG("mig", ns, [][2]string{{"worker", ""}})
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
					Controller: p414Bool(true),
				}},
			},
		}
		r := p414Reconciler(t, compat, rbg, orphan)
		if err := r.deleteOrphanRoles(p414Ctx(), rbg); err != nil {
			// RoleInstanceSet / scaling-adapter cleanup can complain under the fake
			// client; that is not what this test asserts on.
			t.Logf("deleteOrphanRoles returned (non-fatal here): %v", err)
		}
		err := r.client.Get(p414Ctx(),
			types.NamespacedName{Name: orphan.Name, Namespace: ns}, &appsv1.Deployment{})
		if apierrors.IsNotFound(err) {
			return false
		}
		if err != nil {
			t.Fatalf("unexpected error reading orphan Deployment: %v", err)
		}
		return true
	}

	t.Run("control_compatEnabled_orphanIsCleanedUp", func(t *testing.T) {
		if orphanStillExists(t, true) {
			t.Fatal("HARNESS DOES NOT BITE: the orphan survived even with compat enabled, " +
				"so this test is not exercising CleanupOrphanedWorkloads at all")
		}
	})

	t.Run("compatDisabled_orphanMustStillBeCleanedUp", func(t *testing.T) {
		if orphanStillExists(t, false) {
			t.Fatal("F6 REPRODUCED: an RBG-owned orphaned Deployment was NOT deleted with " +
				"--enable-v1alpha1-compat=false. Cleanup of already-created legacy workloads " +
				"is a different concern from managing new ones and should stay enabled; " +
				"whatever the decision, the delete RBAC for legacy types must be retained " +
				"or the cleanup will 403.")
		}
	})
}

// ---------------------------------------------------------------------------
// F7 [CONTRACT] -- RoleBasedGroupSet has no compat awareness at all, so it
// reproduces exactly the infinite-backoff problem this PR set out to fix, one
// level up.
//
// With compat disabled:
//   - RoleBasedGroupSet has NO validating webhook (only a conversion webhook;
//     see SetupWebhookWithManager in rolebasedgroup_webhook.go), so an RBGSet
//     whose groupTemplate uses a legacy workload type is admitted happily.
//   - Its controller then calls client.Create for each child RBG, and the RBG
//     validating webhook -- which DOES know about the flag -- rejects every one.
//   - scaleUp aggregates that into a returned error, Reconcile returns it, and
//     controller-runtime backs off exponentially. Forever. No terminal
//     condition, no LegacyWorkloadsDisabled event, nothing in the RBGSet status.
//
// The interceptor below stands in for the admission webhook and returns the
// error the real validator produces (asserted separately in the v1alpha2
// package test), so this reproduces the loop without needing a cluster.
//
// Expected RED on 66a2500a.
// ---------------------------------------------------------------------------
func TestVerifyPR414_F7_RBGSetErrorLoopsForeverWhenCompatDisabled(t *testing.T) {
	const ns = "default"
	s := p414Scheme(t)

	rbgset := &workloadsv1alpha2.RoleBasedGroupSet{
		ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: ns, UID: "uid-svc", Generation: 1},
		Spec: workloadsv1alpha2.RoleBasedGroupSetSpec{
			Replicas: p414Int32(2),
			GroupTemplate: workloadsv1alpha2.RoleBasedGroupTemplateSpec{
				Spec: workloadsv1alpha2.RoleBasedGroupSpec{
					Roles: []workloadsv1alpha2.RoleSpec{{
						Name: "worker",
						Annotations: map[string]string{
							constants.RoleWorkloadTypeAnnotationKey: constants.StatefulSetWorkloadType,
						},
					}},
				},
			},
		},
	}

	// Stand in for the RBG validating webhook with compat disabled.
	var createAttempts int
	admissionDenied := apierrors.NewInvalid(
		schema.GroupKind{Group: "workloads.x-k8s.io", Kind: "RoleBasedGroup"},
		"svc-0",
		field.ErrorList{field.Invalid(
			field.NewPath("spec", "roles").Index(0),
			constants.StatefulSetWorkloadType,
			"workload type is a v1alpha1 indirect workload type, which is not supported "+
				"when v1alpha1 compat is disabled (--enable-v1alpha1-compat=false)",
		)},
	)

	c := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(rbgset).
		WithStatusSubresource(&workloadsv1alpha2.RoleBasedGroupSet{}).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if rbg, ok := obj.(*workloadsv1alpha2.RoleBasedGroup); ok {
					for i := range rbg.Spec.Roles {
						if p414IsLegacy(rbg.Spec.Roles[i].GetWorkloadType()) {
							createAttempts++
							return admissionDenied
						}
					}
				}
				return cl.Create(ctx, obj, opts...)
			},
		}).
		Build()

	r := &RoleBasedGroupSetReconciler{
		client:    c,
		apiReader: c,
		scheme:    s,
		recorder:  record.NewFakeRecorder(200),
	}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "svc", Namespace: ns}}

	// Drive several reconciles, the way controller-runtime would on backoff.
	const rounds = 5
	var lastErr error
	for i := 0; i < rounds; i++ {
		res, err := r.Reconcile(p414Ctx(), req)
		lastErr = err
		if err == nil {
			t.Fatalf("round %d: expected the rejected child Create to surface as a reconcile "+
				"error (that is what causes the backoff loop); got Result=%+v, err=nil", i, res)
		}
	}

	if createAttempts < rounds {
		t.Fatalf("HARNESS DOES NOT BITE: the webhook stand-in was only consulted %d times "+
			"across %d reconciles; the loop is not being exercised", createAttempts, rounds)
	}

	// No child RBG exists.
	var kids workloadsv1alpha2.RoleBasedGroupList
	if err := c.List(p414Ctx(), &kids, client.InNamespace(ns)); err != nil {
		t.Fatalf("list child RBGs: %v", err)
	}

	// And the RBGSet carries no terminal condition explaining why.
	got := &workloadsv1alpha2.RoleBasedGroupSet{}
	if err := c.Get(p414Ctx(), req.NamespacedName, got); err != nil {
		t.Fatalf("get rbgset: %v", err)
	}
	terminal := false
	for _, cond := range got.Status.Conditions {
		if cond.Reason == LegacyWorkloadsDisabled {
			terminal = true
		}
	}

	if !terminal {
		t.Fatalf("F7 REPRODUCED: after %d reconciles the RBGSet still has 0 child RBGs "+
			"(%d admission rejections), keeps returning errors (%v) so controller-runtime "+
			"backs off and retries forever, and its status carries no %s condition and no "+
			"event to tell the operator why. This is the same indefinite-backoff-for-a-"+
			"configuration-error that handleLegacyWorkloads fixes for RoleBasedGroup; "+
			"RoleBasedGroupSet needs the same treatment (and a validating webhook, which "+
			"it currently has none of).",
			rounds, createAttempts, lastErr, LegacyWorkloadsDisabled)
	}
}

// ---------------------------------------------------------------------------
// F8 [CONTRACT] -- the "is this a legacy type" decision is duplicated three
// times across two packages, with no shared source of truth:
//
//	1. internal/controller/workloads/rolebasedgroup_controller.go isLegacyWorkloadType
//	2. an inline switch in getOrCreateWorkloadReconciler in the same file
//	3. api/workloads/v1alpha2/rolebasedgroup_validation.go isLegacyWorkloadType
//
// (2) is also unreachable in the compat=false path that motivates it, because
// handleLegacyWorkloads stops Reconcile before any reconciler is built.
//
// This test pins (1) against (2) inside this package; the v1alpha2 package test
// pins (3). Expected GREEN today -- it exists so that adding a fourth legacy
// type turns exactly one of them red instead of silently half-applying.
// ---------------------------------------------------------------------------
func TestVerifyPR414_F8_LegacyTypeListsAgree(t *testing.T) {
	all := append(p414LegacyTypes(),
		constants.RoleInstanceSetWorkloadType,
		"apps/v1/DaemonSet",
		"",
	)
	for _, wt := range all {
		want := isLegacyWorkloadType(wt)

		r := p414Reconciler(t, false)
		role := workloadsv1alpha2.RoleSpec{Name: "r"}
		if wt != "" {
			role.Annotations = map[string]string{constants.RoleWorkloadTypeAnnotationKey: wt}
		}
		_, err := r.getOrCreateWorkloadReconciler(p414Ctx(), role.GetWorkloadSpec())
		gotRejected := err != nil &&
			strings.Contains(err.Error(), "not supported when v1alpha1 compat is disabled")

		if want != gotRejected {
			t.Errorf("DRIFT for %q: isLegacyWorkloadType=%v but getOrCreateWorkloadReconciler "+
				"rejected=%v (err=%v). The two copies of the legacy-type list have diverged.",
				wt, want, gotRejected, err)
		}
	}
}

// p414LegacyTypes is the reviewer's independent list, deliberately written out
// rather than derived from the code under test.
func p414LegacyTypes() []string {
	return []string{
		constants.DeploymentWorkloadType,
		constants.StatefulSetWorkloadType,
		constants.LeaderWorkerSetWorkloadType,
	}
}

func p414IsLegacy(wt string) bool {
	for _, l := range p414LegacyTypes() {
		if wt == l {
			return true
		}
	}
	return false
}

func p414Bool(b bool) *bool    { return &b }
func p414Int32(i int32) *int32 { return &i }

var _ = fmt.Sprintf
