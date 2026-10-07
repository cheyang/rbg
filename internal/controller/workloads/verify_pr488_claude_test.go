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

// Reviewer verification harness for PR #488 (branch
// verify/pr488-customized-action-reporting-claude). Additive only — no
// production code is changed on this branch.
//
// Claims under test (see docs/verification/pr488-customized-action-reporting-claude/README.md):
//
//   C1 [contract, red while P1 open]   A successful result must keep per-container
//       detail when the status budget is not exhausted. Pure-function minimal repro
//       of prior finding P1 (successful results lose container details unconditionally).
//   C2 [contract, red while P1 open]   Same claim one layer up: updateStatus must
//       report container detail for a single-node success (mirrors the e2e
//       assertion at test/e2e/testcase/v1alpha2/warmup.go:245 that is red on this head).
//   C3 [contract, red while P1 open]   Second affected path: merged/deduplicated
//       container identities (the "preserve original container identities" feature)
//       must survive into the reported result of a successful pod.
//   C4 [contract, expected green]      Guard for the event path no upstream test
//       covers: a node whose result transitions at global timeout must emit a
//       per-node transition event (not only the aggregate job-failure event).
//   C5 [contract, expected green]      Prior finding P2 refutation half: waiting
//       failures DO record the failing pod-container identity and waiting reason
//       in the per-container results, so the status CAN identify the failing container.
//   C6 [contract, red while P2 residual open] Prior finding P2 refined residual:
//       the aggregate message and Warning event for a waiting failure should
//       identify the failing container (#486 asks for the container name when
//       available).

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func verifyClaudeSucceededResult(nodeName, podName, containerName, podContainerName string) workloadsv1alpha2.CustomizedActionResult {
	return workloadsv1alpha2.CustomizedActionResult{
		NodeName: nodeName,
		PodName:  podName,
		State:    workloadsv1alpha2.CustomizedActionStateSucceeded,
		Reason:   CustomizedActionReasonCompleted,
		Containers: []workloadsv1alpha2.CustomizedActionContainerResult{{
			ContainerName:    containerName,
			PodContainerName: podContainerName,
			State:            workloadsv1alpha2.CustomizedActionContainerStateSucceeded,
			ExitCode:         ptr.To(int32(0)),
		}},
	}
}

// C1: minimal repro of P1 — the drop of successful detail is unconditional,
// not size-driven. A single ~200-byte result must keep its container detail.
func TestVerifyC1SuccessfulResultKeepsContainerDetail(t *testing.T) {
	result := verifyClaudeSucceededResult("node-1", "attempt-1", "decode-task", "custom-0")

	limited, truncated := limitCustomizedActionResults([]workloadsv1alpha2.CustomizedActionResult{result})
	t.Logf("observed: containers=%d truncated=%v", len(limited[0].Containers), truncated)
	if len(limited) != 1 {
		t.Fatalf("expected the single result to fit, got %d results", len(limited))
	}
	if len(limited[0].Containers) != 1 {
		t.Fatalf("successful result lost container detail although the status budget was not exhausted: %#v", limited[0])
	}
	container := limited[0].Containers[0]
	if container.ContainerName != "decode-task" || container.ExitCode == nil || *container.ExitCode != 0 {
		t.Fatalf("successful container detail corrupted: %#v", container)
	}
	if truncated {
		t.Fatal("nothing was omitted for size, but CustomizedActionResultsTruncated was reported true")
	}
}

// C2: the same claim through the full status pipeline (fake client), mirroring
// the e2e assertion that is red on this head.
func TestVerifyC2UpdateStatusReportsSuccessfulContainerDetail(t *testing.T) {
	warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{Name: "verify-c2", Namespace: "default", UID: "uid-c2", Generation: 1},
	}
	pod := mappedWarmupPod("node-1", "attempt-1", corev1.PodSucceeded, terminatedStatus("custom-0", 0, "Completed", ""))
	desired := map[string][]workloadsv1alpha2.WarmupActions{
		"node-1": {{CustomizedAction: &workloadsv1alpha2.CustomizedAction{
			Containers: []corev1.Container{{Name: "decode-task", Image: "busybox"}},
		}}},
	}
	r := newWarmupReconciler(warmup)

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
		t.Fatalf("expected success, got %#v", result)
	}
	if len(result.Containers) != 1 {
		t.Fatalf("successful result lost container detail through updateStatus: %#v", result)
	}
	if result.Containers[0].ContainerName != "node-check" || result.Containers[0].ExitCode == nil || *result.Containers[0].ExitCode != 0 {
		t.Fatalf("successful container detail corrupted: %#v", result.Containers[0])
	}
	if updated.Status.CustomizedActionResultsTruncated {
		t.Fatal("single tiny result reports CustomizedActionResultsTruncated=true; the flag is meaningless for successful runs")
	}
}

