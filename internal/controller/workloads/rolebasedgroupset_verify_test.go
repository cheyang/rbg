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

package workloads

// Reviewer harness for PR #474 (RoleBasedGroupSet rolling update).
// Polarity is documented per test:
//   - "contract": asserts intended-correct behavior; RED on buggy code.
//   - "canary":   asserts the currently observed (suspect) behavior; flips RED
//                 when the behavior changes, and must then be inverted.
//
// Claims verified here:
//   P0  (canary, base-reproducible): the static path updates every outdated
//        child within a single Reconcile — the un-paced propagation the PR
//        claims to fix.
//   C1  (contract): with spec.rolloutStrategy set, one outdated child is
//        deleted per reconcile under maxUnavailable=1.
//   C2  (contract): repeated reconciles converge; the rollout never deletes
//        more than maxUnavailable ready children in one pass; A->B->A
//        converges back.
//   C3  (contract): enabling rolloutStrategy on legacy children whose content
//        already matches the template does not recreate them.
//   C4  (canary): the static path now overwrites the child's whole spec
//        (RoleTemplates included), which the PR body describes as unchanged.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func verifyRollingPodTemplate(image string) corev1.PodTemplateSpec {
	return corev1.PodTemplateSpec{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "c", Image: image}},
		},
	}
}

func verifyRollingSet(name, image string, replicas int32, strategy *workloadsv1alpha2.GroupSetRolloutStrategy) *workloadsv1alpha2.RoleBasedGroupSet {
	return &workloadsv1alpha2.RoleBasedGroupSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", UID: types.UID("verify-set-" + name)},
		Spec: workloadsv1alpha2.RoleBasedGroupSetSpec{
			Replicas: ptr.To(replicas),
			GroupTemplate: workloadsv1alpha2.RoleBasedGroupTemplateSpec{
				Spec: workloadsv1alpha2.RoleBasedGroupSpec{
					Roles: []workloadsv1alpha2.RoleSpec{{
						Name:     "worker",
						Replicas: ptr.To(int32(0)),
						Pattern: workloadsv1alpha2.Pattern{
							StandalonePattern: &workloadsv1alpha2.StandalonePattern{
								TemplateSource: workloadsv1alpha2.TemplateSource{
									Template: ptr.To(verifyRollingPodTemplate(image)),
								},
							},
						},
					}},
				},
			},
			RolloutStrategy: strategy,
		},
	}
}

func verifyReady(child *workloadsv1alpha2.RoleBasedGroup) *workloadsv1alpha2.RoleBasedGroup {
	child.Status = workloadsv1alpha2.RoleBasedGroupStatus{
		ObservedGeneration: child.Generation,
		Conditions: []metav1.Condition{{
			Type: string(workloadsv1alpha2.RoleBasedGroupReady), Status: metav1.ConditionTrue,
		}},
	}
	return child
}

// verifyChildren seeds children as the legacy/static path would have left them:
// spec copied from the template at `image`, name+index labels, Ready.
func verifyScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	return scheme
}

func verifyChildren(t *testing.T, set *workloadsv1alpha2.RoleBasedGroupSet, image string, count int) []client.Object {
	t.Helper()
	scheme := verifyScheme(t)
	objects := make([]client.Object, 0, count)
	for i := 0; i < count; i++ {
		child := newRBGForSet(set, i)
		child.Spec.Roles[0].Pattern.StandalonePattern.TemplateSource.Template = ptr.To(verifyRollingPodTemplate(image))
		require.NoError(t, controllerutil.SetControllerReference(set, child, scheme))
		verifyReady(child)
		objects = append(objects, child)
	}
	return objects
}

func verifyReconciler(t *testing.T, objects ...client.Object) (*RoleBasedGroupSetReconciler, client.Client) {
	t.Helper()
	scheme := verifyScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).
		WithStatusSubresource(&workloadsv1alpha2.RoleBasedGroupSet{}, &workloadsv1alpha2.RoleBasedGroup{}).Build()
	return &RoleBasedGroupSetReconciler{client: c, apiReader: c, scheme: scheme}, c
}

