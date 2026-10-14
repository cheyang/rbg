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

// Reviewer harness for PR #474, round 4 — the 8c024b28 delta
// ("fix(rbgs): exclude conversion annotations from RBGSet revision and sync").
//
// The delta adds the three conversion annotation keys to
// systemManagedAnnotationKeys, which excludes them from the revision hash
// (normalizedGroupSetTemplate), from the child/template drift comparison, and
// from syncRBGMetadata's wipe of child annotations.
//
// Claims verified here (polarity per test):
//   C10 (contract, revision-regression): a v1alpha1 write that changes
//        CoordinationRequirements changes the stored hub template's
//        conversion annotation (rolebasedgroupset_conversion.go ConvertTo
//        injects it into GroupTemplate.Annotations so children can carry it).
//        The rolling path must converge existing children to the new value.
//        On 98720938 the annotation was part of the revision hash, so the
//        change rolled out; on 8c024b28 it is excluded, the revision name
//        does not change, up-to-date children are skipped by
//        classifyGroupSetChildrenForRollout, and nothing propagates.
//   C11 (contract, the delta's own claim): the revision hash ignores the
//        conversion annotations — the same template with and without them
//        resolves to the same revision name. This is the coverage the delta
//        itself did not add.
//   C12 (canary): a child whose revision label was computed by the pre-delta
//        hash (annotation included) is re-rolled once on the first reconcile
//        after the controller upgrade, even though its content is identical
//        to the template. Documents the one-time migration churn; if the
//        author adds seamless migration, this flips and must be inverted.
//   C13 (contract): conversion-stash annotations on a child (from an
//        out-of-band v1alpha1 write of the child) survive a rolling update.
//        v1alpha1-pod-group-policy is NOT in systemManagedAnnotationKeys, so
//        syncRBGMetadata still wipes it — the exclusion list is incomplete
//        for the same class of keys.

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha1 "sigs.k8s.io/rbgs/api/workloads/v1alpha1"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

const verifyPodGroupPolicyStash = `{"kubeScheduling":{"scheduleTimeoutSeconds":60}}`

// The conversion annotation keys as literal strings so this file compiles on
// both sides of the 8c024b28 delta (the constants were added by the delta;
// the key values are identical on both heads).
const (
	verifyCoordinationAnnKey    = "conversion.workloads.x-k8s.io/v1alpha1-coordination"
	verifyRolloutStrategyAnnKey = "conversion.workloads.x-k8s.io/v1alpha2-rollout-strategy"
	verifyRolloutStatusAnnKey   = "conversion.workloads.x-k8s.io/v1alpha2-rollout-status"
	verifyPodGroupPolicyAnnKey  = "conversion.workloads.x-k8s.io/v1alpha1-pod-group-policy"
)

// verifyWriteThroughV1alpha1 simulates the API-server path of a v1alpha1
// full-object write: hub -> v1alpha1 view (read), apply the edit, view ->
// hub (write). Returns the stored hub object.
func verifyWriteThroughV1alpha1(
	t *testing.T, stored *workloadsv1alpha2.RoleBasedGroupSet,
	edit func(view *workloadsv1alpha1.RoleBasedGroupSet),
) *workloadsv1alpha2.RoleBasedGroupSet {
	t.Helper()
	view := &workloadsv1alpha1.RoleBasedGroupSet{}
	require.NoError(t, view.ConvertFrom(stored.DeepCopy()))
	edit(view)
	hub := &workloadsv1alpha2.RoleBasedGroupSet{}
	require.NoError(t, view.ConvertTo(hub))
	return hub
}

func verifyInPlaceStrategy() *workloadsv1alpha2.GroupSetRolloutStrategy {
	return &workloadsv1alpha2.GroupSetRolloutStrategy{
		Type:           workloadsv1alpha2.InPlaceUpdateStrategyType,
		MaxUnavailable: ptr.To(intstr.FromInt32(1)),
	}
}

func verifyReconcileN(t *testing.T, r *RoleBasedGroupSetReconciler, set *workloadsv1alpha2.RoleBasedGroupSet, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(set)})
		require.NoError(t, err)
	}
}

// verifyMarkChildrenReady flips every child of the set to Ready with the
// rolling condition settled — the child (RBG) controller's job, simulated.
func verifyMarkChildrenReady(t *testing.T, c client.Client, set *workloadsv1alpha2.RoleBasedGroupSet) {
	t.Helper()
	list := &workloadsv1alpha2.RoleBasedGroupList{}
	require.NoError(t, c.List(context.Background(), list, client.InNamespace(set.Namespace),
		client.MatchingLabels{constants.GroupSetNameLabelKey: set.Name}))
	for i := range list.Items {
		child := list.Items[i].DeepCopy()
		child.Status.ObservedGeneration = child.Generation
		apimeta.SetStatusCondition(&child.Status.Conditions, metav1.Condition{
			Type: string(workloadsv1alpha2.RoleBasedGroupReady), Status: metav1.ConditionTrue,
			LastTransitionTime: metav1.Now(),
		})
		apimeta.SetStatusCondition(&child.Status.Conditions, metav1.Condition{
			Type: string(workloadsv1alpha2.RoleBasedGroupRollingUpdateInProgress), Status: metav1.ConditionFalse,
			Reason: "AllRolesUpdated", ObservedGeneration: child.Generation, LastTransitionTime: metav1.Now(),
		})
		require.NoError(t, c.Status().Update(context.Background(), child))
	}
}

