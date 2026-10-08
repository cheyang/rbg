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

// Verification harness for PR #474 (reviewer additive tests; production code untouched).
//
// These tests drive the full rolling reconcile entry point (reconcileRollingGroupSet)
// in a loop against a fake client, simulating the child RoleBasedGroup controller by
// marking children Ready after a startup cooldown. Per step they assert the budget
// invariant the PR promises: the controller never deletes more ready groups than the
// budget (maxUnavailable widened by ready surge) allows, and the rollout converges to
// a quiet steady state (no hot requeue loop).
//
// Polarity: all tests in this file are CONTRACT tests - they assert the intended
// behavior and must FAIL on buggy code and PASS on correct code.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// rollingDrive is a step-by-step driver around reconcileRollingGroupSet. The fake
// client assigns no UIDs, so recreation is tracked by name disappearance instead.
type rollingDrive struct {
	t       *testing.T
	ctx     context.Context
	c       client.Client
	r       *RoleBasedGroupSetReconciler
	key     types.NamespacedName
	cool    map[string]int // child name -> reconcile steps until it reports Ready
	step    int
	deleted map[string]bool // child names that disappeared in some reconcile step
}

func newRollingDrive(t *testing.T, set *workloadsv1alpha2.RoleBasedGroupSet) *rollingDrive {
	scheme := runtime.NewScheme()
	require.NoError(t, workloadsv1alpha2.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(set).
		WithStatusSubresource(&workloadsv1alpha2.RoleBasedGroupSet{}, &workloadsv1alpha2.RoleBasedGroup{}).
		Build()
	return &rollingDrive{
		t:       t,
		ctx:     context.Background(),
		c:       c,
		r:       &RoleBasedGroupSetReconciler{client: c, apiReader: c, scheme: scheme},
		key:     client.ObjectKeyFromObject(set),
		cool:    map[string]int{},
		deleted: map[string]bool{},
	}
}

func rollingVerifySet(name string, replicas int32, strategy *workloadsv1alpha2.GroupSetRolloutStrategy) *workloadsv1alpha2.RoleBasedGroupSet {
	return &workloadsv1alpha2.RoleBasedGroupSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: workloadsv1alpha2.RoleBasedGroupSetSpec{
			Replicas: ptr.To(replicas),
			GroupTemplate: workloadsv1alpha2.RoleBasedGroupTemplateSpec{
				Spec: workloadsv1alpha2.RoleBasedGroupSpec{
					Roles: []workloadsv1alpha2.RoleSpec{{
						Name:     "worker",
						Replicas: ptr.To(int32(int32(1))),
						Pattern: workloadsv1alpha2.Pattern{
							StandalonePattern: &workloadsv1alpha2.StandalonePattern{
								TemplateSource: workloadsv1alpha2.TemplateSource{Template: premisePodTemplate("image:v1")},
							},
						},
					}},
				},
			},
			RolloutStrategy: strategy,
		},
	}
}

func intPtrStrategy(maxUnavailable, maxSurge, partition int) *workloadsv1alpha2.GroupSetRolloutStrategy {
	return &workloadsv1alpha2.GroupSetRolloutStrategy{
		Type:           workloadsv1alpha2.RecreateStrategyType,
		MaxUnavailable: ptr.To(intstr.FromInt(maxUnavailable)),
		MaxSurge:       ptr.To(intstr.FromInt(maxSurge)),
		Partition:      ptr.To(intstr.FromInt(partition)),
	}
}

func (d *rollingDrive) listChildren() []workloadsv1alpha2.RoleBasedGroup {
	children := &workloadsv1alpha2.RoleBasedGroupList{}
	require.NoError(d.t, d.c.List(d.ctx, children, client.InNamespace(d.key.Namespace)))
	return children.Items
}

// simulateChildren imitates the child RoleBasedGroup controller: a child reports Ready
// once it has survived its cooldown since creation/spec-change.
func (d *rollingDrive) simulateChildren() {
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
		if latest.Status.ObservedGeneration >= latest.Generation &&
			meta.IsStatusConditionTrue(latest.Status.Conditions, string(workloadsv1alpha2.RoleBasedGroupReady)) {
			continue
		}
		latest.Status.ObservedGeneration = latest.Generation
		meta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{
			Type: string(workloadsv1alpha2.RoleBasedGroupReady), Status: metav1.ConditionTrue,
			Reason: "Ready", ObservedGeneration: latest.Generation,
		})
		require.NoError(d.t, d.c.Status().Update(d.ctx, latest))
	}
}