func verifyChildImages(c client.Client, set *workloadsv1alpha2.RoleBasedGroupSet) map[string]string {
	ctx := context.Background()
	children := &workloadsv1alpha2.RoleBasedGroupList{}
	_ = c.List(ctx, children, client.InNamespace(set.Namespace), client.MatchingLabels{constants.GroupSetNameLabelKey: set.Name})
	out := make(map[string]string, len(children.Items))
	for i := range children.Items {
		child := &children.Items[i]
		out[child.Name] = child.Spec.Roles[0].Pattern.StandalonePattern.TemplateSource.Template.Spec.Containers[0].Image
	}
	return out
}

// TestVerifyPR474_Premise_StaticPathUpdatesAllChildrenAtOnce is the P0 claim,
// verified against the BASE branch semantics: with no rolloutStrategy the
// static path rewrites every outdated child in a single Reconcile.
// Polarity: canary — documents the un-paced propagation the PR fixes.
func TestVerifyPR474_Premise_StaticPathUpdatesAllChildrenAtOnce(t *testing.T) {
	set := verifyRollingSet("premise", "v2", 3, nil)
	children := verifyChildren(t, set, "v1", 3)
	r, c := verifyReconciler(t, append([]client.Object{set}, children...)...)

	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(set)})
	require.NoError(t, err)

	got := verifyChildImages(c, set)
	require.Len(t, got, 3, "no child should be deleted on the static path")
	assert.Equal(t, map[string]string{
		"premise-0": "v2", "premise-1": "v2", "premise-2": "v2",
	}, got, "base semantics: every outdated child updated in one reconcile")
}

// TestVerifyPR474_Rollout_PacedRecreate: C1, polarity contract.
// maxUnavailable=1, three outdated ready children -> exactly one deletion per
// reconcile, and the replacement is created only once the ordinal is free.
func TestVerifyPR474_Rollout_PacedRecreate(t *testing.T) {
	strategy := &workloadsv1alpha2.GroupSetRolloutStrategy{
		Type:           workloadsv1alpha2.RecreateStrategyType,
		MaxUnavailable: ptr.To(intstr.FromInt32(1)),
	}
	set := verifyRollingSet("paced", "v2", 3, strategy)
	children := verifyChildren(t, set, "v1", 3)
	r, c := verifyReconciler(t, append([]client.Object{set}, children...)...)

	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(set)})
	require.NoError(t, err)

	got := verifyChildImages(c, set)
	assert.Len(t, got, 2, "exactly one child should be deleted per reconcile")
	assert.Equal(t, "v1", got["paced-0"], "the lowest ordinal must be held back first")
	assert.Equal(t, "v1", got["paced-1"])
	assert.NotContains(t, got, "paced-2", "the highest ordinal should roll first")

	// The replacement for ordinal 2 is not created until the ordinal is free —
	// and the deleted child is recreated at the update revision.
	_, err = r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(set)})
	require.NoError(t, err)
	got = verifyChildImages(c, set)
	assert.Equal(t, "v2", got["paced-2"], "the freed ordinal is refilled at the update revision")
	assert.Len(t, got, 3)
}

