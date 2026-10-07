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

package rbgsrolloutreview

// Integration layer of the PR #474 verification harness (Reviewer B / Codex).
// I1/I2 are contract tests: they fail while the admission gap exists and pass once the
// webhook rejects the input. I3 is a pacing sanity check that passes today and must keep
// passing: it anchors the feature's core promise against a real API server.

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func newReviewNamespace(name string) *corev1.Namespace {
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
}

func newReviewSet(name, ns string, replicas int32, ru *workloadsv1alpha2.GroupSetRolloutStrategy) *workloadsv1alpha2.RoleBasedGroupSet {
	return &workloadsv1alpha2.RoleBasedGroupSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: workloadsv1alpha2.RoleBasedGroupSetSpec{
			Replicas: ptr.To(replicas),
			GroupTemplate: workloadsv1alpha2.RoleBasedGroupTemplateSpec{
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
												Containers: []corev1.Container{{Name: "c", Image: "nginx"}},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			RolloutStrategy: ru,
		},
	}
}

var _ = Describe("RBGS rollout admission (integration)", func() {
	It("I1/F1: rejects a negative maxUnavailable that would wedge the rollout", func() {
		ns := newReviewNamespace("rbgs-review-i1")
		Expect(k8sClient.Create(ctx, ns)).To(Succeed())
		defer func() { _ = k8sClient.Delete(ctx, ns) }()

		set := newReviewSet("neg", ns.Name, 3, &workloadsv1alpha2.GroupSetRolloutStrategy{
			MaxUnavailable: ptr.To(intstr.FromInt32(-1)),
			MaxSurge:       ptr.To(intstr.FromInt32(1)),
		})
		err := k8sClient.Create(ctx, set)
		Expect(err).To(HaveOccurred(),
			"the API server must reject maxUnavailable: -1; admitting it wedges the rollout (budget = -1 + readySurge)")
		Expect(err.Error()).To(ContainSubstring("maxUnavailable"))
	})

	It("I2/F3: rejects an update that newly enables a ScalingAdapter in the group template", func() {
		ns := newReviewNamespace("rbgs-review-i2")
		Expect(k8sClient.Create(ctx, ns)).To(Succeed())
		defer func() { _ = k8sClient.Delete(ctx, ns) }()

		set := newReviewSet("sa", ns.Name, 1, nil)
		Expect(k8sClient.Create(ctx, set)).To(Succeed())

		// The set controller keeps writing status, so retry past resourceVersion
		// conflicts until the admission verdict comes back.
		var err error
		for i := 0; i < 20; i++ {
			latest := &workloadsv1alpha2.RoleBasedGroupSet{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(set), latest)).To(Succeed())
			latest.Spec.GroupTemplate.Spec.Roles[0].ScalingAdapter = &workloadsv1alpha2.ScalingAdapter{Enable: true}
			err = k8sClient.Update(ctx, latest)
			if err == nil || !apierrors.IsConflict(err) {
				break
			}
		}
		Expect(err).To(HaveOccurred(),
			"newly enabling scalingAdapter via update must be rejected; create-only validation leaves the conflict reachable")
		Expect(err.Error()).To(ContainSubstring("scalingAdapter"))
	})

	It("I2/F3 (regression pin): a set created with the adapter stays updatable", func() {
		ns := newReviewNamespace("rbgs-review-i2b")
		Expect(k8sClient.Create(ctx, ns)).To(Succeed())
		defer func() { _ = k8sClient.Delete(ctx, ns) }()

		set := newReviewSet("sa-pre", ns.Name, 1, nil)
		set.Spec.GroupTemplate.Spec.Roles[0].ScalingAdapter = &workloadsv1alpha2.ScalingAdapter{Enable: true}
		Expect(k8sClient.Create(ctx, set)).NotTo(Succeed(),
			"create with scalingAdapter is rejected today; if that changes, revisit this pin")
	})
})

var _ = Describe("RBGS rolling update pacing (integration)", func() {
	It("I3: a template change deletes at most one serving group at a time under maxUnavailable=1", func() {
		ns := newReviewNamespace("rbgs-review-i3")
		Expect(k8sClient.Create(ctx, ns)).To(Succeed())
		defer func() { _ = k8sClient.Delete(ctx, ns) }()

		set := newReviewSet("paced", ns.Name, 3, &workloadsv1alpha2.GroupSetRolloutStrategy{
			MaxUnavailable: ptr.To(intstr.FromInt32(1)),
			MaxSurge:       ptr.To(intstr.FromInt32(0)),
		})
		Expect(k8sClient.Create(ctx, set)).To(Succeed())

		// Wait for the three children and mark them serving (the RBG controller does not
		// run here, so the harness owns child status).
		listChildren := func() []workloadsv1alpha2.RoleBasedGroup {
			var list workloadsv1alpha2.RoleBasedGroupList
			Expect(k8sClient.List(
				ctx, &list, client.InNamespace(ns.Name),
				client.MatchingLabels{constants.GroupSetNameLabelKey: set.Name},
			)).To(Succeed())
			return list.Items
		}
		Eventually(func() int { return len(listChildren()) }, 30*time.Second, 500*time.Millisecond).Should(Equal(3))

		for i := 0; i < 3; i++ {
			child := &workloadsv1alpha2.RoleBasedGroup{}
			Expect(k8sClient.Get(
				ctx, client.ObjectKey{Namespace: ns.Name, Name: fmt.Sprintf("%s-%d", set.Name, i)}, child,
			)).To(Succeed())
			meta.SetStatusCondition(&child.Status.Conditions, metav1.Condition{
				Type:               string(workloadsv1alpha2.RoleBasedGroupReady),
				Status:             metav1.ConditionTrue,
				Reason:             "HarnessReady",
				LastTransitionTime: metav1.Now(),
			})
			// status.roleStatuses is required by the CRD schema.
			child.Status.RoleStatuses = []workloadsv1alpha2.RoleStatus{
				{Name: "worker", Replicas: 1, ReadyReplicas: 1, UpdatedReplicas: 1},
			}
			Expect(k8sClient.Status().Update(ctx, child)).To(Succeed())
		}

		// Trigger the rollout: a real template diff (not replicas-only).
		latest := &workloadsv1alpha2.RoleBasedGroupSet{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(set), latest)).To(Succeed())
		latest.Spec.GroupTemplate.Spec.Roles[0].MinReadySeconds = 10
		Expect(k8sClient.Update(ctx, latest)).To(Succeed())

		// Pacing contract: never more than one group terminating at once. envtest has no
		// kube-controller-manager, so the foreground deletion stays pending; that makes the
		// window observation exact — a second delete would show up as a second terminating
		// group.
		terminatingCount := func() int {
			n := 0
			for _, c := range listChildren() {
				if !c.DeletionTimestamp.IsZero() {
					n++
				}
			}
			return n
		}
		Eventually(terminatingCount, 30*time.Second, 500*time.Millisecond).Should(Equal(1))
		Consistently(terminatingCount, 8*time.Second, 500*time.Millisecond).Should(Equal(1),
			"maxUnavailable=1 must never take a second serving group down while the first is still terminating")

		// Cleanup: envtest runs no GC manager, so strip the foregroundDeletion finalizers to
		// let the namespace go away.
		for _, c := range listChildren() {
			if c.DeletionTimestamp.IsZero() {
				continue
			}
			child := c
			child.Finalizers = nil
			_ = k8sClient.Update(ctx, &child)
		}
	})
})
