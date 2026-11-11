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

// Premise verification for PR #559 / issue #542 — runs against the BASE branch
// (without the patch). Copy into pkg/reconciler/roleinstance/sync/ on a base
// checkout and run: go test -run 'TestVerify559Base_' .
//
// P0 claims from the issue, tested here at the cheapest deciding layer:
//  1. "only Phase == Failed inactive Pods are turned into delete/replace
//     actions ... Pod deletion is not a restart signal at all" — a deleted
//     (terminating) member pod never triggers instance recreation.
//  2. "shouldRecreateInstance() gates on the current [Ready] condition ... the
//     restart policy is permanently disabled" — once Ready flips False and no
//     recent restart-policy rebuild exists, no failure signal re-arms it.

package sync

import (
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/stretchr/testify/require"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func base559Instance() *workloadsv1alpha2.RoleInstance {
	return &workloadsv1alpha2.RoleInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "base559", Namespace: "default", Generation: 1},
		Spec: workloadsv1alpha2.RoleInstanceSpec{
			RestartPolicyConfig: &workloadsv1alpha2.RestartPolicyConfig{Type: workloadsv1alpha2.RecreateRoleInstanceOnPodRestart},
			Components:          []workloadsv1alpha2.RoleInstanceComponent{{Name: "worker", Size: ptr.To[int32](1)}},
		},
		Status: workloadsv1alpha2.RoleInstanceStatus{ObservedGeneration: 1},
	}
}

func base559Pod() *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "base559-worker-0",
			Namespace: "default",
			Labels: map[string]string{
				constants.ComponentNameLabelKey: "worker",
				constants.ComponentIDLabelKey:   "0",
			},
		},
		Status: v1.PodStatus{Phase: v1.PodRunning},
	}
}

// P0-1: a deleted member pod (DeletionTimestamp set, as after eviction) is not
// a recreation trigger on base. Premise CONFIRMED iff this passes on base.
func TestVerify559Base_DeletedMemberIsNotATrigger(t *testing.T) {
	instance := base559Instance()
	instance.Status.Conditions = []workloadsv1alpha2.RoleInstanceCondition{
		{Type: workloadsv1alpha2.RoleInstanceReady, Status: v1.ConditionTrue},
	}
	pod := base559Pod()
	now := metav1.Now()
	pod.DeletionTimestamp = &now
	require.False(t, shouldRecreateInstance(instance, []*v1.Pod{pod}, nil),
		"P0-1 confirmed on base: member deletion does not trigger recreation")
}

// P0-2: once Ready has flipped False (e.g. node unreachable) and no restart
// has happened, a Failed pod does NOT trigger recreation — the gate is closed.
// Premise CONFIRMED iff this passes on base.
func TestVerify559Base_ReadyGateClosedAfterNotReady(t *testing.T) {
	instance := base559Instance() // no Ready condition, no LastRestartTime
	pod := base559Pod()
	pod.Status.Phase = v1.PodFailed
	require.False(t, shouldRecreateInstance(instance, []*v1.Pod{pod}, nil),
		"P0-2 confirmed on base: NotReady + no recent restart => policy inert")
}

// P0-3 (permanence): the only escape hatch is hasRecentRestart; without a
// prior restart it stays closed even long after the NotReady transition, and
// with a stale (expired) restart it closes again.
func TestVerify559Base_GateStaysClosed(t *testing.T) {
	instance := base559Instance()
	pod := base559Pod()
	pod.Status.Phase = v1.PodFailed
	// stale restart far beyond the stable threshold: gate closed again
	stale := metav1.NewTime(time.Now().Add(-2 * time.Hour))
	instance.Status.LastRestartTime = &stale
	instance.Status.RestartCount = 3
	require.False(t, shouldRecreateInstance(instance, []*v1.Pod{pod}, nil),
		"P0-3 confirmed on base: stale restart does not re-arm the gate")
}

// F3 base-side delta: on base, an Ignore-annotated Failed pod holds the
// backoff slow path open (positive requeue). This is the behavior the PR
// silently changes. Passes on base, flips on head.
func TestVerify559Base_IgnorePodHoldsBackoffOpen(t *testing.T) {
	ctrl := &realControl{}
	instance := base559Instance()
	instance.Status.Conditions = []workloadsv1alpha2.RoleInstanceCondition{
		{Type: workloadsv1alpha2.RoleInstanceReady, Status: v1.ConditionTrue},
	}
	now := metav1.Now()
	instance.Status.RestartCount = 1
	instance.Status.LastRestartTime = &now
	pod := base559Pod()
	pod.Status.Phase = v1.PodFailed
	pod.Annotations = map[string]string{
		constants.RestartTriggerPolicyAnnotationKey: constants.RestartTriggerPolicyIgnore,
	}
	requeue := ctrl.checkRestartBackoff(instance, nil, nil, []*v1.Pod{pod})
	require.Greater(t, requeue, time.Duration(0),
		"base behavior: Ignore-annotated Failed pod holds the backoff window open")
}