// TestVerifyPR474_Rollout_ConvergesUnderBudget: C2, polarity contract.
// Drives the full reconcile loop; the number of children removed in a single
// reconcile must never exceed maxUnavailable while children are ready, and the
// rollout must converge, including a flip back to the original template.
func TestVerifyPR474_Rollout_ConvergesUnderBudget(t *testing.T) {
	strategy := &workloadsv1alpha2.GroupSetRolloutStrategy{
		Type:           workloadsv1alpha2.RecreateStrategyType,
		MaxUnavailable: ptr.To(intstr.FromInt32(1)),
	}
	set := verifyRollingSet("converge", "v1", 3, strategy)
	children := verifyChildren(t, set, "v1", 3)
	r, c := verifyReconciler(t, append([]client.Object{set}, children...)...)

	markReady := func() {
		list := &workloadsv1alpha2.RoleBasedGroupList{}
		require.NoError(t, c.List(context.Background(), list, client.InNamespace(set.Namespace)))
		for i := range list.Items {
			child := list.Items[i].DeepCopy()
			verifyReady(child)
			require.NoError(t, c.Status().Update(context.Background(), child))
		}
	}

	// Roll v1 -> v2.
	updateTemplate := func(image string) {
		fresh := &workloadsv1alpha2.RoleBasedGroupSet{}
		require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(set), fresh))
		fresh.Spec.GroupTemplate.Spec.Roles[0].Pattern.StandalonePattern.TemplateSource.Template =
			ptr.To(verifyRollingPodTemplate(image))
		require.NoError(t, c.Update(context.Background(), fresh))
	}
	updateTemplate("v2")

	for step := 0; step < 30; step++ {
		before := verifyChildImages(c, set)
		_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(set)})
		require.NoError(t, err)
		after := verifyChildImages(c, set)
		removed := 0
		for name := range before {
			if _, ok := after[name]; !ok {
				removed++
			}
		}
		assert.LessOrEqual(t, removed, 1, "step %d: budget maxUnavailable=1 violated (%d removed)", step, removed)
		markReady()
		if len(after) == 3 && after["converge-0"] == "v2" && after["converge-1"] == "v2" && after["converge-2"] == "v2" {
			break
		}
	}
	assert.Equal(t, map[string]string{
		"converge-0": "v2", "converge-1": "v2", "converge-2": "v2",
	}, verifyChildImages(c, set), "rollout must converge to the update revision")

	// Flip back v2 -> v1 and converge again (revision identity is content-based).
	updateTemplate("v1")
	for step := 0; step < 30; step++ {
		before := verifyChildImages(c, set)
		_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(set)})
		require.NoError(t, err)
		after := verifyChildImages(c, set)
		removed := 0
		for name := range before {
			if _, ok := after[name]; !ok {
				removed++
			}
		}
		assert.LessOrEqual(t, removed, 1, "flip-back step %d: budget violated (%d removed)", step, removed)
		markReady()
		if len(after) == 3 && after["converge-0"] == "v1" && after["converge-1"] == "v1" && after["converge-2"] == "v1" {
			break
		}
	}
	assert.Equal(t, map[string]string{
		"converge-0": "v1", "converge-1": "v1", "converge-2": "v1",
	}, verifyChildImages(c, set), "A->B->A flip must converge back to A")
}

// TestVerifyPR474_Rollout_MigrationNoRecreate: C3, polarity contract.
// Enabling rolloutStrategy over legacy children whose content already matches
// the template must not recreate anything.
func TestVerifyPR474_Rollout_MigrationNoRecreate(t *testing.T) {
	strategy := &workloadsv1alpha2.GroupSetRolloutStrategy{
		Type:           workloadsv1alpha2.RecreateStrategyType,
		MaxUnavailable: ptr.To(intstr.FromInt32(1)),
	}
	set := verifyRollingSet("migrate", "v1", 3, strategy)
	children := verifyChildren(t, set, "v1", 3)
	r, c := verifyReconciler(t, append([]client.Object{set}, children...)...)

	for i := 0; i < 3; i++ {
		_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(set)})
		require.NoError(t, err)
	}

	got := verifyChildImages(c, set)
	assert.Len(t, got, 3, "no child may be recreated when content already matches")
	for i := range children {
		child := children[i].(*workloadsv1alpha2.RoleBasedGroup)
		fresh := &workloadsv1alpha2.RoleBasedGroup{}
		require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(child), fresh))
		assert.Equal(t, child.UID, fresh.UID, "child %s must keep its UID", child.Name)
	}
}

