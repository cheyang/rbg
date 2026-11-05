/*
Verification harness for PR #553 (issue #547) — Reviewer B (Codex).
Additive only; does not modify production code.

Contract polarity: FAILS on the base commit (the reconcile panics with an
index-out-of-range in rollingUpdatePartition, controller-runtime recovers, and
the StatefulSet stays at the shrunken replica count), PASSES on the PR head
(the controller scales the StatefulSet back to the desired count).
*/

package stsscaleup

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

var _ = Describe("PR553 verify: sts scale-up before rolling when live replicas are low", func() {
	const (
		timeout  = 60 * time.Second
		interval = 500 * time.Millisecond
	)

	var testNs string

	BeforeEach(func() {
		testNs = fmt.Sprintf("test-pr553-%d", time.Now().UnixNano())
		Expect(k8sClient.Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: testNs},
		})).To(Succeed())
	})

	AfterEach(func() {
		ns := &corev1.Namespace{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: testNs}, ns); err == nil {
			_ = k8sClient.Delete(ctx, ns)
		}
	})

	It("scales the StatefulSet back to the desired count after an external scale-down", func() {
		rbgName := "pr553-rbg"
		roleName := "worker"
		stsName := fmt.Sprintf("%s-%s", rbgName, roleName)

		rbg := &workloadsv1alpha2.RoleBasedGroup{
			ObjectMeta: metav1.ObjectMeta{Name: rbgName, Namespace: testNs},
			Spec: workloadsv1alpha2.RoleBasedGroupSpec{
				Roles: []workloadsv1alpha2.RoleSpec{
					{
						Name:     roleName,
						Replicas: ptr.To(int32(4)),
						Annotations: map[string]string{
							constants.RoleWorkloadTypeAnnotationKey: "apps/v1/StatefulSet",
						},
						RolloutStrategy: &workloadsv1alpha2.RolloutStrategy{
							Type: workloadsv1alpha2.RollingUpdateStrategyType,
							RollingUpdate: &workloadsv1alpha2.RollingUpdate{
								MaxUnavailable: ptr.To(intstr.FromInt32(1)),
								MaxSurge:       ptr.To(intstr.FromInt32(1)),
							},
						},
						Pattern: workloadsv1alpha2.Pattern{
							StandalonePattern: &workloadsv1alpha2.StandalonePattern{
								TemplateSource: workloadsv1alpha2.TemplateSource{
									Template: ptr.To(corev1.PodTemplateSpec{
										Spec: corev1.PodSpec{
											Containers: []corev1.Container{
												{Name: "nginx", Image: "nginx:latest"},
											},
										},
									}),
								},
							},
						},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, rbg)).To(Succeed())

		stsKey := types.NamespacedName{Name: stsName, Namespace: testNs}
		sts := &appsv1.StatefulSet{}

		By("waiting for the StatefulSet to be created at the desired replica count")
		Eventually(func() (int32, error) {
			if err := k8sClient.Get(ctx, stsKey, sts); err != nil {
				return -1, err
			}
			return *sts.Spec.Replicas, nil
		}, timeout, interval).Should(Equal(int32(4)), "controller should create the sts at replicas=4")

		By("creating the StatefulSet controller revision (emulating the kube sts controller)")
		// In a real cluster the kube StatefulSet controller creates a
		// ControllerRevision carrying the pod template labels; getReplicaStates
		// looks it up via the sts selector. envtest runs no sts controller, so
		// the harness creates it explicitly (same shape as production).
		Expect(k8sClient.Get(ctx, stsKey, sts)).To(Succeed())
		stsRevision := &appsv1.ControllerRevision{
			ObjectMeta: metav1.ObjectMeta{
				Name:      stsName + "-emulated",
				Namespace: testNs,
				Labels:    sts.Spec.Selector.MatchLabels,
			},
			Data:     runtime.RawExtension{Raw: []byte("{}")},
			Revision: 1,
		}
		Expect(k8sClient.Create(ctx, stsRevision)).To(Succeed())

		By("simulating an external scale-down (kubectl scale) to 1 replica")
		Expect(k8sClient.Get(ctx, stsKey, sts)).To(Succeed())
		sts.Spec.Replicas = ptr.To(int32(1))
		Expect(k8sClient.Update(ctx, sts)).To(Succeed())
		// The built-in sts controller does not run in envtest; emulate its status
		// update so the RBG controller's workload predicate (which compares
		// template+status, not spec.replicas) observes the change.
		Expect(k8sClient.Get(ctx, stsKey, sts)).To(Succeed())
		sts.Status.Replicas = 1
		Expect(k8sClient.Status().Update(ctx, sts)).To(Succeed())

		By("expecting the controller to scale the StatefulSet back to 4")
		// Base commit: case 6 panics (index out of range), controller-runtime
		// recovers and requeues, the panic repeats, and the sts stays at 1 ->
		// this Eventually times out. PR head: the new scale-up-first branch
		// returns replicas=4 and the patch restores the desired count.
		Eventually(func() (int32, error) {
			if err := k8sClient.Get(ctx, stsKey, sts); err != nil {
				return -1, err
			}
			return *sts.Spec.Replicas, nil
		}, timeout, interval).Should(Equal(int32(4)), "controller should restore the desired replica count")
	})
})
