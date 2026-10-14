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

// Verification harness for PR #474 round 4 (commit 8c024b28 "fix(rbgs): exclude
// conversion annotations from RBGSet revision and sync"). Reviewer additive tests;
// production code untouched.
//
// The delta moves the v1alpha1 conversion coordination annotation into
// systemManagedAnnotationKeys. That single map feeds THREE consumers:
//   (a) normalizedGroupSetTemplate -> revision hashing + drift comparison,
//   (b) syncRBGMetadata / needsTemplateAnnotationUpdate -> metadata sync,
//   (c) revision.template -> the material newRBGForSetRevision builds children from.
// Consumer (c) means the exclusion also strips the annotation from children at
// creation time. The conversion's ConvertTo copies the annotation into
// GroupTemplate.Annotations precisely so children carry it and the RBG
// reconciler's EnsureV1alpha1CoordinatedPolicy (rolebasedgroup_controller.go:229)
// can migrate v1alpha1 CoordinationRequirements into a CoordinatedPolicy.
//
// Tests 1-2 pin the fix's own claims (churn + wipe are real and now fixed).
// Tests 3-4 pin the propagation contract the fix must not break.
//
// Polarity: all four are CONTRACT tests - they assert intended behavior.
// The coordination key is written as a literal (not the new constant) so this
// file still compiles on the pre-fix head 98720938 for the differential control.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

const verifyCoordinationAnnotation = "conversion.workloads.x-k8s.io/v1alpha1-coordination"

const (
	verifyCoordV1 = `[{"roles":["worker"],"strategy":{"type":"RollingUpdate"}}]`
	verifyCoordV2 = `[{"roles":["worker"],"strategy":{"type":"Scaling"}}]`
)

func withCoordinationAnnotation(set *workloadsv1alpha2.RoleBasedGroupSet, value string) *workloadsv1alpha2.RoleBasedGroupSet {
	if set.Spec.GroupTemplate.Annotations == nil {
		set.Spec.GroupTemplate.Annotations = map[string]string{}
	}
	set.Spec.GroupTemplate.Annotations[verifyCoordinationAnnotation] = value
	return set
}

func coordInPlaceStrategy() *workloadsv1alpha2.GroupSetRolloutStrategy {
	return &workloadsv1alpha2.GroupSetRolloutStrategy{
		Type:           workloadsv1alpha2.InPlaceUpdateStrategyType,
		MaxUnavailable: ptr.To(intstr.FromInt(1)),
	}
}

// Test 1 (fix claim, churn): the revision identity must not depend on the
// conversion coordination annotation - a v1alpha1 round trip adding/removing it
// must not mint a new revision. PASS on 8c024b28, FAIL on 98720938 (control:
// proves the churn the commit fixes was real).
func TestVerify_ConversionAnnotation_RevisionIgnoresCoordination(t *testing.T) {
	set := rollingVerifySet("coord-hash", 1, coordInPlaceStrategy())
	d := newRollingDrive(t, set)

	without, err := d.r.ensureGroupSetRevision(d.ctx, set, &set.Spec.GroupTemplate)
	require.NoError(t, err)

	withAnn := withCoordinationAnnotation(set.DeepCopy(), verifyCoordV1)
	with, err := d.r.ensureGroupSetRevision(d.ctx, set, &withAnn.Spec.GroupTemplate)
	require.NoError(t, err)

	assert.Equal(t, without.name, with.name,
		"revision name must be stable w.r.t. the conversion coordination annotation (got %s vs %s)",
		without.name, with.name)
	assert.True(t, groupSetTemplatesEqual(without.template, with.template),
		"normalized templates differing only in the coordination annotation must be equal")
}

// Test 2 (fix claim, wipe): a child carrying the coordination annotation must
// survive metadata sync even when the (v1alpha2-written) template lacks the key.
// PASS on 8c024b28, FAIL on 98720938 (control: proves the wipe was real).
func TestVerify_ConversionAnnotation_SyncPreservesChildCoordination(t *testing.T) {
	set := rollingVerifySet("coord-sync", 1, coordInPlaceStrategy())
	child := &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "coord-sync-0",
			Namespace:   "default",
			Labels:      map[string]string{},
			Annotations: map[string]string{verifyCoordinationAnnotation: verifyCoordV1},
		},
	}
	d := newRollingDrive(t, set)
	d.r.syncRBGMetadata(set, child)
	assert.Equal(t, verifyCoordV1, child.Annotations[verifyCoordinationAnnotation],
		"syncRBGMetadata must not wipe the conversion coordination annotation from the child")
}

