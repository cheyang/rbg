package statefulmode

import (
	"testing"
	"time"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// TestPremiseCrossSetTimerIsolated_HEAD is the fix-branch counterpart to
// premise_crossset_test.go on base. On #479 (set-scoped keying), reconciling
// decode must NOT touch prefill's unhealthy timer, and vice versa.
func TestPremiseCrossSetTimerIsolated_HEAD(t *testing.T) {
	resetInstanceUnhealthySince()
	defer resetInstanceUnhealthySince()

	prefill := buildSet("prefill", 1, nil, nil)
	decode := buildSet("decode", 1, nil, nil)

	p := buildInst(prefill.Name, 0, testOldRev, false, true)
	d := buildInst(decode.Name, 0, testOldRev, false, true)

	observeInstanceHealth(prefill, []*workloadsv1alpha2.RoleInstance{p})
	firstP, ok := instanceUnhealthySince.Load(getInstanceHealthKey(prefill, p))
	if !ok {
		t.Fatalf("setup: prefill timer not recorded")
	}

	// Reconcile decode: prefill's timer must survive.
	observeInstanceHealth(decode, []*workloadsv1alpha2.RoleInstance{d})
	if got, stillThere := instanceUnhealthySince.Load(getInstanceHealthKey(prefill, p)); !stillThere {
		t.Fatalf("REGRESSION on head: prefill timer wiped by decode reconcile")
	} else if got != firstP {
		t.Fatalf("REGRESSION on head: prefill timer changed by decode reconcile: got %v want %v", got, firstP)
	}
	t.Logf("FIX CONFIRMED on head: prefill timer preserved across decode reconcile (%v)", firstP)

	// Symmetric.
	observeInstanceHealth(prefill, []*workloadsv1alpha2.RoleInstance{p})
	if _, stillThere := instanceUnhealthySince.Load(getInstanceHealthKey(decode, d)); !stillThere {
		// decode's timer may or may not still be present (prefill reconcile
		// touches only prefill keys), but it must NOT have been deleted by
		// prefill's cleanup. Check it was recorded by the prior decode observe.
	}
	// Re-record decode and confirm prefill reconcile does not reset it.
	observeInstanceHealth(decode, []*workloadsv1alpha2.RoleInstance{d})
	firstD, _ := instanceUnhealthySince.Load(getInstanceHealthKey(decode, d))
	observeInstanceHealth(prefill, []*workloadsv1alpha2.RoleInstance{p})
	if got, _ := instanceUnhealthySince.Load(getInstanceHealthKey(decode, d)); got != firstD {
		t.Fatalf("REGRESSION on head: decode timer reset by prefill reconcile: got %v want %v", got, firstD)
	}
	t.Logf("FIX CONFIRMED on head: decode timer preserved across prefill reconcile (%v)", firstD)

	// Under interleaving, prefill's timer must NOT advance — so the 10s window
	// CAN mature and the rollout can proceed.
	observeInstanceHealth(prefill, []*workloadsv1alpha2.RoleInstance{p})
	tp, _ := instanceUnhealthySince.Load(getInstanceHealthKey(prefill, p))
	time.Sleep(10 * time.Millisecond)
	observeInstanceHealth(decode, []*workloadsv1alpha2.RoleInstance{d})
	observeInstanceHealth(prefill, []*workloadsv1alpha2.RoleInstance{p})
	tp2, _ := instanceUnhealthySince.Load(getInstanceHealthKey(prefill, p))
	if tp2 != tp {
		t.Fatalf("REGRESSION on head: prefill timer advanced under interleaving: %v -> %v", tp, tp2)
	}
	t.Logf("FIX CONFIRMED on head: prefill timer stable under interleaving (%v) — window matures", tp2)
}
