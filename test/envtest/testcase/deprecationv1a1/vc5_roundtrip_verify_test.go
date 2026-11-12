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

// vc5_roundtrip_verify_test.go is REVIEW HARNESS (PR #568, verify/rbg-conventions-skill-codex).
// It independently replicates verification case VC-5 of the rbg-conventions skill against
// envtest with the production conversion and admission webhooks: reading a RoleBasedGroup
// through v1alpha1 and writing it back unchanged must leave the stored v1alpha2 object
// untouched. The skill documents that this FAILS on main today; these specs are bug-canaries
// for that documented pre-existing behavior (they pass while the lossy conversion exists and
// flip red once conversion becomes lossless).

package deprecationv1a1

import (
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	workloadsv1alpha1 "sigs.k8s.io/rbgs/api/workloads/v1alpha1"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/test/envtest/testutil"
)

// envtest has no namespace controller, so a deleted namespace never leaves Terminating;
// each spec gets its own namespace instead.
var vc5ns string

func vc5Template() *corev1.PodTemplateSpec {
	return &corev1.PodTemplateSpec{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name: "main", Image: "busybox:1.36",
				Command: []string{"sh", "-c", "sleep 2147483647"},
			}},
		},
	}
}

// roundTrip reads the named RBG through v1alpha1 and writes it back unchanged,
// the way an old client holding v1alpha1 manifests does.
func roundTrip(name string) error {
	v1 := &workloadsv1alpha1.RoleBasedGroup{}
	if err := testutil.K8sClient.Get(testutil.Ctx,
		types.NamespacedName{Name: name, Namespace: vc5ns}, v1); err != nil {
		return err
	}
	return testutil.K8sClient.Update(testutil.Ctx, v1)
}

var _ = Describe("VC-5 v1alpha1 round trip (review harness, canary polarity)", func() {
	BeforeEach(func() {
		vc5ns = fmt.Sprintf("vc5-%d", time.Now().UnixNano())
		testutil.CreateNamespace(vc5ns)
	})

	It("canary: the legacy restartPolicy string is replaced by a defaulted restartPolicyConfig "+
		"and every role gains the workload-type annotation", func() {
		rbg := &workloadsv1alpha2.RoleBasedGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "vc5-legacy", Namespace: vc5ns},
			Spec: workloadsv1alpha2.RoleBasedGroupSpec{
				Roles: []workloadsv1alpha2.RoleSpec{{
					Name:     "engine",
					Replicas: ptr.To(int32(1)),
					Pattern: workloadsv1alpha2.Pattern{
						LeaderWorkerPattern: &workloadsv1alpha2.LeaderWorkerPattern{
							Size:           ptr.To(int32(2)),
							TemplateSource: workloadsv1alpha2.TemplateSource{Template: vc5Template()},
							RestartPolicy:  workloadsv1alpha2.RecreateRoleInstanceOnPodRestart,
						},
					},
				}},
			},
		}
		Expect(testutil.K8sClient.Create(testutil.Ctx, rbg)).To(Succeed())

		Expect(roundTrip("vc5-legacy")).To(Succeed())

		after := &workloadsv1alpha2.RoleBasedGroup{}
		Expect(testutil.K8sClient.Get(testutil.Ctx, types.NamespacedName{Name: "vc5-legacy", Namespace: vc5ns}, after)).To(Succeed())
		role := after.Spec.Roles[0]
		// the stored spec changed although the write was a no-op for the client
		Expect(role.Annotations).To(HaveKeyWithValue(
			"rbg.workloads.x-k8s.io/role-workload-type", "workloads.x-k8s.io/v1alpha2/RoleInstanceSet"))
		Expect(role.LeaderWorkerPattern.RestartPolicy).To(BeEmpty(),
			"deprecated string survives only if the round trip is lossless")
		cfg := role.LeaderWorkerPattern.RestartPolicyConfig
		Expect(cfg).NotTo(BeNil(), "restartPolicyConfig materialized by conversion")
		Expect(cfg.BaseDelaySeconds).NotTo(BeNil())
		Expect(*cfg.BaseDelaySeconds).To(Equal(int32(30)), "CRD default materialized")
		Expect(cfg.MaxDelaySeconds).NotTo(BeNil())
		Expect(*cfg.MaxDelaySeconds).To(Equal(int32(600)), "CRD default materialized")
	})

	It("canary: a configured baseDelaySeconds is reset to the default (data loss)", func() {
		rbg := &workloadsv1alpha2.RoleBasedGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "vc5-backoff", Namespace: vc5ns},
			Spec: workloadsv1alpha2.RoleBasedGroupSpec{
				Roles: []workloadsv1alpha2.RoleSpec{{
					Name:     "engine",
					Replicas: ptr.To(int32(1)),
					Pattern: workloadsv1alpha2.Pattern{
						LeaderWorkerPattern: &workloadsv1alpha2.LeaderWorkerPattern{
							Size:           ptr.To(int32(2)),
							TemplateSource: workloadsv1alpha2.TemplateSource{Template: vc5Template()},
							RestartPolicyConfig: &workloadsv1alpha2.RestartPolicyConfig{
								Type:             workloadsv1alpha2.RecreateRoleInstanceOnPodRestart,
								BaseDelaySeconds: ptr.To(int32(10)),
							},
						},
					},
				}},
			},
		}
		Expect(testutil.K8sClient.Create(testutil.Ctx, rbg)).To(Succeed())

		Expect(roundTrip("vc5-backoff")).To(Succeed())

		after := &workloadsv1alpha2.RoleBasedGroup{}
		Expect(testutil.K8sClient.Get(testutil.Ctx, types.NamespacedName{Name: "vc5-backoff", Namespace: vc5ns}, after)).To(Succeed())
		cfg := after.Spec.Roles[0].LeaderWorkerPattern.RestartPolicyConfig
		Expect(cfg).NotTo(BeNil())
		Expect(cfg.BaseDelaySeconds).NotTo(BeNil())
		Expect(*cfg.BaseDelaySeconds).To(Equal(int32(30)),
			"v1alpha1 has no field for the configured delay, so the round trip resets it to the default")
	})

	It("canary: a customComponentsPattern role is rejected outright on a v1alpha1 write", func() {
		rbg := &workloadsv1alpha2.RoleBasedGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "vc5-components", Namespace: vc5ns},
			Spec: workloadsv1alpha2.RoleBasedGroupSpec{
				Roles: []workloadsv1alpha2.RoleSpec{{
					Name:     "engine",
					Replicas: ptr.To(int32(1)),
					Pattern: workloadsv1alpha2.Pattern{
						CustomComponentsPattern: &workloadsv1alpha2.CustomComponentsPattern{
							Components: []workloadsv1alpha2.InstanceComponent{
								{Name: "router", Size: ptr.To(int32(1)), Template: *vc5Template()},
								{Name: "worker", Size: ptr.To(int32(1)), Template: *vc5Template()},
							},
						},
					},
				}},
			},
		}
		Expect(testutil.K8sClient.Create(testutil.Ctx, rbg)).To(Succeed())

		err := roundTrip("vc5-components")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("template or templateRef must be set"))
		Expect(strings.Contains(err.Error(), "Invalid") || strings.Contains(err.Error(), "denied")).To(BeTrue())
	})
})
