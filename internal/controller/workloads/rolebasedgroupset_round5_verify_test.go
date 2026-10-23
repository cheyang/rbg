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

// Verification harness additions for PR #474 round 5 (commits 6dd7de5f, 51bf17bb,
// 4a9043c3). Reviewer additive tests; production code untouched.
//
// Round 5 closes the round-4 blocker (rolling-path conversion-annotation
// propagation) and answers the carried Copilot flag C4273377135 with a
// request/acknowledge handshake (rbg.workloads.x-k8s.io/rollout-request +
// status.observedRolloutRequest). The contracts here pin the new behavior:
//
//  1. A template METADATA change under InPlaceUpdate is budget-paced by the
//     handshake: a child is written with a fresh request token, and no further
//     child is touched until the token is acknowledged. Without any ack the
//     rollout holds indefinitely (safety, not a wedge: the child controller acks
//     in production).
//  2. The v1alpha1 PodGroupPolicy stash now propagates through the rolling path:
//     children carry it at creation, converge in place when the value changes
//     (the key is unversioned), and lose it once the template drops it and the
//     revision snapshot is released.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

const verifyPodGroupPolicyAnnotation = "conversion.workloads.x-k8s.io/v1alpha1-pod-group-policy"

// Test 1 (request/ack handshake, C4273377135): a template-annotation-only change
// under InPlaceUpdate leaves each child's generation untouched, so readiness used
// to be released immediately. The handshake must now hold the budget: one child
// carries the write until its controller acknowledges it.
func TestVerify_RolloutAck_MetadataOnlyTemplateChangePaced(t *testing.T) {
	set := rollingVerifySet("meta-ack", 3, inPlaceStrategy(1))
	d := newInPlaceDrive(t, set)
	d.settle(40)

	// Metadata-only template change: labels/annotations only, no spec diff.
	latest := d.getSet()
	latest.Spec.GroupTemplate.Annotations = map[string]string{"team": "b"}
	require.NoError(t, d.c.Update(d.ctx, latest))

	d.step()
	touched := 0
	var first string
	for _, item := range d.listChildren() {
		if item.Annotations["team"] == "b" {
			touched++
			first = item.Name
			assert.NotEmpty(t, item.Annotations[verifyRolloutRequestAnnotation],
				"child %s written by the rollout must carry a request token", item.Name)
		}
	}
	require.Equal(t, 1, touched,
		"maxUnavailable=1 must pace the metadata-only roll to one child per acknowledgement window")

	// Freeze the child controller: no acknowledgement ever arrives for the first
	// child, so no further child may be written, however many passes run.
	d.noAck[first] = true
	for i := 0; i < 6; i++ {
		d.step()
		touched = 0
		for _, item := range d.listChildren() {
			if item.Annotations["team"] == "b" {
				touched++
			}
		}
		assert.Equal(t, 1, touched,
			"pass %d: an unacknowledged child must hold the whole budget", i)
	}
	rolling := rollingCondition(t, d.getSet())
	assert.Equal(t, "RolloutInProgress", rolling.Reason,
		"the set must keep reporting an in-progress rollout while an ack is outstanding")

	// Unfreeze: the sim acks, and the roll converges with all UIDs preserved.
	delete(d.noAck, first)
	d.settle(60)
	uids := map[string]string{}
	for _, item := range d.listChildren() {
		assert.Equal(t, "b", item.Annotations["team"])
		assert.Equal(t, item.Annotations[verifyRolloutRequestAnnotation],
			item.Status.ObservedRolloutRequest,
			"child %s must settle with its request acknowledged", item.Name)
		uids[item.Name] = string(item.UID)
	}
	final := d.getSet()
	assert.Equal(t, "RolloutComplete", rollingCondition(t, final).Reason)
}

