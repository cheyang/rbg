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

package deprecation

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha1 "sigs.k8s.io/rbgs/api/workloads/v1alpha1"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	wrappersv1 "sigs.k8s.io/rbgs/test/wrappers/v1alpha1"
)

// PR #509 verification harness (F1 + gate contracts), integration layer.
//
// F1 claim (BUG-CANARY polarity): with compatibility mode on
// (--enable-deprecated-workload-types=true), a RoleBasedGroupSet whose template
// still carries a legacy workload type cannot create new child
// RoleBasedGroups. The controller's scaleUp issues a CREATE that the new
// "new uses are not accepted" gate rejects, so scale-up and child recreation
// fail with a requeue loop. The canary below asserts the *broken* state (child
// never appears); when the behavior is fixed it flips to red and must then be
// inverted (or promoted to a contract test asserting the child appears).
//
// The other specs are CONTRACT polarity (intended behavior, green on the PR
// head) and double as the positive controls proving the harness bites: the same
// controller, admission chain, and seeding path demonstrably create a child
// when the template is not legacy.

var _ = Describe("0.9.0 deprecated workload compatibility gate", func() {
	var testNs string

	BeforeEach(func() {
		testNs = fmt.Sprintf("pr509-%d", time.Now().UnixNano())
		createNamespace(testNs)
	})

	AfterEach(func() {
		deleteNamespace(testNs)
	})

	legacyTemplateSet := func(name string, replicas int32) *workloadsv1alpha2.RoleBasedGroupSet {
		role := workloadsv1alpha2.RoleSpec{
			Name:     "worker",
			Replicas: ptr.To(int32(1)),
			RolloutStrategy: &workloadsv1alpha2.RolloutStrategy{
				Type: workloadsv1alpha2.RollingUpdateStrategyType,
			},
			Annotations: map[string]string{
				constants.RoleWorkloadTypeAnnotationKey: constants.StatefulSetWorkloadType,
			},
			Pattern: workloadsv1alpha2.Pattern{
				StandalonePattern: &workloadsv1alpha2.StandalonePattern{
					TemplateSource: workloadsv1alpha2.TemplateSource{
						Template: ptr.To(pr509BasicPodTemplate()),
					},
				},
			},
		}
		return &workloadsv1alpha2.RoleBasedGroupSet{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNs},
			Spec: workloadsv1alpha2.RoleBasedGroupSetSpec{
				Replicas: ptr.To(replicas),
				GroupTemplate: workloadsv1alpha2.RoleBasedGroupTemplateSpec{
					Spec: workloadsv1alpha2.RoleBasedGroupSpec{Roles: []workloadsv1alpha2.RoleSpec{role}},
				},
			},
		}
	}

	childName := func(setName string, index int) string {
		return fmt.Sprintf("%s-%d", setName, index)
	}

	fetchSet := func(name string) *workloadsv1alpha2.RoleBasedGroupSet {
		set := &workloadsv1alpha2.RoleBasedGroupSet{}
		ExpectWithOffset(1, k8sClient.Get(
			testCtx, types.NamespacedName{Name: name, Namespace: testNs}, set,
		)).To(Succeed())
		return set
	}

	It("control: scales a non-legacy RoleBasedGroupSet under live admission", func() {
		set := legacyTemplateSet("ctl-set", 1)
		delete(set.Spec.GroupTemplate.Spec.Roles[0].Annotations, constants.RoleWorkloadTypeAnnotationKey)

		Expect(k8sClient.Create(testCtx, set)).To(Succeed())

		// The controller creates child-0 through live admission.
		Eventually(func() error {
			return k8sClient.Get(testCtx, types.NamespacedName{
				Name: childName("ctl-set", 0), Namespace: testNs,
			}, &workloadsv1alpha2.RoleBasedGroup{})
		}, 30*time.Second, 500*time.Millisecond).Should(Succeed())

		updated := fetchSet("ctl-set")
		updated.Spec.Replicas = ptr.To(int32(2))
		Expect(k8sClient.Update(testCtx, updated)).To(Succeed())

		Eventually(func() error {
			return k8sClient.Get(testCtx, types.NamespacedName{
				Name: childName("ctl-set", 1), Namespace: testNs,
			}, &workloadsv1alpha2.RoleBasedGroup{})
		}, 30*time.Second, 500*time.Millisecond).Should(Succeed())
	})

	It("contract: admits an update of a grandfathered legacy child and set", func() {
		restore := relaxAdmission()
		Expect(k8sClient.Create(testCtx, legacyTemplateSet("gf-set", 1))).To(Succeed())
		Eventually(func() error {
			return k8sClient.Get(testCtx, types.NamespacedName{
				Name: childName("gf-set", 0), Namespace: testNs,
			}, &workloadsv1alpha2.RoleBasedGroup{})
		}, 30*time.Second, 500*time.Millisecond).Should(Succeed())
		restore()

		By("updating the legacy template in place — grandfathered, must be admitted")
		updated := fetchSet("gf-set")
		if updated.Labels == nil {
			updated.Labels = map[string]string{}
		}
		updated.Labels["touch"] = "yes"
		Expect(k8sClient.Update(testCtx, updated)).To(Succeed(),
			"an existing set whose template carries a legacy role must remain writable in compat mode")

		By("updating the legacy child in place — grandfathered, must be admitted")
		child := &workloadsv1alpha2.RoleBasedGroup{}
		Expect(k8sClient.Get(testCtx, types.NamespacedName{
			Name: childName("gf-set", 0), Namespace: testNs,
		}, child)).To(Succeed())
		if child.Labels == nil {
			child.Labels = map[string]string{}
		}
		child.Labels["touch"] = "yes"
		Expect(k8sClient.Update(testCtx, child)).To(Succeed(),
			"an existing child whose role carries a legacy workload type must remain writable in compat mode")
	})

	It("contract: rejects a direct create of a legacy-workload role (the gate itself)", func() {
		rbg := &workloadsv1alpha2.RoleBasedGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "gate-rbg", Namespace: testNs},
			Spec:       legacyTemplateSet("gate-set", 1).Spec.GroupTemplate.Spec,
		}
		err := k8sClient.Create(testCtx, rbg)
		Expect(err).To(HaveOccurred(), "the compat gate should reject a new legacy-workload role")
		Expect(err.Error()).To(ContainSubstring("new uses are not accepted"))
	})

	It("F1 canary: a legacy RoleBasedGroupSet cannot scale up in compat mode", func() {
		restore := relaxAdmission()
		Expect(k8sClient.Create(testCtx, legacyTemplateSet("legacy-set", 1))).To(Succeed())
		Eventually(func() error {
			return k8sClient.Get(testCtx, types.NamespacedName{
				Name: childName("legacy-set", 0), Namespace: testNs,
			}, &workloadsv1alpha2.RoleBasedGroup{})
		}, 30*time.Second, 500*time.Millisecond).Should(Succeed())
		restore()

		By("scaling the legacy set 1 -> 2 (the set update itself is grandfathered)")
		updated := fetchSet("legacy-set")
		updated.Spec.Replicas = ptr.To(int32(2))
		Expect(k8sClient.Update(testCtx, updated)).To(Succeed())

		By("watching whether the controller manages to create child-1")
		// BUG-CANARY: on the PR head the child never appears because the
		// controller's CREATE is rejected by the compat gate and the reconcile
		// requeues forever ("Failed to scale up ... new uses are not accepted"
		// in the manager log above). If this flips — i.e. the child DOES get
		// created — the gate now exempts controller-issued children and this
		// canary must be inverted into a contract test.
		Consistently(func() error {
			return k8sClient.Get(testCtx, types.NamespacedName{
				Name: childName("legacy-set", 1), Namespace: testNs,
			}, &workloadsv1alpha2.RoleBasedGroup{})
		}, 15*time.Second, 500*time.Millisecond).Should(HaveOccurred(),
			"child-1 exists: the gate no longer rejects controller-issued creates; invert this canary")
	})

	It("contract: v1alpha1 writes hit the deprecation webhook", func() {
		By("admitting a v1alpha1 create that names RoleInstanceSet explicitly")
		okRbg := wrappersv1.BuildBasicRoleBasedGroup("v1a1-ok", testNs).
			WithRoles([]workloadsv1alpha1.RoleSpec{
				wrappersv1.BuildBasicRole("worker").WithRoleInstanceSet().Obj(),
			}).Obj()
		Expect(k8sClient.Create(testCtx, okRbg)).To(Succeed())

		By("rejecting a v1alpha1 create that names a deprecated workload type")
		badRbg := wrappersv1.BuildBasicRoleBasedGroup("v1a1-bad", testNs).
			WithRoles([]workloadsv1alpha1.RoleSpec{
				wrappersv1.BuildBasicRole("worker").
					WithWorkload(workloadsv1alpha1.StatefulSetWorkloadType).Obj(),
			}).Obj()
		err := k8sClient.Create(testCtx, badRbg)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("new uses are not accepted"),
			"the v1alpha1 deprecation webhook should reject the deprecated workload use, got: %v", err)
	})
})

func pr509BasicPodTemplate() corev1.PodTemplateSpec {
	template := corev1.PodTemplateSpec{}
	template.Spec.Containers = []corev1.Container{{
		Name:  "worker",
		Image: "registry.k8s.io/pause:3.9",
	}}
	return template
}

func createNamespace(name string) {
	ExpectWithOffset(1, k8sClient.Create(testCtx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	})).To(Succeed())
}

// deleteNamespace is best-effort: the RBGSet controller's requeue loop can keep
// a terminating namespace busy for a while, and the whole envtest apiserver is
// torn down in AfterSuite anyway, so a slow namespace GC must not fail the suite.
func deleteNamespace(name string) {
	_ = k8sClient.Delete(testCtx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	})
	_ = eventuallyGone(name)
}

func eventuallyGone(name string) error {
	var lastErr error
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		lastErr = k8sClient.Get(testCtx, types.NamespacedName{Name: name}, &corev1.Namespace{})
		if lastErr != nil {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return lastErr
}
