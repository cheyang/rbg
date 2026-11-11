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

import (
	"fmt"
	"sort"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

// Reviewer A round-3 envtests (PR #538, head 2c347c47). These are written
// independently of the PR's own selective_scale_down_test.go specs; they pin
// the two controller-chain contracts this round's fixes claim:
//
//  1. F5 (e340f9f7): an invalid exclusion input on one role must not stop
//     other roles from reconciling, nor stop cleanup of a removed role.
//  2. discovery-gates-adoption (eec29c95): an ownerless dense orphan child
//     must be adoptable through the full controller chain (Step-3 discovery
//     included), with the persisted discovery ConfigMap reflecting the
//     resulting sparse membership (the round-2 AC-10 envtest assertion).
var _ = Describe("Claude round-3 selective verification", func() {
	const timeout = 30 * time.Second
	const interval = 200 * time.Millisecond
	var namespace string
	BeforeEach(func() {
		namespace = fmt.Sprintf("claude-r3-%d", time.Now().UnixNano())
		testutil.CreateNamespace(namespace)
	})
	AfterEach(func() { testutil.DeleteNamespace(namespace) })

	members := func(key client.ObjectKey) []int {
		set := &workloadsv1alpha2.RoleInstanceSet{}
		if err := testutil.K8sClient.Get(testutil.Ctx, key, set); err != nil {
			return nil
		}
		values, err := set.CommittedOrdinals()
		if err != nil {
			return nil
		}
		return values
	}
	instanceNames := func(groupName string) []string {
		list := &workloadsv1alpha2.RoleInstanceList{}
		if err := testutil.K8sClient.List(testutil.Ctx, list,
			client.InNamespace(namespace), client.MatchingLabels{constants.GroupNameLabelKey: groupName}); err != nil {
			return nil
		}
		names := make([]string, 0, len(list.Items))
		for _, item := range list.Items {
			if item.DeletionTimestamp == nil {
				names = append(names, item.Name)
			}
		}
		sort.Strings(names)
		return names
	}
	updateRBG := func(key client.ObjectKey, mutate func(*workloadsv1alpha2.RoleBasedGroup)) {
		Eventually(func() error {
			return retry.RetryOnConflict(retry.DefaultRetry, func() error {
				latest := &workloadsv1alpha2.RoleBasedGroup{}
				if err := testutil.K8sClient.Get(testutil.Ctx, key, latest); err != nil {
					return err
				}
				mutate(latest)
				return testutil.K8sClient.Update(testutil.Ctx, latest)
			})
		}, timeout, interval).Should(Succeed())
	}

	It("keeps other roles and removed-role cleanup running while one role's exclusion input is invalid", func() {
		worker := wrappers.BuildStandaloneRole("worker").WithReplicas(3).Obj()
		spare := wrappers.BuildStandaloneRole("spare").WithReplicas(4).Obj()
		doomed := wrappers.BuildStandaloneRole("doomed").WithReplicas(2).Obj()
		group := wrappers.BuildBasicRoleBasedGroup("claude-f5", namespace).
			WithRoles([]workloadsv1alpha2.RoleSpec{worker, spare, doomed}).Obj()
		Expect(testutil.K8sClient.Create(testutil.Ctx, group)).To(Succeed())
		key := client.ObjectKeyFromObject(group)
		workerSet := client.ObjectKey{Namespace: namespace, Name: group.GetWorkloadName(&group.Spec.Roles[0])}
		spareSet := client.ObjectKey{Namespace: namespace, Name: group.GetWorkloadName(&group.Spec.Roles[1])}
		doomedSet := client.ObjectKey{Namespace: namespace, Name: group.GetWorkloadName(&group.Spec.Roles[2])}

		Eventually(func() []int { return members(workerSet) }, timeout, interval).Should(Equal([]int{0, 1, 2}))
		Eventually(func() []int { return members(spareSet) }, timeout, interval).Should(Equal([]int{0, 1, 2, 3}))
		frozenUIDs := map[string]types.UID{}
		for _, name := range instanceNames(group.Name) {
			instance := &workloadsv1alpha2.RoleInstance{}
			Expect(testutil.K8sClient.Get(testutil.Ctx, client.ObjectKey{Namespace: namespace, Name: name}, instance)).To(Succeed())
			frozenUIDs[name] = instance.UID
		}

		// One edit, three effects: worker gets INVALID exclusion input,
		// spare gets a valid selective scale-down, doomed is removed.
		updateRBG(key, func(g *workloadsv1alpha2.RoleBasedGroup) {
			g.Spec.Roles = g.Spec.Roles[:2]
			g.Spec.Roles[0].Replicas = ptr.To[int32](3)
			g.Spec.Roles[0].Annotations = map[string]string{constants.ExcludedOrdinalsAnnotationKey: "4-2"}
			g.Spec.Roles[1].Replicas = ptr.To[int32](2)
			g.Spec.Roles[1].Annotations = map[string]string{constants.ExcludedOrdinalsAnnotationKey: "0,3"}
		})

		// The RBG reports the invalid input without hard-blocking.
		Eventually(func() string {
			latest := &workloadsv1alpha2.RoleBasedGroup{}
			if err := testutil.K8sClient.Get(testutil.Ctx, key, latest); err != nil {
				return ""
			}
			condition := apimeta.FindStatusCondition(latest.Status.Conditions, string(workloadsv1alpha2.RoleBasedGroupReady))
			if condition == nil {
				return ""
			}
			return condition.Reason
		}, timeout, interval).Should(Equal("InvalidExcludedOrdinals"))

		// The valid role's selective scale-down proceeds.
		Eventually(func() []int { return members(spareSet) }, timeout, interval).Should(Equal([]int{1, 2}))

		// The removed role's workload is cleaned up (the round-2 F5 wedge).
		// envtest has no garbage collector: deleting the child RIS is the
		// controller-level cleanup; the cascade to RoleInstances/Pods is GC
		// work that only happens in a real cluster.
		Eventually(func() bool {
			return apierrors.IsNotFound(testutil.K8sClient.Get(testutil.Ctx, doomedSet, &workloadsv1alpha2.RoleInstanceSet{}))
		}, timeout, interval).Should(BeTrue())

		// The invalid role is frozen: membership and identities unchanged.
		Consistently(func() []int { return members(workerSet) }, 2*time.Second, interval).
			Should(Equal([]int{0, 1, 2}))
		for _, name := range []string{
			workerSet.Name + "-0", workerSet.Name + "-1", workerSet.Name + "-2",
		} {
			instance := &workloadsv1alpha2.RoleInstance{}
			Expect(testutil.K8sClient.Get(testutil.Ctx, client.ObjectKey{Namespace: namespace, Name: name}, instance)).To(Succeed())
			Expect(instance.UID).To(Equal(frozenUIDs[name]), "frozen role must keep its instance identities")
		}

		// Repairing the input unfreezes the role. Fixed-replicas E edits keep
		// M by contract, so the repair also scales down to make resumption
		// observable: replicas 3 -> 2 with E "0" retires ordinal 0 first.
		updateRBG(key, func(g *workloadsv1alpha2.RoleBasedGroup) {
			g.Spec.Roles[0].Replicas = ptr.To[int32](2)
			g.Spec.Roles[0].Annotations[constants.ExcludedOrdinalsAnnotationKey] = "0"
		})
		Eventually(func() []int { return members(workerSet) }, timeout, interval).Should(Equal([]int{1, 2}))
	})

	It("adopts an ownerless dense orphan through the full controller chain and publishes sparse discovery", func() {
		worker := wrappers.BuildStandaloneRole("worker").WithReplicas(5).Obj()
		group := wrappers.BuildBasicRoleBasedGroup("claude-orphan", namespace).
			WithRoles([]workloadsv1alpha2.RoleSpec{worker}).Obj()
		Expect(testutil.K8sClient.Create(testutil.Ctx, group)).To(Succeed())
		key := client.ObjectKeyFromObject(group)
		setKey := client.ObjectKey{Namespace: namespace, Name: group.GetWorkloadName(&group.Spec.Roles[0])}

		Eventually(func() []int { return members(setKey) }, timeout, interval).Should(Equal([]int{0, 1, 2, 3, 4}))
		dense := &workloadsv1alpha2.RoleInstanceSet{}
		Expect(testutil.K8sClient.Get(testutil.Ctx, setKey, dense)).To(Succeed())
		denseUID := dense.UID
		oldRBGUID := group.UID
		for _, ordinal := range []int{0, 1, 2, 3, 4} {
			Expect(instanceNames(group.Name)).To(ContainElement(fmt.Sprintf("%s-%d", setKey.Name, ordinal)))
		}
		survivors := map[string]types.UID{}
		for _, name := range instanceNames(group.Name) {
			instance := &workloadsv1alpha2.RoleInstance{}
			Expect(testutil.K8sClient.Get(testutil.Ctx, client.ObjectKey{Namespace: namespace, Name: name}, instance)).To(Succeed())
			survivors[name] = instance.UID
		}

		// Delete the RBG and model the orphan policy (envtest has no GC).
		Expect(testutil.K8sClient.Delete(testutil.Ctx, group)).To(Succeed())
		Eventually(func() bool {
			return apierrors.IsNotFound(testutil.K8sClient.Get(testutil.Ctx, key, &workloadsv1alpha2.RoleBasedGroup{}))
		}, timeout, interval).Should(BeTrue())
		stripOwner := func(obj client.Object) {
			refs := obj.GetOwnerReferences()
			kept := make([]metav1.OwnerReference, 0, len(refs))
			for _, ref := range refs {
				if ref.UID != oldRBGUID {
					kept = append(kept, ref)
				}
			}
			before := obj.DeepCopyObject().(client.Object)
			obj.SetOwnerReferences(kept)
			Expect(testutil.K8sClient.Patch(testutil.Ctx, obj, client.MergeFrom(before))).To(Succeed())
		}
		orphaned := &workloadsv1alpha2.RoleInstanceSet{}
		Expect(testutil.K8sClient.Get(testutil.Ctx, setKey, orphaned)).To(Succeed())
		stripOwner(orphaned)
		services := &corev1.ServiceList{}
		Expect(testutil.K8sClient.List(testutil.Ctx, services, client.InNamespace(namespace))).To(Succeed())
		for i := range services.Items {
			stripOwner(&services.Items[i])
		}
		configMaps := &corev1.ConfigMapList{}
		Expect(testutil.K8sClient.List(testutil.Ctx, configMaps, client.InNamespace(namespace))).To(Succeed())
		for i := range configMaps.Items {
			stripOwner(&configMaps.Items[i])
		}
		// A real orphan-policy delete also orphans the ControllerRevisions;
		// leaving them owned by the deleted RBG wedges revision creation for
		// the recreated RBG (Step 1), which is a different failure family.
		revisions := &appsv1.ControllerRevisionList{}
		Expect(testutil.K8sClient.List(testutil.Ctx, revisions, client.InNamespace(namespace))).To(Succeed())
		for i := range revisions.Items {
			stripOwner(&revisions.Items[i])
		}

		// Recreate the same-name RBG with exclusions: the round-2 wedge had
		// Step-3 discovery reject the ownerless child before role reconcile.
		worker.Replicas = ptr.To[int32](3)
		worker.Annotations = map[string]string{constants.ExcludedOrdinalsAnnotationKey: "1,3"}
		recreated := wrappers.BuildBasicRoleBasedGroup("claude-orphan", namespace).
			WithRoles([]workloadsv1alpha2.RoleSpec{worker}).Obj()
		Expect(testutil.K8sClient.Create(testutil.Ctx, recreated)).To(Succeed())

		// The child is adopted, not recreated, and commits sparse membership.
		Eventually(func(g Gomega) {
			adopted := &workloadsv1alpha2.RoleInstanceSet{}
			g.Expect(testutil.K8sClient.Get(testutil.Ctx, setKey, adopted)).To(Succeed())
			g.Expect(adopted.UID).To(Equal(denseUID), "the dense orphan must be adopted, not replaced")
			owner := metav1.GetControllerOf(adopted)
			g.Expect(owner).NotTo(BeNil())
			g.Expect(owner.UID).To(Equal(recreated.UID))
			g.Expect(members(setKey)).To(Equal([]int{0, 2, 4}))
		}, timeout, interval).Should(Succeed())

		// Survivors keep their identities; retired ordinals 1 and 3 go away.
		Eventually(func() []string { return instanceNames(recreated.Name) }, timeout, interval).Should(Equal(
			[]string{setKey.Name + "-0", setKey.Name + "-2", setKey.Name + "-4"},
		))
		for _, ordinal := range []int{0, 2, 4} {
			name := fmt.Sprintf("%s-%d", setKey.Name, ordinal)
			instance := &workloadsv1alpha2.RoleInstance{}
			Expect(testutil.K8sClient.Get(testutil.Ctx, client.ObjectKey{Namespace: namespace, Name: name}, instance)).To(Succeed())
			Expect(instance.UID).To(Equal(survivors[name]))
		}

		// The round-2 AC-10 gap: the persisted discovery ConfigMap must
		// reflect the sparse membership through the full controller chain.
		Eventually(func(g Gomega) {
			cm := &corev1.ConfigMap{}
			g.Expect(testutil.K8sClient.Get(testutil.Ctx, key, cm)).To(Succeed())
			var config discovery.ClusterConfig
			g.Expect(yaml.UnmarshalStrict([]byte(cm.Data["config.yaml"]), &config)).To(Succeed())
			g.Expect(config.Roles["worker"].Instances).To(Equal([]discovery.Instance{
				{Address: setKey.Name + "-0.s-" + setKey.Name},
				{Address: setKey.Name + "-2.s-" + setKey.Name},
				{Address: setKey.Name + "-4.s-" + setKey.Name},
			}))
		}, timeout, interval).Should(Succeed())
	})
})
