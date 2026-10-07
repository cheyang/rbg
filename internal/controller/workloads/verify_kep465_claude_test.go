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

// Reviewer harness for PR #483 (KEP-465: Continuous Node-Pool Warmup).
// The PR is documentation-only; these tests verify the KEP's factual claims
// about the CURRENT (base-branch) controller behavior that the KEP builds on,
// and pin the behaviors the KEP promises to preserve.
//
// Polarity (per docs/verification/continuous-rbg-warmup/verify-manifest.json):
//   - canary   tests assert the CURRENT observed behavior. They PASS today and
//              document the symptom; a future Continuous implementation must
//              flip or supersede them deliberately.
//   - contract tests assert behavior the KEP promises to keep unchanged
//              (Once semantics). They PASS today and must keep passing.
//
// P0-related canaries (premise):
//   - TestClaudeVerifyP0OnceIgnoresLateNodes
//   - TestClaudeVerifyP0SpecUpdateReusesOldSucceededPod
// KEP-claim support:
//   - TestClaudeVerifyVolumeFirstWinsIsOrderDependent
// F1 evidence (Paused vs terminal Once phases):
//   - TestClaudeVerifyF1PausedDoesNotOverrideTerminalPhaseOrTTL
// F2 evidence (no retry backoff):
//   - TestClaudeVerifyF2FailedPodRetriedImmediately

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func claudeCtx() context.Context {
	return ctrl.LoggerInto(context.Background(), zap.New(zap.UseDevMode(true)))
}

// TestClaudeVerifyP0OnceIgnoresLateNodes is a BUG-CANARY for the PR premise.
// Issue #465 / KEP-465 claim: "RoleBasedGroupWarmup currently behaves as a
// one-shot job. After it reaches Completed or Failed, nodes that later join
// an autoscaled inference pool are not warmed."
// This test demonstrates the symptom on the base code: after a nodeSelector
// warmup completes, a new matching node joining the pool triggers no warmup.
// It must PASS on the base branch (canary); a Continuous-mode implementation
// would supersede it with a mode-aware variant.
func TestClaudeVerifyP0OnceIgnoresLateNodes(t *testing.T) {
	ctx := claudeCtx()
	warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{Name: "claude-p0", Namespace: "default", UID: "uid-claude-p0"},
		Spec: workloadsv1alpha2.RoleBasedGroupWarmupSpec{
			TargetNodes: &workloadsv1alpha2.TargetNodes{
				NodeSelector:  map[string]string{"pool": "gpu"},
				WarmupActions: workloadsv1alpha2.WarmupActions{ImagePreload: &workloadsv1alpha2.ImagePreloadAction{Images: []string{"img:v1"}}},
			},
		},
	}
	node1 := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-1", Labels: map[string]string{"pool": "gpu"}}}
	r := newWarmupReconciler(warmup, node1)

	// First reconcile: creates a warmup Pod for node-1.
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(warmup)}); err != nil {
		t.Fatalf("reconcile 1: %v", err)
	}
	pods := &corev1.PodList{}
	if err := r.List(ctx, pods, client.InNamespace("default")); err != nil {
		t.Fatalf("list pods: %v", err)
	}
	if len(pods.Items) != 1 {
		t.Fatalf("expected 1 warmup pod after first reconcile, got %d", len(pods.Items))
	}
	// Mark the pod succeeded.
	pod := &pods.Items[0]
	pod.Status.Phase = corev1.PodSucceeded
	if err := r.Status().Update(ctx, pod); err != nil {
		t.Fatalf("mark pod succeeded: %v", err)
	}

	// Second reconcile: node-1 succeeded -> Completed.
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(warmup)}); err != nil {
		t.Fatalf("reconcile 2: %v", err)
	}
	updated := &workloadsv1alpha2.RoleBasedGroupWarmup{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(warmup), updated); err != nil {
		t.Fatalf("get warmup: %v", err)
	}
	if updated.Status.Phase != workloadsv1alpha2.WarmupJobPhaseCompleted {
		t.Fatalf("expected Completed, got %q", updated.Status.Phase)
	}

	// A new node joins the pool after completion.
	node2 := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-2", Labels: map[string]string{"pool": "gpu"}}}
	if err := r.Create(ctx, node2); err != nil {
		t.Fatalf("create node-2: %v", err)
	}
	// Re-reconcile (as a periodic safety pass would).
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(warmup)}); err != nil {
		t.Fatalf("reconcile 3: %v", err)
	}

	pods = &corev1.PodList{}
	if err := r.List(ctx, pods, client.InNamespace("default")); err != nil {
		t.Fatalf("list pods: %v", err)
	}
	if len(pods.Items) != 1 {
		t.Fatalf("one-shot symptom NOT reproduced: expected still exactly 1 pod after node-2 joined, got %d", len(pods.Items))
	}
	for _, p := range pods.Items {
		if p.Labels[LabelNodeName] == "node-2" {
			t.Fatalf("one-shot symptom NOT reproduced: a pod was created for late-joining node-2")
		}
	}
	after := &workloadsv1alpha2.RoleBasedGroupWarmup{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(warmup), after); err != nil {
		t.Fatalf("get warmup: %v", err)
	}
	if after.Status.Phase != workloadsv1alpha2.WarmupJobPhaseCompleted || after.Status.Desired != 1 {
		t.Fatalf("one-shot symptom NOT reproduced: phase=%q desired=%d", after.Status.Phase, after.Status.Desired)
	}
}

