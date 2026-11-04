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

package deprecationv1a1helm

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	workloadsv1alpha1 "sigs.k8s.io/rbgs/api/workloads/v1alpha1"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/test/envtest/testutil"
)

// These specs pin the behavior the PR #552 chart change must deliver: the
// Helm-rendered webhook configuration — and nothing hand-written — gates
// v1alpha1 writes exactly like the shipped config/webhook/manifests.yaml.
// Polarity: CONTRACT. On the base chart (premise mode) the gate specs fail,
// which is the premise evidence.
var _ = Describe("v1alpha1 deprecation gate via the Helm chart render", Ordered, func() {
	BeforeEach(func() {
		if suiteMode() == "premise" {
			Skip("premise mode only asserts the absence of the gate on the base chart")
		}
		testutil.ContractWarnings.Reset()
	})

	It("rejects a new deprecated workload use through the v1alpha1 API", func() {
		rbg := buildV1alpha1RBG("chart-reject-explicit")
		rbg.Spec.Roles[0].Workload = workloadsv1alpha1.WorkloadSpec{
			APIVersion: "apps/v1",
			Kind:       "StatefulSet",
		}
		err := testutil.K8sClient.Create(testutil.Ctx, rbg)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("new uses are not accepted"))
	})

	It("rejects a new v1alpha1 object that relies on the schema-defaulted workload", func() {
		// v1alpha1 defaults spec.roles[].workload to apps/v1 StatefulSet, so a
		// manifest that never names a workload is still a new deprecated use.
		// The typed client's Workload field is a value struct that always
		// serializes, so build the object unstructured with the key absent —
		// exactly what a kubectl user without a workload line submits.
		rbg := map[string]interface{}{
			"apiVersion": "workloads.x-k8s.io/v1alpha1",
			"kind":       "RoleBasedGroup",
			"metadata":   map[string]interface{}{"name": "chart-reject-defaulted", "namespace": helmContractNamespace},
			"spec": map[string]interface{}{
				"roles": []interface{}{
					map[string]interface{}{
						"name":     "worker",
						"replicas": 1,
						"template": map[string]interface{}{
							"spec": map[string]interface{}{
								"containers": []interface{}{
									map[string]interface{}{"name": "nginx", "image": "nginx:latest"},
								},
							},
						},
					},
				},
			},
		}
		obj := &unstructured.Unstructured{Object: rbg}
		err := testutil.K8sClient.Create(testutil.Ctx, obj)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("new uses are not accepted"))
	})

	It("returns the on-the-wire deprecation warning for v1alpha1 creates", func() {
		rbg := buildV1alpha1RBG("chart-warn-create")
		rbg.Spec.Roles[0].Workload = workloadsv1alpha1.WorkloadSpec{
			APIVersion: "workloads.x-k8s.io/v1alpha2",
			Kind:       "RoleInstanceSet",
		}
		Expect(testutil.K8sClient.Create(testutil.Ctx, rbg)).To(Succeed())
		Expect(testutil.ContractWarnings.Containing("v1alpha1 is deprecated")).NotTo(BeEmpty())
		Expect(testutil.ContractWarnings.Containing("removed in 0.10.0")).NotTo(BeEmpty())
	})

	It("returns the on-the-wire warning for every warning-only kind", func() {
		// failurePolicy is Ignore for these four entries: a path typo in the
		// chart render fails silently unless each warning is asserted.
		basicTemplate := corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "nginx", Image: "nginx:latest"}},
			},
		}
		warningOnlyObjects := []struct {
			kind string
			obj  client.Object
		}{
			{
				kind: "InstanceSet",
				obj: &workloadsv1alpha1.InstanceSet{
					ObjectMeta: metav1.ObjectMeta{Name: "chart-instanceset", Namespace: helmContractNamespace},
					Spec: workloadsv1alpha1.InstanceSetSpec{
						Replicas: ptr.To(int32(1)),
						InstanceTemplate: workloadsv1alpha1.InstanceTemplate{
							InstanceSpec: workloadsv1alpha1.InstanceSpec{
								Components: []workloadsv1alpha1.InstanceComponent{{
									Name:     "worker",
									Size:     ptr.To(int32(1)),
									Template: basicTemplate,
								}},
							},
						},
					},
				},
			},
			{
				kind: "Instance",
				obj: &workloadsv1alpha1.Instance{
					ObjectMeta: metav1.ObjectMeta{Name: "chart-instance", Namespace: helmContractNamespace},
					Spec: workloadsv1alpha1.InstanceSpec{
						Components: []workloadsv1alpha1.InstanceComponent{{
							Name:     "worker",
							Size:     ptr.To(int32(1)),
							Template: basicTemplate,
						}},
					},
				},
			},
			{
				kind: "RoleBasedGroupScalingAdapter",
				obj: &workloadsv1alpha1.RoleBasedGroupScalingAdapter{
					ObjectMeta: metav1.ObjectMeta{Name: "chart-adapter", Namespace: helmContractNamespace},
					Spec: workloadsv1alpha1.RoleBasedGroupScalingAdapterSpec{
						Replicas: ptr.To(int32(1)),
						ScaleTargetRef: &workloadsv1alpha1.AdapterScaleTargetRef{
							Name: "chart-rbg",
							Role: "worker",
						},
					},
				},
			},
			{
				kind: "ClusterEngineRuntimeProfile",
				obj: &workloadsv1alpha1.ClusterEngineRuntimeProfile{
					ObjectMeta: metav1.ObjectMeta{Name: "chart-runtime-profile"},
					Spec: workloadsv1alpha1.ClusterEngineRuntimeProfileSpec{
						UpdateStrategy: "NoUpdate",
					},
				},
			},
		}

		for _, entry := range warningOnlyObjects {
			testutil.ContractWarnings.Reset()
			Expect(testutil.K8sClient.Create(testutil.Ctx, entry.obj)).To(Succeed())
			Expect(testutil.ContractWarnings.Containing("v1alpha1 is deprecated")).NotTo(BeEmpty(),
				"warning-only kind %s must warn through the chart-rendered webhook entry", entry.kind)
		}
	})

	It("keeps the seeded pre-gate object writable under type-exact grandfathering", func() {
		stored := &workloadsv1alpha1.RoleBasedGroup{}
		Expect(testutil.K8sClient.Get(testutil.Ctx,
			types.NamespacedName{Name: seededRBGName, Namespace: helmContractNamespace}, stored)).To(Succeed())

		testutil.ContractWarnings.Reset()
		stored.Spec.Roles[0].Replicas = ptr.To(int32(2))
		Expect(testutil.K8sClient.Update(testutil.Ctx, stored)).To(Succeed(),
			"an existing role keeping its exact deprecated type must stay writable")
		Expect(testutil.ContractWarnings.Containing("v1alpha1 is deprecated")).NotTo(BeEmpty())
	})

	It("rejects switching the seeded role between deprecated workload types", func() {
		stored := &workloadsv1alpha1.RoleBasedGroup{}
		Expect(testutil.K8sClient.Get(testutil.Ctx,
			types.NamespacedName{Name: seededRBGName, Namespace: helmContractNamespace}, stored)).To(Succeed())

		stored.Spec.Roles[0].Workload = workloadsv1alpha1.WorkloadSpec{
			APIVersion: "apps/v1",
			Kind:       "Deployment",
		}
		err := testutil.K8sClient.Update(testutil.Ctx, stored)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("new uses are not accepted"))
	})

	It("does not warn on v1alpha2 writes through the Exact match policy", func() {
		rbg := &workloadsv1alpha2.RoleBasedGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "chart-v1a2", Namespace: helmContractNamespace},
			Spec: workloadsv1alpha2.RoleBasedGroupSpec{
				Roles: []workloadsv1alpha2.RoleSpec{{
					Name:     "worker",
					Replicas: ptr.To(int32(1)),
					Pattern: workloadsv1alpha2.Pattern{
						StandalonePattern: &workloadsv1alpha2.StandalonePattern{
							TemplateSource: workloadsv1alpha2.TemplateSource{
								Template: ptr.To(corev1.PodTemplateSpec{
									Spec: corev1.PodSpec{
										Containers: []corev1.Container{{Name: "nginx", Image: "nginx:latest"}},
									},
								}),
							},
						},
					},
				}},
			},
		}
		Expect(testutil.K8sClient.Create(testutil.Ctx, rbg)).To(Succeed())
		Expect(testutil.ContractWarnings.All()).To(BeEmpty(),
			"a v1alpha2 write must not route to the chart-rendered v1alpha1 Exact-match webhooks")
	})

	It("leaves v1alpha1 deletes ungated by the chart-rendered webhooks", func() {
		rbg := buildV1alpha1RBG("chart-delete")
		rbg.Spec.Roles[0].Workload = workloadsv1alpha1.WorkloadSpec{
			APIVersion: "workloads.x-k8s.io/v1alpha2",
			Kind:       "RoleInstanceSet",
		}
		Expect(testutil.K8sClient.Create(testutil.Ctx, rbg)).To(Succeed())

		stored := &workloadsv1alpha1.RoleBasedGroup{}
		Expect(testutil.K8sClient.Get(testutil.Ctx,
			types.NamespacedName{Name: rbg.Name, Namespace: helmContractNamespace}, stored)).To(Succeed())
		Expect(testutil.K8sClient.Delete(testutil.Ctx, stored)).To(Succeed())
	})
})

