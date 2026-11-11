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

package sync

// PR #559 verification harness — unit layer (L1), head-only.
//
// Each test below encodes one finding from the review of PR #559. Polarity is
// declared in the test name/comment:
//   - CONTRACT: asserts intended-correct behavior. FAILS on buggy code.
//   - CANARY:   asserts current observed behavior. Flips to FAIL when the
//               behavior changes; invert at that point.

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func fromCreationInstance() *workloadsv1alpha2.RoleInstance {
	return &workloadsv1alpha2.RoleInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "fc-instance",
			Namespace:  "default",
			Generation: 1,
		},
		Spec: workloadsv1alpha2.RoleInstanceSpec{
			RestartPolicyConfig: &workloadsv1alpha2.RestartPolicyConfig{
				Type: workloadsv1alpha2.RecreateRoleInstanceOnPodRestartFromCreation,
			},
			Components: []workloadsv1alpha2.RoleInstanceComponent{
				{Name: "worker", Size: ptr.To[int32](2)},
			},
		},
		Status: workloadsv1alpha2.RoleInstanceStatus{
			ObservedGeneration: 1,
			Conditions: []workloadsv1alpha2.RoleInstanceCondition{
				{Type: workloadsv1alpha2.RoleInstanceReady, Status: corev1.ConditionFalse},
			},
		},
	}
}

func fromCreationPod(name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{APIVersion: "workloads.x-k8s.io/v1alpha2", Kind: "RoleInstance",
					Name: "fc-instance", UID: "instance-uid", Controller: ptr.To(true)},
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

// F1 (CONTRACT): observed deletion of a desired member triggers whole-instance
// recreation under the new policy even before the instance ever became Ready.
// This is the core claim of PR #559. (Duplicate coverage with the PR's own test
// matrix is intentional: this file is the reviewer's regression suite.)
func TestFromCreation_DeletionTriggerPreReady_Contract(t *testing.T) {
	instance := fromCreationInstance()
	pod := fromCreationPod("fc-instance-worker-1")
	now := metav1.Now()
	pod.DeletionTimestamp = &now

	if !shouldRecreateInstance(instance, []*corev1.Pod{pod}, nil) {
		t.Fatal("FromCreation: observed deletion of a desired member did not trigger recreation")
	}
}

// F2 (CONTRACT): container crash before first Ready triggers recreation
// (pre-Ready recovery — the second half of the #542 fix).
func TestFromCreation_ContainerCrashPreReady_Contract(t *testing.T) {
	instance := fromCreationInstance()
	pod := fromCompletionlessPod()
	pod.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: "main", RestartCount: 1}}

	if !shouldRecreateInstance(instance, []*corev1.Pod{pod}, nil) {
		t.Fatal("FromCreation: unexpected container restart before Ready did not trigger recreation")
	}
}

func fromCompletionlessPod() *corev1.Pod {
	pod := fromCreationPod("fc-instance-worker-1")
	return pod
}

// F3 (CANARY): the Restarting guard suppresses re-triggers while the
// Restarting condition is True — including pre-Ready member loss under the
// new policy. shouldRecreateInstanceGuarded returns false for a member
// deletion that occurs during an in-flight restart cycle, and the condition
// only clears when the instance becomes Ready (see instance_status.go: "Clear
// Restarting condition when instance becomes Ready"). If a member is lost
// while the instance is recovering from a previous rebuild and that loss
// prevents Ready, no further rebuild can happen.
//
// Current observed behavior asserted here: suppression. If the maintainers
// decide mid-recovery member loss must re-trigger, this test flips and must
// be inverted.
func TestFromCreation_RestartingGuardSuppressesMemberLossDuringRecovery_Canary(t *testing.T) {
	instance := fromCreationInstance()
	// In-flight restart cycle: persisted Restarting condition.
	instance.Status.Conditions = append(instance.Status.Conditions,
		workloadsv1alpha2.RoleInstanceCondition{
			Type:   workloadsv1alpha2.RoleInstanceRestarting,
			Status: corev1.ConditionTrue,
		})

	pod := fromCreationPod("fc-instance-worker-1")
	now := metav1.Now()
	pod.DeletionTimestamp = &now

	scheme := runtime.NewScheme()
	_ = workloadsv1alpha2.AddToScheme(scheme)
	fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(instance).Build()
	ctl := &realControl{
		Client:           fc,
		apiReader:        fc,
		recorder:         record.NewFakeRecorder(16),
		inplaceControl:   nil,
		lifecycleControl: nil,
	}
	defer ctl.ClearRestarting(instance)

	if ctl.shouldRecreateInstanceGuarded(context.Background(), instance, []*corev1.Pod{pod}, nil) {
		t.Fatal("Restarting guard did not suppress a re-trigger during an active restart cycle")
	}
}