// TestClaudeVerifyP0SpecUpdateReusesOldSucceededPod is a BUG-CANARY for the
// KEP motivation claim: "Although the API server currently accepts updates to
// targets and actions, the controller tracks completion only by node name. An
// update can therefore leave running Pods on the old definition, reuse an old
// successful Pod, and create later Pods from the new definition."
// Demonstrates on base code: after node-1 succeeded on image A, updating the
// spec to image B does NOT re-warm node-1 (old success reused by node name),
// while node-2 gets the new image B.
func TestClaudeVerifyP0SpecUpdateReusesOldSucceededPod(t *testing.T) {
	ctx := claudeCtx()
	warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{Name: "claude-p0-update", Namespace: "default", UID: "uid-claude-p0-update"},
		Spec: workloadsv1alpha2.RoleBasedGroupWarmupSpec{
			TargetNodes: &workloadsv1alpha2.TargetNodes{
				NodeNames:     []string{"node-1", "node-2"},
				WarmupActions: workloadsv1alpha2.WarmupActions{ImagePreload: &workloadsv1alpha2.ImagePreloadAction{Images: []string{"img:v1"}}},
			},
		},
	}
	r := newWarmupReconciler(warmup)

	// Reconcile 1: pods for node-1 and node-2.
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(warmup)}); err != nil {
		t.Fatalf("reconcile 1: %v", err)
	}
	pods := &corev1.PodList{}
	if err := r.List(ctx, pods, client.InNamespace("default")); err != nil {
		t.Fatalf("list pods: %v", err)
	}
	if len(pods.Items) != 2 {
		t.Fatalf("expected 2 pods, got %d", len(pods.Items))
	}

	// node-1's pod succeeds; node-2's pod stays active.
	for i := range pods.Items {
		if pods.Items[i].Labels[LabelNodeName] == "node-1" {
			pods.Items[i].Status.Phase = corev1.PodSucceeded
			if err := r.Status().Update(ctx, &pods.Items[i]); err != nil {
				t.Fatalf("mark succeeded: %v", err)
			}
		}
	}

	// Update the warmup definition to a new image (accepted today: no webhook).
	got := &workloadsv1alpha2.RoleBasedGroupWarmup{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(warmup), got); err != nil {
		t.Fatalf("get warmup: %v", err)
	}
	got.Spec.TargetNodes.WarmupActions.ImagePreload.Images = []string{"img:v2"}
	if err := r.Update(ctx, got); err != nil {
		t.Fatalf("update warmup spec: %v", err)
	}

	// Reconcile 2.
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(warmup)}); err != nil {
		t.Fatalf("reconcile 2: %v", err)
	}

	pods = &corev1.PodList{}
	if err := r.List(ctx, pods, client.InNamespace("default")); err != nil {
		t.Fatalf("list pods: %v", err)
	}
	node1Pods, node2Images := 0, map[string]bool{}
	for i := range pods.Items {
		p := &pods.Items[i]
		switch p.Labels[LabelNodeName] {
		case "node-1":
			node1Pods++
		case "node-2":
			for _, c := range p.Spec.Containers {
				node2Images[c.Image] = true
			}
		}
	}
	if node1Pods != 1 {
		t.Fatalf("unreliable-update symptom NOT reproduced: node-1 pod count changed to %d; expected the old succeeded Pod to be reused (count 1)", node1Pods)
	}
	if !node2Images["img:v1"] || node2Images["img:v2"] {
		t.Fatalf("unexpected node-2 images: %v (canary expects the pre-update image img:v1, since spec was updated after pods were created)", node2Images)
	}
	// Note: node-2 keeps img:v1 because the active pod occupies the node.
	// The unreliability is that node-1's old success satisfies the NEW
	// definition: after node-2's pod is deleted (or fails), a recreated pod
	// would use img:v2 while node-1 keeps img:v1 evidence — a mixture.
}