// C3: second affected path — two roles with identical container specs are
// merged into one pod container; the mapping records both original names, but
// on success neither identity survives into the reported result.
func TestVerifyC3MergedIdentitiesSurviveSuccessfulRun(t *testing.T) {
	warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{Name: "verify-c3", Namespace: "default", UID: "uid-c3", Generation: 1},
	}
	r := newWarmupReconciler(warmup)
	actions := []workloadsv1alpha2.WarmupActions{
		{CustomizedAction: &workloadsv1alpha2.CustomizedAction{Containers: []corev1.Container{
			{Name: "prefill-check", Image: "busybox", Command: []string{"sh", "-c", "exit 0"}},
		}}},
		{CustomizedAction: &workloadsv1alpha2.CustomizedAction{Containers: []corev1.Container{
			{Name: "decode-check", Image: "busybox", Command: []string{"sh", "-c", "exit 0"}},
		}}},
	}
	pod, _ := r.buildWarmupPod(warmup, "node-1", actions)

	mappings, err := customizedActionMappingsFromPod(pod)
	if err != nil {
		t.Fatalf("decode mappings: %v", err)
	}
	if len(mappings) != 1 || len(mappings[0].ContainerNames) != 2 {
		t.Fatalf("expected one merged mapping covering both identities, got %#v", mappings)
	}

	pod.Name = "attempt-1"
	pod.CreationTimestamp = metav1.Now()
	statuses := make([]corev1.ContainerStatus, 0, len(pod.Spec.Containers))
	for i := range pod.Spec.Containers {
		statuses = append(statuses, terminatedStatus(pod.Spec.Containers[i].Name, 0, "Completed", ""))
	}
	pod.Status = corev1.PodStatus{Phase: corev1.PodSucceeded, ContainerStatuses: statuses}

	desired := map[string][]workloadsv1alpha2.WarmupActions{"node-1": actions}
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
	containers := updated.Status.CustomizedActionResults[0].Containers
	if len(containers) != 2 {
		t.Fatalf("merged identities lost on success: the mapping recorded %v but the result reports %#v",
			mappings[0].ContainerNames, containers)
	}
	names := []string{containers[0].ContainerName, containers[1].ContainerName}
	if names[0] != "decode-check" || names[1] != "prefill-check" {
		t.Fatalf("expected both original identities, got %v", names)
	}
}

// C4: guard for the global-timeout event path — no upstream test asserts that
// per-node transition events are emitted when the global timeout flips running
// nodes to failed. Expected green; if it ever goes red, per-node observability
// at global timeout regressed.
func TestVerifyC4GlobalTimeoutEmitsPerNodeTransitionEvents(t *testing.T) {
	startTime := metav1.NewTime(time.Now().Add(-2 * time.Minute))
	warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{Name: "verify-c4", Namespace: "default", UID: "uid-c4", Generation: 2},
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
			Phase: workloadsv1alpha2.WarmupJobPhaseRunning,
			StartTime: &startTime,
			CustomizedActionResults: []workloadsv1alpha2.CustomizedActionResult{{
				NodeName: "node-1", PodName: "attempt-1",
				State: workloadsv1alpha2.CustomizedActionStateRunning,
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
	recorder := record.NewFakeRecorder(20)
	r.Recorder = recorder

	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(warmup)}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	events := drainClaudeEvents(recorder)
	perNode := ""
	for _, ev := range events {
		if strings.Contains(ev, "node=node-1") && strings.Contains(ev, CustomizedActionReasonGlobalTimeout) {
			perNode = ev
			break
		}
	}
	if perNode == "" {
		t.Fatalf("expected a per-node transition event for node-1 at global timeout, got %v", events)
	}
}

func verifyClaudeMappedPod(nodeName, podName string, phase corev1.PodPhase, mappings []customizedActionContainerMapping, statuses ...corev1.ContainerStatus) *corev1.Pod {
	raw, err := json.Marshal(mappings)
	if err != nil {
		panic(err)
	}
	containers := make([]corev1.Container, 0, len(statuses))
	for i := range mappings {
		containers = append(containers, corev1.Container{Name: mappings[i].PodContainerName, Image: "busybox"})
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        podName,
			Labels:      map[string]string{LabelNodeName: nodeName},
			Annotations: map[string]string{AnnotationCustomizedActionContainers: string(raw)},
		},
		Spec:   corev1.PodSpec{Containers: containers},
		Status: corev1.PodStatus{Phase: phase, ContainerStatuses: statuses},
	}
}

