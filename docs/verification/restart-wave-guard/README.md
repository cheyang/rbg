# restart-wave-guard — bug verification (PR #573)

Reproducible evidence for the findings raised while reviewing
https://github.com/sgl-project/rbg/pull/573
("fix(restart): release recreation guard when pre-restart Pods are gone (#563)").

- Reviewed head: `145cbaddff580cce6bb469401322ff9a48192f13`
- Base: `ea5e82ae59169e0e954fb1c7068f9480fb4b7375`
- Production code on this branch is **untouched** (harness is additive test files + this doc).

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit | full `Reconcile` against a fake client with an injected `Delete` failure (F1); real `syncInstance` + `UpdateInstanceStatus` seam (F2) | `go test ./pkg/reconciler/roleinstance/ -run 'TestVerifyWaveDeletionRetriedAfterFailure|TestVerifyCompletedWaveConditionFlipIsPersisted' -count=1 -v` |
| 2. Integration | real API server + real RBG/RoleInstance controllers (envtest), #563 scenario | `KUBEBUILDER_ASSETS=$(setup-envtest use 1.31.0 -p path) go test ./test/envtest/testcase/restart_policy/ -run TestRestartPolicy -ginkgo.focus 'verify-563' -count=1 -v` |
| 3. Live | skipped (debate setup: no KUBECONFIG layer) | — |

> Polarity: all harness tests are **contract** tests (assert intended behavior): they FAIL
> while the bug is present and PASS once fixed. There are no bug-canaries.

## Problem premise (P0)