// Test 3 (propagation contract, THE REGRESSION): children created on the rolling
// path must carry the template's coordination annotation, exactly as children of
// the static path do via newRBGForSet. Without it the child RBG reconciler's
// EnsureV1alpha1CoordinatedPolicy is a permanent no-op and the v1alpha1
// CoordinationRequirements silently never materialize as a CoordinatedPolicy.
// FAIL on 8c024b28 (annotation stripped by normalizedGroupSetTemplate before
// newRBGForSetRevision), PASS on 98720938 (control).
func TestVerify_ConversionAnnotation_RollingCreatePropagatesToChildren(t *testing.T) {
	for _, tc := range []struct {
		name     string
		strategy *workloadsv1alpha2.GroupSetRolloutStrategy
	}{
		{"InPlaceUpdate", coordInPlaceStrategy()},
		{"Recreate", intPtrStrategy(1, 0, 0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			set := withCoordinationAnnotation(rollingVerifySet("coord-create", 2, tc.strategy), verifyCoordV1)
			d := newRollingDrive(t, set)

			d.waitSettled(2, 1, 20)

			children := d.listChildren()
			require.Len(t, children, 2)
			for i := range children {
				assert.Equalf(t, verifyCoordV1, children[i].Annotations[verifyCoordinationAnnotation],
					"child %s created on the rolling path must carry the template's coordination annotation",
					children[i].Name)
			}
		})
	}
}

// Test 4 (propagation contract, value change): with the annotation excluded from
// the revision hash, a coordination-only template change no longer mints a new
// revision (test 1), so the new value must reach existing children some other
// way. It does not: children match the update revision on both normalized sides
// and are classified up-to-date, so no sync ever runs and the stale value
// persists forever. FAIL on 8c024b28, PASS on 98720938 (control: there the value
// change mints a new revision and the rollout propagates it).
func TestVerify_ConversionAnnotation_CoordinationValueChangeReachesChildren(t *testing.T) {
	set := withCoordinationAnnotation(rollingVerifySet("coord-update", 2, coordInPlaceStrategy()), verifyCoordV1)
	d := newRollingDrive(t, set)
	d.waitSettled(2, 1, 20)

	children := d.listChildren()
	require.Len(t, children, 2)
	for i := range children {
		require.Equal(t, verifyCoordV1, children[i].Annotations[verifyCoordinationAnnotation],
			"precondition: child carries the initial coordination value")
	}

	// Change ONLY the coordination annotation value (what a v1alpha1 write with
	// updated CoordinationRequirements stores into the hub template).
	latest := d.getSet()
	latest.Spec.GroupTemplate.Annotations[verifyCoordinationAnnotation] = verifyCoordV2
	require.NoError(t, d.c.Update(d.ctx, latest))

	d.waitSettled(2, 1, 20)

	children = d.listChildren()
	require.Len(t, children, 2)
	for i := range children {
		assert.Equalf(t, verifyCoordV2, children[i].Annotations[verifyCoordinationAnnotation],
			"child %s must converge to the template's updated coordination annotation", children[i].Name)
	}
}

// Test 5 (no regression in the static path): the static controller must still
// propagate the coordination annotation both at create (newRBGForSet) and on
// update (syncRBGMetadata applies template annotations). PASS on both heads.
func TestVerify_ConversionAnnotation_StaticPathStillPropagates(t *testing.T) {
	set := withCoordinationAnnotation(rollingVerifySet("coord-static", 2, nil), verifyCoordV1)
	child := newRBGForSet(set, 0)
	assert.Equal(t, verifyCoordV1, child.Annotations[verifyCoordinationAnnotation],
		"static create path must copy the coordination annotation")

	// and needsUpdate must see a coordination-only value change as drift on the
	// static path (template->child direction has no system-key skip).
	setV2 := withCoordinationAnnotation(set.DeepCopy(), verifyCoordV2)
	assert.True(t, (&RoleBasedGroupSetReconciler{}).needsTemplateAnnotationUpdate(setV2, child),
		"static path must detect a coordination value change as annotation drift")
}
