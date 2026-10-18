# PR #501 perf: throttle stateful instance health pruning — verification (Reviewer B / Codex)

Reproducible evidence for the review of https://github.com/sgl-project/rbg/pull/501
(head `8e5c51f109f6f97f91e739b96d4efb346691c911`, base `56c49531084191ad982c7b1512d64bb83acd2098`).

Layers run against the **code under review** (`8e5c51f1`); the premise benchmark (P0) runs against the **base**.

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit | `pruneInstanceHealth` throttle: interval gate, TryLock single-sweep, stale-record reclaim, isolation | `go test -mod=vendor -race -count=1 -run 'TestVerifyPr501|TestPruneInstanceHealth|TestPruneEarlyRollbackReplacementSignals' ./pkg/reconciler/roleinstanceset/statefulmode/` |
| 1b. Bench | per-call cost, base vs head | head: `go test -mod=vendor ./pkg/reconciler/roleinstanceset/statefulmode -run '^$' -bench '^BenchmarkPruneInstanceHealth$' -benchmem -benchtime=200ms`; base: copy `scripts/premise_p0_base_bench_test.go` into the package on a base checkout, run with `-bench '^BenchmarkPremiseP0BasePruneEveryReconcile$'` |
| 2. Integration | real API server (envtest 1.31.0), full stateful RoleInstanceSet controller + RBG flows | `KUBEBUILDER_ASSETS=$(setup-envtest use 1.31.0 -p path) go test -mod=vendor -count=1 ./test/envtest/testcase/stateful/ ./test/envtest/testcase/rbg/ -timeout 15m` |
| 3. Live | — | skipped per review scope (no KUBECONFIG layer) |

> Test polarity: every test here is a **contract** test (asserts intended behavior): FAIL on
> un-throttled code, PASS on the PR head. See `results/harness-bites.txt`.

## Problem premise (P0)