// Test 2 (F13 fix contract): the v1alpha1 PodGroupPolicy stash must survive the
// rolling path end to end - propagation at creation, in-place value convergence
// (unversioned key), and gated removal.
func TestVerify_ConversionAnnotation_PodGroupPolicyRollingLifecycle(t *testing.T) {
	const policyV1 = `{"minMember":2,"scheduleTimeoutSeconds":30}`
	const policyV2 = `{"minMember":2,"scheduleTimeoutSeconds":60}`

	withPolicy := func(set *workloadsv1alpha2.RoleBasedGroupSet, value string) *workloadsv1alpha2.RoleBasedGroupSet {
		if set.Spec.GroupTemplate.Annotations == nil {
			set.Spec.GroupTemplate.Annotations = map[string]string{}
		}
		set.Spec.GroupTemplate.Annotations[verifyPodGroupPolicyAnnotation] = value
		return set
	}
	childPolicy := func(d *rollingDrive) map[string]string {
		out := map[string]string{}
		for _, item := range d.listChildren() {
			out[item.Name] = item.Annotations[verifyPodGroupPolicyAnnotation]
		}
		return out
	}

	for _, tc := range []struct {
		name     string
		strategy *workloadsv1alpha2.GroupSetRolloutStrategy
	}{
		{"InPlaceUpdate", coordInPlaceStrategy()},
		{"Recreate", intPtrStrategy(1, 0, 0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			set := withPolicy(rollingVerifySet("pgp", 2, tc.strategy), policyV1)
			d := newRollingDrive(t, set)
			d.waitSettled(2, 1, 30)

			for name, value := range childPolicy(d) {
				assert.Equalf(t, policyV1, value,
					"child %s created on the rolling path must carry the PodGroupPolicy stash", name)
			}
			d.deleted = map[string]bool{}

			// Value change: the key is unversioned, so children must converge IN PLACE.
			latest := d.getSet()
			withPolicy(latest, policyV2)
			require.NoError(t, d.c.Update(d.ctx, latest))
			d.waitSettled(2, 1, 40)
			for name, value := range childPolicy(d) {
				assert.Equalf(t, policyV2, value,
					"child %s must converge to the updated PodGroupPolicy stash", name)
				assert.Falsef(t, d.deleted[name],
					"an unversioned annotation change must not recreate child %s", name)
			}

			// Removal: once the template drops the policy and every child at the
			// snapshot's revision released it, children must lose the key too.
			latest = d.getSet()
			delete(latest.Spec.GroupTemplate.Annotations, verifyPodGroupPolicyAnnotation)
			require.NoError(t, d.c.Update(d.ctx, latest))
			d.waitSettled(2, 1, 40)
			for name, value := range childPolicy(d) {
				assert.Emptyf(t, value,
					"child %s must drop the PodGroupPolicy stash once the template removes it", name)
			}

			// The removal must be stable: further reconciles neither resurrect the
			// key nor keep requeueing.
			for i := 0; i < 3; i++ {
				res := d.reconcileOnce(2, 1)
				assert.Zero(t, res.RequeueAfter, "steady state after removal must not requeue")
			}
			for name, value := range childPolicy(d) {
				assert.Emptyf(t, value, "child %s: removed policy must not be resurrected", name)
			}
		})
	}
}

// Test 3 (snapshot semantics): the stash is owned by the revision that was live
// when the child was pinned. A policy change combined with a hashed template change
// mints a new revision; a partition-held ordinal stays on the old revision and must
// keep the OLD policy, while the in-scope ordinal rolls to the new one.
func TestVerify_ConversionAnnotation_PodGroupPolicyHeldOrdinalKeepsSnapshot(t *testing.T) {
	const policyV1 = `{"minMember":1}`
	const policyV2 = `{"minMember":4}`

	set := rollingVerifySet("pgp-hold", 2, coordInPlaceStrategy())
	set.Spec.GroupTemplate.Annotations = map[string]string{verifyPodGroupPolicyAnnotation: policyV1}
	d := newRollingDrive(t, set)
	d.waitSettled(2, 1, 30)
	for name, value := range func() map[string]string {
		out := map[string]string{}
		for _, item := range d.listChildren() {
			out[item.Name] = item.Annotations[verifyPodGroupPolicyAnnotation]
		}
		return out
	}() {
		require.Equalf(t, policyV1, value, "precondition: child %s carries the initial policy", name)
	}

	// Hold ordinal 0, then change the policy AND the pod template together.
	latest := d.getSet()
	latest.Spec.RolloutStrategy.Partition = ptr.To(intstr.FromInt(1))
	latest.Spec.GroupTemplate.Annotations[verifyPodGroupPolicyAnnotation] = policyV2
	latest.Spec.GroupTemplate.Spec.Roles[0].StandalonePattern.Template = premisePodTemplate("image:v2")
	require.NoError(t, d.c.Update(d.ctx, latest))
	d.waitSettled(2, 1, 60)

	for i, item := range d.listChildren() {
		idx, ok := groupSetOrdinal(d.getSet(), &d.listChildren()[i])
		require.True(t, ok)
		if idx == 0 {
			assert.Equal(t, policyV1, item.Annotations[verifyPodGroupPolicyAnnotation],
				"the partition-held ordinal must keep the policy its pinned revision owns")
			assert.Equal(t, "image:v1", childImage(&item),
				"the partition-held ordinal must stay on the old template")
		} else {
			assert.Equal(t, policyV2, item.Annotations[verifyPodGroupPolicyAnnotation],
				"the in-scope ordinal must roll to the new revision with the new policy")
			assert.Equal(t, "image:v2", childImage(&item))
		}
	}
}
