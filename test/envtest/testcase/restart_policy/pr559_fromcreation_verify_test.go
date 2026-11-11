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

package restart_policy

// PR #559 verification harness — integration layer (L2, envtest).
//
// Scenarios (polarity declared per scenario):
//   A  CONTRACT  post-Ready observed member deletion → whole-instance rebuild
//   B  CONTRACT  pre-Ready container crash → whole-instance rebuild
//   C  CANARY    member loss during an in-flight rebuild (Restarting=True, not
//                yet Ready) is suppressed by the Restarting guard; CONTRACT:
//                once Ready clears Restarting, the standing trigger fires
//   D  CANARY    force-deleted member (no observed termination window) falls
//                back to single-pod replacement — no rebuild
//
// Realism notes:
//   - Pods are bound to a fake node (spec.nodeName) before deletion so a
//     graceful Delete leaves the object Terminating with DeletionTimestamp —
//     exactly what the controller sees during a real node failure / eviction.
//     (Envtest has no kubelet; unbound pods are removed instantly by the API
//     server, which would hide the trigger.)
//   - Force delete (grace 0) emulates `kubectl delete pod --force` and the
//     pod-GC path after a node object is removed.

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/test/envtest/testutil"
	wrappersv2 "sigs.k8s.io/rbgs/test/wrappers/v1alpha2"
)

const fcTimeout = time.Second * 90
const fcInterval = time.Millisecond * 250

