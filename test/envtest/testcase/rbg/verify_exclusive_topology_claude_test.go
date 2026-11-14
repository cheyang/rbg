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

// Reviewer A (Claude) verification harness for PR #588 / issue #518.
//
// Layer: L2 (integration / envtest) against the real RoleBasedGroup and
// RoleInstanceSet controllers. envtest has no kube-scheduler, so this cannot
// prove the Pending->Scheduled transition (that is the live e2e layer); it
// proves the controller-side contract of the fix: the workload Pod template
// the controllers actually persist carries the label its own affinity
// selects, and toggling the annotation rewrites the template.
//
// Contract (L2): a v1alpha2 RoleBasedGroup with the group-exclusive-topology
// annotation gets a RoleInstanceSet whose component Pod template (a) carries
// rbg.workloads.x-k8s.io/group-uid = GenGroupUniqueKey(), (b) has a required
// podAffinity on the same topology key selecting group-uid with that value,
// and (c) a required podAntiAffinity that excludes the group's own uid.
// Removing the annotation removes the injected affinity (predicate +
// reconstruct-from-RBG-spec path). On the merge-base this is RED (selector
// key group-unique-hash never matches the Pod labels).

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/test/envtest/testutil"
)

var _ = Describe("RoleBasedGroup exclusive topology (reviewer verify)", func() {
	const (
		verifyTimeout  = time.Second * 30
		verifyInterval = time.Millisecond * 250
	)

	var testNs string

	BeforeEach(func() {
		testNs = "test-verify-excl-" + time.Now().Format("150405-000000000")
		testutil.CreateNamespace(testNs)
	})

	AfterEach(func() {
		testutil.DeleteNamespace(testNs)
	})

	buildVerifyRBG := func(name string, annotations map[string]string) *workloadsv1alpha2.RoleBasedGroup {
		return &workloadsv1alpha2.RoleBasedGroup{
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Namespace:   testNs,
				Annotations: annotations,
			},
			Spec: workloadsv1alpha2.RoleBasedGroupSpec{
				Roles: []workloadsv1alpha2.RoleSpec{
					{
						Name:     "worker",
						Replicas: ptr.To(int32(1)),
						Pattern: workloadsv1alpha2.Pattern{
							StandalonePattern: &workloadsv1alpha2.StandalonePattern{
								TemplateSource: workloadsv1alpha2.TemplateSource{
									Template: ptr.To(nginxPodTemplate()),
								},
							},
						},
					},
				},
			},
		}
	}

	// componentTemplate returns the first component Pod template of the role's
	// RoleInstanceSet, or an error so it can be used inside Eventually polls.
	componentTemplate := func(rbg *workloadsv1alpha2.RoleBasedGroup) (corev1.PodTemplateSpec, error) {
		ris := &workloadsv1alpha2.RoleInstanceSet{}
		if err := testutil.K8sClient.Get(testutil.Ctx, types.NamespacedName{
			Name: rbg.GetWorkloadName(&rbg.Spec.Roles[0]), Namespace: testNs,
		}, ris); err != nil {
			return corev1.PodTemplateSpec{}, err
		}
		if len(ris.Spec.RoleInstanceTemplate.Components) == 0 {
			return corev1.PodTemplateSpec{}, fmt.Errorf("no components in RoleInstanceSet yet")
		}
		return ris.Spec.RoleInstanceTemplate.Components[0].Template, nil
	}

	It("injects group-uid affinity that matches the Pod's own labels, and toggles with the annotation", func() {
		rbgName := "verify-excl"
		rbg := buildVerifyRBG(rbgName, map[string]string{
			constants.GroupExclusiveTopologyKey: corev1.LabelHostname,
		})
		Expect(testutil.K8sClient.Create(testutil.Ctx, rbg)).To(Succeed())

		expectedUID := rbg.GenGroupUniqueKey()

		// The workload is created and its template carries a self-consistent
		// affinity: the selector key/value must match the template labels.
		Eventually(func() error {
			tpl, err := componentTemplate(rbg)
			if err != nil {
				return err
			}
			if got := tpl.Labels[constants.GroupUIDLabelKey]; got != expectedUID {
				return fmt.Errorf("pod template labels %v lack group-uid %q", tpl.Labels, expectedUID)
			}
			if tpl.Spec.Affinity == nil || tpl.Spec.Affinity.PodAffinity == nil {
				return fmt.Errorf("no required podAffinity injected; affinity=%v", tpl.Spec.Affinity)
			}
			terms := tpl.Spec.Affinity.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution
			if len(terms) == 0 {
				return fmt.Errorf("no required podAffinity terms")
			}
			for _, term := range terms {
				if term.TopologyKey != corev1.LabelHostname {
					return fmt.Errorf("affinity topology key %q != hostname", term.TopologyKey)
				}
				if term.LabelSelector == nil || len(term.LabelSelector.MatchExpressions) == 0 {
					return fmt.Errorf("affinity term has no selector")
				}
				for _, expr := range term.LabelSelector.MatchExpressions {
					if expr.Key != constants.GroupUIDLabelKey {
						return fmt.Errorf("affinity selector key %q != %q (issue #518: Pods carry group-uid)",
							expr.Key, constants.GroupUIDLabelKey)
					}
					if len(expr.Values) != 1 || expr.Values[0] != expectedUID {
						return fmt.Errorf("affinity selector values %v != [%s]", expr.Values, expectedUID)
					}
				}
				selector, err := metav1.LabelSelectorAsSelector(term.LabelSelector)
				if err != nil {
					return err
				}
				if !selector.Matches(labels.Set(tpl.Labels)) {
					return fmt.Errorf("required affinity %q does not match the template's own labels %v (first Pod stays Pending)",
						selector, tpl.Labels)
				}
			}
			return nil
		}, verifyTimeout, verifyInterval).Should(Succeed())

		// Anti-affinity must exclude the group's own uid while matching other
		// groups' pods (contract from the PR docs).
		tpl, err := componentTemplate(rbg)
		Expect(err).NotTo(HaveOccurred())
		Expect(tpl.Spec.Affinity.PodAntiAffinity).NotTo(BeNil())
		antiTerms := tpl.Spec.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution
		Expect(antiTerms).NotTo(BeEmpty())
		for _, term := range antiTerms {
			selector, err := metav1.LabelSelectorAsSelector(term.LabelSelector)
			Expect(err).NotTo(HaveOccurred())
			Expect(selector.Matches(labels.Set(tpl.Labels))).To(BeFalse(),
				"anti-affinity must not match the group's own Pods")
			otherGroup := labels.Set(map[string]string{constants.GroupUIDLabelKey: "other-group-uid"})
			Expect(selector.Matches(otherGroup)).To(BeTrue(),
				"anti-affinity must match other groups carrying group-uid")
		}

		// Toggling the annotation off must rewrite the workload template
		// (RBGPredicate now watches the annotation; the template is rebuilt
		// from the RBG spec each reconcile, so the injected affinity is
		// dropped).
		updated := &workloadsv1alpha2.RoleBasedGroup{}
		Expect(testutil.K8sClient.Get(testutil.Ctx, types.NamespacedName{
			Name: rbgName, Namespace: testNs,
		}, updated)).To(Succeed())
		updated.Annotations = nil
		Expect(testutil.K8sClient.Update(testutil.Ctx, updated)).To(Succeed())

		Eventually(func() error {
			tpl, err := componentTemplate(rbg)
			if err != nil {
				return err
			}
			if tpl.Spec.Affinity != nil {
				return fmt.Errorf("affinity still present after removing the annotation: %+v", tpl.Spec.Affinity)
			}
			return nil
		}, verifyTimeout, verifyInterval).Should(Succeed())
	})
})
