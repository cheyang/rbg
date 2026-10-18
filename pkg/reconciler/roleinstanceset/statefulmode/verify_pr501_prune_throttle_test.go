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

package statefulmode

// Reviewer verification harness for PR #501 ("perf: throttle stateful
// instance health pruning"), branch verify/pr501-prune-throttle-claude.
//
// Additive only: no production code is changed on this branch. The tests
// below are the reviewer's independent regression suite for the throttled
// prune behavior; they intentionally re-derive the contracts from the
// linked issue #481 rather than trusting the PR's own tests.
//
// Layer: deterministic unit (L1). The claim under test is pure in-memory
// control flow around two package-level sync.Maps plus one informer-lister
// read per entry, which a cache.Indexer fake exercises fully.

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	instancelisters "sigs.k8s.io/rbgs/client-go/listers/workloads/v1alpha2"
	"k8s.io/client-go/tools/cache"
)

// v501CountingIndexer counts lister reads so tests can observe how many
// entries a prune call actually touched. Independent of the PR's own
// healthCountingIndexer so later PR revisions cannot silently change what
// this harness measures.
type v501CountingIndexer struct {
	cache.Indexer
	lookups atomic.Int32
}

func (i *v501CountingIndexer) GetByKey(key string) (interface{}, bool, error) {
	i.lookups.Add(1)
	return i.Indexer.GetByKey(key)
}

// TestV501PruneSweepCadence is the core contract test for the throttle:
// the first call sweeps, calls within instanceHealthPruneInterval perform
// no informer lookups at all, and the first call at/after the interval
// sweeps again and reclaims stale records for deleted or replaced sets
// while leaving other sets' records (including same-named sets in other
// namespaces) untouched.
func TestV501PruneSweepCadence(t *testing.T) {
	for _, state := range []string{"deleted", "replaced"} {
		t.Run(state, func(t *testing.T) {
			resetInstanceUnhealthySince()
			resetEarlyRollbackReplacementSignals()
			t.Cleanup(resetInstanceUnhealthySince)
			t.Cleanup(resetEarlyRollbackReplacementSignals)

			set := buildSet("cadence", 1, nil, nil)
			set.UID = "cadence-set"
			otherSet := set.DeepCopy()
			otherSet.Namespace = "other"
			otherSet.UID = "other-set"
			instance := buildInst(set.Name, 0, testOldRev, false, true)
			now := time.Now()
			firstObserved := now.Add(-2 * stableUnhealthyDuration)

			indexer := &v501CountingIndexer{Indexer: cache.NewIndexer(cache.MetaNamespaceKeyFunc, nil)}
			for _, liveSet := range []*workloadsv1alpha2.RoleInstanceSet{set, otherSet} {
				if err := indexer.Add(liveSet); err != nil {
					t.Fatal(err)
				}
				instanceUnhealthySince.Store(getInstanceHealthKey(liveSet, instance), firstObserved)
				recordEarlyRollbackReplacementSignal(liveSet, 0, testUpdateRev)
			}
			controller := &ReconcileStatefulInstanceSet{roleInstanceSetLister: instancelisters.NewRoleInstanceSetLister(indexer)}

			// First call after startup must sweep: 2 unhealthy entries + 2
			// rollback signals = 4 lister reads.
			controller.pruneInstanceHealth(now)
			if got := indexer.lookups.Load(); got != 4 {
				t.Fatalf("first call made %d lister lookups, want 4", got)
			}

			// Calls within the interval must not touch the informer at all.
			for _, offset := range []time.Duration{time.Second, 30 * time.Second, instanceHealthPruneInterval - time.Nanosecond} {
				controller.pruneInstanceHealth(now.Add(offset))
			}
			if got := indexer.lookups.Load(); got != 4 {
				t.Fatalf("throttled calls made extra lookups: got %d, want 4", got)
			}

			// The set disappears (or is replaced) while the throttle window
			// is still open — throttled calls must still skip the sweep.
			if state == "deleted" {
				if err := indexer.Delete(set); err != nil {
					t.Fatal(err)
				}
			} else {
				replacement := set.DeepCopy()
				replacement.UID = "replacement-set"
				if err := indexer.Update(replacement); err != nil {
					t.Fatal(err)
				}
			}
			controller.pruneInstanceHealth(now.Add(30 * time.Second))
			if got := indexer.lookups.Load(); got != 4 {
				t.Fatalf("call mid-interval after set removal made extra lookups: got %d, want 4", got)
			}
			if _, ok := instanceUnhealthySince.Load(getInstanceHealthKey(set, instance)); !ok {
				t.Fatalf("%s: stale timer vanished before the due sweep", state)
			}

			// First call at the interval boundary sweeps and reclaims.
			controller.pruneInstanceHealth(now.Add(instanceHealthPruneInterval))
			if got := indexer.lookups.Load(); got != 8 {
				t.Fatalf("due sweep made %d total lookups, want 8", got)
			}
			if _, ok := instanceUnhealthySince.Load(getInstanceHealthKey(set, instance)); ok {
				t.Fatalf("%s: stale unhealthy timer was not reclaimed by the due sweep", state)
			}
			if _, ok := earlyRollbackReplacementSignals.Load(getInstanceSetKey(set)); ok {
				t.Fatalf("%s: stale rollback signal was not reclaimed by the due sweep", state)
			}

			// Isolation: the other set's records survive the sweep unchanged.
			if got, ok := instanceUnhealthySince.Load(getInstanceHealthKey(otherSet, instance)); !ok || got != firstObserved {
				t.Fatalf("another set's unhealthy timer changed: got %v, exists=%v", got, ok)
			}
			if _, ok := earlyRollbackReplacementSignals.Load(getInstanceSetKey(otherSet)); !ok {
				t.Fatalf("another set's rollback signal was removed by the sweep")
			}
		})
	}
}