| | |
|---|---|
| Claimed symptom | "If a member of the rebuilt group fails again before the instance is Ready ... nothing happens ... the instance stays NotReady until the RoleInstance is deleted by hand." (issue #563) |
| Linked issue | #563, OPEN, filed 2026-11-11, still valid |
| Reported component | RoleInstance restart-policy guard (`shouldRecreateInstanceGuarded`/`isAlreadyRestarting`) |
| Patched component | same (`pkg/reconciler/roleinstance{,/sync}`) |
| Component match | Yes |
| **Verdict** | **Confirmed** |
| Evidence | On base, the contract spec times out at the "completed wave must persist Restarting=False" assertion — the guard holds until Ready (results/premise-envtest-base.txt). On head the same spec passes (results/premise-envtest-head.txt). |

## Summary of results

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| P0 | On base, a second pre-Ready failure never starts another recreation | 2 | Confirmed (premise real; fix works) | base: FAIL at wave-release assertions; head: PASS — results/premise-envtest-{base,head}.txt |
| F1 | A failed/partial pod deletion during a recreation wedges the restart policy permanently | 1 | Confirmed (regression vs base) | head: FAIL (no retry, pods survive); base: PASS (retry) — results/head-unit-contracts.txt, results/base-wedge-control.txt |
| F2 | `mirrorRestartingCondition` blinds `inconsistentStatus`; a failed completion write leaves persisted `Restarting=True` stuck | 1 | Confirmed | head: FAIL (stale True survives) — results/head-unit-contracts.txt |
| — | PR's own envtest suite (28 specs) + this harness's spec still pass on head | 2 | Pass | results/envtest-suite-head.txt |

## Per-finding detail

### F1 — failed wave deletion is never retried → restart policy wedges (blocker)

Mechanism (head):
1. A trigger fires (e.g. container restart while the pod snapshot is still Ready).
   `rememberRestartWave` records the pre-restart pod UIDs; `setRestartingCondition`
   marks the instance; `deletePods` then fails (transient API error / admission rejection).
2. Because the PR propagates the `Restarting=True` condition **unconditionally** (base
   gated it on `scaling`), the condition is persisted even when nothing was deleted.
3. The next reconcile's `Reconcile`-level cleanup (`ClearRestarting` on Ready) is skipped
   because `newStatus` contains `Restarting=True` (`restartingInProgress`).
4. The wave guard consumes every re-trigger (`wavePodsRemaining` is true while the
   surviving recorded pods exist), and **nothing re-issues the failed delete**: the
   recreation decision is the only path that deletes those pods.
5. Exit conditions: manual intervention (delete the pod / flip the condition / delete the
   instance). The total-failure variant additionally unwedges on controller restart
   (LastRestartTime was never persisted), the partial-failure variant does not.

Base heals the same failure: nothing is persisted when `scaling=false`, and the Ready
snapshot clears the in-memory marker, so the next reconcile re-attempts the recreation.

Command (head): see Layer 1. Observed: `TestVerifyWaveDeletionRetriedAfterFailure` FAILS —
zero `Delete` calls on the surviving pre-restart pods across two healthy reconciles, and the
original pod UIDs are still present. On base the identical test PASSES.

Harness-bites: with a candidate fix that re-issues surviving wave deletions in
`calculateDiffsWithExpectation`, the test goes green and all pre-existing unit tests in the
two touched packages still pass (results/bites-f1-fix.txt). The fix is NOT applied here.

### F2 — mirror blinds the status diff: completed-wave flip may never persist (minor)

`syncInstance` calls `mirrorRestartingCondition(instance, updateInstance)` so
`setInstanceConditions`' carry-forward cannot resurrect a stale condition. But the mirror
also mutates the very object `UpdateInstanceStatus` later diffs `newStatus` against
(`inconsistentStatus`). If the status write of the wave-completion reconcile failed once,
every later quiescent reconcile recomputes the flip, mirrors it onto the informer copy, and
then concludes "nothing changed" — the persisted `Restarting=True` survives indefinitely
until some unrelated status change, a Ready transition, or the next wave overwrites it.
Functional impact is small (each reconcile re-derives wave completion in memory, so guard
and backoff behave correctly), but the persisted condition misreports the instance state.

The harness drives the real `syncInstance` (fake `sync.Interface` applies the flip exactly
like `sync.releaseCompletedRestartWave`) with an otherwise quiescent instance.
Observed on head: `inconsistentStatus=false`, no write, live object keeps
`True/RestartPolicyTriggered` → test FAILS.

Harness-bites: dropping the mirror and never carrying `Restarting` forward from the live
object flips the test green (results/bites-f2-fix.txt). Note: the PR's own
`TestRestartingConditionLifecycleWithReadiness` block 3 pins the carry-forward of a
live `Restarting=False` with an empty inner-conditions input — a shape the real pipeline
never produces (syncInstance always propagates the sync view into `newStatus`); a fix along
these lines must rework that block.

## Proposed fixes (NOT applied to production here)

- **F1**: while the in-memory wave marker has surviving recorded pods, re-issue their
  deletion from `calculateDiffsWithExpectation` (idempotent; `deletePods` skips
  terminating/NotFound pods). Alternatively gate the wave release / Ready-clear skip on
  `deletion actually issued`, or drop the `Restarting=True` propagation when `scaling=false`
  so the base self-heal path keeps working.
- **F2**: drop `mirrorRestartingCondition`; treat `Restarting` as controller-owned in
  `setInstanceConditions`' carry-forward (never resurrect from the live object), and rework
  the block-3 unit expectation accordingly. Alternative: make the status-diff reference the
  pre-mirror (or live API) conditions instead of the mirrored informer copy.

## Continuing after the fix (possibly on another machine)

The harness is on branch `verify/restart-wave-guard-codex` (fork `cheyang/rbg`).

1. `git fetch https://github.com/cheyang/rbg.git verify/restart-wave-guard-codex`
2. From a checkout of the fixed PR head:
   `bash docs/verification/restart-wave-guard/scripts/re-verify.sh` (fetches the current PR
   head itself when run from this branch; or pass a ref explicitly).
3. Layer 2 needs envtest binaries: `KUBEBUILDER_ASSETS=$(setup-envtest use 1.31.0 -p path)`.
4. All tests are contract tests: green = fixed. `.last-reviewed` records the reviewed head;
   the script prints the incremental review range.