func verifyFirstChild(t *testing.T, c client.Client, set *workloadsv1alpha2.RoleBasedGroupSet) *workloadsv1alpha2.RoleBasedGroup {
	t.Helper()
	list := &workloadsv1alpha2.RoleBasedGroupList{}
	require.NoError(t, c.List(context.Background(), list, client.InNamespace(set.Namespace),
		client.MatchingLabels{constants.GroupSetNameLabelKey: set.Name}))
	require.Len(t, list.Items, 1, "expected exactly one child")
	return list.Items[0].DeepCopy()
}

// TestVerifyPR474_Rolling_CoordinationChangePropagatesToChildren is C10,
// polarity contract. The full user flow: the set is written through the
// still-served v1alpha1 API, the template carries the coordination conversion
// annotation, children converge; the user then changes CoordinationRequirements
// through v1alpha1 and the controller must converge existing children.
func TestVerifyPR474_Rolling_CoordinationChangePropagatesToChildren(t *testing.T) {
	coordA := []workloadsv1alpha1.Coordination{{Name: "coord-a", Roles: []string{"worker"}}}
	coordB := []workloadsv1alpha1.Coordination{{Name: "coord-b", Roles: []string{"worker"}}}
	wantA, err := json.Marshal(coordA)
	require.NoError(t, err)
	wantB, err := json.Marshal(coordB)
	require.NoError(t, err)

	set := verifyRollingSet("coord-prop", "v1", 1, verifyInPlaceStrategy())
	stored := verifyWriteThroughV1alpha1(t, set, func(view *workloadsv1alpha1.RoleBasedGroupSet) {
		view.Spec.Template.CoordinationRequirements = coordA
	})
	require.NotEmpty(t, stored.Spec.GroupTemplate.Annotations[verifyCoordinationAnnKey],
		"the v1alpha1 write must inject the coordination annotation into the stored template")

	r, c := verifyReconciler(t, stored)
	verifyReconcileN(t, r, stored, 1) // children are created
	verifyMarkChildrenReady(t, c, stored)
	verifyReconcileN(t, r, stored, 2)

	child := verifyFirstChild(t, c, stored)
	assert.Equal(t, string(wantA),
		child.Annotations[verifyCoordinationAnnKey],
		"children must carry the v1alpha1 coordination stash from creation")

	// The v1alpha1 user now changes the coordination requirements.
	current := &workloadsv1alpha2.RoleBasedGroupSet{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(stored), current))
	updated := verifyWriteThroughV1alpha1(t, current, func(view *workloadsv1alpha1.RoleBasedGroupSet) {
		view.Spec.Template.CoordinationRequirements = coordB
	})
	current.Spec = updated.Spec
	current.Annotations = updated.Annotations
	require.NoError(t, c.Update(context.Background(), current))

	// Plenty of reconciles to converge (paced updates would still finish).
	for i := 0; i < 5; i++ {
		verifyMarkChildrenReady(t, c, stored)
		verifyReconcileN(t, r, stored, 1)
	}

	child = verifyFirstChild(t, c, stored)
	assert.Equal(t, string(wantB),
		child.Annotations[verifyCoordinationAnnKey],
		"a CoordinationRequirements change through v1alpha1 must reach existing children")
}

// TestVerifyPR474_RevisionHash_IgnoresConversionAnnotations is C11, polarity
// contract — the delta's own claim, which the delta did not test.
func TestVerifyPR474_RevisionHash_IgnoresConversionAnnotations(t *testing.T) {
	set := verifyRollingSet("hash-excl", "v1", 1, verifyInPlaceStrategy())
	r, _ := verifyReconciler(t, set)
	ctx := context.Background()

	plain := set.Spec.GroupTemplate.DeepCopy()
	withAnn := set.Spec.GroupTemplate.DeepCopy()
	withAnn.Annotations = map[string]string{
		verifyCoordinationAnnKey:    `[{"name":"c","roles":["worker"]}]`,
		verifyRolloutStrategyAnnKey: `{"type":"Recreate"}`,
		verifyRolloutStatusAnnKey:   `{"updatedReplicas":1}`,
	}

	revPlain, err := r.ensureGroupSetRevision(ctx, set, plain)
	require.NoError(t, err)
	revAnn, err := r.ensureGroupSetRevision(ctx, set, withAnn)
	require.NoError(t, err)
	assert.Equal(t, revPlain.name, revAnn.name,
		"conversion annotations must not change the revision name")
}

