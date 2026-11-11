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

// Verification harness for PR #559 review (Reviewer B / Codex) — integration
// layer (envtest, real API server + real controllers). Additive only.
//
//	TL1 (contract): pre-Ready Failed pod triggers whole-instance recreation
//	                under RecreateRoleInstanceOnPodRestartFromCreation.
//	TL2 (contract): an observed desired-member deletion triggers whole-instance
//	                recreation under FromCreation (no Failed pod and no container
//	                restart exist at that moment, so only the deletion trigger
//	                can have fired).
//	TL2c (control): under the LEGACY policy the same member deletion pre-Ready
//	                only replaces the deleted pod — no whole-group recreation.
//	TL3 (contract): a SECOND pre-Ready failure must trigger another
//	                whole-instance recreation. FAILS on the PR head: the
//	                Restarting state is cleared only once the instance becomes
//	                Ready, so isAlreadyRestarting suppresses every subsequent
//	                pre-Ready recreation (finding F1).
//
// envtest notes:
//   - There is no kubelet; the API server removes deleted pods almost
//     immediately, so a UID change is the reliable recreation observable. Do
//     NOT assert on the Restarting condition here: updateStatus carries only
//     non-owned conditions forward from the live object, so a reconcile
//     computed from a stale informer cache can drop the freshly persisted
//     Restarting condition within a second (observed during harness bring-up).
//   - Pods are never made Ready: the instance stays pre-Ready throughout.

package restart_policy

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/test/envtest/testutil"
	wrappersv2 "sigs.k8s.io/rbgs/test/wrappers/v1alpha2"
)

