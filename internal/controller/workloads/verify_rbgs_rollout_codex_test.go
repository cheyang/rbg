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

// Verification harness for PR #474 review (Reviewer B / Codex). Additive only: no
// production code is touched by this file. Each test names the finding it pins and
// its polarity (contract = fails while the bug is present, passes once fixed;
// canary = documents current buggy behavior, flips when the code changes).

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// F1 (mechanism demo, canary): a negative maxUnavailable resolves through
// resolveRollingParams untouched when maxSurge > 0, and recreateOutdatedGroups then
// computes budget = -1 + readySurge. Once one surge group is ready the budget is 0 and
// "unavailableBase(0) >= budget(0)" is true, so no serving group is ever deleted: the
// rollout wedges silently with Rolling=True forever. The gate that must reject this
// input is the validating webhook (see the F1 contract test in
// api/workloads/v1alpha2); this test documents what the wedge looks like inside the
// controller so the blast radius of the admission gap is visible. It is a canary for
// the controller layer only: the fix lands in the webhook, so this test is expected to
// keep passing after the fix (defense in depth in the controller would flip it, which
// would also be fine).
func TestVerify_F1_NegativeMaxUnavailableWedgesRollout(t *testing.T) {
	scheme := rbgsTestScheme(t)

	oldRoles := []workloadsv1alpha2.RoleSpec{{Name: "worker", Replicas: ptr.To(int32(1))}}
	newRoles := []workloadsv1alpha2.RoleSpec{{Name: "worker", Replicas: ptr.To(int32(1)), MinReadySeconds: 10}}

	set := rollingTestSet("s", 2, newRoles, &workloadsv1alpha2.GroupSetRolloutStrategy{
		MaxUnavailable: ptr.To(intstr.FromInt32(-1)),
		MaxSurge:       ptr.To(intstr.FromInt32(1)),
	})
	r := newRollingTestReconciler(scheme, set,
		rollingTestChild("s", 0, oldRoles, true),
		rollingTestChild("s", 1, oldRoles, true),
		// A surge group that is already up and serving.
		rollingTestChild("s", 2, newRoles, true),
	)

	params := resolveRollingParams(set)
	assert.Equal(t, -1, params.maxUnavailable,
		"negative maxUnavailable passes resolution untouched when maxSurge > 0")

	reconcileSet(t, r, "s")

	children := listChildren(t, r, "s")
	assert.Len(t, children, 3,
		"no base group is ever deleted: budget = maxUnavailable(-1) + readySurge(1) = 0 wedges the rollout")
}

// F4 (contract): with partition > 0, a held-back group (ordinal < partition) that stops
// serving blocks surge reclamation forever — rolloutComplete requires EVERY base group to
// be serving — while updateStatus reports Rolling=False/RolloutComplete over in-scope
// ordinals only. Status and surge handling disagree: the user sees a completed rollout
// while surge capacity is never released. Either the status must reflect the not-serving
// held-back group, or the surge release must not be gated on it; the two must agree.
func TestVerify_F4_BrokenHeldBackGroupLeaksSurgeWhileStatusComplete(t *testing.T) {
	scheme := rbgsTestScheme(t)

	oldRoles := []workloadsv1alpha2.RoleSpec{{Name: "worker", Replicas: ptr.To(int32(1))}}
	newRoles := []workloadsv1alpha2.RoleSpec{{Name: "worker", Replicas: ptr.To(int32(1)), MinReadySeconds: 10}}

	set := rollingTestSet("s", 3, newRoles, &workloadsv1alpha2.GroupSetRolloutStrategy{
		MaxUnavailable: ptr.To(intstr.FromInt32(1)),
		MaxSurge:       ptr.To(intstr.FromInt32(1)),
		Partition:      ptr.To(intstr.FromInt32(1)),
	})
	r := newRollingTestReconciler(scheme, set,
		// Held back by the partition and broken (e.g. unschedulable on the old template).
		rollingTestChild("s", 0, oldRoles, false),
		// In-scope ordinals: rolled and serving.
		rollingTestChild("s", 1, newRoles, true),
		rollingTestChild("s", 2, newRoles, true),
		// Surge capacity from the rollout.
		rollingTestChild("s", 3, newRoles, true),
	)

	reconcileSet(t, r, "s")
	// A second pass proves the end state is stable, not a transient.
	reconcileSet(t, r, "s")

	updated := getSet(t, r, "s")
	rolling := meta.FindStatusCondition(updated.Status.Conditions, string(workloadsv1alpha2.RoleBasedGroupSetRolling))
	require.NotNil(t, rolling, "the Rolling condition must exist once rolloutStrategy is set")

	children := listChildren(t, r, "s")
	surgeLeft := 0
	for i := range children {
		if children[i].Labels[constants.GroupSetIndexLabelKey] == "3" {
			surgeLeft++
		}
	}

	// The contract is consistency between what the status reports and what the surge
	// handling does. Two fixes satisfy it: (A) the Rolling condition only reports
	// RolloutComplete once every base group is serving, matching rolloutComplete; or
	// (B) surge release and the status both scope serving to ordinals >= partition.
	// On the buggy code status says RolloutComplete while the surge group is kept.
	complete := rolling.Reason == "RolloutComplete"
	assert.Equal(t, complete, surgeLeft == 0,
		"status and surge handling disagree: Rolling reason=%q but %d surge group(s) are still around; "+
			"a rollout the status calls complete must not hold surge capacity, and a rollout still holding surge must not report complete",
		rolling.Reason, surgeLeft)
}

// F5 (contract): the scale-only path calls updateExistingRBGs, which syncs template
// labels/annotations, and step 5 of reconcileRolling is not gated by paused. So while a
// rollout is paused, a combined replicas+metadata change still propagates the metadata —
// although the pure metadata path right below IS frozen by pause, and the field
// documents "Paused freezes the rollout". Template metadata must not propagate through
// the scale-only path while paused either.
func TestVerify_F5_PausedScaleOnlyPathPropagatesTemplateMetadata(t *testing.T) {
	scheme := rbgsTestScheme(t)

	oldRoles := []workloadsv1alpha2.RoleSpec{{Name: "worker", Replicas: ptr.To(int32(1))}}
	newRoles := []workloadsv1alpha2.RoleSpec{{Name: "worker", Replicas: ptr.To(int32(4))}}

	set := rollingTestSet("s", 2, newRoles, &workloadsv1alpha2.GroupSetRolloutStrategy{
		Paused: true,
	})
	set.Spec.GroupTemplate.Labels = map[string]string{"app": "v2"}
	r := newRollingTestReconciler(scheme, set,
		rollingTestChild("s", 0, oldRoles, true),
		rollingTestChild("s", 1, oldRoles, true),
	)

	reconcileSet(t, r, "s")

	for _, child := range listChildren(t, r, "s") {
		// The scale itself is intended to bypass pause.
		assert.Equal(t, ptr.To(int32(4)), child.Spec.Roles[0].Replicas,
			"scaling in place while paused is intended")
		assert.NotContains(t, child.Labels, "app",
			"template metadata must not propagate while the rollout is paused (group %s)", child.Name)
	}
}
