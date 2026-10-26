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

// Reviewer verification harness for PR #526 / issue #517.
//
// These specs are additive review artifacts; production code is untouched.
// Run the whole package normally (the specs are part of the suite) or focus
// with: go test ./test/envtest/testcase/rbg/ -ginkgo.focus "v1alpha1 coordination migration"
//
// Polarity:
//   - "contract: ..." asserts the intended behavior (green = fixed, red = broken).
//   - "canary: ..." asserts the currently observed (residual-gap) behavior;
//     green today, flips to red when the gap is closed — invert it then.

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	workloadsv1alpha1 "sigs.k8s.io/rbgs/api/workloads/v1alpha1"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	workloadscontroller "sigs.k8s.io/rbgs/internal/controller/workloads"
	"sigs.k8s.io/rbgs/test/envtest/testutil"
)

var _ = Describe("v1alpha1 coordination migration", Label("verify:coordname"), func() {
	const (
		vTimeout  = time.Second * 30
		vInterval = time.Millisecond * 250
	)

	var vNs string

	BeforeEach(func() {
		vNs = fmt.Sprintf("test-rbg-verify-coord-%d", time.Now().UnixNano())
		testutil.CreateNamespace(vNs)
	})

	AfterEach(func() {
		testutil.DeleteNamespace(vNs)
	})

	// vSrc builds a v1alpha1 RoleBasedGroup and converts it to the hub version
	// the conversion webhook would have stored (envtest runs no webhook).
	vSrc := func(name string, coords []workloadsv1alpha1.Coordination, roleNames []string) *workloadsv1alpha2.RoleBasedGroup {
		src := &workloadsv1alpha1.RoleBasedGroup{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: vNs},
			Spec: workloadsv1alpha1.RoleBasedGroupSpec{
				CoordinationRequirements: coords,
			},
		}
		for _, rn := range roleNames {
			src.Spec.Roles = append(src.Spec.Roles, workloadsv1alpha1.RoleSpec{
				Name:           rn,
				Replicas:       ptr.To(int32(1)),
				TemplateSource: workloadsv1alpha1.TemplateSource{Template: ptr.To(nginxPodTemplate())},
			})
		}
		hub := &workloadsv1alpha2.RoleBasedGroup{}
		Expect(src.ConvertTo(hub)).To(Succeed())
		return hub
	}

	// contract: every policy the migration layer applies must carry its
	// list-map key, so the server-side apply against a real apiserver is
	// accepted and the reconcile proceeds past step 5 to create workloads.
	// Two entries also exercise the associative-list merge with >1 element,
	// which the PR's own test (single policy) does not cover.
	It("contract: names every applied policy so the apply is accepted and workloads appear", func() {
		const rbgName = "verify-coord-two"
		roleNames := []string{"prefill", "decode", "router"}
		maxSkewA, maxSkewB := "50%", "25%"

		hub := vSrc(rbgName, []workloadsv1alpha1.Coordination{
			{
				Name:  "pd-ratio",
				Roles: []string{"prefill", "decode"},
				Strategy: &workloadsv1alpha1.CoordinationStrategy{
					RollingUpdate: &workloadsv1alpha1.CoordinationRollingUpdate{MaxSkew: &maxSkewA},
				},
			},
			{
				Name:  "router-ratio",
				Roles: []string{"router"},
				Strategy: &workloadsv1alpha1.CoordinationStrategy{
					RollingUpdate: &workloadsv1alpha1.CoordinationRollingUpdate{MaxSkew: &maxSkewB},
				},
			},
		}, roleNames)
		Expect(testutil.K8sClient.Create(testutil.Ctx, hub)).To(Succeed())

		policy := &workloadsv1alpha2.CoordinatedPolicy{}
		Eventually(func() error {
			return testutil.K8sClient.Get(testutil.Ctx, types.NamespacedName{Name: rbgName, Namespace: vNs}, policy)
		}, vTimeout, vInterval).Should(Succeed())

		Expect(policy.Spec.Policies).To(HaveLen(2))
		byName := map[string]workloadsv1alpha2.CoordinatedPolicyRule{}
		for _, p := range policy.Spec.Policies {
			byName[p.Name] = p
		}
		Expect(byName).To(HaveKey("pd-ratio"))
		Expect(byName).To(HaveKey("router-ratio"))
		Expect(byName["pd-ratio"].Roles).To(ConsistOf("prefill", "decode"))
		Expect(byName["pd-ratio"].Strategy.RollingUpdate.MaxSkew.String()).To(Equal("50%"))
		Expect(byName["router-ratio"].Strategy.RollingUpdate.MaxSkew.String()).To(Equal("25%"))

		// The rejected apply used to abort the reconcile before step 8, so no
		// workload was created at all — the symptom reported in #517.
		for i := range hub.Spec.Roles {
			role := &hub.Spec.Roles[i]
			ris := &workloadsv1alpha2.RoleInstanceSet{}
			Eventually(func() error {
				return testutil.K8sClient.Get(testutil.Ctx, types.NamespacedName{
					Name: hub.GetWorkloadName(role), Namespace: vNs,
				}, ris)
			}, vTimeout, vInterval).Should(Succeed(), "no workload created for role %s", role.Name)
		}
	})

	// canary: v1alpha1 Coordination.Strategy is optional, but the v1alpha2
	// CoordinatedPolicyRule requires it, so a strategy-less coordination is
	// still rejected by the real apiserver and the RBG it belongs to still
	// gets no workloads. The fake-client unit tests cannot see this (they
	// construct exactly this input and pass). Asserts current behavior:
	// green while the gap exists, red once it is closed — invert then.
	It("canary: a strategy-less v1alpha1 coordination is still rejected by the apiserver", func() {
		const rbgName = "verify-coord-bare"
		hub := vSrc(rbgName, []workloadsv1alpha1.Coordination{
			{Name: "bare", Roles: []string{"prefill"}},
		}, []string{"prefill"})
		Expect(testutil.K8sClient.Create(testutil.Ctx, hub)).To(Succeed())

		// Re-fetch so the object carries the UID the ownerReference needs.
		live := &workloadsv1alpha2.RoleBasedGroup{}
		Expect(testutil.K8sClient.Get(testutil.Ctx, types.NamespacedName{Name: rbgName, Namespace: vNs}, live)).To(Succeed())

		err := workloadscontroller.EnsureV1alpha1CoordinatedPolicy(testutil.Ctx, testutil.K8sClient, live)
		By(fmt.Sprintf("EnsureV1alpha1CoordinatedPolicy returned: %v", err))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("strategy"))

		policy := &workloadsv1alpha2.CoordinatedPolicy{}
		err = testutil.K8sClient.Get(testutil.Ctx, types.NamespacedName{Name: rbgName, Namespace: vNs}, policy)
		Expect(err).To(HaveOccurred(), "no CoordinatedPolicy should exist for a strategy-less coordination")
	})
})