func podReady(pod *corev1.Pod) bool {
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

var _ = Describe("RecreateRoleInstanceOnPodRestartFromCreation (PR #559 verification)", func() {
	var fcNs string

	BeforeEach(func() {
		fcNs = fmt.Sprintf("test-fc559-%d", time.Now().UnixNano())
		testutil.CreateNamespace(fcNs)
	})

	AfterEach(func() {
		testutil.DeleteNamespace(fcNs)
	})

	// --- local helpers (the existing suite's helpers are Describe-scoped) ---

	fcListPods := func(rbgName, roleName string) *corev1.PodList {
		podList := &corev1.PodList{}
		ExpectWithOffset(1, testutil.K8sClient.List(testutil.Ctx, podList,
			client.InNamespace(fcNs),
			client.MatchingLabels{
				constants.GroupNameLabelKey: rbgName,
				constants.RoleNameLabelKey:  roleName,
			},
		)).Should(Succeed())
		return podList
	}

	fcGetRoleInstance := func(rbgName, roleName string) *workloadsv1alpha2.RoleInstance {
		riList := &workloadsv1alpha2.RoleInstanceList{}
		ExpectWithOffset(1, testutil.K8sClient.List(testutil.Ctx, riList,
			client.InNamespace(fcNs),
			client.MatchingLabels{
				constants.GroupNameLabelKey: rbgName,
				constants.RoleNameLabelKey:  roleName,
			},
		)).Should(Succeed())
		if len(riList.Items) == 0 {
			return nil
		}
		return &riList.Items[0]
	}

	fcMakePodsReady := func(rbgName, roleName string) {
		// Unconditional: re-applies Running+Ready even to pods already Running
		// (scenario C leaves fresh pods Running-but-NotReady before recovery),
		// while preserving existing container RestartCounts so a standing crash
		// trigger is not erased by the fixture.
		EventuallyWithOffset(1, func() error {
			podList := fcListPods(rbgName, roleName)
			for i := range podList.Items {
				pod := &podList.Items[i]
				fresh := &corev1.Pod{}
				if err := testutil.K8sClient.Get(testutil.Ctx, client.ObjectKeyFromObject(pod), fresh); err != nil {
					return err
				}
				if fresh.Status.Phase == corev1.PodRunning && podReady(fresh) {
					continue
				}
				restarts := map[string]int32{}
				for _, cs := range fresh.Status.ContainerStatuses {
					restarts[cs.Name] = cs.RestartCount
				}
				testutil.SetPodRunningAndReady(fresh)
				for j := range fresh.Status.ContainerStatuses {
					if rc, ok := restarts[fresh.Status.ContainerStatuses[j].Name]; ok {
						fresh.Status.ContainerStatuses[j].RestartCount = rc
					}
				}
				if err := testutil.K8sClient.Status().Update(testutil.Ctx, fresh); err != nil {
					return err
				}
			}
			return nil
		}, fcTimeout, fcInterval).Should(Succeed())
	}

	fcBindPodsToNode := func(rbgName, roleName, node string) {
		EventuallyWithOffset(1, func() error {
			podList := fcListPods(rbgName, roleName)
			for i := range podList.Items {
				pod := &podList.Items[i]
				if pod.Spec.NodeName == node {
					continue
				}
				fresh := &corev1.Pod{}
				if err := testutil.K8sClient.Get(testutil.Ctx, client.ObjectKeyFromObject(pod), fresh); err != nil {
					return err
				}
				binding := &corev1.Binding{
					ObjectMeta: metav1.ObjectMeta{Name: fresh.Name, Namespace: fresh.Namespace},
					Target:     corev1.ObjectReference{Name: node},
				}
				// spec.nodeName is immutable via Update; bind through the subresource
				// so a graceful Delete later leaves the pod Terminating (like a real
				// node failure) instead of vanishing instantly.
				if err := testutil.K8sClient.SubResource("binding").Create(testutil.Ctx, fresh, binding); err != nil {
					return err
				}
			}
			return nil
		}, fcTimeout, fcInterval).Should(Succeed())
	}

	fcWaitActivePods := func(rbgName, roleName string, count int) {
		EventuallyWithOffset(1, func() int {
			podList := fcListPods(rbgName, roleName)
			active := 0
			for i := range podList.Items {
				p := &podList.Items[i]
				if p.DeletionTimestamp != nil || p.Status.Phase == corev1.PodFailed || p.Status.Phase == corev1.PodSucceeded {
					continue
				}
				active++
			}
			return active
		}, fcTimeout, fcInterval).Should(Equal(count))
	}

	fcWaitInstanceReady := func(rbgName, roleName string) {
		EventuallyWithOffset(1, func() string {
			ri := fcGetRoleInstance(rbgName, roleName)
			if ri == nil {
				return "no instance"
			}
			desc := fmt.Sprintf("observedGen=%d restarts=%d lastRestart=%v conds=[",
				ri.Status.ObservedGeneration, ri.Status.RestartCount, ri.Status.LastRestartTime)
			for _, cond := range ri.Status.Conditions {
				desc += fmt.Sprintf("%s=%s(%s;%s) ", cond.Type, cond.Status, cond.Reason, cond.Message)
			}
			desc += "] pods=["
			podList := fcListPods(rbgName, roleName)
			for i := range podList.Items {
				p := &podList.Items[i]
				rdy := "None"
				for _, c := range p.Status.Conditions {
					if c.Type == corev1.PodReady {
						rdy = string(c.Status)
					}
				}
				desc += fmt.Sprintf("%s:phase=%s,ready=%s,node=%s,del=%v ", p.Name, p.Status.Phase, rdy, p.Spec.NodeName, p.DeletionTimestamp != nil)
			}
			return desc + "]"
		}, fcTimeout, fcInterval).Should(ContainSubstring("RoleInstanceReady=True"))
	}

	fcWaitRestartCount := func(rbgName, roleName string, want int32) {
		EventuallyWithOffset(1, func() int32 {
			ri := fcGetRoleInstance(rbgName, roleName)
			if ri == nil {
				return -1
			}
			return ri.Status.RestartCount
		}, fcTimeout, fcInterval).Should(Equal(want))
	}

	fcIsRestarting := func(rbgName, roleName string) bool {
		ri := fcGetRoleInstance(rbgName, roleName)
		if ri == nil {
			return false
		}
		for _, cond := range ri.Status.Conditions {
			if cond.Type == workloadsv1alpha2.RoleInstanceRestarting && cond.Status == corev1.ConditionTrue {
				return true
			}
		}
		return false
	}

	fcCrashContainer := func(rbgName, roleName, podName string, restartCount int32) {
		EventuallyWithOffset(1, func() error {
			fresh := &corev1.Pod{}
			if err := testutil.K8sClient.Get(testutil.Ctx, client.ObjectKey{Namespace: fcNs, Name: podName}, fresh); err != nil {
				return err
			}
			testutil.SetPodContainerRestarted(fresh, fresh.Spec.Containers[0].Name, restartCount)
			return testutil.K8sClient.Status().Update(testutil.Ctx, fresh)
		}, fcTimeout, fcInterval).Should(Succeed())
	}

	// fcSetPodsRunningNotReady puts pods in Phase=Running with container statuses
	// (needed so a later RestartCount update is not a no-op on Pending pods)
	// but leaves the pod NotReady, so the instance never becomes Ready — the
	// crash-looping-during-initial-startup shape.
	fcSetPodsRunningNotReady := func(rbgName, roleName string) {
		EventuallyWithOffset(1, func() error {
			podList := fcListPods(rbgName, roleName)
			for i := range podList.Items {
				pod := &podList.Items[i]
				if pod.Status.Phase == corev1.PodRunning {
					continue
				}
				fresh := &corev1.Pod{}
				if err := testutil.K8sClient.Get(testutil.Ctx, client.ObjectKeyFromObject(pod), fresh); err != nil {
					return err
				}
				fresh.Status.Phase = corev1.PodRunning
				fresh.Status.Conditions = []corev1.PodCondition{
					{Type: corev1.PodReady, Status: corev1.ConditionFalse, LastTransitionTime: metav1.Now()},
					{Type: corev1.ContainersReady, Status: corev1.ConditionFalse, LastTransitionTime: metav1.Now()},
				}
				fresh.Status.ContainerStatuses = make([]corev1.ContainerStatus, len(fresh.Spec.Containers))
				for j, c := range fresh.Spec.Containers {
					fresh.Status.ContainerStatuses[j] = corev1.ContainerStatus{
						Name:    c.Name,
						Image:   c.Image,
						Ready:   false,
						Started: ptr.To(true),
					}
				}
				if err := testutil.K8sClient.Status().Update(testutil.Ctx, fresh); err != nil {
					return err
				}
			}
			return nil
		}, fcTimeout, fcInterval).Should(Succeed())
	}

	fcNewGroup := func(rbgName string) *workloadsv1alpha2.RoleBasedGroup {
		return wrappersv2.BuildBasicRoleBasedGroup(rbgName, fcNs).WithRoles(
			[]workloadsv1alpha2.RoleSpec{
				wrappersv2.BuildLeaderWorkerRole("role-1").
					WithReplicas(1).
					WithSize(2).
					WithRestartPolicy(workloadsv1alpha2.RecreateRoleInstanceOnPodRestartFromCreation).
					WithBaseDelaySeconds(1).
					WithMaxDelaySeconds(2).
					Obj(),
			}).Obj()
	}

	// Scenario A (CONTRACT): a member pod deleted (graceful, observed) after the
	// instance is Ready triggers a whole-instance rebuild under the new policy —
	// the core fix for issue #542. The legacy policy does not do this (see P0).
	It("A: observed member deletion after Ready rebuilds the whole instance", func() {
		rbgName := "fc-a"
		roleName := "role-1"
		Expect(testutil.K8sClient.Create(testutil.Ctx, fcNewGroup(rbgName))).Should(Succeed())

		fcWaitActivePods(rbgName, roleName, 2)
		fcBindPodsToNode(rbgName, roleName, "fc-node-a")
		fcMakePodsReady(rbgName, roleName)
		fcWaitInstanceReady(rbgName, roleName)

		podList := fcListPods(rbgName, roleName)
		Expect(podList.Items).Should(HaveLen(2))
		victim := &podList.Items[0]
		siblingUID := podList.Items[1].UID

		// Graceful delete: bound pod stays Terminating with DeletionTimestamp.
		Expect(testutil.K8sClient.Delete(testutil.Ctx, victim)).Should(Succeed())

		// The whole-instance rebuild must trigger: RestartCount increments and
		// the Restarting condition is set.
		fcWaitRestartCount(rbgName, roleName, 1)
		Eventually(func() bool { return fcIsRestarting(rbgName, roleName) }, fcTimeout, fcInterval).Should(BeTrue())

		// The healthy sibling is deleted too (whole-instance rebuild).
		Eventually(func() bool {
			fresh := &corev1.Pod{}
			if err := testutil.K8sClient.Get(testutil.Ctx,
				client.ObjectKey{Namespace: fcNs, Name: podList.Items[1].Name}, fresh); err != nil {
				return false // gone: also acceptable
			}
			return fresh.DeletionTimestamp != nil
		}, fcTimeout, fcInterval).Should(BeTrue())

		// Emulate the node coming back / force cleanup so the names free up.
		Eventually(func() error {
			list := fcListPods(rbgName, roleName)
			for i := range list.Items {
				p := &list.Items[i]
				if p.DeletionTimestamp == nil {
					continue
				}
				if err := testutil.K8sClient.Delete(testutil.Ctx, p, client.GracePeriodSeconds(0)); err != nil {
					return err
				}
			}
			return nil
		}, fcTimeout, fcInterval).Should(Succeed())

		// Fresh pods are created for both members.
		fcWaitActivePods(rbgName, roleName, 2)
		newList := fcListPods(rbgName, roleName)
		for i := range newList.Items {
			Expect(newList.Items[i].UID).ShouldNot(Equal(siblingUID))
		}

		// Recovery completes: Ready again, Restarting cleared.
		fcMakePodsReady(rbgName, roleName)
		fcWaitInstanceReady(rbgName, roleName)
		Eventually(func() bool { return fcIsRestarting(rbgName, roleName) }, fcTimeout, fcInterval).Should(BeFalse())
	})

	// Scenario B (CONTRACT): a container crash before the instance first becomes
	// Ready triggers a whole-instance rebuild under the new policy.
	It("B: pre-Ready container crash rebuilds the whole instance", func() {
		rbgName := "fc-b"
		roleName := "role-1"
		Expect(testutil.K8sClient.Create(testutil.Ctx, fcNewGroup(rbgName))).Should(Succeed())

		fcWaitActivePods(rbgName, roleName, 2)
		// Deliberately NOT making pods Ready — the instance has never been Ready.
		podList := fcListPods(rbgName, roleName)
		Expect(podList.Items).Should(HaveLen(2))
		crashed := podList.Items[0].Name
		oldUIDs := map[string]types.UID{}
		for i := range podList.Items {
			oldUIDs[podList.Items[i].Name] = podList.Items[i].UID
		}

		// Pods are Running with container statuses but NotReady (crash-looping
		// during initial startup); the instance has never been Ready.
		fcSetPodsRunningNotReady(rbgName, roleName)
		fcCrashContainer(rbgName, roleName, crashed, 1)

		// Whole-instance rebuild triggers although the instance was never Ready.
		fcWaitRestartCount(rbgName, roleName, 1)

		// Both members are replaced.
		fcWaitActivePods(rbgName, roleName, 2)
		newList := fcListPods(rbgName, roleName)
		Expect(newList.Items).Should(HaveLen(2))
		replaced := 0
		for i := range newList.Items {
			if uid, ok := oldUIDs[newList.Items[i].Name]; ok && newList.Items[i].UID != uid {
				replaced++
			}
		}
		Expect(replaced).Should(Equal(2), "both members should be recreated, not just the crashed one")

		// And the instance can become Ready afterwards.
		fcMakePodsReady(rbgName, roleName)
		fcWaitInstanceReady(rbgName, roleName)
	})

	// Scenario C (CANARY + CONTRACT): while a rebuild is in flight (Restarting=True,
	// fresh pods not yet Ready), a further member loss is suppressed by the
	// Restarting guard; the condition only clears when the instance becomes Ready.
	// CONTRACT part: once Ready clears Restarting, the standing crash trigger fires.
	It("C: mid-recovery member loss is suppressed until Ready", func() {
		rbgName := "fc-c"
		roleName := "role-1"
		Expect(testutil.K8sClient.Create(testutil.Ctx, fcNewGroup(rbgName))).Should(Succeed())

		fcWaitActivePods(rbgName, roleName, 2)
		fcBindPodsToNode(rbgName, roleName, "fc-node-c")
		fcMakePodsReady(rbgName, roleName)
		fcWaitInstanceReady(rbgName, roleName)

		// Trigger rebuild #1 with an observed deletion.
		podList := fcListPods(rbgName, roleName)
		Expect(testutil.K8sClient.Delete(testutil.Ctx, &podList.Items[0])).Should(Succeed())
		fcWaitRestartCount(rbgName, roleName, 1)

		// Free the names (emulate node recovery / force cleanup for ALL
		// terminating pods) and let fresh pods appear (not Ready).
		Eventually(func() error {
			list := fcListPods(rbgName, roleName)
			for i := range list.Items {
				p := &list.Items[i]
				if p.DeletionTimestamp == nil {
					continue
				}
				if err := testutil.K8sClient.Delete(testutil.Ctx, p, client.GracePeriodSeconds(0)); err != nil {
					return err
				}
			}
			return nil
		}, fcTimeout, fcInterval).Should(Succeed())
		fcWaitActivePods(rbgName, roleName, 2)

		// CANARY: a further member loss (container crash on a fresh pod) while the
		// instance is still recovering (Restarting=True, not Ready) does NOT
		// trigger a second rebuild.
		fcSetPodsRunningNotReady(rbgName, roleName)
		freshList := fcListPods(rbgName, roleName)
		fcCrashContainer(rbgName, roleName, freshList.Items[0].Name, 1)
		Consistently(func() int32 {
			ri := fcGetRoleInstance(rbgName, roleName)
			if ri == nil {
				return -1
			}
			return ri.Status.RestartCount
		}, 15*time.Second, 500*time.Millisecond).Should(Equal(int32(1)),
			"mid-recovery member loss should be suppressed by the Restarting guard (current behavior)")

		// CONTRACT: after the instance becomes Ready (Restarting cleared), the
		// standing crash trigger fires and a second rebuild happens. The
		// Restarting condition is only cleared by the Ready transition, and the
		// backoff (baseDelay=1s) is long elapsed, so restarts reaching 2 proves
		// the guard was released through Ready rather than by time.
		fcMakePodsReady(rbgName, roleName)
		fcWaitRestartCount(rbgName, roleName, 2)

		// Recovery completes after the second rebuild.
		fcMakePodsReady(rbgName, roleName)
		fcWaitInstanceReady(rbgName, roleName)
	})

	// Scenario D (CONTRACT — flipped from an initially suspected canary): a member
	// pod force-deleted (grace 0) still triggers the whole-instance rebuild. The
	// reviewer's initial hypothesis was that a force-deleted pod vanishes without
	// an observable termination window and the trigger would be missed; envtest
	// REFUTED that: the API server still exposes the pod with DeletionTimestamp
	// (MODIFIED watch event) before removal, and the event-driven reconcile
	// observes it. Kept as a regression test for that observation path.
	It("D: force-deleted member still triggers a whole-instance rebuild", func() {
		rbgName := "fc-d"
		roleName := "role-1"
		Expect(testutil.K8sClient.Create(testutil.Ctx, fcNewGroup(rbgName))).Should(Succeed())

		fcWaitActivePods(rbgName, roleName, 2)
		fcBindPodsToNode(rbgName, roleName, "fc-node-d")
		fcMakePodsReady(rbgName, roleName)
		fcWaitInstanceReady(rbgName, roleName)

		podList := fcListPods(rbgName, roleName)
		Expect(podList.Items).Should(HaveLen(2))
		siblingName := podList.Items[1].Name
		siblingUID := podList.Items[1].UID

		// Force delete: the object disappears without a lingering Terminating window.
		Expect(testutil.K8sClient.Delete(testutil.Ctx, &podList.Items[0], client.GracePeriodSeconds(0))).Should(Succeed())

		// The whole-instance rebuild still triggers.
		fcWaitRestartCount(rbgName, roleName, 1)
		Eventually(func() bool { return fcIsRestarting(rbgName, roleName) }, fcTimeout, fcInterval).Should(BeTrue())

		// The sibling is torn down as part of the group rebuild.
		Eventually(func() bool {
			fresh := &corev1.Pod{}
			if err := testutil.K8sClient.Get(testutil.Ctx, client.ObjectKey{Namespace: fcNs, Name: siblingName}, fresh); err != nil {
				return false // gone is fine
			}
			return fresh.UID != siblingUID || fresh.DeletionTimestamp != nil
		}, fcTimeout, fcInterval).Should(BeTrue(), "sibling should be torn down during the group rebuild")
	})
})
