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

// P0 — premise check for PR #559 / issue #542.
//
// Claim (issue #542, quoted):
//   "the same reconcile flips RoleInstanceReady to False, and shouldRecreateInstance()
//    gates on that current condition, so from then on the restart policy is permanently
//    disabled for that instance"
//   "A member lost this way is only replaced individually, the replacement cannot re-join
//    an established process group, and the instance stays NotReady until the RoleInstance
//    is deleted by hand."
//
// This test is a BUG-CANARY: it asserts the *legacy-policy* behavior that issue #542
// reports. It must PASS on the merge-base (symptom present) and, because PR #559
// deliberately leaves RecreateRoleInstanceOnPodRestart unchanged, it must keep passing
// on the PR head for that policy. The fix for the symptom is the new opt-in policy
// RecreateRoleInstanceOnPodRestartFromCreation, covered separately.
//
// This file intentionally references only symbols that exist on the merge-base so it
// compiles on both base and head.

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func p0Instance() *workloadsv1alpha2.RoleInstance {
	return &workloadsv1alpha2.RoleInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "p0-instance", Generation: 1},
		Spec: workloadsv1alpha2.RoleInstanceSpec{
			// Legacy policy, as reported in the issue.
			RestartPolicyConfig: &workloadsv1alpha2.RestartPolicyConfig{
				Type: workloadsv1alpha2.RecreateRoleInstanceOnPodRestart,
			},
			Components: []workloadsv1alpha2.RoleInstanceComponent{
				{Name: "worker", Size: ptr.To[int32](1)},
			},
		},
		Status: workloadsv1alpha2.RoleInstanceStatus{
			// Ready flipped to False when the node became unreachable (issue timeline t0).
			ObservedGeneration: 1,
			Conditions: []workloadsv1alpha2.RoleInstanceCondition{
				{Type: workloadsv1alpha2.RoleInstanceReady, Status: corev1.ConditionFalse},
			},
		},
	}
}

func p0Pod() *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "p0-instance-worker-0",
			Labels: map[string]string{},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

// Premise symptom (a): an observed member deletion (eviction) is not a restart-policy
// trigger for the legacy policy — the member is replaced individually instead.
func TestP0_LegacyPolicy_IgnoresObservedMemberDeletion(t *testing.T) {
	instance := p0Instance()
	pod := p0Pod()
	now := metav1.Now()
	pod.DeletionTimestamp = &now

	if shouldRecreateInstance(instance, []*corev1.Pod{pod}, nil) {
		t.Fatal("legacy policy unexpectedly recreated on member deletion; premise of #542 would be refuted")
	}
}

// Premise symptom (b): once the instance is NotReady with no prior restart-policy
// rebuild, the gate stays closed even for an ordinary unexpected container restart
// (issue repro A step 5) — "permanently disabled".
func TestP0_LegacyPolicy_RestartGateClosedOnceNotReady(t *testing.T) {
	instance := p0Instance()
	pod := p0Pod()
	pod.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: "main", RestartCount: 1}}

	if shouldRecreateInstance(instance, []*corev1.Pod{pod}, nil) {
		t.Fatal("legacy policy unexpectedly recreated after NotReady without recent restart; premise of #542 would be refuted")
	}
}

// Control: the same NotReady instance with a recent restart-policy rebuild still
// triggers (hasRecentRestart bypass) — showing the bypass requires a prior rebuild,
// which an evicted/deleted member never produces. This is the mechanism the issue
// calls "permanently disabled": the only escape hatch needs the very event that
// never happens.
func TestP0_LegacyPolicy_BypassRequiresPriorRebuild(t *testing.T) {
	instance := p0Instance()
	now := metav1.Now()
	instance.Status.LastRestartTime = &now
	instance.Status.RestartCount = 1
	pod := p0Pod()
	pod.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: "main", RestartCount: 1}}

	if !shouldRecreateInstance(instance, []*corev1.Pod{pod}, nil) {
		t.Fatal("hasRecentRestart bypass no longer works for the legacy policy; base behavior changed")
	}
}
