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

// Verification harness for PR #474 round 2 (reviewer additive tests; production code
// untouched). Covers the InPlaceUpdate group-set strategy added in 9e423e86.
//
// The fake client never manages metadata.generation (it stays 0), which would hide the
// readiness-barrier the InPlace budget relies on (groupSetChildReady requires
// status.observedGeneration >= generation). The drive below emulates the API server:
// it bumps a child's generation itself whenever the controller rewrites the child spec,
// and emulates the child RBG controller by flipping observedGeneration only after a
// cooldown (the in-flight window).
//
// Polarity: CONTRACT tests assert intended behavior (fail on buggy code). The one
// CANARY pins observed scale-only pacing under InPlaceUpdate, whose intended behavior
// is questioned (Recreate mode and the PR body promise un-paced in-place scaling).

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func inPlaceStrategy(maxUnavailable int) *workloadsv1alpha2.GroupSetRolloutStrategy {
	return &workloadsv1alpha2.GroupSetRolloutStrategy{
		Type:           workloadsv1alpha2.InPlaceUpdateStrategyType,
		MaxUnavailable: ptr.To(intstr.FromInt(maxUnavailable)),
	}
}

// inPlaceDrive wraps rollingDrive with API-server generation emulation. The embedded
// drive's simulateChildren is bypassed in favor of inPlaceSimulate.
type inPlaceDrive struct {
	*rollingDrive
	specSeen map[string]string // child name -> last observed spec fingerprint
	cooldown int               // passes a rewritten child stays not-ready
	noAck    map[string]bool   // children whose simulated controller never acknowledges
}

func newInPlaceDrive(t *testing.T, set *workloadsv1alpha2.RoleBasedGroupSet) *inPlaceDrive {
	return &inPlaceDrive{
		rollingDrive: newRollingDrive(t, set),
		specSeen:     map[string]string{},
		cooldown:     2,
		noAck:        map[string]bool{},
	}
}

func childFingerprint(child *workloadsv1alpha2.RoleBasedGroup) string {
	fp := childImage(child)
	for _, role := range child.Spec.Roles {
		fp += fmt.Sprintf("|%s=%d", role.Name, ptr.Deref(role.Replicas, -1))
	}
	return fp
}

// emulateAPIServer bumps the generation of any child whose spec changed since the last
// pass (what the real API server does on a spec write) and starts the in-flight window.
// It reports how many children were rewritten since the previous pass.
func (d *inPlaceDrive) emulateAPIServer() int {
	rewritten := 0
	for _, item := range d.listChildren() {
		fp := childFingerprint(&item)
		seen, known := d.specSeen[item.Name]
		if known && seen == fp {
			continue
		}
		d.specSeen[item.Name] = fp
		if !known {
			d.cool[item.Name] = max(d.cool[item.Name], 1) // startup latency
			continue
		}
		rewritten++
		latest := &workloadsv1alpha2.RoleBasedGroup{}
		require.NoError(d.t, d.c.Get(d.ctx, client.ObjectKeyFromObject(&item), latest))
		latest.Generation++ // the API server does this; the fake client does not
		require.NoError(d.t, d.c.Update(d.ctx, latest))
		d.cool[item.Name] = max(d.cool[item.Name], d.cooldown)
	}
	return rewritten
}

