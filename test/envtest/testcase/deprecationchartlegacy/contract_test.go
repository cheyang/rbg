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

package deprecationchartlegacy

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	workloadsv1alpha1 "sigs.k8s.io/rbgs/api/workloads/v1alpha1"
	"sigs.k8s.io/rbgs/test/envtest/testutil"
	wrappersv1 "sigs.k8s.io/rbgs/test/wrappers/v1alpha1"
)

// Mechanism witness for F1 (canary polarity: these specs PASS while the
// chart-renders-paths-the-default-image-does-not-serve state is in effect).
// They do not flip when the image is bumped; the deployment-state tracker is
// TestChartDefaultImageServesRenderedWebhookPaths in test/chart. What these
// specs prove is the causal chain: unregistered path -> 404 -> admission
// error -> failurePolicy applied.
var _ = Describe("chart webhooks against a pre-gate binary", Ordered, func() {
	BeforeEach(func() {
		testutil.ContractWarnings.Reset()
	})

	It("fail-closes v1alpha1 RoleBasedGroup creates through the Fail-policy webhook", func() {
		rbg := wrappersv1.BuildBasicRoleBasedGroup("legacy-create", testutil.ChartContractNamespace).
			WithRoles([]workloadsv1alpha1.RoleSpec{
				wrappersv1.BuildBasicRole("worker").Obj(),
			}).Obj()
		err := testutil.K8sClient.Create(testutil.Ctx, rbg)
		// A RoleInstanceSet role is exactly what the compatibility window
		// should keep accepting — but the deprecation webhook itself cannot be
		// reached, so the write dies in admission regardless of content.
		Expect(err).To(HaveOccurred())
		By("observed admission error: " + err.Error())
		Expect(err.Error()).To(ContainSubstring("vrolebasedgroup-v1alpha1-deprecation.kb.io"))
	})

	It("fail-closes v1alpha1 RoleBasedGroupSet creates through the Fail-policy webhook", func() {
		set := wrappersv1.BuildBasicRoleBasedGroupSet("legacy-set", testutil.ChartContractNamespace).Obj()
		err := testutil.K8sClient.Create(testutil.Ctx, set)
		Expect(err).To(HaveOccurred())
		By("observed admission error: " + err.Error())
		Expect(err.Error()).To(ContainSubstring("vrolebasedgroupset-v1alpha1-deprecation.kb.io"))
	})

	It("absorbs the unreachable warning-only endpoints (failurePolicy=Ignore)", func() {
		// The four warning-only kinds carry failurePolicy=Ignore, so an
		// unreachable endpoint costs only the warning, not the write. This is
		// the asymmetry the PR body documents as the reason for the split.
		instanceSet := &workloadsv1alpha1.InstanceSet{
			ObjectMeta: metav1.ObjectMeta{Name: "legacy-instanceset", Namespace: testutil.ChartContractNamespace},
			Spec: workloadsv1alpha1.InstanceSetSpec{
				Replicas: ptr.To(int32(1)),
				InstanceTemplate: workloadsv1alpha1.InstanceTemplate{
					InstanceSpec: workloadsv1alpha1.InstanceSpec{
						Components: []workloadsv1alpha1.InstanceComponent{{
							Name:     "worker",
							Size:     ptr.To(int32(1)),
							Template: corev1.PodTemplateSpec{
								Spec: corev1.PodSpec{
									Containers: []corev1.Container{{Name: "nginx", Image: "nginx:latest"}},
								},
							},
						}},
					},
				},
			},
		}
		Expect(testutil.K8sClient.Create(testutil.Ctx, instanceSet)).To(Succeed(),
			"an Ignore-policy endpoint must not reject the write even when unreachable")
	})
})
