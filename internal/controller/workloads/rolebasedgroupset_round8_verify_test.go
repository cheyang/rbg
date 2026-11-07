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

// Verification harness for PR #474 round 8 (reviewer additive tests; production code
// untouched). Covers the round-8 delta (39732268..09aaa4d7):
//
//   R8ROLESEQ  - regression guard for the round-7 blocker C4358788576, fixed at 94981f04:
//                rolesEqual must treat explicitly-empty and omitted role collections as
//                equal (JSON round-trip drops them). Contract; GREEN at head.
//   R8SURGEDOC - the PR body documents the desired surge count as
//                min(maxSurge, max(0, servingOutdated - max(0, readyBase - floor)));
//                90858736 implements min(maxSurge, max(0, servingOutdated + floor - readyBase)).
//                The two disagree exactly when readyBase < floor and servingOutdated > 0
//                (the deficit case the commit fixes). Contract (body must describe the
//                shipped formula); RED at 09aaa4d7.
//   R8TPLCHURN - needsUpdate (static path) must not report a perpetual diff when an
//                explicitly-empty collection inside RoleTemplates is dropped by JSON
//                round-trip storage. Roles moved to apiequality.Semantic at 94981f04;
//                RoleTemplates still uses reflect.DeepEqual. Contract; polarity check
//                decides whether this is a live residual.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// TestVerify_R8_RolesEqualEmptyCollections pins the blocker fix: a stored set whose role
// carries an explicit empty annotations/labels map (Helm-rendered YAML commonly emits
// `annotations: {}`) must compare equal to the child the controller wrote (whose typed
// serialization drops the empty map). RED before 94981f04, GREEN at head.
func TestVerify_R8_RolesEqualEmptyCollections(t *testing.T) {
	template := revisionTestTemplate(nil)
	require.NoError(t, json.Unmarshal([]byte(`{"annotations":{},"labels":{}}`), &template.Spec.Roles[0]))

	data, err := json.Marshal(template.Spec.Roles)
	require.NoError(t, err)
	roundTripped := []workloadsv1alpha2.RoleSpec(nil)
	require.NoError(t, json.Unmarshal(data, &roundTripped))

	r := &RoleBasedGroupSetReconciler{}
	assert.True(t, r.rolesEqual(template.Spec.Roles, roundTripped),
		"rolesEqual must treat explicit empty maps as equal to omitted ones")
	assert.True(t, r.rolesEqual(roundTripped, template.Spec.Roles),
		"rolesEqual must be symmetric for empty vs omitted maps")

	// Control: a real difference must still be detected.
	changed := append([]workloadsv1alpha2.RoleSpec(nil), roundTripped...)
	changed[0].Replicas = ptr.To(int32(3))
	assert.False(t, r.rolesEqual(template.Spec.Roles, changed))
}

// bodyDocumentedSurgeFormula transcribes the PR body's section
// "Rolling and live-budget surge sizing" verbatim. Round-9 transcription, from the
// body live at 2026-11-07T17:25:21Z (updated after round 8 flagged the drift):
//
//	floor = max(0, replicas - maxUnavailable)
//	desired surge = min(maxSurge, max(0, servingOutdated + floor - readyBase))
//	                when servingOutdated > 0, otherwise 0
//
// (Round 8 and earlier transcribed the pre-fix body: headroom = max(0, readyBase -
// floor); desired = min(maxSurge, max(0, servingOutdated - headroom)). The body edit
// resolved DOC-SURGE-FORMULA-R8 in the direction of the shipped code.)
func bodyDocumentedSurgeFormula(replicas, maxUnavailable, maxSurge, servingOutdated, readyBase int) int {
	floor := max(0, replicas-maxUnavailable)
	if servingOutdated == 0 {
		return 0
	}
	return min(maxSurge, max(0, servingOutdated+floor-readyBase))
}