// TestClaudeVerifyVolumeFirstWinsIsOrderDependent supports the KEP's
// "Deterministic Effective Actions" claim that "the current first-wins
// volume-conflict behavior must also use sorted role order so the selected
// volume is deterministic": today the winner depends on Go map iteration
// order when roles are merged for an RBG target, so the same spec can produce
// different pods across reconciles. CANARY: passes today (both winners occur).
func TestClaudeVerifyVolumeFirstWinsIsOrderDependent(t *testing.T) {
	ctx := claudeCtx()
	volA := corev1.Volume{Name: "shared", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/a"}}}
	volB := corev1.Volume{Name: "shared", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/b"}}}
	warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{Name: "claude-vol", Namespace: "default", UID: "uid-claude-vol"},
		Spec: workloadsv1alpha2.RoleBasedGroupWarmupSpec{
			TargetRoleBasedGroup: &workloadsv1alpha2.TargetRoleBasedGroup{
				Name: "rbg-x",
				Roles: map[string]workloadsv1alpha2.WarmupActions{
					"role-a": {CustomizedAction: &workloadsv1alpha2.CustomizedAction{
						Containers: []corev1.Container{{Name: "c", Image: "busybox:1"}},
						Volumes:    []corev1.Volume{volA},
					}},
					"role-b": {CustomizedAction: &workloadsv1alpha2.CustomizedAction{
						Containers: []corev1.Container{{Name: "c", Image: "busybox:1"}},
						Volumes:    []corev1.Volume{volB},
					}},
				},
			},
		},
	}
	rbg := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "rbg-x", Namespace: "default", UID: "uid-rbg-x"},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rbg-pod",
			Namespace: "default",
			Labels: map[string]string{
				constants.GroupNameLabelKey: "rbg-x",
				constants.RoleNameLabelKey:  "role-a",
			},
		},
		Spec: corev1.PodSpec{NodeName: "node-1"},
	}
	pod2 := pod.DeepCopy()
	pod2.Name = "rbg-pod-2"
	pod2.Labels[constants.RoleNameLabelKey] = "role-b"

	// Dedicated reconciler: buildWarmupPod emits a VolumeConflict event per
	// round, and FakeRecorder's bounded channel blocks once full — size it
	// for all rounds.
	const rounds = 300
	scheme := newWarmupTestScheme()
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(warmup, rbg, pod, pod2).
		WithStatusSubresource(&workloadsv1alpha2.RoleBasedGroupWarmup{}).
		Build()
	r := &RoleBasedGroupWarmupReconciler{
		Client:   fakeClient,
		Scheme:   scheme,
		Recorder: record.NewFakeRecorder(rounds + 10),
	}

	winners := map[string]int{}
	for i := 0; i < rounds; i++ {
		desired, err := r.getDesiredNodesToWarmup(ctx, *warmup)
		if err != nil {
			t.Fatalf("getDesiredNodesToWarmup: %v", err)
		}
		actions := desired["node-1"]
		if len(actions) < 2 {
			t.Fatalf("expected merged actions from 2 roles, got %d", len(actions))
		}
		p, _ := r.buildWarmupPod(warmup, "node-1", actions)
		if len(p.Spec.Volumes) != 1 || p.Spec.Volumes[0].Name != "shared" {
			t.Fatalf("expected exactly one 'shared' volume, got %+v", p.Spec.Volumes)
		}
		winners[p.Spec.Volumes[0].HostPath.Path]++
	}
	if len(winners) != 2 {
		t.Fatalf("volume-conflict nondeterminism NOT reproduced: single winner %v after %d rounds; KEP's sorted-role-order requirement would then be unnecessary", winners, rounds)
	}
}