// F4 (CANARY — real-world relevance refuted by envtest, see scenario D in
// pr559_fromcreation_verify_test.go): a member pod that is simply ABSENT from
// the pod list does not trigger the FromCreation policy by itself. In a live
// cluster even a grace-0 delete still exposes the pod with DeletionTimestamp
// via a watch MODIFIED event before removal, so the event-driven reconcile
// observes it; the pure-predicate gap only matters if the controller misses
// that window entirely (e.g. down or lagging behind the watch at that
// moment).
func TestFromCreation_MissedDeletionWindow_Canary(t *testing.T) {
	instance := fromCreationInstance()
	// Member 1 vanished between reconciles; member 0 is healthy and present.
	survivor := fromCreationPod("fc-instance-worker-0")

	if shouldRecreateInstance(instance, []*corev1.Pod{survivor}, nil) {
		t.Fatal("FromCreation: recreation triggered from a surviving pod with no failure signal; canary is stale")
	}
}

// F5 (CONTRACT, negative): isDesiredMember must not treat scale-in-removed
// ordinals as member loss (pod id >= component size).
func TestFromCreation_ScaleInRemovalNotMemberLoss_Contract(t *testing.T) {
	instance := fromCreationInstance() // component size 2
	pod := fromCreationPod("fc-instance-worker-5")
	now := metav1.Now()
	pod.DeletionTimestamp = &now // controller deleting a scaled-in pod

	if shouldRecreateInstance(instance, []*corev1.Pod{pod}, nil) {
		t.Fatal("FromCreation: scale-in removal of a non-desired ordinal triggered recreation")
	}
}

// F6 (CONTRACT): failed pod still triggers even while the pod is in-place
// updating under the new policy (member loss is not masked by update state).
func TestFromCreation_FailedPodDuringInPlaceUpdate_Contract(t *testing.T) {
	instance := fromCreationInstance()
	pod := fromCreationPod("fc-instance-worker-1")
	pod.Status.Phase = corev1.PodFailed
	// In-place update condition False → legacy policy would exempt this pod's
	// restarts; FromCreation must not exempt a Failed member.
	pod.Status.Conditions = []corev1.PodCondition{{Type: constants.InPlaceUpdateReady, Status: corev1.ConditionFalse}}

	if !shouldRecreateInstance(instance, []*corev1.Pod{pod}, nil) {
		t.Fatal("FromCreation: Failed pod during in-place update did not trigger recreation")
	}
}

// F7 (CANARY): a SUCCEEDED desired member that is being deleted (external
// delete of a normally-completed pod) DOES trigger a whole-instance rebuild
// under FromCreation, because the deletion branch does not exclude
// PodSucceeded — while the doc comment above shouldRecreateInstance still
// says "Per KEP Non-Goals: Succeeded pods are NOT handled here - they
// represent normal completion and should not trigger Instance recreation"
// and the PR's own test matrix calls normal completion a non-trigger.
// Current behavior asserted; flip if Succeeded is excluded from the
// deletion trigger.
func TestFromCreation_DeletedSucceededMemberTriggersRebuild_Canary(t *testing.T) {
	instance := fromCreationInstance()
	pod := fromCreationPod("fc-instance-worker-1")
	pod.Status.Phase = corev1.PodSucceeded
	now := metav1.Now()
	pod.DeletionTimestamp = &now

	if !shouldRecreateInstance(instance, []*corev1.Pod{pod}, nil) {
		t.Fatal("FromCreation: deleting a Succeeded member no longer triggers recreation; canary is stale")
	}
}
