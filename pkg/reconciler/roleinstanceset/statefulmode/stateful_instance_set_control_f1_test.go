// F1 canary — additive harness for PR #470 review verification.
//
// Claim: the fix only schedules a requeue from the FIRST budget-exhausted
// target (buildUpdateTargets is highest-ordinal-first). When that first blocker
// is healthy (instanceUnhealthySince entry deleted by observeInstanceHealth)
// while a DIFFERENT freshly-unhealthy target carries a pending 10s window, no
// requeue is pushed and the rollout stalls for that config.
//
// Polarity: BUG-CANARY. On the current PR head the asserted (buggy) behavior is
// Pop(key)==0 (no requeue). If the fix is extended to scan all stalled targets
// for the earliest expiring window, this flips to fail and must be inverted
// (promote to a contract test asserting a positive wait <= remaining window).
package statefulmode

import (
	"context"
	"testing"
	"time"

	apps "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	intstrutil "k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/rbgs/api/workloads/constants"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// TestF1MixedHealthyBlockerStillStalls verifies that PR #470's requeue is not
// scheduled when the first budget-exhausted target is healthy (no unhealthy-since
// entry) even though a different freshly-unhealthy target has a pending window.
func TestF1MixedHealthyBlockerStillStalls(t *testing.T) {
	resetInstanceUnhealthySince()
	t.Cleanup(resetInstanceUnhealthySince)

	set := newRevisionTestSet("nginx:1.0")
	set.Spec.Replicas = ptr.To[int32](2)
	set.Spec.PodManagementPolicy = constants.ParallelPodManagement
	set.Spec.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{"app": set.Name}}
	set.Spec.UpdateStrategy.MaxSurge = ptr.To(intstrutil.FromInt32(0))
	set.Spec.UpdateStrategy.MaxUnavailable = ptr.To(intstrutil.FromInt32(1)) // budget=1
	// Two revisions so updateRev != currentRev and both ordinals need updating.
	currentRev, err := newRevision(set, 1, ptr.To[int32](0))
	if err != nil {
		t.Fatal(err)
	}
	set.Spec.RoleInstanceTemplate.Components[0].Template.Spec.Containers[0].Image = "nginx:1.1"
	updateRev, err := newRevision(set, 2, ptr.To[int32](0))
	if err != nil {
		t.Fatal(err)
	}
	set.Status.CurrentRevision = currentRev.Name
	set.Status.UpdateRevision = updateRev.Name

	// ord 1 (highest) = HEALTHY at currentRev; ord 0 = freshly unhealthy at currentRev.
	instances := []*workloadsv1alpha2.RoleInstance{
		buildInst(set.Name, 0, currentRev.Name, false, true), // unhealthy
		buildInst(set.Name, 1, currentRev.Name, true, true),  // healthy
	}
	// Seed ord 0 as freshly-unhealthy with a pending ~5s window. observeInstanceHealth
	// uses LoadOrStore so this entry survives (it does not overwrite an existing entry);
	// ord 1 is healthy so its entry (if any) is deleted by observeInstanceHealth.
	seedObserved := time.Now().Add(-5 * time.Second)
	instanceUnhealthySince.Store(instances[0].UID, seedObserved)

	key := getInstanceSetKey(set)
	durationStore.Pop(key)
	t.Cleanup(func() { durationStore.Pop(key) })

	objectManager := &fakeInstanceObjectManager{}
	recorder := record.NewFakeRecorder(64)
	control := &defaultStatefulInstanceSetControl{
		instanceControl: NewStatefulInstanceControlFromManager(objectManager, recorder),
		inplaceControl:  &fakeInplaceControl{},
		recorder:        recorder,
	}
	_, err = control.updateStatefulInstanceSet(context.Background(), set, currentRev, updateRev, 0,
		instances, []*apps.ControllerRevision{currentRev, updateRev})
	if err != nil {
		t.Fatal(err)
	}
	wait := durationStore.Pop(key)

	// BUG-CANARY: the fix returns on the first budget-exhausted target (ord 1,
	// healthy, no unhealthy-since entry) and never reaches ord 0's pending
	// window. No requeue is pushed, so the mixed-config rollout still stalls.
	if wait != 0 {
		t.Fatalf("F1 canary: expected NO requeue (wait=0) for healthy-blocker mixed config, got wait=%v — "+
			"the fix now scans all stalled targets; invert this canary into a contract test asserting a "+
			"positive wait <= 5s (the ord-0 remaining window).", wait)
	}
	if len(objectManager.deleted) != 0 {
		t.Fatalf("F1 canary: instances deleted unexpectedly: %v", objectManager.deleted)
	}
	// Sanity: ord 0 still has a pending window (the requeue the fix missed).
	if first, ok := instanceUnhealthySince.Load(instances[0].UID); ok {
		if ft, ok := first.(time.Time); !ok || time.Until(ft.Add(stableUnhealthyDuration)) <= 0 {
			t.Fatalf("F1 canary: ord 0 window already expired, scenario is not 'freshly unhealthy'")
		}
	} else {
		t.Fatalf("F1 canary: ord 0 lost its unhealthy-since entry; observeInstanceHealth changed scenario")
	}
}

// Ensure the types import is exercised even if a future refactor drops a usage.
var _ = types.UID("")
