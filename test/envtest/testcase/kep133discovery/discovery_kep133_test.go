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

package kep133discovery

import (
	"fmt"
	"sort"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/pkg/discovery"
	"sigs.k8s.io/rbgs/test/envtest/testutil"
	wrappers "sigs.k8s.io/rbgs/test/wrappers/v1alpha2"
	"sigs.k8s.io/yaml"
)

// These specs verify claims made by keps/133-configmap-refine/README.md
// (PR #570) against the real controller stack.
var _ = Describe("KEP-133 claims vs current controller", func() {
	const timeout = 60 * time.Second
	const interval = 300 * time.Millisecond

	var namespace string
	BeforeEach(func() {
		namespace = fmt.Sprintf("kep133-%d", time.Now().UnixNano())
		testutil.CreateNamespace(namespace)
	})
	AfterEach(func() { testutil.DeleteNamespace(namespace) })

	getCM := func(key client.ObjectKey) (*corev1.ConfigMap, error) {
		cm := &corev1.ConfigMap{}
		err := testutil.K8sClient.Get(testutil.Ctx, key, cm)
		return cm, err
	}
	parseInstances := func(cm *corev1.ConfigMap, role string) []string {
		var cfg discovery.ClusterConfig
		if err := yaml.Unmarshal([]byte(cm.Data["config.yaml"]), &cfg); err != nil {
			return nil
		}
		var out []string
		for _, inst := range cfg.Roles[role].Instances {
			out = append(out, inst.Address)
		}
		sort.Strings(out)
		return out
	}
	// The RBG controller reconciles on SPEC changes only (RBGPredicate), and
	// does not watch ConfigMaps at all - so a drift/overwrite of the shared
	// ConfigMap is only corrected when the next RBG spec event lands (scale,
	// rollout, ...). A spec nudge is what a real post-downgrade reconcile
	// looks like.
	nudgeSpec := func(key client.ObjectKey) {
		Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
			latest := &workloadsv1alpha2.RoleBasedGroup{}
			if err := testutil.K8sClient.Get(testutil.Ctx, key, latest); err != nil {
				return err
			}
			r := *latest.Spec.Roles[0].Replicas
			latest.Spec.Roles[0].Replicas = ptr.To(r + 1)
			return testutil.K8sClient.Update(testutil.Ctx, latest)
		})).To(Succeed())
	}

	// CANARY pin (passes today): evidence that KEP-133's "Today" section is
	// stale - one RBG-level ConfigMap with relative addresses is already the
	// default for new RBGs.
	It("F2/F4: a new RBG already gets a single RBG-level ConfigMap with relative addresses", func() {
		router := wrappers.BuildStandaloneRole("router").WithReplicas(2).Obj()
		prefill := wrappers.BuildLeaderWorkerRole("prefill").WithReplicas(1).WithSize(2).Obj()
		group := wrappers.BuildBasicRoleBasedGroup("group", namespace).
			WithRoles([]workloadsv1alpha2.RoleSpec{router, prefill}).Obj()
		Expect(testutil.K8sClient.Create(testutil.Ctx, group)).To(Succeed())
		key := client.ObjectKeyFromObject(group)

		Eventually(func(g Gomega) {
			latest := &workloadsv1alpha2.RoleBasedGroup{}
			g.Expect(testutil.K8sClient.Get(testutil.Ctx, key, latest)).To(Succeed())
			g.Expect(latest.GetDiscoveryConfigMode()).To(Equal(constants.RefineDiscoveryConfigMode))
			cm, err := getCM(types.NamespacedName{Name: group.Name, Namespace: namespace})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(parseInstances(cm, "router")).To(Equal([]string{
				"group-router-0.s-group-router",
				"group-router-1.s-group-router",
			}))
			g.Expect(parseInstances(cm, "prefill")).To(Equal([]string{
				"group-prefill-0-0.s-group-prefill",
				"group-prefill-0-1.s-group-prefill",
			}))
		}, timeout, interval).Should(Succeed())

		Consistently(func() int {
			list := &corev1.ConfigMapList{}
			if err := testutil.K8sClient.List(testutil.Ctx, list, client.InNamespace(namespace)); err != nil {
				return -1
			}
			return len(list.Items)
		}, 5*time.Second, interval).Should(Equal(1), "exactly one RBG-level ConfigMap should exist; no per-role ConfigMaps")
	})

	// CONTRACT test (FAILS on current main == reproduction of the KEP's false
	// version-skew claim): seed the shared ConfigMap with the KEP-133 compact
	// schema; per the KEP, "older controllers ignore it". The current
	// controller (the downgrade target) must leave the compact content intact
	// for the KEP's downgrade story to hold.
	It("F3: the current controller preserves compact-schema content in the shared ConfigMap", func() {
		role := wrappers.BuildStandaloneRole("worker").WithReplicas(1).Obj()
		group := wrappers.BuildBasicRoleBasedGroup("skew", namespace).
			WithRoles([]workloadsv1alpha2.RoleSpec{role}).Obj()
		Expect(testutil.K8sClient.Create(testutil.Ctx, group)).To(Succeed())
		key := client.ObjectKeyFromObject(group)
		cmKey := types.NamespacedName{Name: group.Name, Namespace: namespace}

		Eventually(func(g Gomega) {
			_, err := getCM(cmKey)
			g.Expect(err).NotTo(HaveOccurred())
		}, timeout, interval).Should(Succeed())

		compactDoc := "format: refine\nnamespace: " + namespace + "\ngroup: skew\nroles: []\n"
		Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
			cm, err := getCM(cmKey)
			if err != nil {
				return err
			}
			cm.Data["config.yaml"] = compactDoc
			return testutil.K8sClient.Update(testutil.Ctx, cm)
		})).To(Succeed())
		nudgeSpec(key) // any post-downgrade reconcile (here: scale 1 -> 2)

		Consistently(func() string {
			cm, err := getCM(cmKey)
			if err != nil {
				return ""
			}
			return cm.Data["config.yaml"]
		}, 10*time.Second, interval).Should(Equal(compactDoc),
			"KEP-133 says older controllers do not manage/overwrite this object; "+
				"if the current controller rewrites it, the downgrade story is broken")
	})

	// CANARY pin (passes today): with KEP-499 exclusions at creation, the real
	// stack commits membership [0,2,4] and the discovery ConfigMap publishes
	// all three - including ordinal 4, which lies outside [0, replicas=3).
	// KEP-133's documented render rule (dense [0,replicas) minus exclusions)
	// cannot express this state.
	It("F1: discovery publishes committed members beyond [0, replicas)", func() {
		role := wrappers.BuildStandaloneRole("worker").WithReplicas(3).Obj()
		role.Annotations = map[string]string{constants.ExcludedOrdinalsAnnotationKey: "1,3"}
		group := wrappers.BuildBasicRoleBasedGroup("group", namespace).
			WithRoles([]workloadsv1alpha2.RoleSpec{role}).Obj()
		Expect(testutil.K8sClient.Create(testutil.Ctx, group)).To(Succeed())
		key := client.ObjectKeyFromObject(group)
		setKey := client.ObjectKey{Namespace: namespace, Name: group.GetWorkloadName(&role)}

		Eventually(func(g Gomega) {
			set := &workloadsv1alpha2.RoleInstanceSet{}
			g.Expect(testutil.K8sClient.Get(testutil.Ctx, setKey, set)).To(Succeed())
			members, err := set.CommittedOrdinals()
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(members).To(Equal([]int{0, 2, 4}))
			cm, err := getCM(types.NamespacedName{Name: group.Name, Namespace: namespace})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(parseInstances(cm, "worker")).To(Equal([]string{
				setKey.Name + "-0.s-" + setKey.Name,
				setKey.Name + "-2.s-" + setKey.Name,
				setKey.Name + "-4.s-" + setKey.Name,
			}))
		}, timeout, interval).Should(Succeed())

		// Scale 3 -> 4 with E unchanged: KEP-499 keeps existing members and
		// allocates the lowest unused non-excluded ordinal, so M becomes
		// [0,2,4,5] - still not expressible as "dense [0, replicas) minus
		// exclusions" (KEP-133's documented rule would render {0,2}).
		nudgeSpec(key)
		Eventually(func(g Gomega) {
			set := &workloadsv1alpha2.RoleInstanceSet{}
			g.Expect(testutil.K8sClient.Get(testutil.Ctx, setKey, set)).To(Succeed())
			members, err := set.CommittedOrdinals()
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(members).To(Equal([]int{0, 2, 4, 5}))
		}, timeout, interval).Should(Succeed())
		Eventually(func(g Gomega) {
			cm, err := getCM(types.NamespacedName{Name: group.Name, Namespace: namespace})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(parseInstances(cm, "worker")).To(Equal([]string{
				setKey.Name + "-0.s-" + setKey.Name,
				setKey.Name + "-2.s-" + setKey.Name,
				setKey.Name + "-4.s-" + setKey.Name,
				setKey.Name + "-5.s-" + setKey.Name,
			}))
		}, timeout, interval).Should(Succeed())
	})
})