// TestV501PruneNonBlockingWhileSweepInProgress verifies the PR's claim that
// "a non-blocking mutex ... lets other workers continue reconciling
// immediately": while a sweep is in progress (mutex held), another worker's
// prune call must return promptly instead of blocking. This is the
// behavior that distinguishes TryLock from a plain Lock; the PR's own
// TestPruneInstanceHealthConcurrent cannot tell them apart because the
// interval check also suppresses the second sweep.
func TestV501PruneNonBlockingWhileSweepInProgress(t *testing.T) {
	resetInstanceUnhealthySince()
	resetEarlyRollbackReplacementSignals()
	t.Cleanup(resetInstanceUnhealthySince)
	t.Cleanup(resetEarlyRollbackReplacementSignals)

	indexer := &v501CountingIndexer{Indexer: cache.NewIndexer(cache.MetaNamespaceKeyFunc, nil)}
	controller := &ReconcileStatefulInstanceSet{roleInstanceSetLister: instancelisters.NewRoleInstanceSetLister(indexer)}

	// Simulate a sweep in flight: hold the mutex the sweep would hold.
	controller.healthPruneMutex.Lock()
	done := make(chan struct{})
	go func() {
		// Contract: this call skips cleanup and returns without blocking.
		controller.pruneInstanceHealth(time.Now())
		close(done)
	}()
	select {
	case <-done:
		// Skipped, as intended. The skip must not advance the sweep clock.
		if !controller.lastHealthPrune.IsZero() {
			t.Fatalf("skipped call advanced lastHealthPrune to %v", controller.lastHealthPrune)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pruneInstanceHealth blocked while another sweep held the mutex; workers must skip, not wait")
	}
	controller.healthPruneMutex.Unlock()

	// After the sweep completes, the next call sweeps normally.
	controller.pruneInstanceHealth(time.Now())
	if controller.lastHealthPrune.IsZero() {
		t.Fatal("prune after mutex release did not record the sweep time")
	}
}

// TestV501DeferredSweepSafetyForRecreatedSet proves the throttle is
// correctness-safe, not just memory-lazy: within the throttle window (no
// sweep running at all), a same-name recreated set must not inherit the old
// set's unhealthy window or rollback signal, because readers key or guard
// on the set UID. This is what makes "reclaim later" acceptable.
func TestV501DeferredSweepSafetyForRecreatedSet(t *testing.T) {
	resetInstanceUnhealthySince()
	resetEarlyRollbackReplacementSignals()
	t.Cleanup(resetInstanceUnhealthySince)
	t.Cleanup(resetEarlyRollbackReplacementSignals)

	oldSet := buildSet("recreated", 2, nil, nil)
	oldSet.UID = "old-uid"
	instance := buildInst(oldSet.Name, 0, testOldRev, false, true)
	instanceUnhealthySince.Store(getInstanceHealthKey(oldSet, instance), time.Now().Add(-2*stableUnhealthyDuration))
	recordEarlyRollbackReplacementSignal(oldSet, 0, testUpdateRev)

	newSet := oldSet.DeepCopy()
	newSet.UID = "new-uid"

	// Unhealthy-timer side: the recreated set cannot see the old window...
	if isStablyUnhealthy(newSet, instance) {
		t.Fatal("recreated set inherited the old set's unhealthy window")
	}
	// ...and observeInstanceHealth reclaims the old-UID entry on its own,
	// without any prune sweep, while starting a fresh window.
	observeInstanceHealth(newSet, []*workloadsv1alpha2.RoleInstance{instance})
	if _, ok := instanceUnhealthySince.Load(getInstanceHealthKey(oldSet, instance)); ok {
		t.Fatal("old-UID unhealthy timer survived observeInstanceHealth without a prune sweep")
	}
	if _, ok := instanceUnhealthySince.Load(getInstanceHealthKey(newSet, instance)); !ok {
		t.Fatal("recreated set did not get a fresh unhealthy observation")
	}
	if isStablyUnhealthy(newSet, instance) {
		t.Fatal("freshly observed unhealthy instance must not be stably unhealthy yet")
	}

	// Rollback-signal side: the reader self-invalidates on setUID mismatch.
	replacement := buildInst(newSet.Name, 0, testUpdateRev, false, true)
	if earlyRollbackReplacementSignalActive(newSet, []*workloadsv1alpha2.RoleInstance{replacement}, 0, 2, testUpdateRev) {
		t.Fatal("recreated set inherited the old set's rollback signal")
	}
	if _, ok := earlyRollbackReplacementSignals.Load(getInstanceSetKey(newSet)); ok {
		t.Fatal("stale rollback signal survived a reader pass without a prune sweep")
	}
}

// BenchmarkV501PruneHotPath measures the per-reconcile prune cost with the
// throttle engaged (the PR's claimed win) and with a due sweep, at the map
// sizes the PR reports.
func BenchmarkV501PruneHotPath(b *testing.B) {
	for _, count := range []int{100, 10000} {
		b.Run(fmt.Sprint("entries=", count), func(b *testing.B) {
			resetInstanceUnhealthySince()
			resetEarlyRollbackReplacementSignals()
			b.Cleanup(resetInstanceUnhealthySince)
			b.Cleanup(resetEarlyRollbackReplacementSignals)
			set := buildSet("bench", int32(count), nil, nil)
			indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, nil)
			if err := indexer.Add(set); err != nil {
				b.Fatal(err)
			}
			for i := range count {
				instanceUnhealthySince.Store(getInstanceHealthKey(set, buildInst(set.Name, i, testOldRev, false, true)), time.Now())
			}
			controller := &ReconcileStatefulInstanceSet{roleInstanceSetLister: instancelisters.NewRoleInstanceSetLister(indexer)}
			now := time.Now()
			controller.pruneInstanceHealth(now)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				// Throttled: the common case on every reconcile.
				controller.pruneInstanceHealth(now)
			}
		})
	}
}

// BenchmarkV501ObserveInstanceHealthGlobalScan documents that
// observeInstanceHealth — untouched by this PR — still Ranges the entire
// global instanceUnhealthySince map on every reconcile. Its cost scales
// with the TOTAL number of tracked unhealthy instances across all sets,
// not with the reconciled set's own instance count. Evidence for the
// "acceptance criterion only partially met" finding.
func BenchmarkV501ObserveInstanceHealthGlobalScan(b *testing.B) {
	for _, count := range []int{0, 100, 10000} {
		b.Run(fmt.Sprint("otherEntries=", count), func(b *testing.B) {
			resetInstanceUnhealthySince()
			b.Cleanup(resetInstanceUnhealthySince)
			otherSet := buildSet("other", 1, nil, nil)
			otherSet.UID = "other-set"
			for i := range count {
				instanceUnhealthySince.Store(getInstanceHealthKey(otherSet, buildInst(otherSet.Name, i, testOldRev, false, true)), time.Now())
			}
			set := buildSet("mine", 1, nil, nil)
			set.UID = "mine-set"
			healthy := buildInst(set.Name, 0, testOldRev, true, true)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				observeInstanceHealth(set, []*workloadsv1alpha2.RoleInstance{healthy})
			}
		})
	}
}
