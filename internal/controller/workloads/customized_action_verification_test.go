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

// Verification harness for sgl-project/rbg PR #488 (review round 1).
// Findings and polarity are documented in docs/verification/pr488-customized-action-reporting/.
// These tests are ADDITIVE reviewer evidence: production code is untouched.
package workloads

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"

	"k8s.io/utils/ptr"
)

// C1a/C1b (CANARY, L1) — finding R1: timeoutSeconds is applied as a Pod-level
// ActiveDeadlineSeconds on the *merged* warmup Pod, so it bounds image-preload
// containers running in the same Pod, and the smallest timeout across actions
// truncates the others. Documented-intended behavior per the field comment; this
// canary records the mechanism so a future change (e.g. container-scoped
// deadlines, or excluding preload) flips it.
func TestVerifyPR488_MergedPodDeadlineGovernsPreloadContainers(t *testing.T) {
	r := newWarmupReconciler()
	warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{Name: "verify-488", Namespace: "default"},
	}
	actions := []workloadsv1alpha2.WarmupActions{
		{ImagePreload: &workloadsv1alpha2.ImagePreloadAction{
			Images: []string{"registry.example.com/big-model:v1"},
		}},
		{CustomizedAction: &workloadsv1alpha2.CustomizedAction{
			TimeoutSeconds: ptr.To(int64(30)),
			Containers: []corev1.Container{{
				Name: "node-check", Image: "busybox", Command: []string{"true"},
			}},
		}},
	}
	pod, _ := r.buildWarmupPod(warmup, "node-1", actions)

	var hasPreload, hasCustom bool
	for i := range pod.Spec.Containers {
		switch {
		case strings.HasPrefix(pod.Spec.Containers[i].Name, "image-preload-"):
			hasPreload = true
		case strings.HasPrefix(pod.Spec.Containers[i].Name, "custom-"):
			hasCustom = true
		}
	}
	if !hasPreload || !hasCustom {
		t.Fatalf("expected merged pod with both preload and customized containers, got %v", pod.Spec.Containers)
	}
	if pod.Spec.ActiveDeadlineSeconds == nil || *pod.Spec.ActiveDeadlineSeconds != 30 {
		t.Fatalf("CANARY R1: expected ActiveDeadlineSeconds=30 on the merged pod (incl. preload), got %v",
			pod.Spec.ActiveDeadlineSeconds)
	}
}

func TestVerifyPR488_SmallestTimeoutWinsAcrossActions(t *testing.T) {
	r := newWarmupReconciler()
	warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{Name: "verify-488", Namespace: "default"},
	}
	actions := []workloadsv1alpha2.WarmupActions{
		{CustomizedAction: &workloadsv1alpha2.CustomizedAction{
			TimeoutSeconds: ptr.To(int64(120)),
			Containers:     []corev1.Container{{Name: "slow-check", Image: "busybox"}},
		}},
		{CustomizedAction: &workloadsv1alpha2.CustomizedAction{
			TimeoutSeconds: ptr.To(int64(45)),
			Containers:     []corev1.Container{{Name: "fast-check", Image: "busybox"}},
		}},
	}
	pod, _ := r.buildWarmupPod(warmup, "node-1", actions)
	if pod.Spec.ActiveDeadlineSeconds == nil || *pod.Spec.ActiveDeadlineSeconds != 45 {
		t.Fatalf("CANARY R1: expected smallest timeout 45 to win, got %v", pod.Spec.ActiveDeadlineSeconds)
	}

	noTimeout := []workloadsv1alpha2.WarmupActions{
		{CustomizedAction: &workloadsv1alpha2.CustomizedAction{
			Containers: []corev1.Container{{Name: "check", Image: "busybox"}},
		}},
	}
	pod, _ = r.buildWarmupPod(warmup, "node-1", noTimeout)
	if pod.Spec.ActiveDeadlineSeconds != nil {
		t.Fatalf("CANARY R1: expected no deadline when no action configures one, got %v",
			*pod.Spec.ActiveDeadlineSeconds)
	}
}