func countReadyNonTerminating(children []workloadsv1alpha2.RoleBasedGroup) int {
	n := 0
	for i := range children {
		if children[i].DeletionTimestamp.IsZero() &&
			meta.IsStatusConditionTrue(children[i].Status.Conditions, string(workloadsv1alpha2.RoleBasedGroupReady)) {
			n++
		}
	}
	return n
}

// reconcileOnce runs one reconcile and enforces the per-step budget invariant: the
// number of ready groups the controller deletes in one pass must not exceed the budget
// headroom (ready - (replicas - maxUnavailable)) measured at pass entry.
func (d *rollingDrive) reconcileOnce(replicas, maxUnavailable int) ctrl.Result {
	d.simulateChildren()
	before := d.listChildren()
	readyBefore := countReadyNonTerminating(before)

	res, err := d.r.reconcileRollingGroupSet(d.ctx, d.key)
	require.NoError(d.t, err)
	d.step++

	after := d.listChildren()
	namesAfter := map[string]bool{}
	for i := range after {
		namesAfter[after[i].Name] = true
	}
	readyDeleted := 0
	for i := range before {
		if !namesAfter[before[i].Name] {
			readyDeleted++ // every child left by the simulate pass was Ready
			d.deleted[before[i].Name] = true
		}
	}
	headroom := readyBefore - max(0, replicas-maxUnavailable)
	assert.LessOrEqualf(d.t, readyDeleted, max(0, headroom),
		"step %d: deleted %d ready groups with ready=%d replicas=%d maxUnavailable=%d",
		d.step, readyDeleted, readyBefore, replicas, maxUnavailable)
	return res
}

func (d *rollingDrive) getSet() *workloadsv1alpha2.RoleBasedGroupSet {
	set := &workloadsv1alpha2.RoleBasedGroupSet{}
	require.NoError(d.t, d.c.Get(d.ctx, d.key, set))
	return set
}

func (d *rollingDrive) triggerTemplateChange(image string) {
	set := d.getSet()
	set.Spec.GroupTemplate.Spec.Roles[0].StandalonePattern.Template = premisePodTemplate(image)
	require.NoError(d.t, d.c.Update(d.ctx, set))
}

func childImage(child *workloadsv1alpha2.RoleBasedGroup) string {
	return child.Spec.Roles[0].StandalonePattern.Template.Spec.Containers[0].Image
}

// waitSettled reconciles until the controller itself stops asking for a requeue, or the
// step budget runs out.
func (d *rollingDrive) waitSettled(replicas, maxUnavailable, maxSteps int) ctrl.Result {
	var res ctrl.Result
	for i := 0; i < maxSteps; i++ {
		res = d.reconcileOnce(replicas, maxUnavailable)
		if res.RequeueAfter == 0 && !res.Requeue {
			return res
		}
	}
	d.t.Fatalf("did not settle within %d steps", maxSteps)
	return res
}

func rollingCondition(t *testing.T, set *workloadsv1alpha2.RoleBasedGroupSet) *metav1.Condition {
	cond := meta.FindStatusCondition(set.Status.Conditions, string(workloadsv1alpha2.RoleBasedGroupSetRolling))
	require.NotNil(t, cond)
	return cond
}

