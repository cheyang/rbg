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

// Verification harness for sgl-project/rbg PR #488 review (Reviewer B / Codex).
// Additive test file only; no production code is changed by the harness.
//
// Findings under test:
//   F1 (P1 confirm+sharpen): limitCustomizedActionResults strips per-container
//      detail from *successful* results unconditionally and flags even a tiny
//      all-success status as truncated.
//   F2 (P2 refute): waiting image-pull failures keep container identity and the
//      waiting reason through the full evaluate+limit pipeline.
//   F3 (new): a node killed mid-flight by MaxFailedNodesExceeded keeps a stale
//      Running/Pending customized-action result in a terminally Failed job.
//   F4 (new): the global-timeout path rewrites a specific per-node failure
//      reason (ImagePullFailed) to GlobalTimeout at result level.

package workloads

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// F1 contract: a successful result that fits comfortably in the status budget
// must keep its per-container detail. FAILS on the head under review (the
// bounding logic skips Succeeded results in the detail-upgrade pass).
func TestVerifyPR488F1SucceededResultKeepsContainersWithinBudget(t *testing.T) {
	exit := int32(0)
	full := []workloadsv1alpha2.CustomizedActionResult{{
		NodeName: "node-1",
		PodName:  "attempt-1",
		State:    workloadsv1alpha2.CustomizedActionStateSucceeded,
		Reason:   CustomizedActionReasonCompleted,
		Containers: []workloadsv1alpha2.CustomizedActionContainerResult{{
			ContainerName:    "node-check",
			PodContainerName: "custom-0",
			State:            workloadsv1alpha2.CustomizedActionContainerStateSucceeded,
			ExitCode:         &exit,
		}},
	}}

	limited, truncated := limitCustomizedActionResults(full)
	if len(limited) != 1 {
		t.Fatalf("expected the single tiny result to be retained, got %d", len(limited))
	}
	if len(limited[0].Containers) != 1 {
		t.Fatalf("F1: successful result lost its per-container detail despite a free budget: %#v", limited[0])
	}
	if truncated {
		t.Fatalf("F1: a single tiny result must not be reported as truncated")
	}
}

// F1 canary: pins the current (buggy) behavior so the harness flips when the
// fix lands. PASSES on the head under review.
func TestVerifyPR488F1CanaryTinySuccessStrippedAndFlaggedTruncated(t *testing.T) {
	exit := int32(0)
	full := []workloadsv1alpha2.CustomizedActionResult{{
		NodeName: "node-1",
		PodName:  "attempt-1",
		State:    workloadsv1alpha2.CustomizedActionStateSucceeded,
		Reason:   CustomizedActionReasonCompleted,
		Containers: []workloadsv1alpha2.CustomizedActionContainerResult{{
			ContainerName:    "node-check",
			PodContainerName: "custom-0",
			State:            workloadsv1alpha2.CustomizedActionContainerStateSucceeded,
			ExitCode:         &exit,
		}},
	}}

	limited, truncated := limitCustomizedActionResults(full)
	if len(limited) != 1 {
		t.Fatalf("expected the single tiny result to be retained, got %d", len(limited))
	}
	if len(limited[0].Containers) != 0 {
		t.Fatalf("canary flipped (containers kept) - invert this test, F1 is fixed")
	}
	if !truncated {
		t.Fatalf("canary flipped (no truncation reported) - invert this test, F1 is fixed")
	}
}

// F2 contract (expected to PASS on the head under review): a waiting
// image-pull failure keeps the failing container's identity, waiting reason
// and message in the bounded status. This refutes the claim that waiting
// failures drop container identity and reason.
func TestVerifyPR488F2WaitingFailureIdentitySurvivesBounding(t *testing.T) {
	pod := mappedWarmupPod("node-1", "attempt-1", corev1.PodPending,
		waitingStatus("custom-0", "ImagePullBackOff", `Back-off pulling image "registry.invalid/check:v1"`))
	// Two logical containers were deduplicated into one pod container.
	mappings := []customizedActionContainerMapping{{
		PodContainerName: "custom-0",
		ContainerNames:   []string{"gpu-check", "net-check"},
	}}
	raw, err := json.Marshal(mappings)
	if err != nil {
		t.Fatal(err)
	}
	pod.Annotations[AnnotationCustomizedActionContainers] = string(raw)

	desired := map[string][]workloadsv1alpha2.WarmupActions{
		"node-1": {{CustomizedAction: &workloadsv1alpha2.CustomizedAction{
			Containers: []corev1.Container{{Name: "gpu-check", Image: "registry.invalid/check:v1"}},
		}}},
	}
	observed := evaluateCustomizedActionResults(desired, []*corev1.Pod{pod})
	if len(observed) != 1 {
		t.Fatalf("expected one result, got %#v", observed)
	}
	bounded, truncated := limitCustomizedActionResults(observed)
	if truncated {
		t.Fatalf("a single waiting result must not need truncation: %#v", bounded)
	}
	result := bounded[0]
	if result.Reason != CustomizedActionReasonImagePullFailed {
		t.Fatalf("expected aggregate reason ImagePullFailed, got %#v", result)
	}
	if len(result.Containers) != 2 {
		t.Fatalf("F2: waiting failure lost container identity after bounding: %#v", result)
	}
	names := map[string]workloadsv1alpha2.CustomizedActionContainerResult{}
	for _, c := range result.Containers {
		names[c.ContainerName] = c
	}
	for _, want := range []string{"gpu-check", "net-check"} {
		c, ok := names[want]
		if !ok {
			t.Fatalf("F2: container identity %q missing: %#v", want, result.Containers)
		}
		if c.TerminationReason != "ImagePullBackOff" || c.TerminationMessage == "" {
			t.Fatalf("F2: waiting reason/message dropped for %q: %#v", want, c)
		}
	}
}

