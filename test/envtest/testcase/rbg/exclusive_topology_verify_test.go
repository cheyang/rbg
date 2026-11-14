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

package rbg

// Reviewer-verification harness for PR #588 (fixes #518). Additive only.
//
// Runs the REAL RoleBasedGroup + RoleInstanceSet + RoleInstance controllers
// against a real API server (envtest), so it exercises the RBGPredicate watch
// path end to end, not just the template-construction function.
//
// Polarity: CONTRACT tests. On the merge-base (1fc69f70) both specs FAIL —
// the first because the injected required affinity selects
// rbg.workloads.x-k8s.io/group-unique-hash, a label no Pod carries (the
// issue-#518 mechanism), the second because the base predicate never
// re-enqueues the RBG on an exclusive-topology annotation change.

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/test/envtest/testutil"
)

var _ = Describe("Exclusive topology verification (PR #588)", func() {
	const (
		timeout  = time.Second * 60
		interval = time.Millisecond * 250
	)

	var testNs string

	BeforeEach(func() {
		testNs = fmt.Sprintf("test-excl-verify-%d", time.Now().UnixNano())
		testutil.CreateNamespace(testNs)
	})

	AfterEach(func() {
		testutil.DeleteNamespace(testNs)
	})

	newExclusiveRBG := func(name string) *workloadsv1alpha2.RoleBasedGroup {
		return &workloadsv1alpha2.RoleBasedGroup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: testNs,
				Annotations: map[string]string{
					constants.GroupExclusiveTopologyKey: corev1.LabelHostname,
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
											Containers: []corev1.Container{
												{Name: "main", Image: "busybox"},
											},
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

	risTemplate := func(rbg *workloadsv1alpha2.RoleBasedGroup) (*corev1.PodTemplateSpec, error) {
		ris := &workloadsv1alpha2.RoleInstanceSet{}
		if err := testutil.K8sClient.Get(
			testutil.Ctx,
			types.NamespacedName{Name: rbg.GetWorkloadName(&rbg.Spec.Roles[0]), Namespace: testNs},
			ris,
		); err != nil {
			return nil, err
		}
		comps := ris.Spec.RoleInstanceTemplate.Components
		if len(comps) != 1 {
			return nil, fmt.Errorf("expected 1 component, got %d", len(comps))
		}
		return &comps[0].Template, nil
	}

	It("renders a self-matching group-uid affinity on the RoleInstanceSet template", func() {
		rbg := newExclusiveRBG("excl-verify")
		Expect(testutil.K8sClient.Create(testutil.Ctx, rbg)).To(Succeed())

		expectedUID := rbg.GenGroupUniqueKey()
		Eventually(func() error {
			tmpl, err := risTemplate(rbg)
			if err != nil {
				return err
			}
			if tmpl.Labels[constants.GroupUIDLabelKey] != expectedUID {
				return fmt.Errorf(
					"pod template label %s=%q, want %q",
					constants.GroupUIDLabelKey, tmpl.Labels[constants.GroupUIDLabelKey], expectedUID,
				)
			}
			if tmpl.Annotations[constants.GroupExclusiveTopologyKey] != corev1.LabelHostname {
				return fmt.Errorf("pod template missing the exclusive-topology annotation")
			}
			aff := tmpl.Spec.Affinity
			if aff == nil || aff.PodAffinity == nil || aff.PodAntiAffinity == nil {
				return fmt.Errorf("pod template missing injected affinity")
			}
			terms := aff.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution
			antiTerms := aff.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution
			if len(terms) != 1 || len(antiTerms) != 1 {
				return fmt.Errorf("expected exactly one affinity and one anti-affinity term")
			}
			if terms[0].TopologyKey != corev1.LabelHostname {
				return fmt.Errorf("affinity topology key %q", terms[0].TopologyKey)
			}
			ownSelector, err := metav1.LabelSelectorAsSelector(terms[0].LabelSelector)
			if err != nil {
				return err
			}
			// The issue-#518 contract: the pod must carry the label its own
			// required affinity selects, else the first pod never schedules.
			if !ownSelector.Matches(labels.Set(tmpl.Labels)) {
				return fmt.Errorf(
					"affinity selector %v does not match the pod's own labels %v",
					ownSelector, tmpl.Labels,
				)
			}
			otherSelector, err := metav1.LabelSelectorAsSelector(antiTerms[0].LabelSelector)
			if err != nil {
				return err
			}
			if otherSelector.Matches(labels.Set(tmpl.Labels)) {
				return fmt.Errorf("anti-affinity must not match the pod's own group")
			}
			return nil
		}, timeout, interval).Should(Succeed())
	})

	It("reconciles the RoleInstanceSet template when the annotation is toggled", func() {
		rbg := newExclusiveRBG("excl-toggle")
		Expect(testutil.K8sClient.Create(testutil.Ctx, rbg)).To(Succeed())

		Eventually(func() error {
			tmpl, err := risTemplate(rbg)
			if err != nil {
				return err
			}
			if tmpl.Spec.Affinity == nil || tmpl.Spec.Affinity.PodAffinity == nil {
				return fmt.Errorf("waiting for injected affinity")
			}
			return nil
		}, timeout, interval).Should(Succeed())

		// Remove the annotation: the RBG watch predicate must fire and the
		// rendered template must drop the injected affinity.
		Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
			current := &workloadsv1alpha2.RoleBasedGroup{}
			if err := testutil.K8sClient.Get(
				testutil.Ctx, client.ObjectKeyFromObject(rbg), current,
			); err != nil {
				return err
			}
			delete(current.Annotations, constants.GroupExclusiveTopologyKey)
			return testutil.K8sClient.Update(testutil.Ctx, current)
		})).To(Succeed())

		Eventually(func() error {
			tmpl, err := risTemplate(rbg)
			if err != nil {
				return err
			}
			if tmpl.Spec.Affinity != nil {
				return fmt.Errorf("affinity should be removed after the annotation is removed")
			}
			if _, ok := tmpl.Annotations[constants.GroupExclusiveTopologyKey]; ok {
				return fmt.Errorf("pod template annotation should be removed")
			}
			return nil
		}, timeout, interval).Should(Succeed())

		// Re-add with a different topology key: predicate must fire on a value
		// change and the rendered template must pick up the new key.
		Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
			current := &workloadsv1alpha2.RoleBasedGroup{}
			if err := testutil.K8sClient.Get(
				testutil.Ctx, client.ObjectKeyFromObject(rbg), current,
			); err != nil {
				return err
			}
			if current.Annotations == nil {
				current.Annotations = map[string]string{}
			}
			current.Annotations[constants.GroupExclusiveTopologyKey] = "topology.kubernetes.io/zone"
			return testutil.K8sClient.Update(testutil.Ctx, current)
		})).To(Succeed())

		Eventually(func() error {
			tmpl, err := risTemplate(rbg)
			if err != nil {
				return err
			}
			aff := tmpl.Spec.Affinity
			if aff == nil || aff.PodAffinity == nil {
				return fmt.Errorf("waiting for affinity to be re-injected")
			}
			terms := aff.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution
			if len(terms) != 1 || terms[0].TopologyKey != "topology.kubernetes.io/zone" {
				return fmt.Errorf("affinity topology key did not follow the annotation change")
			}
			return nil
		}, timeout, interval).Should(Succeed())
	})
})