// TestVerify_R8_SurgeMatchesBodyDocumentedFormula is a contract test: the shipped surge
// sizing must match the formula the PR body publishes. It exercises the real
// warmUpGroupSetSurge over an availability grid and compares created surge children
// against the documented formula. RED at 09aaa4d7 whenever readyBase < floor and
// servingOutdated > 0 (90858736's new deficit term was undocumented); GREEN at 8f07c37e
// after the body was updated to the deficit-inclusive form.
func TestVerify_R8_SurgeMatchesBodyDocumentedFormula(t *testing.T) {
	const replicas = 4
	scheme := runtime.NewScheme()
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))

	for _, maxUnavailable := range []int{0, 1, 2} {
		for _, maxSurge := range []int{1, 2, 3} {
			for _, servingOutdated := range []int{0, 1, 2} {
				for _, readyBase := range []int{0, 1, 2, 3, 4} {
					set := &workloadsv1alpha2.RoleBasedGroupSet{
						ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default", UID: "test-set"},
						Spec:       workloadsv1alpha2.RoleBasedGroupSetSpec{Replicas: ptr.To(int32(replicas))},
					}
					update := &groupSetRevision{name: "update", template: revisionTestTemplate(nil)}
					existing := make(map[int]*workloadsv1alpha2.RoleBasedGroup)
					c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(set).Build()
					r := &RoleBasedGroupSetReconciler{client: c, apiReader: c, scheme: scheme}
					limits := groupSetRolloutLimits{maxSurge: maxSurge, maxUnavailable: maxUnavailable}
					accounting := groupSetSurgeAccounting{servingOutdated: servingOutdated, readyBase: readyBase}

					_, err := r.warmUpGroupSetSurge(context.Background(), set, existing, replicas, limits, update,
						accounting, maxSurge)
					require.NoError(t, err)
					children := &workloadsv1alpha2.RoleBasedGroupList{}
					require.NoError(t, c.List(context.Background(), children, client.InNamespace(set.Namespace)))
					created := len(children.Items)

					want := bodyDocumentedSurgeFormula(replicas, maxUnavailable, maxSurge, servingOutdated, readyBase)
					assert.Equal(t, want, created,
						"maxUnavailable=%d maxSurge=%d servingOutdated=%d readyBase=%d: body formula says %d, code created %d",
						maxUnavailable, maxSurge, servingOutdated, readyBase, want, created)
				}
			}
		}
	}
}

// TestVerify_R8_NeedsUpdateRoleTemplatesEmptyMap is a contract test for the static path:
// an explicitly-empty map inside spec.groupTemplate.spec.roleTemplates is dropped by the
// API server's JSON storage, so the stored child never byte-matches the template; the
// semantic comparison used for Roles since 94981f04 must apply here too, otherwise the
// static path rewrites the child every reconcile (the same defect class as the round-7
// blocker, one comparison away). Polarity: contract (RED = divergence still live).
func TestVerify_R8_NeedsUpdateRoleTemplatesEmptyMap(t *testing.T) {
	set := &workloadsv1alpha2.RoleBasedGroupSet{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default", UID: "test-set"},
		Spec: workloadsv1alpha2.RoleBasedGroupSetSpec{
			Replicas: ptr.To(int32(1)),
			GroupTemplate: workloadsv1alpha2.RoleBasedGroupTemplateSpec{
				Spec: workloadsv1alpha2.RoleBasedGroupSpec{
					Roles: []workloadsv1alpha2.RoleSpec{{Name: "worker", Replicas: ptr.To(int32(1))}},
					RoleTemplates: []workloadsv1alpha2.RoleTemplate{{
						Name: "tpl",
						Template: corev1.PodTemplateSpec{
							ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{}},
						},
					}},
				},
			},
		},
	}
	// Simulate apiserver storage: JSON round-trip drops the explicit empty map.
	data, err := json.Marshal(set.Spec.GroupTemplate.Spec)
	require.NoError(t, err)
	child := revisionTestChild(nil)
	child.Name = "test-0"
	require.NoError(t, json.Unmarshal(data, &child.Spec))
	require.NotEqual(t, set.Spec.GroupTemplate.Spec.RoleTemplates, child.Spec.RoleTemplates,
		"the JSON round trip must drop the explicit empty collection (premise)")

	r := &RoleBasedGroupSetReconciler{}
	assert.False(t, r.needsUpdate(set, child),
		"an empty-vs-omitted roleTemplates collection must not mark the child outdated")

	// Control: a real roleTemplates change must still be detected.
	changedSet := set.DeepCopy()
	changedSet.Spec.GroupTemplate.Spec.RoleTemplates[0].Template.Annotations = map[string]string{"k": "v"}
	assert.True(t, r.needsUpdate(changedSet, child))
}