// C5: refutes the status half of prior finding P2 — the per-container results
// DO carry the failing pod container's identity and waiting reason, so the
// status can identify which container hit the image-pull error.
func TestVerifyC5WaitingFailureIdentifiesContainerInStatus(t *testing.T) {
	pod := verifyClaudeMappedPod("node-1", "two-containers", corev1.PodPending, []customizedActionContainerMapping{
		{PodContainerName: "custom-0", ContainerNames: []string{"pull-check"}},
		{PodContainerName: "custom-1", ContainerNames: []string{"other-check"}},
	},
		waitingStatus("custom-0", "ImagePullBackOff", "back-off pulling image"),
		waitingStatus("custom-1", "ContainerCreating", ""),
	)

	got := evaluateCustomizedActionPod(pod)
	if got.State != workloadsv1alpha2.CustomizedActionStatePending || got.Reason != CustomizedActionReasonImagePullFailed {
		t.Fatalf("expected pending/image-pull-failed aggregate, got %#v", got)
	}
	if len(got.Containers) != 2 {
		t.Fatalf("expected two container results, got %#v", got.Containers)
	}
	byPodContainer := map[string]workloadsv1alpha2.CustomizedActionContainerResult{}
	for i := range got.Containers {
		byPodContainer[got.Containers[i].PodContainerName] = got.Containers[i]
	}
	failing := byPodContainer["custom-0"]
	if failing.TerminationReason != "ImagePullBackOff" || failing.TerminationMessage != "back-off pulling image" {
		t.Fatalf("expected the failing pod container to carry its waiting reason, got %#v", failing)
	}
	if failing.ContainerName != "pull-check" {
		t.Fatalf("expected the failing pod container to be identified, got %#v", failing)
	}
	healthy := byPodContainer["custom-1"]
	if healthy.TerminationReason != "ContainerCreating" {
		t.Fatalf("expected the healthy pod container to stay distinguishable, got %#v", healthy)
	}
}

// C6: the refined residual of P2 — the aggregate message and the Warning event
// for a waiting failure do not identify the failing container (#486 asks for
// the container name when available).
func TestVerifyC6WaitingFailureAggregateNamesFailingContainer(t *testing.T) {
	pod := verifyClaudeMappedPod("node-1", "two-containers", corev1.PodPending, []customizedActionContainerMapping{
		{PodContainerName: "custom-0", ContainerNames: []string{"pull-check"}},
		{PodContainerName: "custom-1", ContainerNames: []string{"other-check"}},
	},
		waitingStatus("custom-0", "ImagePullBackOff", "back-off pulling image"),
		waitingStatus("custom-1", "ContainerCreating", ""),
	)
	got := evaluateCustomizedActionPod(pod)

	if !strings.Contains(got.Message, "custom-0") && !strings.Contains(got.Message, "pull-check") {
		t.Fatalf("aggregate message does not identify the failing container: %q", got.Message)
	}

	recorder := record.NewFakeRecorder(10)
	warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{Name: "verify-c6", Namespace: "default"},
	}
	recordCustomizedActionEvents(recorder, warmup, nil, []workloadsv1alpha2.CustomizedActionResult{got})
	events := drainClaudeEvents(recorder)
	if len(events) == 0 {
		t.Fatal("expected a Warning event for the image-pull failure")
	}
	if !strings.Contains(events[0], "custom-0") && !strings.Contains(events[0], "pull-check") {
		t.Fatalf("Warning event does not identify the failing container: %q", events[0])
	}
}

func drainClaudeEvents(recorder *record.FakeRecorder) []string {
	events := make([]string, 0, 8)
	for {
		select {
		case ev := <-recorder.Events:
			events = append(events, ev)
		default:
			return events
		}
	}
}

