// Scratch premise-verification benchmark (review harness, not part of the PR).
// P0: on the base branch, Reconcile calls pruneInstanceHealth() on EVERY
// request, and each call performs a full scan with one informer lookup per
// tracked unhealthy instance => per-reconcile cost grows O(n).
package statefulmode

import (
	"fmt"
	"testing"
	"time"

	"k8s.io/client-go/tools/cache"
	instancelisters "sigs.k8s.io/rbgs/client-go/listers/workloads/v1alpha2"
)

func BenchmarkPremiseP0BasePruneEveryReconcile(b *testing.B) {
	for _, count := range []int{100, 10000} {
		b.Run(fmt.Sprintf("instances=%d", count), func(b *testing.B) {
			resetInstanceUnhealthySince()
			b.Cleanup(resetInstanceUnhealthySince)
			set := buildSet("prefill", int32(count), nil, nil)
			indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, nil)
			if err := indexer.Add(set); err != nil {
				b.Fatal(err)
			}
			for i := 0; i < count; i++ {
				instanceUnhealthySince.Store(getInstanceHealthKey(set, buildInst(set.Name, i, testOldRev, false, true)), time.Now())
			}
			controller := &ReconcileStatefulInstanceSet{roleInstanceSetLister: instancelisters.NewRoleInstanceSetLister(indexer)}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				// Base signature: every call sweeps, like every Reconcile does.
				controller.pruneInstanceHealth()
			}
		})
	}
}