// TestVerify_R8_SurgeCoversFloorDeficit pins the 90858736 code behavior independent of the
// doc dispute: with an existing failure below the availability floor, surge must cover both
// the serving outdated batch AND the deficit, so the rollout is not stranded. This guards
// the #519 fix against a "resolve the doc mismatch by reverting the code" outcome.
// Contract; GREEN at 09aaa4d7.
func TestVerify_R8_SurgeCoversFloorDeficit(t *testing.T) {
	const replicas = 4
	scheme := runtime.NewScheme()
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))

	for _, tt := range []struct {
		name                                      string
		maxUnavailable, maxSurge                  int
		servingOutdated, readyBase, existingSurge int
		wantCreated                               int
	}{
		// The #519 repro shape: held-back failure consumes the whole unavailable budget;
		// the free surge slot must still back the serving outdated group.
		{name: "failure below partition still surges", maxUnavailable: 1, maxSurge: 1, servingOutdated: 1, readyBase: 3, wantCreated: 1},
		// The deficit case 90858736 added: one ready group below the floor, one serving
		// outdated group, room for two surge groups -> both are created.
		{name: "deficit below floor is covered", maxUnavailable: 1, maxSurge: 2, servingOutdated: 1, readyBase: 2, wantCreated: 2},
		// Deficit bounded by maxSurge.
		{name: "deficit bounded by maxSurge", maxUnavailable: 1, maxSurge: 1, servingOutdated: 1, readyBase: 2, wantCreated: 1},
		// No serving outdated work -> no surge even with a deficit (down groups freed their slots).
		{name: "no serving work no surge", maxUnavailable: 1, maxSurge: 2, servingOutdated: 0, readyBase: 2, wantCreated: 0},
		// Full availability: maxUnavailable headroom absorbs the batch, no surge.
		{name: "headroom absorbs batch", maxUnavailable: 2, maxSurge: 2, servingOutdated: 2, readyBase: 4, wantCreated: 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			set := &workloadsv1alpha2.RoleBasedGroupSet{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default", UID: "test-set"},
				Spec:       workloadsv1alpha2.RoleBasedGroupSetSpec{Replicas: ptr.To(int32(replicas))},
			}
			update := &groupSetRevision{name: "update", template: revisionTestTemplate(nil)}
			existing := make(map[int]*workloadsv1alpha2.RoleBasedGroup)
			objects := []client.Object{set}
			for i := 0; i < tt.existingSurge; i++ {
				child := newRBGForSetRevision(set, replicas+i, update)
				existing[replicas+i] = child
				objects = append(objects, child)
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			r := &RoleBasedGroupSetReconciler{client: c, apiReader: c, scheme: scheme}
			limits := groupSetRolloutLimits{maxSurge: tt.maxSurge, maxUnavailable: tt.maxUnavailable}
			accounting := groupSetSurgeAccounting{servingOutdated: tt.servingOutdated, readyBase: tt.readyBase}
			_, err := r.warmUpGroupSetSurge(context.Background(), set, existing, replicas, limits, update,
				accounting, tt.maxSurge-tt.existingSurge)
			require.NoError(t, err)
			children := &workloadsv1alpha2.RoleBasedGroupList{}
			require.NoError(t, c.List(context.Background(), children, client.InNamespace(set.Namespace)))
			assert.Equal(t, tt.wantCreated, len(children.Items)-tt.existingSurge)
		})
	}
}