// inPlaceSimulate plays the child RBG controller: a child reports Ready (observing its
// current generation) only after its in-flight window has elapsed.
func (d *inPlaceDrive) inPlaceSimulate() {
	for _, item := range d.listChildren() {
		if !item.DeletionTimestamp.IsZero() {
			continue
		}
		if d.cool[item.Name] > 0 {
			d.cool[item.Name]--
			continue
		}
		latest := &workloadsv1alpha2.RoleBasedGroup{}
		require.NoError(d.t, d.c.Get(d.ctx, client.ObjectKeyFromObject(&item), latest))
		ready := latest.Status.ObservedGeneration >= latest.Generation &&
			meta.IsStatusConditionTrue(latest.Status.Conditions, string(workloadsv1alpha2.RoleBasedGroupReady))
		request := latest.Annotations[verifyRolloutRequestAnnotation]
		if ready && request != "" && latest.Status.ObservedRolloutRequest != request {
			// Metadata-only writes never bump the generation; the child controller
			// acknowledges the request token once its workloads have rolled out.
			if d.noAck[item.Name] {
				continue
			}
			if d.ackWait[item.Name] != request {
				d.ackWait[item.Name] = request
				continue
			}
			delete(d.ackWait, item.Name)
			latest.Status.ObservedRolloutRequest = request
			require.NoError(d.t, d.c.Status().Update(d.ctx, latest))
			continue
		}
		if ready {
			continue
		}
		latest.Status.ObservedGeneration = latest.Generation
		latest.Status.ObservedRolloutRequest = request
		meta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{
			Type: string(workloadsv1alpha2.RoleBasedGroupReady), Status: metav1.ConditionTrue,
			Reason: "Ready", ObservedGeneration: latest.Generation,
		})
		require.NoError(d.t, d.c.Status().Update(d.ctx, latest))
	}
}

func (d *inPlaceDrive) step() (ctrl.Result, int) {
	rewritten := d.emulateAPIServer()
	d.inPlaceSimulate()
	res, err := d.r.reconcileRollingGroupSet(d.ctx, d.key)
	require.NoError(d.t, err)
	d.rollingDrive.step++
	return res, rewritten
}

func (d *inPlaceDrive) inFlight() int {
	n := 0
	for _, item := range d.listChildren() {
		if item.DeletionTimestamp.IsZero() && item.Status.ObservedGeneration < item.Generation {
			n++
		}
	}
	return n
}

// settle drives passes until a true fixpoint: no requeue requested AND the pass changed
// nothing observable. Round 6 made the rolling path event-driven, so a pass that is
// waiting on a child ack no longer requeues; in a real cluster the child's status write
// re-enqueues the set, and here the next sim step plays that event.
func (d *inPlaceDrive) settle(maxSteps int) {
	for i := 0; i < maxSteps; i++ {
		before := d.worldFingerprint()
		res, _ := d.step()
		if res.RequeueAfter == 0 && !res.Requeue && before == d.worldFingerprint() {
			return
		}
	}
	d.t.Fatalf("did not settle within %d steps", maxSteps)
}

// TestVerify_InPlaceRolloutPacedNoRecreate: under InPlaceUpdate a template change moves
// children onto the new template IN PLACE (no child is ever deleted), at most one child
// is in flight at any time (maxUnavailable=1), and the rollout converges quietly.
func TestVerify_InPlaceRolloutPacedNoRecreate(t *testing.T) {
	set := rollingVerifySet("inplace", 3, inPlaceStrategy(1))
	d := newInPlaceDrive(t, set)
	d.settle(20)
	require.Len(t, d.listChildren(), 3)
	d.deleted = map[string]bool{}

	d.triggerTemplateChange("image:v2")
	for i := 0; i < 60; i++ {
		before := d.worldFingerprint()
		res, rewritten := d.step()
		assert.LessOrEqualf(t, rewritten, 1,
			"step %d: maxUnavailable=1 allows at most one child rewrite per pass, got %d", d.step, rewritten)
		assert.LessOrEqualf(t, d.inFlight(), 1,
			"step %d: at most one child may be in flight at a time", d.step)
		if res.RequeueAfter == 0 && !res.Requeue && before == d.worldFingerprint() {
			break
		}
	}

	children := d.listChildren()
	require.Len(t, children, 3)
	for i := range children {
		assert.Equal(t, "image:v2", childImage(&children[i]))
		assert.False(t, d.deleted[children[i].Name],
			"child %s must have been updated IN PLACE, not recreated", children[i].Name)
	}
	final := d.getSet()
	assert.Equal(t, "RolloutComplete", rollingCondition(t, final).Reason)
	assert.Equal(t, final.Status.CurrentRevision, final.Status.UpdateRevision)
	assert.Equal(t, int32(3), final.Status.UpdatedReplicas)

	// Steady state: no churn - further reconciles must not rewrite any child.
	for i := 0; i < 3; i++ {
		res, rewritten := d.step()
		assert.Zero(t, res.RequeueAfter, "steady state must not requeue")
		assert.Zero(t, rewritten, "steady state must not rewrite child specs")
	}
}

