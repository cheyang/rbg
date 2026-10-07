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

package warmupverify

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

const (
	verifyTimeout  = 60 * time.Second
	verifyInterval = 500 * time.Millisecond
)

func newCustomizedWarmup(name, ns, containerName string) *workloadsv1alpha2.RoleBasedGroupWarmup {
	return &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: workloadsv1alpha2.RoleBasedGroupWarmupSpec{
			Policies: &workloadsv1alpha2.WarmupPolicies{
				BackoffLimitPerNode: ptr.To(int32(0)),
				MaxFailedNodes:      ptr.To(int32(0)),
			},
			TargetNodes: &workloadsv1alpha2.TargetNodes{
				NodeNames: []string{"node-1"},
				WarmupActions: workloadsv1alpha2.WarmupActions{CustomizedAction: &workloadsv1alpha2.CustomizedAction{
					Containers: []corev1.Container{{
						Name:    containerName,
						Image:   "busybox",
						Command: []string{"sh", "-c", "exit 0"},
					}},
				}},
			},
		},
	}
}

// waitForWarmupPod waits for the controller to create the warmup pod and
// returns it. The pod carries the controller-written mapping annotation.
func waitForWarmupPod(warmup *workloadsv1alpha2.RoleBasedGroupWarmup) *corev1.Pod {
	podList := &corev1.PodList{}
	Eventually(func() int {
		if err := k8sClient.List(ctx, podList,
			client.InNamespace(warmup.Namespace),
			client.MatchingLabels{"workloads.x-k8s.io/warmup-name": warmup.Name}); err != nil {
			return 0
		}
		return len(podList.Items)
	}, verifyTimeout, verifyInterval).Should(Equal(1))
	pod := &podList.Items[0]
	Expect(pod.Annotations).To(HaveKey("workloads.x-k8s.io/customized-action-containers"))
	return pod
}

func setPodStatus(pod *corev1.Pod, phase corev1.PodPhase, containerState corev1.ContainerState) {
	Eventually(func() error {
		latest := &corev1.Pod{}
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(pod), latest); err != nil {
			return err
		}
		latest.Status.Phase = phase
		latest.Status.ContainerStatuses = []corev1.ContainerStatus{{
			Name:  "custom-0",
			State: containerState,
		}}
		return k8sClient.Status().Update(ctx, latest)
	}, verifyTimeout, verifyInterval).Should(Succeed())
}

func getWarmup(warmup *workloadsv1alpha2.RoleBasedGroupWarmup) *workloadsv1alpha2.RoleBasedGroupWarmup {
	latest := &workloadsv1alpha2.RoleBasedGroupWarmup{}
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(warmup), latest)).To(Succeed())
	return latest
}