| | |
|---|---|
| Claimed symptom | "`pruneInstanceHealth()` is called at the top of every `Reconcile` and performs a full `Range` over the global `instanceUnhealthySince` map, with one informer lister `Get` per entry. This is O(number of tracked unhealthy instances) on every reconcile" (issue #481, follow-up to #479) |
| Linked issue | #481, OPEN, opened 2026-09-22, still valid; explicitly "not a correctness issue" — a hot-path optimization |
| Reported component | `pkg/reconciler/roleinstanceset/statefulmode` (`ReconcileStatefulInstanceSet.Reconcile` → `pruneInstanceHealth`) |
| Patched component | same package, same function — plus tests |
| Component match | Yes |
| **Verdict** | **Confirmed** |
| Evidence | base benchmark: every call sweeps — 9,471 ns/op @100 entries, 1,201,397 ns/op @10,000 entries, 960 kB + 30,000 allocs per call (`results/premise-p0-base-bench.txt`). Head: throttled call 15.3 ns/op, 0 allocs at both sizes (`results/bench-head.txt`). ~78,000x cheaper per throttled reconcile at 10k tracked instances. |

## Summary of results

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| P0 | Full map scan + lister Get per entry on every reconcile | 1b (base) | Confirmed | `results/premise-p0-base-bench.txt` |
| C1 | Within `instanceHealthPruneInterval` (1m) repeat calls perform no lister lookups; the call at the exact boundary sweeps | 1 | Confirmed | `TestVerifyPr501PruneThrottledWithinInterval`, PR's `TestPruneInstanceHealthInterval`; `results/unit-head.txt` |
| C2 | Concurrent reconciles (32 workers) produce exactly one sweep, nobody blocks | 1 | Confirmed | `TestVerifyPr501ConcurrentCallsSweepOnce`, PR's `TestPruneInstanceHealthConcurrent` (run with `-race`); `results/unit-head.txt` |
| C3 | Cleanup contract preserved: deleted/replaced sets' health timers + rollback signals are reclaimed by the next due sweep; same-named sets in other namespaces untouched | 1 | Confirmed | `TestVerifyPr501StaleRecordsReclaimedAfterInterval`, PR's `TestPruneInstanceHealth`/`TestPruneEarlyRollbackReplacementSignals`; `results/unit-head.txt` |
| C4 | No regression in the real controller flows that drive this reconciler | 2 | Confirmed | envtest `testcase/stateful` 19.6s OK, `testcase/rbg` 47.3s OK; `results/integration-envtest.txt` |

No defect findings were confirmed; the review produced **no blockers**. See "Analysis notes" for
why the throttled sweep cannot change controller decisions.

## Per-claim detail

- **P0 (premise, runs against base).** On `56c49531`, `Reconcile` calls `pruneInstanceHealth()`
  unconditionally; the function ranges both global maps with one informer `Get` per entry.
  The scratch benchmark `scripts/premise_p0_base_bench_test.go` (additive, in-package) times
  exactly that call. Observed: O(n) per call as claimed — ~1.2 ms/call at 10k tracked instances.
  On the PR head the throttled path is ~15 ns/call with zero allocations.
- **C1 interval gate.** Two seeded records → first call = 2 lister lookups (one per map entry);
  a call at `now+interval-1ns` adds 0 lookups; a call at `now+interval` sweeps (4 total).
- **C2 single sweep under concurrency.** 32 goroutines released simultaneously against one
  reconciler → exactly 2 lookups total (one sweep). `-race` clean. `TryLock` makes the losing
  workers return immediately instead of queueing on the mutex.
- **C3 cleanup contract.** After deleting (or UID-replacing) the set in the informer cache, the
  pre-interval call sweeps nothing and the stale entries remain; the due sweep reclaims them.
  The other namespace's same-named set keeps both records. Note the throttled delay is
  memory-only, not correctness: `instanceUnhealthySince` keys carry the set UID (a recreated
  set's reads can never hit the old incarnation's entries, and `observeInstanceHealth` drops
  them on the recreated set's first reconcile), and `earlyRollbackReplacementSignalActive`
  validates `signal.setUID != string(set.UID)` on every read, deleting stale signals lazily.
- **C4 integration.** envtest stateful suite (rollout recovery flows that exercise the
  stably-unhealthy gate these maps feed) and the broader rbg suite both pass unchanged.

## Analysis notes (why skipping the sweep is behavior-neutral)

Traced every consumer of the two pruned maps:

- `instanceUnhealthySince` reads: `isStablyUnhealthy`, `remainingUnhealthyWindow`, the
  `progressUpdate` load — all key on `(set namespace/name, set UID, instance UID)`, so a stale
  entry from a deleted/recreated set is unreachable by construction. `observeInstanceHealth`
  (runs on every reconcile, Phase A) already deletes same-name/different-UID entries.
- `earlyRollbackReplacementSignals` reads: `earlyRollbackReplacementSignalActive` and
  `recordEarlyRollbackReplacementSignal` both compare `setUID` (and revision) before use and
  self-delete on mismatch; the deleted-set reconcile path also deletes the key directly.
- Therefore the prune sweep is pure garbage collection of unreachable entries; delaying it by
  up to one minute (plus reconcile arrival) only bounds memory reclamation, it cannot flip any
  controller decision. The 1-minute cadence matches issue #481's first proposed option.

## Proposed fixes

None — no findings confirmed. Review outcome: COMMENT-level (no change requests).

## Continuing after future revisions (possibly on another machine)

The harness lives on branch `verify/pr-501-health-prune-throttle-codex` on the reviewer's fork
(`https://github.com/cheyang/rbg.git`); production code is untouched — the branch diff over the
PR head is only `docs/verification/**` plus `pkg/reconciler/roleinstanceset/statefulmode/verify_pr501_prune_throttle_test.go`.

1. Fetch and re-verify against the current PR head (resolves the PR ref by URL, grafts the
   harness, runs layers 1+2, applies contract polarity):
   ```bash
   git fetch https://github.com/cheyang/rbg.git verify/pr-501-health-prune-throttle-codex
   git checkout verify/pr-501-health-prune-throttle-codex   # or a worktree
   bash docs/verification/pr-501-health-prune-throttle-codex/scripts/re-verify.sh
   ```
   Exit 0 iff every contract still holds. Layer 2 needs envtest assets
   (`setup-envtest use 1.31.0`); without them, run `--layers unit`.
2. Harness-bites re-check: revert the throttle hunk in
   `pkg/reconciler/roleinstanceset/statefulmode/stateful_instance_set_controller.go` on a scratch
   tree — all `TestVerifyPr501*` and the PR's `TestPruneInstanceHealth{Interval,Concurrent}` must
   go red (see `results/harness-bites.txt`).
3. If a future revision renames/reshapes `pruneInstanceHealth` or the package test helpers this
   file reuses (`buildSet`, `buildInst`, `reset*`), the harness stops compiling: that is a
   **Harness-update**, not proof of a fix — adjust the harness, then re-run.
4. Advance `.last-reviewed` after each round so the next one diffs from it.

### Kickoff prompt for a fresh agent
```text
Continue a verification task on branch verify/pr-501-health-prune-throttle-codex
(remote https://github.com/cheyang/rbg.git). Background: PR
https://github.com/sgl-project/rbg/pull/501 throttles pruneInstanceHealth to one sweep per
minute; review found no defects, premise confirmed by benchmark. Read
docs/verification/pr-501-health-prune-throttle-codex/README.md ("Continuing after future
revisions") and follow it: run scripts/re-verify.sh against the current PR head (unit layer
always; integration layer needs KUBEBUILDER_ASSETS from setup-envtest 1.31.0), mind that all
tests are contract polarity, and report an observed-vs-expected table for C1-C4 plus any new
findings from the incremental diff since .last-reviewed.
```