// TestVerify_InPlaceStalledChildBlocksProgress: when the just-updated child never
// reports ready (crashing workload), the rollout must stall instead of taking more
// serving groups down, and the Rolling condition must keep reporting in-progress.
func TestVerify_InPlaceStalledChildBlocksProgress(t *testing.T) {
	set := rollingVerifySet("inplace-stall", 3, inPlaceStrategy(1))
	d := newInPlaceDrive(t, set)
	d.settle(20)
	d.triggerTemplateChange("image:v2")

	// Let the first child be rewritten, absorb that write into the emulation (generation
	// bump), then freeze the child in-flight forever.
	d.step()
	stalled := ""
	for _, item := range d.listChildren() {
		if childImage(&item) == "image:v2" {
			stalled = item.Name
		}
	}
	require.NotEmpty(t, stalled, "first pass must rewrite exactly one child")
	require.Equal(t, 1, d.emulateAPIServer(), "exactly one child was rewritten in the first pass")
	d.cool[stalled] = 1 << 30

	for i := 0; i < 6; i++ {
		_, rewritten := d.step()
		assert.Zero(t, rewritten,
			"a stalled in-flight child must consume the whole budget; no further child may be touched")
	}
	assert.Equal(t, 1, d.inFlight())
	onV2 := 0
	for _, item := range d.listChildren() {
		if childImage(&item) == "image:v2" {
			onV2++
		}
	}
	assert.Equal(t, 1, onV2, "rollout must hold at the stalled child")
	rolling := rollingCondition(t, d.getSet())
	assert.Equal(t, metav1.ConditionTrue, rolling.Status,
		"a stalled rollout must keep reporting Rolling=True")
}

// TestVerify_InPlaceScaleOnlyPacing_Contract pins how a replicas-only template diff
// is applied under InPlaceUpdate: children are updated in place but ONE AT A TIME,
// gated by the rollout budget. Promoted from canary to contract in round 5: commit
// 4a9043c3 ("enforce availability budgets for Recreate replica updates") makes the
// same pacing explicit for Recreate, so budget-paced replicas-only updates are the
// author's settled intent under BOTH strategies; the PR body's "scaling stays
// immediate" claim is stale doc drift (tracked by F10).
func TestVerify_InPlaceScaleOnlyPacing_Contract(t *testing.T) {
	set := rollingVerifySet("inplace-scale", 3, inPlaceStrategy(1))
	d := newInPlaceDrive(t, set)
	d.settle(20)

	// Replicas-only template diff: same pod template, role replicas 1 -> 2.
	latest := d.getSet()
	latest.Spec.GroupTemplate.Spec.Roles[0].Replicas = ptr.To(int32(2))
	require.NoError(t, d.c.Update(d.ctx, latest))

	d.step()
	scaled := 0
	for _, item := range d.listChildren() {
		if ptr.Deref(item.Spec.Roles[0].Replicas, -1) == 2 {
			scaled++
		}
	}
	assert.Equal(t, 1, scaled,
		"a replicas-only diff under InPlaceUpdate is paced by the rollout budget (one child per window)")
}

