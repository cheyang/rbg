# Verification — PR #501 "perf: throttle stateful instance health pruning"

- **Reviewer:** Claude (Reviewer A, debate round 2026-10-19)
- **Branch:** `verify/pr501-prune-throttle-claude` (pushed to the reviewer fork `cheyang/rbg`)
- **PR head:** `8e5c51f109f6f97f91e739b96d4efb346691c911` · **Base:** `56c49531084191ad982c7b1512d64bb83acd2098`
- **Harness is additive only** — no production code is modified on this branch.

## P0 premise verdict: CONFIRMED (against base)

Issue #481 / the PR body claim: stateful RoleInstanceSet reconciles sweep the
global health-observation and early-rollback-signal maps on **every** request,
with one informer lister `Get` per entry.

Reproduced on base `56c4953` with `TestV501PremiseBaseScansOnEveryReconcile`
(`harness/v501_premise_base_test.go.txt`, base-only source — the PR head
changed `pruneInstanceHealth`'s signature, so this file is recorded here
rather than committed as a live test):

- 3 consecutive prune calls over 2 sets × (1 unhealthy timer + 1 rollback
  signal) perform **12 lister reads** — one full sweep per reconcile, no
  time-based skip (`results/base-premise.txt`).
- Per-reconcile cost on base: **9.4 µs** @ 100 tracked entries, **1.19 ms /
  960 KB / 30,000 allocs** @ 10,000 entries — on *every* reconcile.
- Component match: the issue names `pruneInstanceHealth` in exactly the file
  the PR touches; issue #481 is OPEN and is the follow-up to the prior
  #479 review finding F1 (full-scan-per-reconcile).

On head the same sizes cost **15 ns / 0 allocs** on the throttled path —
the claimed optimization is real and reproduced on this machine.

## Observed vs expected

| ID | Claim (contract) | Verdict | Evidence |
| --- | --- | --- | --- |
| P0 | Base sweeps the global maps on every reconcile | **Confirmed** | `results/base-premise.txt` (12 lookups / 3 calls; 1.19ms @ 10k) |
| C1 | First call sweeps; calls within `instanceHealthPruneInterval` do zero informer lookups; first call at/after the interval sweeps again, reclaims deleted/UID-replaced records, preserves other sets' records | **Pass** | `TestV501PruneSweepCadence` (`results/head-unit.txt`) |
| C2 | While a sweep is in progress, other workers' prune calls return promptly without blocking and without advancing the sweep clock | **Pass** | `TestV501PruneNonBlockingWhileSweepInProgress` |
| C3 | Deferred sweep is correctness-safe: a recreated same-name set inherits neither the old unhealthy window nor the old rollback signal within the throttle window | **Pass** | `TestV501DeferredSweepSafetyForRecreatedSet` |

Benchmarks (evidence, not gates): `results/head-benchmarks.txt`.

## Harness bites (Step 4)

Both discriminating tests were proven to bite by temporary regression +
revert (`results/harness-bites.txt`):

- **Bite A** — neutering the throttle (`if false` around the interval check)
  makes `TestV501PruneSweepCadence` FAIL (16 lookups where 4 expected).
- **Bite B** — replacing `TryLock` with a blocking `Lock` makes
  `TestV501PruneNonBlockingWhileSweepInProgress` FAIL after 2 s.
- Production diff confirmed empty again after each revert.

Bite B is the interesting one: the PR's own
`TestPruneInstanceHealthConcurrent` (32 workers → 1 sweep) passes with either
`Lock` or `TryLock`, because the interval check also suppresses the second
sweep. The non-blocking claim needed a test that holds the mutex. See finding
F1 in the review.

## Findings carried by this harness

- **C1/C2/C3** (above) — regression guards for the throttle's behavior.
- **F1 (minor, missing test — open):** the PR's concurrency test does not
  discriminate `TryLock` from `Lock`; the non-blocking property was untested
  until `TestV501PruneNonBlockingWhileSweepInProgress` (added here, and
  available for the author to lift into the PR).
- **F2 (minor, pre-existing — open):** `observeInstanceHealth` still `Range`s
  the *entire* global `instanceUnhealthySince` map on every reconcile —
  116 ns / 856 ns / 214 µs at 0 / 100 / 10,000 other-set entries
  (`BenchmarkV501ObserveInstanceHealthGlobalScan`). Issue #481's acceptance
  criterion "avoid an unconditional full map scan on every reconcile" is met
  for the prune helper only. Pre-existing from #479, not introduced by this
  PR; a per-set index (as the issue suggests) is the natural follow-up.

## How to re-run

```sh
# unit layer (L1) — the whole regression suite for this PR
go test -mod=vendor -race -count=1 -run 'TestV501' \
  ./pkg/reconciler/roleinstanceset/statefulmode/

# benchmarks
go test -mod=vendor -run '^$' -bench 'BenchmarkV501|BenchmarkPruneInstanceHealth' \
  -benchmem -benchtime=200ms -count=1 ./pkg/reconciler/roleinstanceset/statefulmode/

# P0 premise (base-only): copy harness/v501_premise_base_test.go.txt into a
# worktree of 56c49531084191ad982c7b1512d64bb83acd2098 as
# pkg/reconciler/roleinstanceset/statefulmode/v501_premise_base_test.go and run
# go test -mod=vendor -count=1 -run TestV501PremiseBaseScansOnEveryReconcile ./pkg/reconciler/roleinstanceset/statefulmode/

# full re-verify against the current PR head (fetches it, grafts this harness, reports per finding)
bash docs/verification/pr501-prune-throttle/scripts/re-verify.sh
```

Layers: unit only. No envtest/integration layer — the change is pure
in-memory control flow around two package-level `sync.Map`s plus lister reads
that a `cache.Indexer` fake exercises fully; a real API server adds no signal
for this contract. Live layer skipped per the debate-round instructions.

## Continuing after the fix / next round

- Re-run: `bash docs/verification/pr501-prune-throttle/scripts/re-verify.sh`
  (no sha needed — it resolves the current PR head from `manifest.pr`).
  All three contracts should report FIXED; a red C1/C2/C3 means the throttle
  regressed; a HARNESS-UPDATE verdict means the code shape changed — adjust
  the harness, then re-run.
- The delta to review next round is printed from `.last-reviewed`
  (currently `8e5c51f109f6f97f91e739b96d4efb346691c911`); advance it after
  reviewing with the one-liner the script prints.
- If the author lifts `TestV501PruneNonBlockingWhileSweepInProgress` into the
  PR (recommended — it is the only test that pins the non-blocking property),
  keep the harness copy anyway as the reviewer-side guard.

Kickoff prompt for a fresh agent:

> You are resuming a PR review verification. Checkout branch
> `verify/pr501-prune-throttle-claude` (reviewer fork `cheyang/rbg`, PR
> https://github.com/sgl-project/rbg/pull/501). Read
> `docs/verification/pr501-prune-throttle/README.md`, then run
> `bash docs/verification/pr501-prune-throttle/scripts/re-verify.sh` and
> report per-finding Fixed / Still-broken / Partial / Harness-update, honoring
> canary polarity. Then review the `last-reviewed..head` delta for regressions
> against the contracts C1–C3 and update the observed-vs-expected table.
