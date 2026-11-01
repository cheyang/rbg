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

package roleinstance

// Reviewer harness (Claude) for sgl-project/rbg PR #536 — finding F1.
//
// POLARITY: contract test. FAILS on the PR head (bug present), PASSES once the
// stuck lifecycle blocker is released on the PreparingDelete -> Normal
// transition.
//
// Enclosing lifecycle (traced to the controller entry point):
//   - statelessmode/sync/scale.go:266 deleteInstances() puts an instance into
//     PreparingDelete with markNotReady=true -> this PR injects the custom
//     readiness gate into spec and writes a False condition carrying the
//     "preDeleteHook" key -> component Pods are marked NotReady.
//   - statelessmode/sync/scale.go:185 managePreparingDelete() transitions the
//     instance back to Normal with markPodNotReady=FALSE (the "regret" path,
//     allowed once the set no longer has MarkNotReady on its hooks).
//   - executeRoleInstanceNotReadyPolicy() case Normal only removes the
//     preparingUpdateHookKey (and only when markPodNotReady=true), and no code
//     path anywhere removes preparingDeleteHookKey. The gate also stays in
//     spec. customReadinessBlocked() therefore stays true forever and every
//     reconcile re-marks the Pods NotReady.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	api "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	instancelifecycle "sigs.k8s.io/rbgs/pkg/inplace/instance/lifecycle"
	instancereadiness "sigs.k8s.io/rbgs/pkg/inplace/instance/readiness"
)

// TestF1RegretReleaseMustClearLifecycleBlock verifies that after a scale-down
// is regretted (PreparingDelete -> Normal with markPodNotReady=false, exactly
// what managePreparingDelete does), the RoleInstance becomes Ready again and
// its component Pod is unblocked.
func TestF1RegretReleaseMustClearLifecycleBlock(t *testing.T) {
	for _, policy := range []api.RoleInstanceReadyPolicyType{api.RoleInstanceReadyOnAllPodReady, api.RoleInstanceReadyPolicyTypeNone} {
		t.Run(string(policy), func(t *testing.T) {
			instance := newReadinessTestInstance()
			instance.Spec.ReadyPolicy = policy
			pod := newReadinessTestPod(instance, "router")
			pod.Spec.ReadinessGates = []corev1.PodReadinessGate{{ConditionType: constants.InstancePodReadyConditionType}}
			pod.Status.Conditions = append(pod.Status.Conditions,
				corev1.PodCondition{Type: corev1.ContainersReady, Status: corev1.ConditionTrue},
				corev1.PodCondition{Type: constants.InPlaceUpdateReady, Status: corev1.ConditionTrue},
				corev1.PodCondition{Type: constants.InstancePodReadyConditionType, Status: corev1.ConditionTrue})
			c := fake.NewClientBuilder().WithScheme(readinessTestScheme(t)).WithObjects(instance, pod).WithStatusSubresource(instance, pod).Build()
			updater := newStatusUpdater(c).(*realStatusUpdater)
			key := client.ObjectKeyFromObject(instance)
			podKey := client.ObjectKeyFromObject(pod)

			// 1. Scale-down with a PreDelete hook that marks NotReady.
			require.NoError(t, c.Get(context.Background(), key, instance))
			_, _, err := instancelifecycle.New(c).UpdateRoleInstanceLifecycle(instance, constants.RoleInstanceLifecycleStatePreparingDelete, true)
			require.NoError(t, err)
			require.NoError(t, c.Get(context.Background(), key, instance))
			if instancereadiness.GetReadinessCondition(instance) == nil {
				// Base branch: the block is never applied at all (premise bug,
				// claim P0). F1 only concerns the release path once the block
				// exists, so there is nothing to exercise here.
				t.Skipf("premise bug present: no custom condition written, F1 not exercisable")
			}
			require.Equal(t, corev1.ConditionFalse, instancereadiness.GetReadinessCondition(instance).Status, "block applied")
			require.NoError(t, updater.updatePodsLifeCycle(context.Background(), instance, []*corev1.Pod{pod}))
			require.NoError(t, c.Get(context.Background(), podKey, pod))
			require.Equal(t, corev1.ConditionFalse, podInstanceReadyStatus(pod), "pod blocked")

			// 2. Regret: scale back up; the set no longer has MarkNotReady, so
			// managePreparingDelete transitions the instance to Normal with
			// markPodNotReady=false (scale.go:185).
			require.NoError(t, c.Get(context.Background(), key, instance))
			_, _, err = instancelifecycle.New(c).UpdateRoleInstanceLifecycle(instance, constants.RoleInstanceLifecycleStateNormal, false)
			require.NoError(t, err)

			// 3. Contract: the lifecycle block must be released. The instance
			// must be Ready again and the component Pod unblocked.
			require.NoError(t, c.Get(context.Background(), key, instance))
			require.NoError(t, updater.updatePodsLifeCycle(context.Background(), instance, []*corev1.Pod{pod}))
			require.NoError(t, c.Get(context.Background(), key, instance))
			condition := instancereadiness.GetReadinessCondition(instance)
			if condition != nil {
				require.NotContains(t, condition.Message, "preDeleteHook",
					"the preDeleteHook blocker must be removed when the delete is regretted")
				require.Equal(t, corev1.ConditionTrue, condition.Status,
					"the custom ready condition must return to True once no blocker remains")
			}
			ready := getInstanceReadyCondition(instance, &api.RoleInstanceCondition{Type: api.RoleInstanceAllPodsReady, Status: corev1.ConditionTrue},
				&api.RoleInstanceCondition{Type: api.RoleInstanceInPlaceUpdateReady, Status: corev1.ConditionTrue})
			require.Equal(t, corev1.ConditionTrue, ready.Status, "RoleInstance must become Ready again after regret")
			require.NoError(t, c.Get(context.Background(), podKey, pod))
			require.Equal(t, corev1.ConditionTrue, podInstanceReadyStatus(pod), "component Pod must be unblocked after regret")
		})
	}
}

func podInstanceReadyStatus(pod *corev1.Pod) corev1.ConditionStatus {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == constants.InstancePodReadyConditionType {
			return condition.Status
		}
	}
	return ""
}