func TestVerify_RollingRecreatePacedAndConverges(t *testing.T) {
	set := rollingVerifySet("roll", 3, intPtrStrategy(1, 0, 0))
	d := newRollingDrive(t, set)

	d.waitSettled(3, 1, 20)
	require.Len(t, d.listChildren(), 3)
	d.deleted = map[string]bool{}

	// A real template change: 3 groups must be recreated one at a time.
	d.triggerTemplateChange("image:v2")
	for i := 0; i < 30; i++ {
		res := d.reconcileOnce(3, 1)
		// Invariant: at most one group may be down at a time (maxUnavailable=1, no surge).
		assert.GreaterOrEqualf(t, countReadyNonTerminating(d.listChildren()), 2,
			"step %d: ready groups dropped below replicas-maxUnavailable", d.step)
		if res.RequeueAfter == 0 && !res.Requeue {
			break
		}
	}

	children := d.listChildren()
	require.Len(t, children, 3)
	for i := range children {
		assert.Equal(t, "image:v2", childImage(&children[i]))
		assert.True(t, d.deleted[children[i].Name], "child %s must have been recreated", children[i].Name)
	}

	final := d.getSet()
	rolling := rollingCondition(t, final)
	assert.Equal(t, metav1.ConditionFalse, rolling.Status)
	assert.Equal(t, "RolloutComplete", rolling.Reason)
	assert.Equal(t, int32(3), final.Status.UpdatedReplicas)
	assert.NotEmpty(t, final.Status.CurrentRevision)
	assert.Equal(t, final.Status.CurrentRevision, final.Status.UpdateRevision)

	// Steady state: three more reconciles must not requeue (no hot loop).
	for i := 0; i < 3; i++ {
		res := d.reconcileOnce(3, 1)
		assert.False(t, res.Requeue)
		assert.Zero(t, res.RequeueAfter, "steady state must not requeue")
	}
	require.Len(t, d.listChildren(), 3)
	t.Logf("converged in %d reconcile steps", d.step)
}

func TestVerify_RollingSurgeBacksZeroMaxUnavailable(t *testing.T) {
	set := rollingVerifySet("surge", 2, intPtrStrategy(0, 1, 0))
	d := newRollingDrive(t, set)
	d.waitSettled(2, 0, 20)

	d.triggerTemplateChange("image:v2")
	peakChildren := 0
	for i := 0; i < 40; i++ {
		res := d.reconcileOnce(2, 0)
		children := d.listChildren()
		peakChildren = max(peakChildren, len(children))
		// Invariant: maxUnavailable=0 with surge=1 must never drop serving below replicas.
		assert.GreaterOrEqualf(t, countReadyNonTerminating(children), 2,
			"step %d: serving groups dropped below replicas with surge backing", d.step)
		if res.RequeueAfter == 0 && !res.Requeue {
			break
		}
	}
	assert.Equal(t, 3, peakChildren, "expected exactly one surge group at peak")
	// Surge is reclaimed after completion.
	children := d.listChildren()
	require.Len(t, children, 2)
	for i := range children {
		assert.Equal(t, "image:v2", childImage(&children[i]))
	}
	assert.Equal(t, "RolloutComplete", rollingCondition(t, d.getSet()).Reason)
}

func TestVerify_RollingPartitionHoldsBackAndRebuildsAtCurrent(t *testing.T) {
	set := rollingVerifySet("part", 4, intPtrStrategy(1, 0, 2))
	d := newRollingDrive(t, set)
	d.waitSettled(4, 1, 20)
	d.deleted = map[string]bool{}

	d.triggerTemplateChange("image:v2")
	d.waitSettled(4, 1, 40)

	byOrdinal := map[int]*workloadsv1alpha2.RoleBasedGroup{}
	for i := range d.listChildren() {
		child := &d.listChildren()[i]
		idx, ok := groupSetOrdinal(d.getSet(), child)
		require.True(t, ok)
		byOrdinal[idx] = child
	}
	// Ordinals below the partition keep the old template and their identity.
	for _, idx := range []int{0, 1} {
		assert.Equal(t, "image:v1", childImage(byOrdinal[idx]), "ordinal %d must be held back", idx)
		assert.False(t, d.deleted[byOrdinal[idx].Name], "ordinal %d must not be recreated", idx)
	}
	for _, idx := range []int{2, 3} {
		assert.Equal(t, "image:v2", childImage(byOrdinal[idx]), "ordinal %d must roll", idx)
		assert.True(t, d.deleted[byOrdinal[idx].Name], "ordinal %d must be recreated", idx)
	}

	final := d.getSet()
	rolling := rollingCondition(t, final)
	assert.Equal(t, metav1.ConditionFalse, rolling.Status)
	assert.Equal(t, "PartitionComplete", rolling.Reason)
	assert.Equal(t, int32(2), final.Status.ExpectedUpdatedReplicas)

	// A held-back group that disappears must be rebuilt at the OLD (current) revision.
	require.NoError(t, d.c.Delete(d.ctx, byOrdinal[0]))
	d.waitSettled(4, 1, 20)
	rebuilt := &workloadsv1alpha2.RoleBasedGroup{}
	require.NoError(t, d.c.Get(d.ctx, types.NamespacedName{Name: "part-0", Namespace: "default"}, rebuilt))
	assert.Equal(t, "image:v1", childImage(rebuilt),
		"a group rebuilt below the partition must come back on the previous template")
}