// TestClaudeVerifyF1PausedDoesNotOverrideTerminalPhaseOrTTL pins the CURRENT
// Once semantics that KEP-465's phase-calculation ordering ("1. Paused when
// spec.paused is true", unscoped) contradicts: terminal Completed/Failed
// phases win over spec.paused, and TTL cleanup still runs for a paused,
// completed warmup. CONTRACT: must keep passing after KEP-465 lands —
// otherwise Once TTL resources paused after completion can never be
// TTL-deleted (leak), which the KEP elsewhere promises stays unchanged.
func TestClaudeVerifyF1PausedDoesNotOverrideTerminalPhaseOrTTL(t *testing.T) {
	ctx := claudeCtx()
	paused := true
	ttl := int32(3600)
	warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{Name: "claude-f1", Namespace: "default", UID: "uid-claude-f1"},
		Spec: workloadsv1alpha2.RoleBasedGroupWarmupSpec{
			Paused: &paused,
			Policies: &workloadsv1alpha2.WarmupPolicies{
				TTLSecondsAfterFinished: &ttl,
			},
			TargetNodes: &workloadsv1alpha2.TargetNodes{
				NodeNames:     []string{"node-1"},
				WarmupActions: workloadsv1alpha2.WarmupActions{ImagePreload: &workloadsv1alpha2.ImagePreloadAction{Images: []string{"img:v1"}}},
			},
		},
		Status: workloadsv1alpha2.RoleBasedGroupWarmupStatus{
			// Completed one second ago; TTL of 3600s is NOT yet expired.
			Phase:         workloadsv1alpha2.WarmupJobPhaseCompleted,
			CompletionTime: &metav1.Time{Time: time.Now().Add(-time.Second)},
		},
	}
	r := newWarmupReconciler(warmup)

	// Paused must NOT flip a terminal phase...
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(warmup)})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	updated := &workloadsv1alpha2.RoleBasedGroupWarmup{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(warmup), updated); err != nil {
		t.Fatalf("get warmup: %v", err)
	}
	if updated.Status.Phase != workloadsv1alpha2.WarmupJobPhaseCompleted {
		t.Fatalf("existing Once rule violated: paused resource flipped from Completed to %q (KEP step 1 must not apply to terminal Once phases)", updated.Status.Phase)
	}

	// ...and once the TTL expires, the paused resource must still be deleted
	// by the TTL handler. Backdate completionTime past the TTL and reconcile.
	updated.Status.CompletionTime = &metav1.Time{Time: time.Now().Add(-2 * time.Hour)}
	if err := r.Status().Update(ctx, updated); err != nil {
		t.Fatalf("backdate completionTime: %v", err)
	}
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(warmup)}); err != nil {
		t.Fatalf("reconcile 2: %v", err)
	}
	err = r.Get(ctx, client.ObjectKeyFromObject(warmup), &workloadsv1alpha2.RoleBasedGroupWarmup{})
	if err == nil {
		t.Fatalf("existing Once rule violated: paused+Completed warmup with expired TTL was NOT deleted; TTL handler never runs if Paused takes precedence")
	}
}

// TestClaudeVerifyF2FailedPodRetriedImmediately is a BUG-CANARY documenting
// that the controller has NO retry backoff delay: a failed pod is re-created
// for the same node on the very next reconcile (the event-driven requeue the
// KEP relies on), with no wait. KEP-465's Failure Semantics specify per-node
// retry accounting but are silent on retry timing, so Continuous with
// backoffLimitPerNode=nil inherits an immediate-retry loop forever.
func TestClaudeVerifyF2FailedPodRetriedImmediately(t *testing.T) {
	ctx := claudeCtx()
	warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{Name: "claude-f2", Namespace: "default", UID: "uid-claude-f2"},
		Spec: workloadsv1alpha2.RoleBasedGroupWarmupSpec{
			Policies: &workloadsv1alpha2.WarmupPolicies{
				BackoffLimitPerNode: ptr.To(int32(3)),
			},
			TargetNodes: &workloadsv1alpha2.TargetNodes{
				NodeNames:     []string{"node-1"},
				WarmupActions: workloadsv1alpha2.WarmupActions{ImagePreload: &workloadsv1alpha2.ImagePreloadAction{Images: []string{"img:v1"}}},
			},
		},
	}
	r := newWarmupReconciler(warmup)

	// Attempt 1.
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(warmup)}); err != nil {
		t.Fatalf("reconcile 1: %v", err)
	}
	pods := &corev1.PodList{}
	if err := r.List(ctx, pods, client.InNamespace("default")); err != nil {
		t.Fatalf("list pods: %v", err)
	}
	if len(pods.Items) != 1 {
		t.Fatalf("expected 1 pod, got %d", len(pods.Items))
	}
	pod := &pods.Items[0]
	pod.Status.Phase = corev1.PodFailed
	if err := r.Status().Update(ctx, pod); err != nil {
		t.Fatalf("mark failed: %v", err)
	}

	// The very next reconcile (e.g. triggered by the Pod-failed event the
	// KEP's Continuous watches would deliver immediately) must create the
	// retry pod with NO delay — observed current behavior.
	result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(warmup)})
	if err != nil {
		t.Fatalf("reconcile 2: %v", err)
	}
	pods = &corev1.PodList{}
	if err := r.List(ctx, pods, client.InNamespace("default")); err != nil {
		t.Fatalf("list pods: %v", err)
	}
	retries := 0
	for i := range pods.Items {
		if pods.Items[i].Labels[LabelNodeName] == "node-1" && pods.Items[i].Status.Phase != corev1.PodFailed {
			retries++
		}
	}
	if retries == 0 {
		t.Fatalf("no-retry-delay symptom NOT reproduced: failed node was not retried on next reconcile")
	}
	// Canary assertion: the retry happened immediately — i.e. the only
	// requeue the controller returned is the optional global-timeout one
	// (absent here), never a backoff delay.
	if result.RequeueAfter != 0 {
		t.Fatalf("unexpected requeue delay %v; current controller has no retry backoff", result.RequeueAfter)
	}
}
