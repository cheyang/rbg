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

// Verification harness for PR #557 (reviewer: codex). Additive test file; no
// production code is modified.
//
// P0 (premise of issue #542), contract polarity:
// A member Pod that is observed terminating (evicted after a node failure, or
// kubectl-deleted) must trigger RecreateRoleInstanceOnPodRestart even when the
// instance is currently NotReady and no restart has happened recently.
// On the base branch (fd160cc) this test FAILS on both cases: the readiness
// gate (wasInstanceReady/hasRecentRestart) closes permanently and a bare
// deletionTimestamp is not a trigger at all. On the PR head it PASSES.

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func pr557PremiseInstance() *workloadsv1alpha2.RoleInstance {
	return &workloadsv1alpha2.RoleInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name: "rank-group-0", Namespace: "default", UID: types.UID("premise-instance"), Generation: 3,
			Labels: map[string]string{constants.RoleTypeLabelKey: string(constants.ComponentsTemplateType)},
		},
		Spec: workloadsv1alpha2.RoleInstanceSpec{
			RestartPolicyConfig: &workloadsv1alpha2.RestartPolicyConfig{
				Type:             workloadsv1alpha2.RecreateRoleInstanceOnPodRestart,
				BaseDelaySeconds: ptr.To(int32(30)), MaxDelaySeconds: ptr.To(int32(600)),
			},
			Components: []workloadsv1alpha2.RoleInstanceComponent{{
				Name: "worker", Size: ptr.To(int32(2)),
				Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "main", Image: "rbg:latest"}},
				}},
			}},
		},
		// The instance WAS Ready before the fault, then went NotReady (exactly
		// the #542 timeline): the gate state at decision time is Ready=False,
		// and no restart-policy rebuild has ever run (LastRestartTime == nil).
		Status: workloadsv1alpha2.RoleInstanceStatus{
			ObservedGeneration: 3, CurrentRevision: "rev-1", UpdateRevision: "rev-1",
			Conditions: []workloadsv1alpha2.RoleInstanceCondition{{
				Type: workloadsv1alpha2.RoleInstanceReady, Status: corev1.ConditionFalse,
				LastTransitionTime: metav1.NewTime(time.Now()),
			}},
		},
	}
}

func pr557MemberPod(instance *workloadsv1alpha2.RoleInstance, id int, mutate func(*corev1.Pod)) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: instance.Name + "-worker-" + string(rune('0'+id)), Namespace: instance.Namespace,
			UID: types.UID("member-" + string(rune('0'+id))),
			Labels: map[string]string{
				constants.ComponentNameLabelKey:    "worker",
				constants.ComponentIDLabelKey:      string(rune('0' + id)),
				constants.RoleInstanceNameLabelKey: instance.Name,
			},
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(instance, workloadsv1alpha2.GroupVersion.WithKind("RoleInstance"))},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	if mutate != nil {
		mutate(pod)
	}
	return pod
}

// Case 1: member pod observed terminating (node-failure eviction / kubectl
// delete) while the instance is NotReady must trigger recreation.
func TestPR557PremiseTerminatingMemberTriggers(t *testing.T) {
	instance := pr557PremiseInstance()
	now := metav1.Now()
	terminating := pr557MemberPod(instance, 1, func(p *corev1.Pod) { p.DeletionTimestamp = &now })
	survivor := pr557MemberPod(instance, 0, nil)
	if !shouldRecreateInstance(instance, []*corev1.Pod{survivor, terminating}, nil) {
		t.Fatalf("premise of #542 reproduced: an observed member deletion while NotReady does NOT trigger recreation; the replacement pod can never rejoin the group and the instance stays NotReady forever")
	}
}

// Case 2: container restart on a surviving member while the instance is
// NotReady (and no recent restart) must trigger recreation. On base this is
// permanently disabled once Ready flips False.
func TestPR557PremiseRestartWhileNotReadyTriggers(t *testing.T) {
	instance := pr557PremiseInstance()
	crashed := pr557MemberPod(instance, 0, func(p *corev1.Pod) {
		p.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: "main", RestartCount: 1}}
	})
	survivor := pr557MemberPod(instance, 1, nil)
	if !shouldRecreateInstance(instance, []*corev1.Pod{crashed, survivor}, nil) {
		t.Fatalf("premise of #542 reproduced: a container restart while NotReady does NOT trigger recreation (readiness gate permanently closed)")
	}
}