// The premise specs run only in premise mode (base chart): they record positive
// evidence of the intermediate state PR #552 closes — the base chart ships no
// v1alpha1 webhook entries, so no v1alpha1 deprecation warning ever reaches the
// user. The absence check also runs in BeforeSuite; these specs pin the two
// halves of the delta: the warning plane is missing, while RBG/RBGSet new-use
// enforcement was already present through the Equivalent-matching v1alpha2
// validators.
var _ = Describe("premise: the base chart ships no v1alpha1 deprecation plane", Ordered, func() {
	BeforeEach(func() {
		if suiteMode() != "premise" {
			Skip("premise evidence only; run with RBGS_HELM_SUITE_MODE=premise")
		}
		testutil.ContractWarnings.Reset()
	})

	It("admits a v1alpha1 warning-only kind with no deprecation warning", func() {
		testutil.ContractWarnings.Reset()
		obj := &workloadsv1alpha1.InstanceSet{
			ObjectMeta: metav1.ObjectMeta{Name: "premise-ungated", Namespace: helmContractNamespace},
			Spec: workloadsv1alpha1.InstanceSetSpec{
				Replicas: ptr.To(int32(1)),
				InstanceTemplate: workloadsv1alpha1.InstanceTemplate{
					InstanceSpec: workloadsv1alpha1.InstanceSpec{
						Components: []workloadsv1alpha1.InstanceComponent{{
							Name: "worker",
							Size: ptr.To(int32(1)),
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
		Expect(testutil.K8sClient.Create(testutil.Ctx, obj)).To(Succeed())
		Expect(testutil.ContractWarnings.Containing("v1alpha1 is deprecated")).To(BeEmpty(),
			"the base chart render has no v1alpha1 deprecation webhook, so no warning is returned")
	})

	It("still rejects a new RBG deprecated use on the base chart (via the Equivalent v1alpha2 gate), but warns about nothing", func() {
		testutil.ContractWarnings.Reset()
		rbg := buildV1alpha1RBG("premise-rbg-reject")
		rbg.Spec.Roles[0].Workload = workloadsv1alpha1.WorkloadSpec{
			APIVersion: "apps/v1",
			Kind:       "StatefulSet",
		}
		err := testutil.K8sClient.Create(testutil.Ctx, rbg)
		Expect(err).To(HaveOccurred(), "the v1alpha2 Equivalent-matching gate already rejects new uses")
		Expect(err.Error()).To(ContainSubstring("new uses are not accepted"))
		Expect(testutil.ContractWarnings.Containing("v1alpha1 is deprecated")).To(BeEmpty(),
			"even a rejected v1alpha1 write carried no deprecation warning before this PR")
	})
})
