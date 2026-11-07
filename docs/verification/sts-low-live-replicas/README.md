# sts-low-live-replicas — bug verification

Reproducible evidence for the review of PR https://github.com/sgl-project/rbg/pull/553
(fixes https://github.com/sgl-project/rbg/issues/547).

## Round 2 (current) — head `c768d5cdad1acda87b490880beb3e868f089f900`

The PR was rebased onto the new main `73e05db4` (rebase is content-neutral for
`pkg/reconciler/`: `git diff b28c5478 34a64c0e -- pkg/reconciler/` is empty) and gained
one new commit, `c768d5cd "fix(sts): restore surge when scaling back an in-flight
rollout"`, which adopts the debate's F7 recommendation and **deliberately changes the
recovery contract**:

- old (b28c5478): `partition = min(partition, stsReplicas)`; return `(partition, roleReplicas)`
- new (c768d5cd): partition **kept unchanged**; `replicas = wantReplicas(roleUnreadyReplicas)`
  (surge restored) while `partition > 0`, exactly `roleReplicas` when idle (`partition == 0`)

L2 expectations were re-pinned to the new contract. The full evidence set:

| Run | Ref | Result | Evidence file |
|-----|-----|--------|---------------|
| premise (panic on base) | frozen base `73e05db4` | panic `index out of range [1] with length 1` (unchanged from round 1) | `results/base-73e05db4-verify-tests.txt` |
| old contract (red) | previous head `b28c5478` | 3 FAIL: old clamp values (4/1 vs new 5/2), apply leaves 4 not 5, **F7 stall: (1,4) fixed point for 24 iterations** | `results/previous-head-b28c5478-verify-tests.txt` |
| new contract (green) | head `c768d5cd` | 6/6 PASS; F7 converges `(2,5) -> (1,5) -> (0,4)` | `results/head-c768d5cd-verify-tests.txt` |
| head full suite | `c768d5cd` | `go test ./pkg/reconciler` ok, `go build ./...` clean | (this round's review log; same commands as round 1) |

F7 convergence trace on head (maxUnavailable `10%` → 0 for 4 replicas, maxSurge 1,
external shrink mid-rollout at partition 2 / live 1):

```text
iter 0: (2, 5)   # recovery branch: partition kept, surge restored
iter 1: (1, 5)   # case 6 with surge present: rollingStep = 0+1-0 = 1 > 0, partition advances
iter 2: (0, 4)   # surge reclaimed exactly when unready <= maxSurge, partition already 0
iter 3: (0, 4)   # stable
```

On the previous head the same loop parks at `(1, 4)` forever (ordinal 0 never updates) —
see `results/previous-head-b28c5478-verify-tests.txt`. Mechanism: while the surge is
present `rollingStep = maxUnavailable + maxSurge - (burstReplicas - stsReplicas) >= 1`,
so case 6 keeps advancing the partition; the surge is reclaimed only when
`unready <= maxSurge`, and at that point `continuousReadyReplicas` is high enough that
`rollingStepPartition` reaches 0 in the same reconcile.

Round 1 (head `b28c5478`, base `98d3c4be`) — original harness and evidence — is kept
below unchanged.

---

## Round 1 (historical)

Code under review: PR head `b28c5478739b2b3de28f254de1ee4b902527e045`
(base / merge-base: `98d3c4be8c4c018ddbeb516b66a4eba4981c225c`, which is #540).

Layers run against the code under review:

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit | `rollingUpdatePartition` pure function with the issue #547 inputs | `go test -mod=vendor ./pkg/reconciler -run 'TestVerify_L1' -count=1 -v` |
| 2. Integration (fake client) | `rollingUpdateParameters` and the full `reconcileStatefulSet` path for a live STS scaled below desired | `go test -mod=vendor ./pkg/reconciler -run 'TestVerify_L2' -count=1 -v` |
| 3. Live | not run this round (unit + integration only, per pipeline instruction) | — |

> Test polarity: every harness test below is a **contract** test (asserts intended
> correct behavior). They FAIL (panic) on the base branch and PASS on the PR head.
> The negative control passes on both — it pins the unchanged normal-path arithmetic.

## Problem premise (P0)

Run against the **base** branch (`98d3c4be`) without the patch, because the question
is whether the problem exists today.

| | |
|---|---|
| Claimed symptom | "`rollingUpdatePartition` indexes `states` past the end of the slice when the live StatefulSet replica count is lower than the role's desired count by more than `maxUnavailable` plus the number of continuously ready replicas … the workload does not converge" (issue #547) |
| Linked issue | #547, OPEN, created 2026-11-03 (3 days before this review), still valid — base commit 98d3c4be contains the panic |
| Reported component | `pkg/reconciler/sts_reconciler.go` — `StatefulSetReconciler.rollingUpdateParameters`, case 6, then `rollingUpdatePartition` |
| Patched component | same file, same functions |
| Component match | **Yes** |
| **Verdict** | **Confirmed** |
| Evidence | `TestVerify_L2_ReconcileStatefulSet_LiveBelowDesired_ScalesUp` panics on base: `index out of range [1] with length 1` at `sts_reconciler.go:421` (the `unavailable` scan loop), reached through the full `reconcileStatefulSet` path with a semantically-equal, externally-scaled STS (`stsUpdated=false`). See `results/base-verify-tests.txt`. |

Reachability of the premise was validated end to end: `statefulSetSpecEqual` compares
only Selector/ServiceName/Template (not Replicas), so an externally scaled STS
(`kubectl scale`) with no template/revision change keeps `stsUpdated=false`, skips
case 3, and falls into case 6 with `len(states) == sts.Spec.Replicas < role.Replicas`.

## Summary of results

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| P0 | panic exists on base | 2 | **Confirmed** | base run panics; `results/base-verify-tests.txt` |
| F1 | `rollingUpdatePartition` must not panic when the computed partition index exceeds `len(states)` (live 1 with rollingStep -2; live 0) | 1 | **Fixed on head** | `TestVerify_L1_...` panics on base, passes on head |
| F2 | reconcile of a live-below-desired STS must not panic and must scale back to `role.Replicas` | 2 | **Fixed on head** | `TestVerify_L2_RollingUpdateParameters_...` returns (partition 1, replicas 4); `TestVerify_L2_ReconcileStatefulSet_...` leaves the STS at `spec.replicas == 4`; both panic on base |
| NEG1 | normal case-6 arithmetic (mid-rollout, live == desired) is unchanged by the fix | 2 | **Unchanged** | negative control passes on base AND head with identical expected values (partition 1, replicas 5) |

Harness-bites check: the same test file was grafted onto the base worktree and run —
3 of 4 tests panic/fail there and all 4 pass on the head. The base **native** suite
(without the harness) is green (`results/base-native-suite.txt`), so the base
failures are the reproduction, not pre-existing flakes. The head full
`pkg/reconciler` suite and `go build ./...` are green
(`results/head-full-suite.txt`).

## Per-finding detail

### F1 — `rollingUpdatePartition` index-out-of-range (issue #547)

- Mechanism: `getReplicaStates` sizes `states` by `*sts.Spec.Replicas` (live), while
  the partition index reduces to `roleReplicas - continuousReady - maxUnavailable`,
  which is independent of the live count; when live is far enough below desired the
  `unavailable` scan loop indexes past the slice.
- Command: `go test -mod=vendor ./pkg/reconciler -run 'TestVerify_L1' -count=1 -v`
- Observed (base): panic `index out of range [1] with length 1` at `sts_reconciler.go:421`.
- Observed (head): returns `int32(0)` for both the live-1 and live-0 inputs; no panic.
- Fix under review: clamps `rollingStepPartition` to `len(states)` and guards the
  reduce loop with `stateCount > 0`.

### F2 — scale-up before rolling (the user-visible contract)

- Mechanism: on base, the reconcile of a shrunken STS crashes before writing, so the
  workload is left at the reduced count (controller-runtime recovers and requeues
  forever). On head, `rollingUpdateParameters` returns early
  (`stsReplicas < roleReplicas`, role-size annotation unchanged) with
  `replicas = roleReplicas` and `partition = min(partition, stsReplicas)`.
- Command: `go test -mod=vendor ./pkg/reconciler -run 'TestVerify_L2' -count=1 -v`
- Observed (base): panic through the full `reconcileStatefulSet` path.
- Observed (head): parameters `(partition=1, replicas=4, err=nil)`; after
  `reconcileStatefulSet`, the STS in the fake client has `spec.replicas == 4`.

### NEG1 — negative control (regression guard)

Mid-rollout at exactly the desired count (4 replicas, pods 0/1 not ready, 2/3
ready+updated, partition 3): expected `(partition 1, replicas 5)` — captured from
base behavior, which is the correct pre-existing arithmetic for this input.
Passes identically on base and head, so the early return does not disturb the
normal case-6 path.

## Behavior note (not a defect)

For sub-desired live counts that did **not** panic on base (e.g. live 3, desired 4),
the new early return changes behavior: base ran case-6 arithmetic and possibly
burst to `roleReplicas + maxSurge`; head scales exactly to `roleReplicas` and defers
partition progress to the next reconcile. This is the PR's stated design
("scale up before rolling update"), converges in the next cycle, and avoids a
pointless surge when already under desired — reviewed and found acceptable.

Also observed: the partition clamp `min(partition, stsReplicas)` can be raised again
above the live count by the pre-existing deferred role-partition floor
(`stsPartition = max(stsPartition, rolePartitionScaledByRoleReplicas)`) when the role
configures a non-zero `Partition` (verified: role Partition 2, live 1 → returns 2).
This is semantically consistent with the configured partition and does not panic;
noted as a nit in the review (the V(1) log line prints the pre-floor value).

## Live run notes

Not run this round (pipeline instruction: unit + integration only). If run later,
the observable signal is: `kubectl scale` an in-rollout role's STS below
`role.replicas - maxUnavailable - continuousReady`, then confirm the controller logs
`live replicas N are below desired M, scale up before rolling update` and the STS
returns to `spec.replicas == role.Replicas` instead of crash-looping the reconcile.

## Proposed fixes (NOT applied to production here)

- None required — the PR's fix is confirmed at all three levels.
- Optional polish (review nits, not applied): move the cheap
  `stsReplicas < roleReplicas` check above the `getReplicaStates` call so the
  scale-up decision cannot be blocked by pod/revision list errors; mention the
  deferred partition floor in the log message.

## Continuing after the fix (possibly on another machine)

The harness is on branch `verify/sts-low-live-replicas-claude` (production code
untouched — the only non-docs change is the additive test file
`pkg/reconciler/low_live_replicas_verify_test.go`), so it grafts onto whatever the
fixed code is.

1. Get it onto the fixed code:
   ```bash
   git fetch https://github.com/cheyang/rbg.git verify/sts-low-live-replicas-claude
   git checkout <fixed-branch>
   git checkout FETCH_HEAD -- docs/verification/sts-low-live-replicas pkg/reconciler/low_live_replicas_verify_test.go
   ```
2. Prereqs: Layer 1 and 2 need only the Go toolchain and the vendored deps
   (`go test -mod=vendor`). Layer 3 needs a real cluster (`KUBECONFIG`).
3. Re-run:
   ```bash
   go test -mod=vendor ./pkg/reconciler -run 'TestVerify_' -count=1 -v
   bash docs/verification/sts-low-live-replicas/scripts/re-verify.sh   # no arg = current PR head
   ```
4. Polarity table: all harness tests are contract tests — they must be GREEN on
   fixed code. There are no canaries to invert. `NEG1` must stay green; if it goes
   red, the fix changed the normal rollout arithmetic (regression signal).
5. Harness-bites: graft the test file onto commit `98d3c4be` (base) and confirm
   F1/F2 tests panic there.

### Kickoff prompt for a fresh agent
```text
Continue a verification task on branch verify/sts-low-live-replicas-claude
(https://github.com/cheyang/rbg.git). Background: a review of
https://github.com/sgl-project/rbg/pull/553 confirmed issue #547 (rollingUpdatePartition
index-out-of-range panic when live StatefulSet replicas sit far below the desired
count) on base 98d3c4be, and the PR head b28c5478 fixes it. Read
docs/verification/sts-low-live-replicas/README.md ("Continuing after the fix") and
follow it: graft the harness onto the current PR head (re-verify.sh resolves it from
manifest.pr), re-run all layers, mind the polarity table (all contract tests; NEG1
must stay green), run the harness-bites check against 98d3c4be, and report an
observed-vs-expected table. Clean up scoped test resources; do not run
cluster-wide destructive actions.
```