// TestVerify_RecreateScaleOnlyPacedInPlace_Contract replaces the round-4 canary
// TestVerify_RecreateScaleOnlyIsUnpaced, which FLIPPED on 4a9043c3 ("enforce
// availability budgets for Recreate replica updates"): a replicas-only diff under
// Recreate is now budget-paced like any other diff, but still applied IN PLACE
// (child identity preserved). This contract pins the new intended behavior.
func TestVerify_RecreateScaleOnlyPacedInPlace_Contract(t *testing.T) {
	set := rollingVerifySet("recreate-scale", 3, intPtrStrategy(1, 0, 0))
	d := newInPlaceDrive(t, set)
	d.settle(20)
	d.deleted = map[string]bool{}

	latest := d.getSet()
	latest.Spec.GroupTemplate.Spec.Roles[0].Replicas = ptr.To(int32(2))
	require.NoError(t, d.c.Update(d.ctx, latest))

	d.step()
	scaled := 0
	for _, item := range d.listChildren() {
		if ptr.Deref(item.Spec.Roles[0].Replicas, -1) == 2 {
			scaled++
		}
	}
	assert.Equal(t, 1, scaled,
		"maxUnavailable=1 paces a Recreate replicas-only diff to one child per readiness window")

	d.settle(40)
	for _, item := range d.listChildren() {
		assert.Equal(t, int32(2), ptr.Deref(item.Spec.Roles[0].Replicas, -1),
			"child %s must converge to the new replicas", item.Name)
		assert.False(t, d.deleted[item.Name],
			"replicas-only diff must update %s in place, never recreate it", item.Name)
	}
	final := d.getSet()
	assert.Equal(t, "RolloutComplete", rollingCondition(t, final).Reason)
}

// TestVerify_GroupSetChildReadyBarrier pins the readiness gate the InPlace budget
// relies on: a child that is Ready but has a RollingUpdateInProgress=True barrier at
// its current generation must NOT count as serving.
func TestVerify_GroupSetChildReadyBarrier(t *testing.T) {
	mk := func(mut func(*workloadsv1alpha2.RoleBasedGroup)) *workloadsv1alpha2.RoleBasedGroup {
		child := &workloadsv1alpha2.RoleBasedGroup{
			ObjectMeta: metav1.ObjectMeta{Name: "g-0", Namespace: "default", Generation: 2},
		}
		child.Status.ObservedGeneration = 2
		meta.SetStatusCondition(&child.Status.Conditions, metav1.Condition{
			Type: string(workloadsv1alpha2.RoleBasedGroupReady), Status: metav1.ConditionTrue,
			Reason: "Ready", ObservedGeneration: 2,
		})
		mut(child)
		return child
	}
	// No barrier condition (old RBG controller): ready.
	assert.True(t, groupSetChildReady(mk(func(*workloadsv1alpha2.RoleBasedGroup) {})))
	// Barrier True at the current generation: not serving.
	assert.False(t, groupSetChildReady(mk(func(c *workloadsv1alpha2.RoleBasedGroup) {
		meta.SetStatusCondition(&c.Status.Conditions, metav1.Condition{
			Type:   string(workloadsv1alpha2.RoleBasedGroupRollingUpdateInProgress),
			Status: metav1.ConditionTrue, Reason: "RolesUpdating", ObservedGeneration: 2,
		})
	})))
	// Barrier False but observed at a stale generation: not serving.
	assert.False(t, groupSetChildReady(mk(func(c *workloadsv1alpha2.RoleBasedGroup) {
		meta.SetStatusCondition(&c.Status.Conditions, metav1.Condition{
			Type:   string(workloadsv1alpha2.RoleBasedGroupRollingUpdateInProgress),
			Status: metav1.ConditionFalse, Reason: "AllRolesUpdated", ObservedGeneration: 1,
		})
	})))
	// Barrier False at the current generation: serving.
	assert.True(t, groupSetChildReady(mk(func(c *workloadsv1alpha2.RoleBasedGroup) {
		meta.SetStatusCondition(&c.Status.Conditions, metav1.Condition{
			Type:   string(workloadsv1alpha2.RoleBasedGroupRollingUpdateInProgress),
			Status: metav1.ConditionFalse, Reason: "AllRolesUpdated", ObservedGeneration: 2,
		})
	})))
	// Ready but generation not yet observed: not serving.
	assert.False(t, groupSetChildReady(mk(func(c *workloadsv1alpha2.RoleBasedGroup) {
		c.Status.ObservedGeneration = 1
	})))
}

var _ = intstr.FromInt // keep import if unused after edits