// C1d (CONTRACT, L1) — issue #486 acceptance criterion: "A timeout results in a
// failed customized action with reason Timeout". Passes on correct code.
func TestVerifyPR488_DeadlineExceededMapsToTimeout(t *testing.T) {
	pod := mappedWarmupPod("node-1", "attempt-1", corev1.PodFailed,
		corev1.ContainerStatus{
			Name:  "custom-0",
			State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
		})
	pod.Status.Reason = "DeadlineExceeded"
	pod.Status.Message = "Pod was active on the node longer than the specified deadline"

	result := evaluateCustomizedActionPod(pod)
	if result.State != workloadsv1alpha2.CustomizedActionStateFailed {
		t.Fatalf("expected state Failed, got %q", result.State)
	}
	if result.Reason != CustomizedActionReasonTimeout {
		t.Fatalf("expected reason Timeout, got %q", result.Reason)
	}
}

// C2 (CANARY, L1) — finding R2: completionPolicy is accepted and validated but
// has no effect on evaluation; AllSucceeded semantics are hard-coded.
func TestVerifyPR488_CompletionPolicyDoesNotAffectEvaluation(t *testing.T) {
	pod := mappedWarmupPod("node-1", "attempt-1", corev1.PodFailed,
		terminatedStatus("custom-0", 1, "Error", "boom"))
	container := []corev1.Container{{Name: "node-check", Image: "busybox"}}

	withPolicy := map[string][]workloadsv1alpha2.WarmupActions{
		"node-1": {{CustomizedAction: &workloadsv1alpha2.CustomizedAction{
			CompletionPolicy: workloadsv1alpha2.CustomizedActionCompletionPolicyAllSucceeded,
			Containers:       container,
		}}},
	}
	withoutPolicy := map[string][]workloadsv1alpha2.WarmupActions{
		"node-1": {{CustomizedAction: &workloadsv1alpha2.CustomizedAction{
			Containers: container,
		}}},
	}

	withResults := evaluateCustomizedActionResults(withPolicy, []*corev1.Pod{pod})
	withoutResults := evaluateCustomizedActionResults(withoutPolicy, []*corev1.Pod{pod})
	if !apiequality.Semantic.DeepEqual(withResults, withoutResults) {
		t.Fatalf("CANARY R2: results should currently be identical regardless of completionPolicy:\n%#v\nvs\n%#v",
			withResults, withoutResults)
	}
}

// C4 (CONTRACT since round 2 — the round-1 canary flipped when the author fixed
// the wipe; inverted per the polarity rule) — on the InvalidWarmupSpec failure
// path (desiredNodes=nil) previously reported CustomizedActionResults and the
// CustomizedActionComplete condition must be PRESERVED in the status.
// Fixed by commit ed0a2e63 "fix: preserve customized action diagnostics".
func TestVerifyPR488_InvalidSpecPreservesExistingCustomizedActionResults(t *testing.T) {
	warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{
			Name: "verify-488-wipe", Namespace: "default", UID: "uid-verify-488", Generation: 2,
		},
		Spec: workloadsv1alpha2.RoleBasedGroupWarmupSpec{
			TargetNodes: &workloadsv1alpha2.TargetNodes{
				NodeNames: []string{"node-1"},
				WarmupActions: workloadsv1alpha2.WarmupActions{
					CustomizedAction: &workloadsv1alpha2.CustomizedAction{
						Containers: []corev1.Container{{Name: "check", Image: ""}},
					},
				},
			},
		},
		Status: workloadsv1alpha2.RoleBasedGroupWarmupStatus{
			Phase: workloadsv1alpha2.WarmupJobPhaseRunning,
			CustomizedActionResults: []workloadsv1alpha2.CustomizedActionResult{{
				NodeName: "node-1",
				PodName:  "attempt-1",
				State:    workloadsv1alpha2.CustomizedActionStateSucceeded,
				Reason:   CustomizedActionReasonCompleted,
			}},
			Conditions: []metav1.Condition{{
				Type: ConditionCustomizedActionComplete, Status: metav1.ConditionTrue,
				Reason: "AllActionsSucceeded", ObservedGeneration: 1,
			}},
		},
	}
	r := newWarmupReconciler(warmup)

	if _, err := r.Reconcile(context.Background(),
		ctrl.Request{NamespacedName: client.ObjectKeyFromObject(warmup)}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	updated := &workloadsv1alpha2.RoleBasedGroupWarmup{}
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(warmup), updated); err != nil {
		t.Fatalf("get updated warmup: %v", err)
	}
	if updated.Status.Phase != workloadsv1alpha2.WarmupJobPhaseFailed {
		t.Fatalf("expected warmup to fail on invalid spec, got phase %q", updated.Status.Phase)
	}
	if len(updated.Status.CustomizedActionResults) != 1 ||
		updated.Status.CustomizedActionResults[0].NodeName != "node-1" ||
		updated.Status.CustomizedActionResults[0].State != workloadsv1alpha2.CustomizedActionStateSucceeded {
		t.Fatalf("CONTRACT R4: InvalidWarmupSpec path must preserve existing CustomizedActionResults, got %#v",
			updated.Status.CustomizedActionResults)
	}
	if condition := apimeta.FindStatusCondition(updated.Status.Conditions,
		ConditionCustomizedActionComplete); condition == nil || condition.Status != metav1.ConditionTrue {
		t.Fatalf("CONTRACT R4: CustomizedActionComplete condition must be preserved, got %#v", condition)
	}
}