func TestVerify_RollingTemplateFlipFlopConverges(t *testing.T) {
	set := rollingVerifySet("flip", 3, intPtrStrategy(1, 0, 0))
	d := newRollingDrive(t, set)
	d.waitSettled(3, 1, 20)

	// Start rolling to v2, let part of the set move, then flip back to v1.
	d.triggerTemplateChange("image:v2")
	d.reconcileOnce(3, 1)
	d.reconcileOnce(3, 1)
	d.triggerTemplateChange("image:v1")
	d.waitSettled(3, 1, 40)

	for i := range d.listChildren() {
		assert.Equal(t, "image:v1", childImage(&d.listChildren()[i]))
	}
	assert.Equal(t, "RolloutComplete", rollingCondition(t, d.getSet()).Reason)
}

func TestVerify_RollingReplicasOnlyChangeScalesInPlace(t *testing.T) {
	set := rollingVerifySet("scale", 2, intPtrStrategy(1, 0, 0))
	d := newRollingDrive(t, set)
	d.waitSettled(2, 1, 20)
	d.deleted = map[string]bool{}

	// Replicas-only diff inside the group template: must update in place, never recreate.
	latest := d.getSet()
	latest.Spec.GroupTemplate.Spec.Roles[0].Replicas = ptr.To(int32(3))
	require.NoError(t, d.c.Update(d.ctx, latest))
	d.waitSettled(2, 1, 20)

	for i := range d.listChildren() {
		child := &d.listChildren()[i]
		assert.False(t, d.deleted[child.Name], "replicas-only diff must not recreate %s", child.Name)
		assert.Equal(t, int32(3), *child.Spec.Roles[0].Replicas)
	}
}

func TestVerify_RollingScaleDownDuringRollout(t *testing.T) {
	set := rollingVerifySet("scaledown", 4, intPtrStrategy(1, 0, 0))
	d := newRollingDrive(t, set)
	d.waitSettled(4, 1, 20)

	d.triggerTemplateChange("image:v2")
	d.reconcileOnce(4, 1)
	// Scale in mid-rollout; out-of-range ordinals must go away budget-gated.
	latest := d.getSet()
	latest.Spec.Replicas = ptr.To(int32(2))
	require.NoError(t, d.c.Update(d.ctx, latest))
	d.waitSettled(2, 1, 40)

	children := d.listChildren()
	require.Len(t, children, 2)
	for i := range children {
		idx, ok := groupSetOrdinal(d.getSet(), &children[i])
		require.True(t, ok)
		assert.Less(t, idx, 2)
		assert.Equal(t, "image:v2", childImage(&children[i]))
	}
}

func TestVerify_ControllerClampsPercentZeroZero_Canary(t *testing.T) {
	// CANARY (controller side of the webhook inconsistency): a resolved 0/0 is silently
	// rewritten to maxUnavailable=1 instead of being rejected.
	mu := intstr.FromString("0%")
	ms := intstr.FromString("0%")
	set := &workloadsv1alpha2.RoleBasedGroupSet{
		Spec: workloadsv1alpha2.RoleBasedGroupSetSpec{
			Replicas: ptr.To(int32(3)),
			RolloutStrategy: &workloadsv1alpha2.GroupSetRolloutStrategy{
				Type: workloadsv1alpha2.RecreateStrategyType, MaxUnavailable: &mu, MaxSurge: &ms,
			},
		},
	}
	limits, err := resolveGroupSetRollout(set)
	assert.NoError(t, err)
	assert.Equal(t, 1, limits.maxUnavailable, "canary: controller silently clamps 0 to 1")
	assert.Equal(t, 0, limits.maxSurge)
}