// F3 contract: when the job is terminally failed by MaxFailedNodesExceeded,
// active pods are deleted mid-flight; their customized-action results must not
// stay Running/Pending in the final status of a Failed job (the global-timeout
// path already flips non-terminal results to Failed). FAILS on the head under
// review.
func TestVerifyPR488F3TerminalFailureLeavesNoStaleRunningResult(t *testing.T) {
	warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default", UID: "uid-1", Generation: 1},
		Spec: workloadsv1alpha2.RoleBasedGroupWarmupSpec{
			Policies: &workloadsv1alpha2.WarmupPolicies{
				BackoffLimitPerNode: ptr.To(int32(0)),
				MaxFailedNodes:      ptr.To(int32(0)),
			},
			TargetNodes: &workloadsv1alpha2.TargetNodes{
				NodeNames: []string{"node-a", "node-b"},
				WarmupActions: workloadsv1alpha2.WarmupActions{CustomizedAction: &workloadsv1alpha2.CustomizedAction{
					Containers: []corev1.Container{{Name: "node-check", Image: "busybox"}},
				}},
			},
		},
		Status: workloadsv1alpha2.RoleBasedGroupWarmupStatus{Phase: workloadsv1alpha2.WarmupJobPhaseRunning},
	}
	failedPod := mappedWarmupPod("node-a", "attempt-a", corev1.PodFailed, terminatedStatus("custom-0", 1, "Error", "boom"))
	runningPod := mappedWarmupPod("node-b", "attempt-b", corev1.PodRunning, corev1.ContainerStatus{
		Name: "custom-0", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
	})
	for _, pod := range []*corev1.Pod{failedPod, runningPod} {
		pod.Namespace = warmup.Namespace
		pod.Labels[LabelWarmupName] = warmup.Name
		pod.Labels[LabelWarmupUID] = string(warmup.UID)
	}
	r := newWarmupReconciler(warmup, failedPod, runningPod)

	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(warmup)}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	updated := &workloadsv1alpha2.RoleBasedGroupWarmup{}
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(warmup), updated); err != nil {
		t.Fatalf("get updated warmup: %v", err)
	}
	if updated.Status.Phase != workloadsv1alpha2.WarmupJobPhaseFailed {
		t.Fatalf("expected terminally failed job, got %q", updated.Status.Phase)
	}
	for _, result := range updated.Status.CustomizedActionResults {
		if result.State == workloadsv1alpha2.CustomizedActionStateRunning ||
			result.State == workloadsv1alpha2.CustomizedActionStatePending {
			t.Fatalf("F3: node %s keeps stale %q result (pod was deleted, job is terminally Failed): %#v",
				result.NodeName, result.State, result)
		}
	}
}

// F4 canary: the global-timeout path rewrites a specific per-node failure
// reason (ImagePullFailed) to GlobalTimeout at result level. Pins current
// behavior; flip when the fix preserves the original reason. PASSES on the
// head under review.
func TestVerifyPR488F4CanaryGlobalTimeoutClobbersSpecificReason(t *testing.T) {
	startTime := metav1.NewTime(time.Now().Add(-2 * time.Minute))
	warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default", UID: "uid-1", Generation: 1},
		Spec: workloadsv1alpha2.RoleBasedGroupWarmupSpec{
			Policies: &workloadsv1alpha2.WarmupPolicies{GlobalTimeoutSeconds: ptr.To(int64(1))},
			TargetNodes: &workloadsv1alpha2.TargetNodes{
				NodeNames: []string{"node-1"},
				WarmupActions: workloadsv1alpha2.WarmupActions{CustomizedAction: &workloadsv1alpha2.CustomizedAction{
					Containers: []corev1.Container{{Name: "node-check", Image: "busybox"}},
				}},
			},
		},
		Status: workloadsv1alpha2.RoleBasedGroupWarmupStatus{
			Phase:     workloadsv1alpha2.WarmupJobPhaseRunning,
			StartTime: &startTime,
		},
	}
	pod := mappedWarmupPod("node-1", "attempt-1", corev1.PodPending,
		waitingStatus("custom-0", "ImagePullBackOff", `Back-off pulling image "missing"`))
	pod.Namespace = warmup.Namespace
	pod.Labels[LabelWarmupName] = warmup.Name
	pod.Labels[LabelWarmupUID] = string(warmup.UID)
	r := newWarmupReconciler(warmup, pod)

	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(warmup)}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	updated := &workloadsv1alpha2.RoleBasedGroupWarmup{}
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(warmup), updated); err != nil {
		t.Fatalf("get updated warmup: %v", err)
	}
	if len(updated.Status.CustomizedActionResults) != 1 {
		t.Fatalf("expected one result, got %#v", updated.Status.CustomizedActionResults)
	}
	result := updated.Status.CustomizedActionResults[0]
	if result.Reason != CustomizedActionReasonGlobalTimeout {
		t.Fatalf("canary flipped (specific reason %q preserved) - invert this test, F4 is fixed", result.Reason)
	}
}