// C5 (CONTRACT, L2, round 2) — regression introduced by 060ef1b7 "fix: bound
// customized action status size": limitCustomizedActionResults stores every
// entry as a summary (Containers=nil) and only upgrades NON-succeeded entries
// back to full detail, so a SUCCEEDED result loses its per-container details
// even when the status is nowhere near the 512KiB budget. The PR's own e2e
// ("should complete customized action with targetRoleBasedGroup mode and merge
// multi-role actions", warmup.go:245) fails on both e2e jobs at head 060ef1b7
// because of exactly this. Small statuses must keep full detail; compaction
// may only drop detail when the budget is actually exceeded.
func TestVerifyPR488_R5_SmallStatusKeepsSucceededContainerDetails(t *testing.T) {
	warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{Name: "verify-488-r5", Namespace: "default", UID: "uid-r5", Generation: 2},
	}
	pod := mappedWarmupPod("node-1", "attempt-1", corev1.PodSucceeded,
		terminatedStatus("custom-0", 0, "Completed", ""))
	pod.Namespace = warmup.Namespace
	pod.Labels[LabelWarmupName] = warmup.Name
	pod.Labels[LabelWarmupUID] = string(warmup.UID)
	desired := map[string][]workloadsv1alpha2.WarmupActions{
		"node-1": {{CustomizedAction: &workloadsv1alpha2.CustomizedAction{
			Containers: []corev1.Container{{Name: "decode-task", Image: "busybox"}},
		}}},
	}
	r := newWarmupReconciler(warmup, pod)

	if err := r.updateStatus(context.Background(), warmup, nil, []*corev1.Pod{pod}, nil, desired, map[string]bool{}); err != nil {
		t.Fatalf("update status: %v", err)
	}
	updated := &workloadsv1alpha2.RoleBasedGroupWarmup{}
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(warmup), updated); err != nil {
		t.Fatalf("get updated warmup: %v", err)
	}
	if len(updated.Status.CustomizedActionResults) != 1 {
		t.Fatalf("expected one result, got %#v", updated.Status.CustomizedActionResults)
	}
	result := updated.Status.CustomizedActionResults[0]
	if result.State != workloadsv1alpha2.CustomizedActionStateSucceeded {
		t.Fatalf("expected state Succeeded, got %q", result.State)
	}
	if len(result.Containers) != 1 ||
		result.Containers[0].ContainerName != "node-check" ||
		result.Containers[0].ExitCode == nil || *result.Containers[0].ExitCode != 0 {
		t.Fatalf("CONTRACT R5: small status must keep succeeded per-container details, got %#v", result.Containers)
	}
	if updated.Status.CustomizedActionResultsTruncated {
		t.Fatalf("CONTRACT R5: a single tiny result must not be reported as truncated")
	}
}