var _ = Describe("PR488 customized action status verification", func() {

	// F1 contract: a successful customized action must expose its per-container
	// result (this is exactly what the PR's own e2e asserts at
	// test/e2e/testcase/v1alpha2/warmup.go). FAILS on the head under review.
	It("F1: successful customized action keeps per-container results [contract]", func() {
		ns := newVerifyNamespace("verify-f1-contract")
		warmup := newCustomizedWarmup("warmup-f1", ns, "node-check")
		Expect(k8sClient.Create(ctx, warmup)).To(Succeed())

		pod := waitForWarmupPod(warmup)
		setPodStatus(pod, corev1.PodSucceeded, corev1.ContainerState{
			Terminated: &corev1.ContainerStateTerminated{ExitCode: 0, Reason: "Completed"},
		})

		Eventually(func() workloadsv1alpha2.WarmupJobPhase {
			return getWarmup(warmup).Status.Phase
		}, verifyTimeout, verifyInterval).Should(Equal(workloadsv1alpha2.WarmupJobPhaseCompleted))

		latest := getWarmup(warmup)
		Expect(latest.Status.CustomizedActionResults).To(HaveLen(1))
		result := latest.Status.CustomizedActionResults[0]
		Expect(result.State).To(Equal(workloadsv1alpha2.CustomizedActionStateSucceeded))
		Expect(result.Containers).To(HaveLen(1), "F1: successful result must keep per-container detail")
		Expect(result.Containers[0].ContainerName).To(Equal("node-check"))
		Expect(result.Containers[0].ExitCode).NotTo(BeNil())
	})

	// F1 canary: pins the current false-positive truncation signal on a tiny
	// all-success status. PASSES on the head under review; flips once fixed.
	It("F1: tiny successful status is flagged truncated [canary]", func() {
		ns := newVerifyNamespace("verify-f1-canary")
		warmup := newCustomizedWarmup("warmup-f1-canary", ns, "node-check")
		Expect(k8sClient.Create(ctx, warmup)).To(Succeed())

		pod := waitForWarmupPod(warmup)
		setPodStatus(pod, corev1.PodSucceeded, corev1.ContainerState{
			Terminated: &corev1.ContainerStateTerminated{ExitCode: 0, Reason: "Completed"},
		})

		Eventually(func() workloadsv1alpha2.WarmupJobPhase {
			return getWarmup(warmup).Status.Phase
		}, verifyTimeout, verifyInterval).Should(Equal(workloadsv1alpha2.WarmupJobPhaseCompleted))

		latest := getWarmup(warmup)
		Expect(latest.Status.CustomizedActionResultsTruncated).To(BeTrue(),
			"canary flipped (no false truncation signal) - invert this test, F1 is fixed")
	})

	// Control case: failures DO keep per-container detail and drive the
	// CustomizedActionFailed condition. Expected to PASS on the head under
	// review, proving the loss in F1 is specific to the success path.
	It("control: failed customized action keeps exit code and sets failure condition [contract]", func() {
		ns := newVerifyNamespace("verify-control")
		warmup := newCustomizedWarmup("warmup-control", ns, "node-check")
		Expect(k8sClient.Create(ctx, warmup)).To(Succeed())

		pod := waitForWarmupPod(warmup)
		setPodStatus(pod, corev1.PodFailed, corev1.ContainerState{
			Terminated: &corev1.ContainerStateTerminated{ExitCode: 1, Reason: "Error", Message: "check failed"},
		})

		Eventually(func() string {
			latest := getWarmup(warmup)
			if len(latest.Status.CustomizedActionResults) == 0 {
				return ""
			}
			return latest.Status.CustomizedActionResults[0].Reason
		}, verifyTimeout, verifyInterval).Should(Equal("ContainerExitCode"))

		latest := getWarmup(warmup)
		result := latest.Status.CustomizedActionResults[0]
		Expect(result.Containers).To(HaveLen(1))
		Expect(result.Containers[0].ExitCode).NotTo(BeNil())
		Expect(*result.Containers[0].ExitCode).To(Equal(int32(1)))
		condition := apimeta.FindStatusCondition(latest.Status.Conditions, "CustomizedActionFailed")
		Expect(condition).NotTo(BeNil())
		Expect(condition.Status).To(Equal(metav1.ConditionTrue))
	})

	// F2 refutation: a waiting image-pull failure keeps container identity and
	// the waiting reason in the stored (bounded) status. Expected to PASS on
	// the head under review.
	It("F2: image-pull waiting failure keeps container identity and reason [contract]", func() {
		ns := newVerifyNamespace("verify-f2")
		warmup := newCustomizedWarmup("warmup-f2", ns, "pull-check")
		Expect(k8sClient.Create(ctx, warmup)).To(Succeed())

		pod := waitForWarmupPod(warmup)
		setPodStatus(pod, corev1.PodPending, corev1.ContainerState{
			Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff", Message: `Back-off pulling image "busybox"`},
		})

		Eventually(func() string {
			latest := getWarmup(warmup)
			if len(latest.Status.CustomizedActionResults) == 0 {
				return ""
			}
			return latest.Status.CustomizedActionResults[0].Reason
		}, verifyTimeout, verifyInterval).Should(Equal("ImagePullFailed"))

		latest := getWarmup(warmup)
		result := latest.Status.CustomizedActionResults[0]
		Expect(result.Containers).To(HaveLen(1), "F2: waiting failure must keep container identity")
		Expect(result.Containers[0].ContainerName).To(Equal("pull-check"))
		Expect(result.Containers[0].TerminationReason).To(Equal("ImagePullBackOff"))
		Expect(result.Containers[0].TerminationMessage).NotTo(BeEmpty())
	})
})