var _ = Describe("PR559 FromCreation verification", func() {
	var testNs string

	BeforeEach(func() {
		testNs = fmt.Sprintf("test-v559-%d", time.Now().UnixNano())
		testutil.CreateNamespace(testNs)
	})

	AfterEach(func() {
		testutil.DeleteNamespace(testNs)
	})

	listPods := func(rbgName, roleName string) *corev1.PodList {
		podList := &corev1.PodList{}
		ExpectWithOffset(1, testutil.K8sClient.List(testutil.Ctx, podList,
			client.InNamespace(testNs),
			client.MatchingLabels{
				constants.GroupNameLabelKey: rbgName,
				constants.RoleNameLabelKey:  roleName,
			},
		)).Should(Succeed())
		return podList
	}

	activePods := func(podList *corev1.PodList) []*corev1.Pod {
		var active []*corev1.Pod
		for i := range podList.Items {
			p := &podList.Items[i]
			if p.DeletionTimestamp != nil ||
				p.Status.Phase == corev1.PodFailed || p.Status.Phase == corev1.PodSucceeded {
				continue
			}
			active = append(active, p)
		}
		return active
	}

	podUIDs := func(podList *corev1.PodList) map[string]types.UID {
		uids := map[string]types.UID{}
		for i := range podList.Items {
			uids[podList.Items[i].Name] = podList.Items[i].UID
		}
		return uids
	}

	findByComponent := func(podList *corev1.PodList, component string) *corev1.Pod {
		for i := range podList.Items {
			if podList.Items[i].Labels[constants.ComponentNameLabelKey] == component {
				return &podList.Items[i]
			}
		}
		return nil
	}

	failPod := func(pod *corev1.Pod) {
		fresh := &corev1.Pod{}
		ExpectWithOffset(1, testutil.K8sClient.Get(testutil.Ctx, client.ObjectKeyFromObject(pod), fresh)).Should(Succeed())
		fresh.Status.Phase = corev1.PodFailed
		fresh.Status.Reason = "Error"
		fresh.Status.Message = "verify559: injected failure"
		ExpectWithOffset(1, testutil.K8sClient.Status().Update(testutil.Ctx, fresh)).Should(Succeed())
	}

	waitForActivePods := func(rbgName, roleName string, count int) {
		EventuallyWithOffset(1, func() int {
			return len(activePods(listPods(rbgName, roleName)))
		}, timeout, interval).Should(Equal(count))
	}

	// allReplaced reports whether exactly `count` active pods exist and none of
	// them carries a UID from `prev` (i.e. the whole group was recreated).
	allReplaced := func(rbgName, roleName string, prev map[string]types.UID, count int) bool {
		pl := listPods(rbgName, roleName)
		if len(activePods(pl)) != count {
			return false
		}
		for name, uid := range podUIDs(pl) {
			if old, ok := prev[name]; ok && old == uid {
				return false
			}
		}
		return true
	}

	createRBG := func(rbgName, roleName string, policy workloadsv1alpha2.RestartPolicyType) {
		rbg := wrappersv2.BuildBasicRoleBasedGroup(rbgName, testNs).WithRoles(
			[]workloadsv1alpha2.RoleSpec{
				wrappersv2.BuildLeaderWorkerRole(roleName).
					WithReplicas(1).
					WithSize(2).
					WithRestartPolicy(policy).
					WithBaseDelaySeconds(0).
					WithMaxDelaySeconds(0).
					Obj(),
			}).Obj()
		ExpectWithOffset(1, testutil.K8sClient.Create(testutil.Ctx, rbg)).Should(Succeed())
	}

	It("verify559 TL1: FromCreation recreates pre-Ready instance on Failed pod", func() {
		rbgName, roleName := "v559-tl1", defaultRoleName
		createRBG(rbgName, roleName, workloadsv1alpha2.RecreateRoleInstanceOnPodRestartFromCreation)
		waitForActivePods(rbgName, roleName, 2)
		// NOTE: pods are never made Ready — the instance is pre-Ready.

		gen1 := podUIDs(listPods(rbgName, roleName))
		Expect(gen1).Should(HaveLen(2))
		worker := findByComponent(listPods(rbgName, roleName), "worker")
		Expect(worker).NotTo(BeNil())
		failPod(worker)

		Eventually(func() bool {
			return allReplaced(rbgName, roleName, gen1, 2)
		}, timeout, interval).Should(BeTrue(),
			"pre-Ready Failed pod must trigger whole-instance recreation under FromCreation")
	})

	It("verify559 TL2: FromCreation recreates pre-Ready instance on observed member deletion", func() {
		rbgName, roleName := "v559-tl2", defaultRoleName
		createRBG(rbgName, roleName, workloadsv1alpha2.RecreateRoleInstanceOnPodRestartFromCreation)
		waitForActivePods(rbgName, roleName, 2)

		gen1 := podUIDs(listPods(rbgName, roleName))
		Expect(gen1).Should(HaveLen(2))
		worker := findByComponent(listPods(rbgName, roleName), "worker")
		Expect(worker).NotTo(BeNil())
		// Default grace period: the delete first persists a Terminating pod,
		// which is exactly what the controller observes after an eviction on a
		// partitioned node. No pod is Failed and no container has restarted at
		// this point, so only the new deletion trigger can fire.
		Expect(testutil.K8sClient.Delete(testutil.Ctx, worker)).Should(Succeed())

		Eventually(func() bool {
			return allReplaced(rbgName, roleName, gen1, 2)
		}, timeout, interval).Should(BeTrue(),
			"observed desired-member deletion must trigger whole-instance recreation under FromCreation")
	})

	It("verify559 TL2c: legacy policy does NOT recreate pre-Ready instance on member deletion", func() {
		rbgName, roleName := "v559-tl2c", defaultRoleName
		createRBG(rbgName, roleName, workloadsv1alpha2.RecreateRoleInstanceOnPodRestart)
		waitForActivePods(rbgName, roleName, 2)

		gen1 := podUIDs(listPods(rbgName, roleName))
		Expect(gen1).Should(HaveLen(2))
		leader := findByComponent(listPods(rbgName, roleName), "leader")
		Expect(leader).NotTo(BeNil())
		worker := findByComponent(listPods(rbgName, roleName), "worker")
		Expect(worker).NotTo(BeNil())
		Expect(testutil.K8sClient.Delete(testutil.Ctx, worker)).Should(Succeed())

		// The deleted worker is replaced individually; the leader must survive.
		// This is the documented invariant "legacy policy unchanged".
		Eventually(func() bool {
			w := findByComponent(listPods(rbgName, roleName), "worker")
			return w != nil && w.UID != gen1[worker.Name] && w.DeletionTimestamp == nil
		}, timeout, interval).Should(BeTrue(), "deleted member should be replaced individually")
		Consistently(func() types.UID {
			l := findByComponent(listPods(rbgName, roleName), "leader")
			if l == nil {
				return ""
			}
			return l.UID
		}, 15*time.Second, interval).Should(Equal(gen1[leader.Name]),
			"legacy policy must not recreate the instance on member deletion")
	})

	It("verify559 TL3: FromCreation recreates again on a second pre-Ready failure", func() {
		rbgName, roleName := "v559-tl3", defaultRoleName
		createRBG(rbgName, roleName, workloadsv1alpha2.RecreateRoleInstanceOnPodRestartFromCreation)
		waitForActivePods(rbgName, roleName, 2)
		gen1 := podUIDs(listPods(rbgName, roleName))
		Expect(gen1).Should(HaveLen(2))

		// --- failure 1 (pre-Ready): recreation 1 fires and completes ---
		failPod(findByComponent(listPods(rbgName, roleName), "worker"))
		Eventually(func() bool {
			return allReplaced(rbgName, roleName, gen1, 2)
		}, timeout, interval).Should(BeTrue(), "first pre-Ready failure should trigger recreation")

		gen2 := podUIDs(listPods(rbgName, roleName))
		worker2 := findByComponent(listPods(rbgName, roleName), "worker")
		Expect(worker2).NotTo(BeNil())
		leader2 := findByComponent(listPods(rbgName, roleName), "leader")
		Expect(leader2).NotTo(BeNil())

		// --- failure 2 (still pre-Ready): recreation 2 must fire ---
		failPod(worker2)

		// Liveness: the failed worker is at least replaced individually
		// (inactive-pod cleanup), proving the controller keeps processing this
		// instance while the recreation is suppressed.
		Eventually(func() bool {
			w := findByComponent(listPods(rbgName, roleName), "worker")
			return w != nil && w.UID != gen2[worker2.Name] && w.DeletionTimestamp == nil
		}, timeout, interval).Should(BeTrue(),
			"failed gen-2 worker should at least be replaced individually")

		// CONTRACT: whole-group recreation must fire again — the healthy gen-2
		// leader must be replaced too. On the PR head this never happens: the
		// Restarting state is cleared only on Ready, so isAlreadyRestarting
		// suppresses every subsequent pre-Ready recreation.
		Eventually(func() bool {
			l := findByComponent(listPods(rbgName, roleName), "leader")
			return l == nil || l.UID != gen2[leader2.Name] || l.DeletionTimestamp != nil
		}, 30*time.Second, interval).Should(BeTrue(),
			"F1: second pre-Ready failure must trigger another whole-instance recreation")
	})
})