// TestVerifyPR474_StaticPathWipesChildRoleTemplates: C4, polarity canary.
// The static path now copies the whole GroupTemplate spec over the child, so a
// RoleTemplate set on the child out-of-band is wiped. If the project decides
// to preserve child-owned fields instead, this test flips to RED and must be
// inverted.
func TestVerifyPR474_StaticPathWipesChildRoleTemplates(t *testing.T) {
	set := verifyRollingSet("wipe", "v1", 1, nil)
	scheme := verifyScheme(t)
	child := newRBGForSet(set, 0)
	child.Spec.RoleTemplates = []workloadsv1alpha2.RoleTemplate{{
		Name:     "base",
		Template: verifyRollingPodTemplate("scratch"),
	}}
	require.NoError(t, controllerutil.SetControllerReference(set, child, scheme))
	verifyReady(child)
	r, c := verifyReconciler(t, set, child)

	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(set)})
	require.NoError(t, err)

	fresh := &workloadsv1alpha2.RoleBasedGroup{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(child), fresh))
	assert.Empty(t, fresh.Spec.RoleTemplates,
		"canary: the static path overwrites the whole child spec, dropping out-of-band RoleTemplates")
}

// TestVerifyPR474_Resolver_PercentZeroBecomesOne: C6, polarity canary.
// Round 2 note: the webhook now rejects the exact-zero spellings (F2 fixed,
// see the contract test in api/workloads/v1alpha2), so this input is only
// reachable through persisted objects or via a positive percentage that
// rounds down to zero; the resolver keeps coercing both-zero to 1 as
// defense-in-depth, which the author's envtest suite also pins
// ("legacy explicit zero percentages without admission"). Documents the
// persisted-object fallback — flips RED if the resolver starts rejecting
// this input instead, and must then be inverted.
func TestVerifyPR474_Resolver_PercentZeroBecomesOne(t *testing.T) {
	set := verifyRollingSet("resolve", "v1", 3, &workloadsv1alpha2.GroupSetRolloutStrategy{
		Type:           workloadsv1alpha2.RecreateStrategyType,
		MaxUnavailable: ptr.To(intstr.FromString("0%")),
		MaxSurge:       ptr.To(intstr.FromInt32(0)),
	})
	limits, err := resolveGroupSetRollout(set)
	require.NoError(t, err)
	assert.Equal(t, 0, limits.maxSurge)
	assert.Equal(t, 1, limits.maxUnavailable,
		"canary: requested 0%% unavailable resolves to 1 - the availability contract is silently violated")
}

// ── Round 2 (head 9e423e86): InPlaceUpdate rollout strategy ──────────────────
//
// C7 (contract): an InPlaceUpdate rollout never deletes an in-scope child; it
//      rewrites the child spec in place, highest ordinal first, at most
//      maxUnavailable ready children per pass, and holds back while an
//      already-updated child is still rolling (the child-level
//      RollingUpdateInProgress gate in groupSetChildReady).
// C8 (contract): the resolver routes ""/InPlaceUpdate to the in-place path,
//      ignores maxSurge, and keeps the both-zero fallback.
// C9 (contract): an unsupported strategy type is an error, not a silent
//      fallback to either path.

// verifyMarkChildRolling simulates what the real RBG controller publishes
// right after the set rewrote a child spec in place: the child's rollout is
// in progress, so it must stop counting as a ready serving group.
func verifyMarkChildRolling(t *testing.T, c client.Client, child *workloadsv1alpha2.RoleBasedGroup) {
	fresh := &workloadsv1alpha2.RoleBasedGroup{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(child), fresh))
	status := fresh.Status.DeepCopy()
	metaSet := func(conditions []metav1.Condition, cond metav1.Condition) []metav1.Condition {
		apimeta.SetStatusCondition(&conditions, cond)
		return conditions
	}
	status.Conditions = metaSet(status.Conditions, metav1.Condition{
		Type: string(workloadsv1alpha2.RoleBasedGroupRollingUpdateInProgress),
		Status: metav1.ConditionTrue, Reason: "RolesUpdating",
		LastTransitionTime: metav1.Now(),
	})
	fresh.Status = *status
	require.NoError(t, c.Status().Update(context.Background(), fresh))
}

