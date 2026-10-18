// Reviewer verification harness for PR #501 (perf: throttle stateful instance
// health pruning). ADDITIVE ONLY — production code untouched.
//
// All tests are CONTRACT polarity: they assert the intended behavior of the
// throttled prune. They FAIL on un-throttled code (see harness-bites evidence
// in docs/verification/pr-501-health-prune-throttle-codex/results/).
//
// Symbols are prefixed verifyPr501 so this file stays compilable even if the
// PR's own tests are renamed or dropped by a later revision (in which case
// treat a compile break as Harness-update, not as proof of a fix).
package statefulmode

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	instancelisters "sigs.k8s.io/rbgs/client-go/listers/workloads/v1alpha2"
)

type verifyPr501CountingIndexer struct {
	cache.Indexer
	lookups atomic.Int32
}

func (i *verifyPr501CountingIndexer) GetByKey(key string) (interface{}, bool, error) {
	i.lookups.Add(1)
	return i.Indexer.GetByKey(key)
}

// verifyPr501SeedSet builds one live set with one unhealthy-instance timer and
// one early-rollback signal recorded for it.
func verifyPr501SeedSet(t *testing.T, indexer *verifyPr501CountingIndexer, name, namespace, uid string) *workloadsv1alpha2.RoleInstanceSet {
	t.Helper()
	set := buildSet(name, 1, nil, nil)
	set.Namespace = namespace
	set.UID = types.UID("uid-" + uid)
	if err := indexer.Add(set); err != nil {
		t.Fatal(err)
	}
	instanceUnhealthySince.Store(getInstanceHealthKey(set, buildInst(name, 0, testOldRev, false, true)), time.Now())
	recordEarlyRollbackReplacementSignal(set, 0, testUpdateRev)
	return set
}

func verifyPr501NewHarness(t *testing.T) *verifyPr501CountingIndexer {
	t.Helper()
	resetInstanceUnhealthySince()
	resetEarlyRollbackReplacementSignals()
	t.Cleanup(resetInstanceUnhealthySince)
	t.Cleanup(resetEarlyRollbackReplacementSignals)
	return &verifyPr501CountingIndexer{Indexer: cache.NewIndexer(cache.MetaNamespaceKeyFunc, nil)}
}

// C1: within the interval, repeated prune calls perform no lister lookups;
// a call at the exact interval boundary sweeps again.
func TestVerifyPr501PruneThrottledWithinInterval(t *testing.T) {
	indexer := verifyPr501NewHarness(t)
	verifyPr501SeedSet(t, indexer, "prefill", "default", "a")
	controller := &ReconcileStatefulInstanceSet{roleInstanceSetLister: instancelisters.NewRoleInstanceSetLister(indexer)}
	now := time.Now()

	controller.pruneInstanceHealth(now)
	if got := indexer.lookups.Load(); got != 2 {
		t.Fatalf("first call must sweep exactly once: got %d lookups, want 2", got)
	}
	controller.pruneInstanceHealth(now.Add(instanceHealthPruneInterval - time.Nanosecond))
	if got := indexer.lookups.Load(); got != 2 {
		t.Fatalf("call within interval must not sweep: got %d lookups, want 2", got)
	}
	controller.pruneInstanceHealth(now.Add(instanceHealthPruneInterval))
	if got := indexer.lookups.Load(); got != 4 {
		t.Fatalf("call at interval boundary must sweep: got %d lookups, want 4", got)
	}
}

// C2: 32 concurrent prune calls (>= the controller's worker count) result in
// exactly one sweep — no worker blocks and no sweep overlaps.
func TestVerifyPr501ConcurrentCallsSweepOnce(t *testing.T) {
	indexer := verifyPr501NewHarness(t)
	verifyPr501SeedSet(t, indexer, "prefill", "default", "a")
	controller := &ReconcileStatefulInstanceSet{roleInstanceSetLister: instancelisters.NewRoleInstanceSetLister(indexer)}
	now := time.Now()
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := 0; i < 32; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			controller.pruneInstanceHealth(now)
		}()
	}
	close(start)
	workers.Wait()
	if got := indexer.lookups.Load(); got != 2 {
		t.Fatalf("32 concurrent calls must produce exactly one sweep: got %d lookups, want 2", got)
	}
}

// C3: throttling must not lose the cleanup contract — once a sweep is due,
// entries of a deleted (or UID-replaced) set are reclaimed while another
// set's entries stay untouched.
func TestVerifyPr501StaleRecordsReclaimedAfterInterval(t *testing.T) {
	for _, state := range []string{"deleted", "recreated"} {
		t.Run(state, func(t *testing.T) {
			indexer := verifyPr501NewHarness(t)
			set := verifyPr501SeedSet(t, indexer, "prefill", "default", "a")
			other := verifyPr501SeedSet(t, indexer, "prefill", "other", "other")

			controller := &ReconcileStatefulInstanceSet{roleInstanceSetLister: instancelisters.NewRoleInstanceSetLister(indexer)}
			now := time.Now()
			controller.pruneInstanceHealth(now) // first sweep

			if state == "deleted" {
				if err := indexer.Delete(set); err != nil {
					t.Fatal(err)
				}
			} else {
				replacement := set.DeepCopy()
				replacement.UID = "uid-replacement"
				if err := indexer.Update(replacement); err != nil {
					t.Fatal(err)
				}
			}

			// Not due yet: stale entries must still be present (nothing swept).
			controller.pruneInstanceHealth(now.Add(instanceHealthPruneInterval - time.Nanosecond))
			if _, ok := instanceUnhealthySince.Load(getInstanceHealthKey(set, buildInst(set.Name, 0, testOldRev, false, true))); !ok {
				t.Fatal("unhealthy timer vanished before any due sweep")
			}

			// Due sweep: stale entries of the deleted/replaced set are reclaimed.
			controller.pruneInstanceHealth(now.Add(instanceHealthPruneInterval))
			if _, ok := instanceUnhealthySince.Load(getInstanceHealthKey(set, buildInst(set.Name, 0, testOldRev, false, true))); ok {
				t.Fatal("stale unhealthy timer was not reclaimed by the due sweep")
			}
			if _, ok := earlyRollbackReplacementSignals.Load(getInstanceSetKey(set)); ok {
				t.Fatal("stale rollback signal was not reclaimed by the due sweep")
			}
			// Isolation: the other namespace's same-named set keeps its records.
			if _, ok := instanceUnhealthySince.Load(getInstanceHealthKey(other, buildInst(other.Name, 0, testOldRev, false, true))); !ok {
				t.Fatal("another set's unhealthy timer was removed")
			}
			if _, ok := earlyRollbackReplacementSignals.Load(getInstanceSetKey(other)); !ok {
				t.Fatal("another set's rollback signal was removed")
			}
		})
	}
}
