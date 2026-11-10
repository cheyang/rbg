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

// P0 premise evidence for PR #560 ("bound group recovery with maxGroupRestarts").
// Claim under test (PR body): "RBG may automatically rebuild a RoleInstance"
// an unbounded number of times for one Pod-template revision, and the backoff
// counter "may reset after a quiet period".
//
// This file intentionally uses ONLY symbols that already exist on the base
// branch (fd160cc), so the same file can be grafted onto base and head alike:
//   - base: proves the premise mechanism (no bound exists).
//   - head: documents that unset budget keeps the legacy unbounded behavior.
//
// Polarity: premise-evidence (expected to PASS on base, PASS on head with an
// unset budget). It is not a bug canary.

import (
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func verifyP0Instance() *workloadsv1alpha2.RoleInstance {
	return &workloadsv1alpha2.RoleInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "premise-0", Namespace: "default", UID: "premise-uid", Generation: 1},
		Spec: workloadsv1alpha2.RoleInstanceSpec{
			RestartPolicy: workloadsv1alpha2.RecreateRoleInstanceOnPodRestart,
			Components: []workloadsv1alpha2.RoleInstanceComponent{{
				Name: "worker", Size: ptr.To[int32](2),
				Template: v1.PodTemplateSpec{Spec: v1.PodSpec{Containers: []v1.Container{{Name: "worker", Image: "v1"}}}},
			}},
		},
		Status: workloadsv1alpha2.RoleInstanceStatus{
			ObservedGeneration: 1,
			CurrentRevision:    "rev-1",
			UpdateRevision:     "rev-1",
		},
	}
}

func verifyP0FailedPod() *v1.Pod {
	return &v1.Pod{Status: v1.PodStatus{Phase: v1.PodFailed}}
}

// TestVerifyP0EveryReadyFaultCycleTriggersRecreate shows that on an instance
// that becomes Ready between faults, every fault triggers a whole-group
// recreate decision — there is no counter, cap, or other bound anywhere in the
// decision function.
func TestVerifyP0EveryReadyFaultCycleTriggersRecreate(t *testing.T) {
	instance := verifyP0Instance()
	const cycles = 50
	for i := 0; i < cycles; i++ {
		// Instance became Ready again after the previous recreate.
		instance.Status.Conditions = []workloadsv1alpha2.RoleInstanceCondition{{
			Type: workloadsv1alpha2.RoleInstanceReady, Status: v1.ConditionTrue,
		}}
		if !shouldRecreateInstance(instance, []*v1.Pod{verifyP0FailedPod()}, nil) {
			t.Fatalf("cycle %d: expected a whole-group recreate decision, got none", i)
		}
		// Simulate the controller accepting the restart and the group becoming
		// NotReady while recreating (Ready condition cleared).
		updateRestartTracking(instance)
		instance.Status.Conditions = nil
	}
	t.Logf("premise mechanism confirmed: %d consecutive Ready->fault cycles each trigger a group recreate; no bound exists in the decision path", cycles)
}

// TestVerifyP0BackoffCounterSelfResets shows that even the exponential-backoff
// accounting (RestartCount) resets to zero after a quiet period, so restarts
// never become expensive — i.e. backoff is not a bound either.
func TestVerifyP0BackoffCounterSelfResets(t *testing.T) {
	instance := verifyP0Instance()
	old := metav1.NewTime(time.Now().Add(-time.Hour))
	instance.Status.LastRestartTime = &old
	instance.Status.RestartCount = 42
	updateRestartTracking(instance)
	if instance.Status.RestartCount != 1 {
		t.Fatalf("expected quiet-period reset to restart the backoff ladder (0 then increment to 1), got RestartCount=%d", instance.Status.RestartCount)
	}
	t.Logf("premise mechanism confirmed: RestartCount=%d after a 1h quiet period — backoff resets and never bounds total group rebuilds", instance.Status.RestartCount)
}
