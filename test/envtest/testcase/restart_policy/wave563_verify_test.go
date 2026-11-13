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

// Verification harness for PR #573 (premise P0 / issue #563).
//
// Claim under test (issue #563): "When the restart policy recreates a
// RoleInstance, ... shouldRecreateInstanceGuarded drops every new trigger
// through isAlreadyRestarting ... If a member of the rebuilt group fails again
// before the instance is Ready ... the instance stays NotReady until the
// RoleInstance is deleted by hand."
//
// This spec is a contract test: it reproduces the #563 scenario (first
// recreation, rebuilt group fails again before Ready) and asserts the second
// recreation happens, bounded by backoff. It must FAIL on the base branch
// (premise: the bug exists today) and PASS with the fix. The helpers are
// duplicated as local closures so this file compiles unchanged on both.
//
// Run:
//   KUBEBUILDER_ASSETS="$(setup-envtest use 1.31.0 --bin-dir ... -p path)" \
//     go test ./test/envtest/testcase/restart_policy/ -run TestRestartPolicy \
//       -ginkgo.focus 'verify-563' -count=1 -v

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/test/envtest/testutil"
	wrappersv2 "sigs.k8s.io/rbgs/test/wrappers/v1alpha2"
)

var _ = Describe("verify-563: second pre-Ready failure must recreate again", func() {
	var testNs string

	BeforeEach(func() {
		testNs = fmt.Sprintf("test-verify563-%d", time.Now().UnixNano())
		testutil.CreateNamespace(testNs)
	})

	AfterEach(func() {
		testutil.DeleteNamespace(testNs)
	})

	listRolePods := func(rbgName, roleName string) *corev1.PodList {
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
	getPodUIDs := func(podList *corev1.PodList) map[string]types.UID {
		uids := make(map[string]types.UID)
		for i := range podList.Items {
			uids[podList.Items[i].Name] = podList.Items[i].UID
		}
		return uids
	}
	makePodsReady := func(rbgName, roleName string) {
		Eventually(func() error {
			podList := listRolePods(rbgName, roleName)
			for i := range podList.Items {
				pod := &podList.Items[i]
				if pod.Status.Phase == corev1.PodRunning {
					continue
				}
				freshPod := &corev1.Pod{}
				if err := testutil.K8sClient.Get(testutil.Ctx, client.ObjectKeyFromObject(pod), freshPod); err != nil {
					return err
				}
				testutil.SetPodRunningAndReady(freshPod)
				if err := testutil.K8sClient.Status().Update(testutil.Ctx, freshPod); err != nil {
					return err
				}
			}
			return nil
		}, timeout, interval).Should(Succeed())
	}
	waitForPods := func(rbgName, roleName string, count int) {
		Eventually(func() int {
			podList := listRolePods(rbgName, roleName)
			active := 0
			for i := range podList.Items {
				if podList.Items[i].DeletionTimestamp == nil &&
					podList.Items[i].Status.Phase != corev1.PodFailed &&
					podList.Items[i].Status.Phase != corev1.PodSucceeded {
					active++
				}
			}
			return active
		}, timeout, interval).Should(Equal(count))
	}
	getRoleInstance := func(rbgName, roleName string) *workloadsv1alpha2.RoleInstance {
		riList := &workloadsv1alpha2.RoleInstanceList{}
		ExpectWithOffset(1, testutil.K8sClient.List(testutil.Ctx, riList,
			client.InNamespace(testNs),
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
	updatePodStatus := func(key client.ObjectKey, mutate func(*corev1.Pod)) {
		ExpectWithOffset(1, retry.RetryOnConflict(retry.DefaultBackoff, func() error {
			pod := &corev1.Pod{}
			if err := testutil.K8sClient.Get(testutil.Ctx, key, pod); err != nil {
				return err
			}
			mutate(pod)
			return testutil.K8sClient.Status().Update(testutil.Ctx, pod)
		})).Should(Succeed())
	}
	waitForRoleInstanceReady := func(rbgName, roleName string) {
		Eventually(func() bool {
			ri := getRoleInstance(rbgName, roleName)
			if ri == nil {
				return false
			}
			for _, cond := range ri.Status.Conditions {
				if cond.Type == workloadsv1alpha2.RoleInstanceReady && cond.Status == corev1.ConditionTrue {
					return true
				}
			}
			return false
		}, timeout, interval).Should(BeTrue())
	}

	It("recreates again when the rebuilt group fails before Ready", func() {
		rbgName := "verify-563-second-failure"
		roleName := defaultRoleName

		rbg := wrappersv2.BuildBasicRoleBasedGroup(rbgName, testNs).WithRoles(
			[]workloadsv1alpha2.RoleSpec{
				wrappersv2.BuildLeaderWorkerRole(roleName).
					WithReplicas(1).
					WithSize(2).
					WithRestartPolicy(workloadsv1alpha2.RecreateRoleInstanceOnPodRestart).
					WithBaseDelaySeconds(1).
					WithMaxDelaySeconds(2).
					Obj(),
			}).Obj()

		Expect(testutil.K8sClient.Create(testutil.Ctx, rbg)).Should(Succeed())

		waitForPods(rbgName, roleName, 2)
		makePodsReady(rbgName, roleName)
		waitForRoleInstanceReady(rbgName, roleName)

		// First failure: trigger the first whole-instance recreation.
		podList := listRolePods(rbgName, roleName)
		firstGeneration := getPodUIDs(podList)
		freshPod := &corev1.Pod{}
		Expect(testutil.K8sClient.Get(testutil.Ctx,
			client.ObjectKeyFromObject(&podList.Items[0]), freshPod)).Should(Succeed())
		freshPod.Status.Phase = corev1.PodFailed
		Expect(testutil.K8sClient.Status().Update(testutil.Ctx, freshPod)).Should(Succeed())

		// The first wave completes: every pre-restart Pod is gone and both
		// replacements exist. The instance is intentionally NOT made Ready.
		// The completed wave must have released its guard persistently
		// (Restarting=False/RestartCompleted).
		Eventually(func(g Gomega) {
			for name, oldUID := range firstGeneration {
				pod := &corev1.Pod{}
				g.Expect(testutil.K8sClient.Get(testutil.Ctx,
					types.NamespacedName{Name: name, Namespace: testNs}, pod)).Should(Succeed())
				g.Expect(pod.UID).NotTo(BeEmpty())
				g.Expect(pod.UID).NotTo(Equal(oldUID))
				g.Expect(pod.DeletionTimestamp).To(BeNil())
			}
			ri := getRoleInstance(rbgName, roleName)
			g.Expect(ri).NotTo(BeNil())
			g.Expect(ri.Status.RestartCount).To(Equal(int32(1)))
			ready := false
			var restartingCond *workloadsv1alpha2.RoleInstanceCondition
			for _, cond := range ri.Status.Conditions {
				if cond.Type == workloadsv1alpha2.RoleInstanceReady && cond.Status == corev1.ConditionTrue {
					ready = true
				}
				if cond.Type == workloadsv1alpha2.RoleInstanceRestarting {
					restartingCond = &cond
				}
			}
			g.Expect(ready).To(BeFalse(),
				"the second failure must be injected before the rebuilt group becomes Ready")
			g.Expect(restartingCond).NotTo(BeNil(),
				"the completed restart wave must persist a Restarting condition")
			if restartingCond != nil {
				g.Expect(restartingCond.Status).To(Equal(corev1.ConditionFalse),
					"the completed wave must persist Restarting=False")
				g.Expect(restartingCond.Reason).To(Equal("RestartCompleted"),
					"the completed wave must persist the RestartCompleted reason")
			}
		}, timeout, interval).Should(Succeed(),
			"the first recreation must finish before the second fault is injected")

		// Second failure of the rebuilt (not yet Ready) group. On the base
		// branch the Restarting guard stays armed until Ready and swallows this
		// trigger forever (#563); the fix must start a second recreation.
		secondGeneration := getPodUIDs(listRolePods(rbgName, roleName))
		updatePodStatus(client.ObjectKeyFromObject(&podList.Items[0]), func(pod *corev1.Pod) {
			if len(pod.Status.ContainerStatuses) == 0 {
				pod.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: "main"}}
			}
			pod.Status.ContainerStatuses[0].RestartCount = 1
		})

		// Backoff for the second recreation is baseDelay * 2^(RestartCount-1) = 1s,
		// measured from the first recreation. The second wave must eventually run.
		Eventually(func(g Gomega) {
			for name, oldUID := range secondGeneration {
				pod := &corev1.Pod{}
				g.Expect(testutil.K8sClient.Get(testutil.Ctx,
					types.NamespacedName{Name: name, Namespace: testNs}, pod)).Should(Succeed())
				g.Expect(pod.UID).NotTo(BeEmpty())
				g.Expect(pod.UID).NotTo(Equal(oldUID))
				g.Expect(pod.DeletionTimestamp).To(BeNil())
			}
			ri := getRoleInstance(rbgName, roleName)
			g.Expect(ri).NotTo(BeNil())
			g.Expect(ri.Status.RestartCount).To(Equal(int32(2)))
		}, timeout, interval).Should(Succeed(),
			"a pre-Ready failure of the rebuilt group must trigger a second recreation (#563)")
	})
})
