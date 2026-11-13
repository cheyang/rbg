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

// Integration-layer verification for the review of PR #578 (KEP-576).
// F2: the KEP's rollout table models the legacy population as "per-role
// ConfigMaps, direct mount (unchanged)". Against a real API server this spec
// shows what the current controller actually delivers for a legacy-mode RBG:
// the child RoleInstanceSet pod template mounts the RBG-level `<rbg>`
// ConfigMap, and no code path ever creates that object while the mode is
// legacy. The refine control case proves the suite would observe the
// ConfigMap if the controller created it.

package rbg

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/test/envtest/testutil"
)

var _ = Describe("KEP-576 baseline: legacy discovery mode", func() {
	const (
		timeout  = 30 * time.Second
		interval = 200 * time.Millisecond
	)

	var namespace string

	BeforeEach(func() {
		namespace = fmt.Sprintf("kep576-legacy-%d", time.Now().UnixNano())
		testutil.CreateNamespace(namespace)
	})
	AfterEach(func() { testutil.DeleteNamespace(namespace) })

	newStandaloneRBG := func(name, mode string) *workloadsv1alpha2.RoleBasedGroup {
		return &workloadsv1alpha2.RoleBasedGroup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
				Annotations: map[string]string{
					constants.DiscoveryConfigModeAnnotationKey: mode,
				},
			},
			Spec: workloadsv1alpha2.RoleBasedGroupSpec{
				Roles: []workloadsv1alpha2.RoleSpec{
					{
						Name:     "worker",
						Replicas: ptr.To(int32(1)),
						Pattern: workloadsv1alpha2.Pattern{
							StandalonePattern: &workloadsv1alpha2.StandalonePattern{
								TemplateSource: workloadsv1alpha2.TemplateSource{
									Template: &corev1.PodTemplateSpec{
										Spec: corev1.PodSpec{
											Containers: []corev1.Container{{Name: "nginx", Image: "nginx:latest"}},
										},
									},
								},
							},
						},
					},
				},
			},
		}
	}

	risDiscoveryVolume := func(rbgName string) *corev1.Volume {
		set := &workloadsv1alpha2.RoleInstanceSet{}
		if err := testutil.K8sClient.Get(testutil.Ctx, types.NamespacedName{Name: rbgName + "-worker", Namespace: namespace}, set); err != nil {
			return nil
		}
		for ci := range set.Spec.RoleInstanceTemplate.Components {
			tmpl := set.Spec.RoleInstanceTemplate.Components[ci].Template
			for i := range tmpl.Spec.Volumes {
				v := &tmpl.Spec.Volumes[i]
				if v.Name == "rbg-cluster-config" {
					return v
				}
			}
		}
		return nil
	}

	It("legacy-mode RBG: pod template mounts the RBG-level ConfigMap that no legacy code path creates", func() {
		legacy := newStandaloneRBG("legacy-demo", string(constants.LegacyDiscoveryConfigMode))
		Expect(testutil.K8sClient.Create(testutil.Ctx, legacy)).To(Succeed())

		// The child RIS pod template ends up mounting the RBG-level ConfigMap.
		Eventually(func(g Gomega) {
			v := risDiscoveryVolume("legacy-demo")
			g.Expect(v).NotTo(BeNil(), "expected rbg-cluster-config volume on the RIS pod template")
			g.Expect(v.ConfigMap).NotTo(BeNil())
			g.Expect(v.ConfigMap.Name).To(Equal("legacy-demo"))
		}, timeout, interval).Should(Succeed())

		// ...while the controller never creates that object in legacy mode.
		Consistently(func() bool {
			cm := &corev1.ConfigMap{}
			err := testutil.K8sClient.Get(testutil.Ctx, types.NamespacedName{Name: "legacy-demo", Namespace: namespace}, cm)
			return apierrors.IsNotFound(err)
		}, 5*time.Second, interval).Should(BeTrue(),
			"no controller path should create the RBG-level ConfigMap for a legacy-mode RBG")

		// Control: a refine-mode RBG in the same namespace DOES get the object,
		// so the absence above is attributable to the mode gate, not the suite.
		refine := newStandaloneRBG("refine-demo", string(constants.RefineDiscoveryConfigMode))
		Expect(testutil.K8sClient.Create(testutil.Ctx, refine)).To(Succeed())
		Eventually(func() error {
			cm := &corev1.ConfigMap{}
			return testutil.K8sClient.Get(testutil.Ctx, types.NamespacedName{Name: "refine-demo", Namespace: namespace}, cm)
		}, timeout, interval).Should(Succeed())
	})
})
