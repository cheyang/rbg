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

package workloads

// Reviewer verification harness (Claude, PR #488, first round).
// Additive only — production code is untouched.
//
// Polarities (see docs/verification/customized-action-execution-claude/):
//   - F1 contract: TestVerifyFailWarmupJobFinalizationEmitsEvents_NilDesiredNodes
//     is expected RED on the PR head (reproduces the finding) and GREEN after a fix.
//   - F1 control: TestVerifyFailWarmupJobFinalizationEmitsEvents_WithDesiredNodes
//     is expected GREEN on the PR head (proves the harness bites in the sibling path).
//   - F3 canary: TestVerifyBuildWarmupPodDuplicateIdentityAcrossRoles documents the
//     current duplicate-identity behavior; it must FLIP (fail) if identities are deduped.
//   - F4 canary: TestVerifyAggregateStartFailureMessageNotTruncated documents that the
//     aggregate ContainerStartFailed message skips truncation; it must FLIP if truncated.

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func drainVerifyEvents(recorder *record.FakeRecorder) string {
	var builder strings.Builder
	for {
		select {
		case event := <-recorder.Events:
			builder.WriteString(event)
			builder.WriteString("\n")
		default:
			return builder.String()
		}
	}
}

// F1 contract test. When failWarmupJob finalizes customized action results with
// desiredNodes == nil (InvalidWarmupSpec / InvalidTarget paths), the Running->Failed
// transition of a node's result must produce a Kubernetes Event, matching the
// sibling path where desiredNodes != nil. Suspected: oldCustomizedActionResults
// aliases the very slice the finalization loop mutates in place, so
// recordCustomizedActionEvents sees old == new and stays silent.
func TestVerifyFailWarmupJobFinalizationEmitsEvents_NilDesiredNodes(t *testing.T) {
	warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{Name: "verify-f1", Namespace: "default", UID: "uid-verify-f1", Generation: 2},
		Status: workloadsv1alpha2.RoleBasedGroupWarmupStatus{
			Phase: workloadsv1alpha2.WarmupJobPhaseRunning,
			CustomizedActionResults: []workloadsv1alpha2.CustomizedActionResult{{
				NodeName: "node-1",
				PodName:  "attempt-1",
				State:    workloadsv1alpha2.CustomizedActionStateRunning,
			}},
		},
	}
	r := newWarmupReconciler(warmup)

	if err := r.failWarmupJob(
		context.Background(), warmup, nil, nil, nil, nil, "InvalidWarmupSpec", "spec is invalid",
	); err != nil {
		t.Fatalf("fail warmup job: %v", err)
	}

	// Control assertion: the result was finalized to Failed in the status.
	updated := &workloadsv1alpha2.RoleBasedGroupWarmup{}
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(warmup), updated); err != nil {
		t.Fatalf("get updated warmup: %v", err)
	}
	if len(updated.Status.CustomizedActionResults) != 1 ||
		updated.Status.CustomizedActionResults[0].State != workloadsv1alpha2.CustomizedActionStateFailed {
		t.Fatalf("expected finalized Failed result, got %#v", updated.Status.CustomizedActionResults)
	}

	// Contract assertion: the transition must have been reported as an Event.
	events := drainVerifyEvents(r.Recorder.(*record.FakeRecorder))
	foundTransition := false
	for _, line := range strings.Split(events, "\n") {
		if strings.Contains(line, "node=node-1") && strings.Contains(line, "Warning") {
			foundTransition = true
		}
	}
	if !foundTransition {
		t.Fatalf("expected a warning event for node=node-1's Running->Failed finalization, got events:\n%s", events)
	}
}

// F1 control test: the same finalization with desiredNodes != nil does emit the
// transition event, so the silence in the nil path is specific to it.
func TestVerifyFailWarmupJobFinalizationEmitsEvents_WithDesiredNodes(t *testing.T) {
	warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{Name: "verify-f1-control", Namespace: "default", UID: "uid-verify-f1c", Generation: 2},
		Status: workloadsv1alpha2.RoleBasedGroupWarmupStatus{
			Phase: workloadsv1alpha2.WarmupJobPhaseRunning,
			CustomizedActionResults: []workloadsv1alpha2.CustomizedActionResult{{
				NodeName: "node-1",
				PodName:  "attempt-1",
				State:    workloadsv1alpha2.CustomizedActionStateRunning,
			}},
		},
	}
	pod := mappedWarmupPod("node-1", "attempt-1", corev1.PodRunning, corev1.ContainerStatus{
		Name: "custom-0", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
	})
	pod.Namespace = warmup.Namespace
	pod.Labels[LabelWarmupName] = warmup.Name
	pod.Labels[LabelWarmupUID] = string(warmup.UID)
	r := newWarmupReconciler(warmup, pod)

	desired := map[string][]workloadsv1alpha2.WarmupActions{
		"node-1": {{CustomizedAction: &workloadsv1alpha2.CustomizedAction{
			Containers: []corev1.Container{{Name: "check", Image: "busybox"}},
		}}},
	}
	if err := r.failWarmupJob(
		context.Background(), warmup, []*corev1.Pod{pod}, nil, nil, desired,
		"GlobalTimeoutExceeded", "warmup timed out",
	); err != nil {
		t.Fatalf("fail warmup job: %v", err)
	}

	events := drainVerifyEvents(r.Recorder.(*record.FakeRecorder))
	foundTransition := false
	for _, line := range strings.Split(events, "\n") {
		if strings.Contains(line, "node=node-1") && strings.Contains(line, "Warning") {
			foundTransition = true
		}
	}
	if !foundTransition {
		t.Fatalf("control: expected a warning event for node=node-1 in the desiredNodes path, got events:\n%s", events)
	}
}

