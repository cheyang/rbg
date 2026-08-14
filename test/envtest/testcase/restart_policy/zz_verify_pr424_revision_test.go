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

// Reviewer harness for https://github.com/sgl-project/rbg/pull/424.
// Rides the existing envtest suite so the real RoleInstanceSet controller is running.
//
// Question: upgrading from v0.7.0 changes the roleInstanceTemplate serialization
// (restartPolicy string -> restartPolicyConfig object) and therefore the
// ControllerRevision hash. Does that churn actually roll the RoleInstances?
package restart_policy

import (
	"fmt"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/test/envtest/testutil"
)

var _ = Describe("PR424: revision churn from the template rename", func() {
	const ns = "default"

	// v070ShapedRIS builds what a v0.7.0 controller stored: the policy as a bare
	// string on the inlined RoleInstanceSpec.
	v070ShapedRIS := func(name string) *workloadsv1alpha2.RoleInstanceSet {
		return &workloadsv1alpha2.RoleInstanceSet{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Spec: workloadsv1alpha2.RoleInstanceSetSpec{
				Replicas: ptr.To(int32(1)),
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"pr424": name}},
				RoleInstanceTemplate: workloadsv1alpha2.RoleInstanceTemplate{
					RoleInstanceSpec: workloadsv1alpha2.RoleInstanceSpec{
						RestartPolicy: workloadsv1alpha2.RestartPolicyNone, //nolint:staticcheck
						Components: []workloadsv1alpha2.RoleInstanceComponent{{
							Name: "main",
							Size: ptr.To(int32(1)),
							Template: corev1.PodTemplateSpec{
								ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"pr424": name}},
								Spec: corev1.PodSpec{
									Containers: []corev1.Container{{Name: "main", Image: "nginx:latest"}},
								},
							},
						}},
					},
				},
			},
		}
	}

	instanceUIDs := func(setName string) map[string]types.UID {
		list := &workloadsv1alpha2.RoleInstanceList{}
		Expect(testutil.K8sClient.List(testutil.Ctx, list, client.InNamespace(ns))).To(Succeed())
		out := map[string]types.UID{}
		for i := range list.Items {
			ri := &list.Items[i]
			for _, o := range ri.OwnerReferences {
				if o.Name == setName {
					out[ri.Name] = ri.UID
				}
			}
		}
		return out
	}

	revisionsFor := func(setName string) []string {
		crList := &apps.ControllerRevisionList{}
		Expect(testutil.K8sClient.List(testutil.Ctx, crList, client.InNamespace(ns))).To(Succeed())
		var names []string
		for i := range crList.Items {
			cr := &crList.Items[i]
			for _, o := range cr.OwnerReferences {
				if o.Name == setName {
					names = append(names, fmt.Sprintf("%s(rev=%d)", cr.Name, cr.Revision))
				}
			}
		}
		return names
	}

	It("renaming restartPolicy to restartPolicyConfig in the template must not roll the RoleInstances", func() {
		name := "pr424-churn"
		set := v070ShapedRIS(name)
		Expect(testutil.K8sClient.Create(testutil.Ctx, set)).To(Succeed())

		By("waiting for the controller to create the RoleInstance and its revision")
		var before map[string]types.UID
		Eventually(func() int {
			before = instanceUIDs(name)
			return len(before)
		}, 60*time.Second, time.Second).Should(BeNumerically(">", 0),
			"controller should create at least one RoleInstance")

		stored := &workloadsv1alpha2.RoleInstanceSet{}
		Expect(testutil.K8sClient.Get(testutil.Ctx,
			client.ObjectKey{Namespace: ns, Name: name}, stored)).To(Succeed())
		GinkgoWriter.Printf("BEFORE: instances=%v\n", before)
		GinkgoWriter.Printf("BEFORE: currentRevision=%q updateRevision=%q\n",
			stored.Status.CurrentRevision, stored.Status.UpdateRevision)
		GinkgoWriter.Printf("BEFORE: revisions=%v\n", revisionsFor(name))
		beforeCurrent := stored.Status.CurrentRevision

		By("simulating the controller upgrade: same effective policy, new serialization")
		Eventually(func() error {
			live := &workloadsv1alpha2.RoleInstanceSet{}
			if err := testutil.K8sClient.Get(testutil.Ctx,
				client.ObjectKey{Namespace: ns, Name: name}, live); err != nil {
				return err
			}
			live.Spec.RoleInstanceTemplate.RestartPolicy = "" //nolint:staticcheck
			live.Spec.RoleInstanceTemplate.RestartPolicyConfig = &workloadsv1alpha2.RestartPolicyConfig{
				Type:             workloadsv1alpha2.RestartPolicyNone,
				BaseDelaySeconds: ptr.To(int32(30)),
				MaxDelaySeconds:  ptr.To(int32(600)),
			}
			return testutil.K8sClient.Update(testutil.Ctx, live)
		}, 30*time.Second, time.Second).Should(Succeed())

		By("letting the controller settle")
		Consistently(func() map[string]types.UID {
			return instanceUIDs(name)
		}, 45*time.Second, 3*time.Second).Should(Equal(before),
			"the RoleInstances must not be recreated by a pure serialization change")

		after := &workloadsv1alpha2.RoleInstanceSet{}
		Expect(testutil.K8sClient.Get(testutil.Ctx,
			client.ObjectKey{Namespace: ns, Name: name}, after)).To(Succeed())

		// Two assertions together are the answer. The Consistently above already
		// showed the RoleInstances were not recreated. This one shows the revision
		// DID move, so the churn is real and was absorbed rather than absent.
		// The churn shows up on updateRevision: the controller derives a new revision
		// from the reserialized template and points updateRevision at it, leaving
		// currentRevision behind until the instances roll to it.
		Expect(after.Status.UpdateRevision).NotTo(Equal(beforeCurrent), fmt.Sprintf(
			"CANARY: expected a new revision from the pure serialization change. "+
				"before=%q current=%q update=%q instances=%v revisions=%v",
			beforeCurrent, after.Status.CurrentRevision, after.Status.UpdateRevision,
			instanceUIDs(name), revisionsFor(name)))

		// Surface the observed values on success too, by failing a deliberately
		// impossible check only when asked via PR424_DUMP=1.
		if os.Getenv("PR424_DUMP") == "1" {
			Fail(fmt.Sprintf("DUMP(not a real failure): before=%q after=%q update=%q instances=%v revisions=%v",
				beforeCurrent, after.Status.CurrentRevision, after.Status.UpdateRevision,
				instanceUIDs(name), revisionsFor(name)))
		}

		// Cleanup so the shared namespace stays usable for other specs.
		Expect(testutil.K8sClient.Delete(testutil.Ctx, after)).To(Succeed())
	})
})
