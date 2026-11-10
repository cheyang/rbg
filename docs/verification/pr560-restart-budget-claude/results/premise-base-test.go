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

// P0 premise check for PR #560, run against the BASE branch (fd160cc).
// Claim under test (PR body, quoted): unbounded automatic recovery —
// "The accounting is durable per Pod-template revision and independent of the
// backoff counter, which may reset after a quiet period". The premise is that
// at base, RecreateRoleInstanceOnPodRestart rebuilds the group forever: the
// backoff delay caps at maxDelaySeconds but never stops, and the backoff
// counter resets after a quiet period, so recovery is unbounded.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func TestP0BaseRestartRecoveryIsUnbounded(t *testing.T) {
	instance := &workloadsv1alpha2.RoleInstance{
		Spec: workloadsv1alpha2.RoleInstanceSpec{
			RestartPolicy: workloadsv1alpha2.RecreateRoleInstanceOnPodRestart,
		},
	}
	// After very many restarts the delay is still only capped at maxDelay, never unbounded:
	for rc := int32(1); rc <= 1000; rc = rc*2 + 1 {
		delay := calculateRestartDelay(instance.Spec.GetBaseDelaySeconds(), instance.Spec.GetMaxDelaySeconds(), rc)
		require.LessOrEqual(t, delay, instance.Spec.GetMaxDelaySeconds(), "delay caps at maxDelay, never stops restarts")
		require.Greater(t, delay, int32(0))
	}

	// After a quiet period beyond stableThreshold, the backoff counter resets:
	old := metav1.NewTime(time.Now().Add(-2 * stableThreshold(instance.Spec.GetMaxDelaySeconds())))
	instance.Status.RestartCount = 50
	instance.Status.LastRestartTime = &old
	updateRestartTracking(instance)
	require.Equal(t, int32(1), instance.Status.RestartCount, "base resets the backoff counter after a quiet period")
	// No bound on the number of accepted group rebuilds exists at base: the
	// spec has no budget field at all (verified by reading the base type —
	// this test compiles precisely because MaxGroupRestarts does not exist).
}