// TestVerifyPR474_InPlaceUpdate_PacesSpecUpdates: C7, polarity contract.
func TestVerifyPR474_InPlaceUpdate_PacesSpecUpdates(t *testing.T) {
	set := verifyRollingSet("inplace-paced", "v2", 3, &workloadsv1alpha2.GroupSetRolloutStrategy{
		Type:           workloadsv1alpha2.InPlaceUpdateStrategyType,
		MaxUnavailable: ptr.To(intstr.FromInt32(1)),
	})
	children := verifyChildren(t, set, "v1", 3)
	uids := map[string]types.UID{}
	for _, obj := range children {
		child := obj.(*workloadsv1alpha2.RoleBasedGroup)
		uids[child.Name] = child.UID
	}
	r, c := verifyReconciler(t, append([]client.Object{set}, children...)...)

	updated := func() int {
		got := verifyChildImages(c, set)
		n := 0
		for _, image := range got {
			if image == "v2" {
				n++
			}
		}
		return n
	}

	// Pass 1: exactly one ready child is updated in place (highest ordinal
	// first); nothing is deleted.
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(set)})
	require.NoError(t, err)
	got := verifyChildImages(c, set)
	assert.Len(t, got, 3, "InPlaceUpdate must never delete an in-scope child")
	assert.Equal(t, "v2", got["inplace-paced-2"], "highest ordinal rolls first")
	assert.Equal(t, "v1", got["inplace-paced-1"])
	assert.Equal(t, "v1", got["inplace-paced-0"])
	assert.Equal(t, 1, updated())

	// While the updated child is still rolling, the availability floor holds:
	// no further child may be touched.
	verifyMarkChildRolling(t, c, &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "inplace-paced-2", Namespace: set.Namespace},
	})
	_, err = r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(set)})
	require.NoError(t, err)
	assert.Equal(t, 1, updated(), "no second child may start rolling while the first is in flight")

	// The rolled child finishes (RollingUpdateInProgress=False): the next
	// child can now be updated.
	//
	// Round 5 harness-update (head 4a9043c): the child RBG controller now
	// acknowledges a budgeted child write by echoing the
	// rbg.workloads.x-k8s.io/rollout-request annotation into
	// status.observedRolloutRequest (RoleBasedGroupReconciler.acknowledgeRolloutRequest).
	// groupSetChildReady will not count a child as ready until that ack
	// arrives, so the fixture must model it — conditions alone no longer
	// release the rollout slot.
	verifyMarkChildDone := func(name string, acknowledge bool) {
		fresh := &workloadsv1alpha2.RoleBasedGroup{}
		require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: name, Namespace: set.Namespace}, fresh))
		status := fresh.Status.DeepCopy()
		conditions := status.Conditions
		apimeta.SetStatusCondition(&conditions, metav1.Condition{
			Type: string(workloadsv1alpha2.RoleBasedGroupRollingUpdateInProgress),
			Status: metav1.ConditionFalse, Reason: "AllRolesUpdated",
			LastTransitionTime: metav1.Now(),
		})
		status.Conditions = conditions
		if acknowledge {
			status.ObservedRolloutRequest = fresh.Annotations[constants.RolloutRequestAnnotationKey]
		}
		fresh.Status = *status
		require.NoError(t, c.Status().Update(context.Background(), fresh))
	}
	// C10 (contract, new in 51bf17bb): a done-looking child that has not yet
	// echoed the rollout request must still hold the budget.
	verifyMarkChildDone("inplace-paced-2", false)
	_, err = r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(set)})
	require.NoError(t, err)
	assert.Equal(t, 1, updated(), "conditions alone must not release the slot; the request acknowledgement must")

	verifyMarkChildDone("inplace-paced-2", true)
	_, err = r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(set)})
	require.NoError(t, err)
	assert.Equal(t, 2, updated(), "the next child rolls once the previous is done")

	// Converge: all children at v2, every original UID preserved.
	for _, name := range []string{"inplace-paced-1", "inplace-paced-0"} {
		verifyMarkChildRolling(t, c, &workloadsv1alpha2.RoleBasedGroup{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: set.Namespace},
		})
		verifyMarkChildDone(name, true)
		_, err = r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(set)})
		require.NoError(t, err)
	}
	assert.Equal(t, map[string]string{
		"inplace-paced-0": "v2", "inplace-paced-1": "v2", "inplace-paced-2": "v2",
	}, verifyChildImages(c, set), "InPlaceUpdate rollout must converge")

	list := &workloadsv1alpha2.RoleBasedGroupList{}
	require.NoError(t, c.List(context.Background(), list, client.InNamespace(set.Namespace)))
	for i := range list.Items {
		child := &list.Items[i]
		assert.Equal(t, uids[child.Name], child.UID, "child %s must keep its UID (no recreate)", child.Name)
	}

	// The set reports the rollout complete once every child serves the new template.
	final := &workloadsv1alpha2.RoleBasedGroupSet{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(set), final))
	cond := apimeta.FindStatusCondition(final.Status.Conditions, string(workloadsv1alpha2.RoleBasedGroupSetRolling))
	require.NotNil(t, cond, "the set must publish a Rolling condition")
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, "RolloutComplete", cond.Reason)
}

