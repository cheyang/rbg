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

// Premise verification for PR #478 (reviewer B / codex). This file deliberately
// uses only symbols that exist on the BASE branch (merge-base 35e5d029) so the
// same file compiles and runs there. Run it on a base worktree with
// VERIFY_PREMISE_ON_BASE=1; on the PR head it skips (behavior changed by design).

package workloads

import (
	"context"
	"os"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// premiseSpec/premiseRBG/premiseTargetPod are local copies so this file does not
// depend on helpers that only exist in the PR-head test suite.
func premiseSpec(rbgName string) workloadsv1alpha2.RoleBasedGroupWarmupSpec {
	return workloadsv1alpha2.RoleBasedGroupWarmupSpec{
		TargetRoleBasedGroup: &workloadsv1alpha2.TargetRoleBasedGroup{
			Name: rbgName,
			Roles: map[string]workloadsv1alpha2.WarmupActions{
				"worker": {ImagePreload: &workloadsv1alpha2.ImagePreloadAction{Images: []string{"busybox:1.36"}}},
			},
		},
	}
}

func premiseRBG(name string) *workloadsv1alpha2.RoleBasedGroup {
	return &workloadsv1alpha2.RoleBasedGroup{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"}}
}

func premiseCtx() context.Context {
	return ctrl.LoggerInto(context.Background(), zap.New(zap.UseDevMode(true)))
}

func premiseTargetPod(name, rbg, role, node string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: "default",
			Labels: map[string]string{constants.GroupNameLabelKey: rbg, constants.RoleNameLabelKey: role},
		},
		Spec: corev1.PodSpec{NodeName: node},
	}
}

// P0-1 (premise, runsAgainst: BASE): a missing target RoleBasedGroup is an
// immediate terminal failure (Failed/InvalidTarget, no requeue) on base.
// Complements the base suite's own TestReconcile_MissingTargetRBGFailsWithoutRequeue.
func TestVerify_Premise_MissingTargetFailsTerminallyOnBase(t *testing.T) {
	if os.Getenv("VERIFY_PREMISE_ON_BASE") == "" {
		t.Skip("premise claim: run on the base branch with VERIFY_PREMISE_ON_BASE=1")
	}
	warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{Name: "p0-missing", Namespace: "default", UID: "uid-p0-missing"},
		Spec:       premiseSpec("p0-missing-rbg"),
	}
	r := newWarmupReconciler(warmup)
	res, err := r.Reconcile(premiseCtx(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(warmup)})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.RequeueAfter != 0 {
		t.Fatalf("base premise: expected no requeue for a missing target, got %#v", res)
	}
	updated := &workloadsv1alpha2.RoleBasedGroupWarmup{}
	if err := r.Get(premiseCtx(), client.ObjectKeyFromObject(warmup), updated); err != nil {
		t.Fatalf("get warmup: %v", err)
	}
	if updated.Status.Phase != workloadsv1alpha2.WarmupJobPhaseFailed {
		t.Fatalf("base premise: expected terminal Failed for a missing target, got %q", updated.Status.Phase)
	}
	cond := apimeta.FindStatusCondition(updated.Status.Conditions, "Failed")
	if cond == nil || cond.Reason != "InvalidTarget" {
		t.Fatalf("base premise: expected Failed/InvalidTarget, got %#v", updated.Status.Conditions)
	}
}

// ---------------------------------------------------------------------------
// P0-2 (premise, runsAgainst: BASE branch 35e5d029): on the base code an
// existing target RBG whose Pods are not scheduled yet completes the Warmup
// with Completed/NoNodesMatched (a false success). Guarded so that a plain
// `go test` on the PR head skips it: on PR head the behavior intentionally
// changed to waiting (that new behavior is covered by the PR's own tests).
// Run on the base worktree with VERIFY_PREMISE_ON_BASE=1.
// ---------------------------------------------------------------------------
func TestVerify_Premise_UnscheduledTargetPodsCompleteOnBase(t *testing.T) {
	if os.Getenv("VERIFY_PREMISE_ON_BASE") == "" {
		t.Skip("premise claim: run on the base branch with VERIFY_PREMISE_ON_BASE=1")
	}
	warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{Name: "p0-unscheduled", Namespace: "default", UID: "uid-p0-unscheduled"},
		Spec:       premiseSpec("p0-rbg"),
	}
	r := newWarmupReconciler(warmup, premiseRBG("p0-rbg"), premiseTargetPod("p0-worker-0", "p0-rbg", "worker", ""))

	if _, err := r.Reconcile(premiseCtx(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(warmup)}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	updated := &workloadsv1alpha2.RoleBasedGroupWarmup{}
	if err := r.Get(premiseCtx(), client.ObjectKeyFromObject(warmup), updated); err != nil {
		t.Fatalf("get warmup: %v", err)
	}
	if updated.Status.Phase != workloadsv1alpha2.WarmupJobPhaseCompleted {
		t.Fatalf("base premise: expected Completed/NoNodesMatched for unscheduled target pods, got phase=%q conditions=%#v",
			updated.Status.Phase, updated.Status.Conditions)
	}
	cond := apimeta.FindStatusCondition(updated.Status.Conditions, "Complete")
	if cond == nil || cond.Reason != "NoNodesMatched" {
		t.Fatalf("base premise: expected Complete/NoNodesMatched condition, got %#v", updated.Status.Conditions)
	}
	if updated.Status.Desired != 0 {
		t.Fatalf("base premise: expected desired=0 (nothing warmed), got %d", updated.Status.Desired)
	}
}