// F3 canary: two roles contributing the *same* container (identical spec AND
// identical user-visible name) produce a mapping with a duplicated
// ContainerNames entry and therefore duplicated per-container status entries.
// Documents current behavior; must flip if identities get deduplicated.
func TestVerifyBuildWarmupPodDuplicateIdentityAcrossRoles(t *testing.T) {
	r := newWarmupReconciler()
	warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{Name: "verify-f3", Namespace: "default", UID: "uid-verify-f3"},
	}
	same := corev1.Container{Name: "check", Image: "busybox", Command: []string{"true"}}
	actions := []workloadsv1alpha2.WarmupActions{
		{CustomizedAction: &workloadsv1alpha2.CustomizedAction{Containers: []corev1.Container{same}}},
		{CustomizedAction: &workloadsv1alpha2.CustomizedAction{Containers: []corev1.Container{same}}},
	}

	pod, _ := r.buildWarmupPod(warmup, "node-1", actions)
	if len(pod.Spec.Containers) != 1 {
		t.Fatalf("expected hash-deduped single pod container, got %d", len(pod.Spec.Containers))
	}
	mappings, err := customizedActionMappingsFromPod(pod)
	if err != nil {
		t.Fatalf("decode mappings: %v", err)
	}
	if len(mappings) != 1 {
		t.Fatalf("expected one mapping, got %#v", mappings)
	}
	names := mappings[0].ContainerNames
	if len(names) != 2 || names[0] != "check" || names[1] != "check" {
		t.Fatalf("canary flipped: duplicate identity no longer reported, got %#v", names)
	}

	pod.Status = corev1.PodStatus{
		Phase:             corev1.PodSucceeded,
		ContainerStatuses: []corev1.ContainerStatus{terminatedStatus("custom-0", 0, "Completed", "")},
	}
	result := evaluateCustomizedActionPod(pod)
	if len(result.Containers) != 2 {
		t.Fatalf("canary flipped: expected 2 duplicate logical container results, got %#v", result.Containers)
	}
	if result.Containers[0].ContainerName != "check" || result.Containers[1].ContainerName != "check" {
		t.Fatalf("unexpected identity in results: %#v", result.Containers)
	}
}

// F4 canary: the aggregate message for a terminated start failure
// (ContainerCannotRun / StartError) is taken raw from the container status and
// skips the 1024-byte truncation applied to the per-container
// TerminationMessage. Documents current behavior; must flip if truncation is
// applied to the aggregate path too.
func TestVerifyAggregateStartFailureMessageNotTruncated(t *testing.T) {
	raw := strings.Repeat("x", 4*customizedActionTerminationMessageLimit)
	pod := mappedWarmupPod("node-1", "start-fail", corev1.PodFailed,
		terminatedStatus("custom-0", 127, "ContainerCannotRun", raw))

	got := evaluateCustomizedActionPod(pod)
	if got.Reason != CustomizedActionReasonContainerStartFailed {
		t.Fatalf("unexpected reason %q", got.Reason)
	}
	if len(got.Message) <= customizedActionTerminationMessageLimit {
		t.Fatalf("canary flipped: aggregate message is now truncated (%d bytes)", len(got.Message))
	}
	if len(got.Containers[0].TerminationMessage) > customizedActionTerminationMessageLimit {
		t.Fatalf("per-container termination message exceeded the limit: %d", len(got.Containers[0].TerminationMessage))
	}
}

// F5 evidence test: an image pull that never succeeds keeps the node result in
// Pending (not Failed) — the customized action never terminalizes on its own
// without timeoutSeconds / globalTimeoutSeconds. Documents current behavior
// (matches upstream unit test expectations); not a bug-canary, this is the
// documented contract of the Waiting -> Pending mapping.
func TestVerifyImagePullBackOffStaysPending(t *testing.T) {
	pod := mappedWarmupPod("node-1", "pull-stuck", corev1.PodPending,
		waitingStatus("custom-0", "ImagePullBackOff", "back-off pulling image"))
	pod.Spec.ActiveDeadlineSeconds = nil // no per-action timeout configured

	got := evaluateCustomizedActionPod(pod)
	if got.State != workloadsv1alpha2.CustomizedActionStatePending ||
		got.Reason != CustomizedActionReasonImagePullFailed {
		t.Fatalf("expected Pending/ImagePullFailed, got %s/%s", got.State, got.Reason)
	}
}
