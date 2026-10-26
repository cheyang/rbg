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

// Review tripwire (verify/durable-pod-failure-injection-codex): the migrated e2e
// specs in PR sgl-project/rbg#514 rest on three controller identities. If any of
// them changes, those specs silently stop testing what they claim to test:
//
//  1. firstRestartBackoffDeadline (test/e2e/testcase/v1alpha2/restart_policy_stability.go)
//     computes the backoff deadline as LastRestartTime + baseDelaySeconds. That is
//     only the controller's real deadline if the first backoff (RestartCount=1)
//     equals baseDelay.
//  2. RestartNginxContainer (test/utils/pod_failure.go) is a valid trigger for
//     RecreateRoleInstanceOnPodRestart specs only if a container RestartCount>0 on
//     a Ready, generation-converged instance triggers shouldRecreateInstance.
//  3. The ignored-component spec is meaningful only if the
//     RestartTriggerPolicyIgnore annotation suppresses that trigger.
//
// These are contract tests: they pass on correct controller code and fail if the
// identities the e2e suite depends on drift.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func readyRecreateInstance(name string) *workloadsv1alpha2.RoleInstance {
	return &workloadsv1alpha2.RoleInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Namespace:  "default",
			Generation: 1,
		},
		Spec: workloadsv1alpha2.RoleInstanceSpec{
			RestartPolicyConfig: &workloadsv1alpha2.RestartPolicyConfig{
				Type: workloadsv1alpha2.RecreateRoleInstanceOnPodRestart,
			},
			Components: []workloadsv1alpha2.RoleInstanceComponent{
				{Size: ptr.To[int32](1)},
			},
		},
		Status: workloadsv1alpha2.RoleInstanceStatus{
			ObservedGeneration: 1,
			Conditions: []workloadsv1alpha2.RoleInstanceCondition{
				{Type: workloadsv1alpha2.RoleInstanceReady, Status: corev1.ConditionTrue},
			},
		},
	}
}

// Identity 1: the first backoff equals baseDelay, so the e2e deadline anchor
// LastRestartTime + baseDelaySeconds matches the controller's computation for
// the exact (base, max) pairs the backoff specs use.
func TestReviewAnchorFirstBackoffEqualsBaseDelay(t *testing.T) {
	for _, tc := range []struct{ base, max int32 }{{90, 300}, {120, 300}} {
		assert.Equal(t, tc.base, calculateRestartDelay(tc.base, tc.max, 1),
			"e2e firstRestartBackoffDeadline assumes first backoff == baseDelay (base=%d)", tc.base)
	}
	// And no backoff may apply before any recreation (RestartCount=0), or the
	// specs' "first restart has no backoff" phase would break.
	assert.Equal(t, int32(0), calculateRestartDelay(90, 300, 0),
		"first recreation must not be delayed")
}

func podWithRestartedContainer(ignore bool) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pod-0"},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: "nginx", RestartCount: 1},
			},
		},
	}
	if ignore {
		pod.Annotations = map[string]string{
			constants.RestartTriggerPolicyAnnotationKey: constants.RestartTriggerPolicyIgnore,
		}
	}
	return pod
}

// Identity 2/3: a container restart (what RestartNginxContainer produces, with
// the Pod staying Running) must trigger recreation for a Ready instance, and the
// Ignore annotation must suppress that trigger.
func TestReviewAnchorContainerRestartTrigger(t *testing.T) {
	assert.True(t, shouldRecreateInstance(readyRecreateInstance("ri-a"),
		[]*corev1.Pod{podWithRestartedContainer(false)}, nil),
		"container RestartCount>0 must trigger recreation, or every RestartNginxContainer-based spec is vacuous")
	assert.False(t, shouldRecreateInstance(readyRecreateInstance("ri-b"),
		[]*corev1.Pod{podWithRestartedContainer(true)}, nil),
		"Ignore annotation must suppress the container-restart trigger")
}