// verifyLegacyRevisionName recomputes the revision name the PRE-delta
// controller produced for a template carrying the coordination annotation:
// the head's normalization with the excluded annotation re-added before
// hashing.
func verifyLegacyRevisionName(set *workloadsv1alpha2.RoleBasedGroupSet) (string, error) {
	tpl := normalizedGroupSetTemplate(&set.Spec.GroupTemplate)
	if tpl.Annotations == nil {
		tpl.Annotations = map[string]string{}
	}
	tpl.Annotations[verifyCoordinationAnnKey] =
		set.Spec.GroupTemplate.Annotations[verifyCoordinationAnnKey]
	data, err := json.Marshal(tpl)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(append([]byte(string(set.UID)+"\x00"), data...))
	return fmt.Sprintf("rbgs-%x", hash[:24]), nil
}

// TestVerifyPR474_Canary_LegacyRevisionLabelRerolledOnce is C12, polarity
// canary: it pins the currently observed migration behavior. A set stored by
// the pre-delta controller (children labeled with the annotation-inclusive
// hash) is re-rolled on the first reconcile after the upgrade even though
// the child content is identical to the template.
func TestVerifyPR474_Canary_LegacyRevisionLabelRerolledOnce(t *testing.T) {
	coordA := []workloadsv1alpha1.Coordination{{Name: "coord-a", Roles: []string{"worker"}}}
	set := verifyRollingSet("migrate", "v1", 1, verifyInPlaceStrategy())
	stored := verifyWriteThroughV1alpha1(t, set, func(view *workloadsv1alpha1.RoleBasedGroupSet) {
		view.Spec.Template.CoordinationRequirements = coordA
	})
	legacyName, err := verifyLegacyRevisionName(stored)
	require.NoError(t, err)

	// A child exactly as the pre-delta controller left it: template content,
	// coordination annotation, legacy revision label, Ready.
	scheme := verifyScheme(t)
	child := newRBGForSet(stored, 0)
	child.Labels[constants.GroupSetRevisionLabelKey] = legacyName
	require.NoError(t, controllerutil.SetControllerReference(stored, child, scheme))
	verifyReady(child)

	r, c := verifyReconciler(t, append([]client.Object{stored}, child)...)
	verifyReconcileN(t, r, stored, 2)

	after := verifyFirstChild(t, c, stored)
	assert.NotEqual(t, legacyName, after.Labels[constants.GroupSetRevisionLabelKey],
		"canary: the legacy revision label is rewritten (one-time re-roll)")
	assert.Equal(t, child.UID, after.UID, "InPlaceUpdate must relabel in place, not recreate")
	assert.NotEmpty(t, after.Annotations[verifyCoordinationAnnKey],
		"the coordination stash must survive the migration re-roll")
}

// TestVerifyPR474_Rolling_PreservesChildConversionStash is C13, polarity
// contract. An operator writes the child through v1alpha1 (GET + PUT); the
// RBG conversion stashes v1alpha1-only fields as annotations on the child
// hub object. A subsequent rolling update must not destroy them.
func TestVerifyPR474_Rolling_PreservesChildConversionStash(t *testing.T) {
	set := verifyRollingSet("stash-keep", "v1", 1, verifyInPlaceStrategy())
	r, c := verifyReconciler(t, set)
	verifyReconcileN(t, r, set, 1)
	verifyMarkChildrenReady(t, c, set)
	verifyReconcileN(t, r, set, 2)

	// Out-of-band v1alpha1 write of the child: both conversion stashes land
	// on the child's annotations, exactly as the RBG ConvertTo leaves them.
	child := verifyFirstChild(t, c, set)
	if child.Annotations == nil {
		child.Annotations = map[string]string{}
	}
	child.Annotations[verifyCoordinationAnnKey] = `[{"name":"c","roles":["worker"]}]`
	// v1alpha1-pod-group-policy has no shared constant — itself part of the
	// gap this test documents.
	child.Annotations[verifyPodGroupPolicyAnnKey] = verifyPodGroupPolicyStash
	require.NoError(t, c.Update(context.Background(), child))

	for i := 0; i < 5; i++ {
		verifyMarkChildrenReady(t, c, set)
		verifyReconcileN(t, r, set, 1)
	}

	after := verifyFirstChild(t, c, set)
	assert.Equal(t, `[{"name":"c","roles":["worker"]}]`,
		after.Annotations[verifyCoordinationAnnKey],
		"the coordination stash is system-managed and must survive")
	assert.Equal(t, verifyPodGroupPolicyStash,
		after.Annotations[verifyPodGroupPolicyAnnKey],
		"the pod-group-policy stash is the same conversion-machinery class and must survive too")
}