// TestVerifyPR474_InPlaceUpdate_ResolverLimits: C8, polarity contract.
func TestVerifyPR474_InPlaceUpdate_ResolverLimits(t *testing.T) {
	// Empty type routes to the in-place path and ignores a persisted surge.
	set := verifyRollingSet("resolver", "v1", 3, &workloadsv1alpha2.GroupSetRolloutStrategy{
		MaxSurge: ptr.To(intstr.FromInt32(2)),
	})
	limits, err := resolveGroupSetRollout(set)
	require.NoError(t, err)
	assert.True(t, limits.inPlaceUpdate, "empty type must default to the in-place path")
	assert.Equal(t, 0, limits.maxSurge, "maxSurge is ignored for InPlaceUpdate")

	// Explicit InPlaceUpdate with a resolved-zero budget falls back to 1.
	set = verifyRollingSet("resolver2", "v1", 3, &workloadsv1alpha2.GroupSetRolloutStrategy{
		Type:           workloadsv1alpha2.InPlaceUpdateStrategyType,
		MaxUnavailable: ptr.To(intstr.FromString("1%")),
	})
	limits, err = resolveGroupSetRollout(set)
	require.NoError(t, err)
	assert.True(t, limits.inPlaceUpdate)
	assert.Equal(t, 1, limits.maxUnavailable, "1% of 3 replicas rounds to 0; the resolver enforces the minimum of 1")

	// Recreate keeps resolving surge.
	set = verifyRollingSet("resolver3", "v1", 3, &workloadsv1alpha2.GroupSetRolloutStrategy{
		Type:    workloadsv1alpha2.RecreateStrategyType,
		MaxSurge: ptr.To(intstr.FromInt32(2)),
	})
	limits, err = resolveGroupSetRollout(set)
	require.NoError(t, err)
	assert.False(t, limits.inPlaceUpdate)
	assert.Equal(t, 2, limits.maxSurge)
}

// TestVerifyPR474_Resolver_RejectsUnknownType: C9, polarity contract.
func TestVerifyPR474_Resolver_RejectsUnknownType(t *testing.T) {
	set := verifyRollingSet("resolver-bad", "v1", 3, &workloadsv1alpha2.GroupSetRolloutStrategy{
		Type: workloadsv1alpha2.GroupUpdateStrategyType("Bogus"),
	})
	_, err := resolveGroupSetRollout(set)
	assert.Error(t, err, "an unsupported strategy type must fail loudly, not fall back to either path")
}
