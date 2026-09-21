package statefulmode

import (
	"testing"
	"time"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// TestPremiseCrossSetTimerWipe demonstrates PR #479's premise on the BASE
// branch (#470, pre-#479): observeInstanceHealth keys the unhealthy-timer map
// by RoleInstance UID only, and its cleanup pass deletes any UID absent from
// the supplied instances slice. Because only the current set's instances are
// supplied, reconciling set B deletes set A's unhealthy start time, and vice
// versa — so neither set's 10s unhealthy window can complete.
func TestPremiseCrossSetTimerWipe(t *testing.T) {
	resetInstanceUnhealthySince()
	defer resetInstanceUnhealthySince()

	prefill := buildSet("prefill", 1, nil, nil)
	decode := buildSet("decode", 1, nil, nil)

	p := buildInst(prefill.Name, 0, testOldRev, false, true) // unhealthy
	d := buildInst(decode.Name, 0, testOldRev, false, true)  // unhealthy

	// Reconcile prefill: its unhealthy instance gets a timer.
	observeInstanceHealth([]*workloadsv1alpha2.RoleInstance{p})
	firstP, ok := instanceUnhealthySince.Load(p.UID)
	if !ok {
		t.Fatalf("premise setup: prefill timer not recorded")
	}

	// Reconcile decode (its instances only). On base, the cleanup pass sees
	// p.UID absent from decode's instances and DELETES it.
	observeInstanceHealth([]*workloadsv1alpha2.RoleInstance{d})

	if _, stillThere := instanceUnhealthySince.Load(p.UID); stillThere {
		t.Fatalf("PREMISE NOT REPRODUCED on base: prefill timer survived decode reconcile = %v", firstP)
	}
	t.Logf("PREMISE CONFIRMED on base: prefill unhealthy timer wiped by decode reconcile (was %v)", firstP)

	// Symmetric: reconciling prefill again wipes decode's timer.
	observeInstanceHealth([]*workloadsv1alpha2.RoleInstance{p})
	if _, stillThere := instanceUnhealthySince.Load(d.UID); stillThere {
		t.Fatalf("PREMISE NOT REPRODUCED: decode timer survived prefill reconcile")
	}
	t.Logf("PREMISE CONFIRMED on base: decode unhealthy timer wiped by prefill reconcile")

	// Consequence: each reconcile starts a FRESH 10s window for its own
	// instance, so the window never matures if sets interleave faster than
	// stableUnhealthyDuration.
	observeInstanceHealth([]*workloadsv1alpha2.RoleInstance{p})
	tp, _ := instanceUnhealthySince.Load(p.UID)
	time.Sleep(10 * time.Millisecond)
	observeInstanceHealth([]*workloadsv1alpha2.RoleInstance{d})
	observeInstanceHealth([]*workloadsv1alpha2.RoleInstance{p})
	tp2, _ := instanceUnhealthySince.Load(p.UID)
	if tp2.(time.Time).Sub(tp.(time.Time)) > 0 {
		t.Logf("PREMISE CONFIRMED on base: prefill timer restarted at %v (was %v) — window reset, never matures under interleaving", tp2, tp)
	} else {
		t.Fatalf("PREMISE: timer not restarted as expected")
	}
}
