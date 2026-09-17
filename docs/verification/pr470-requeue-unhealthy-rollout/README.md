# PR #470 verification — retry stateful rollouts after the unhealthy window

PR: https://github.com/sgl-project/rbg/pull/470 (`fix: retry stateful rollouts after the unhealthy window`, fixes #464)
Branch: `verify/pr470-requeue-unhealthy-rollout` (based on PR head `2efff36a`, round 2). Production code untouched.

## Round summary

- **Round 1 (head `ec577f76`, 2026-09-16):** premise P0 + fix Confirmed (unit + live); F1
  mixed-config gap filed as major (canary); F2 nit. Verdict: REQUEST_CHANGES (not published).
- **Round 2 (head `2efff36a`, 2026-09-17):** the author pushed two follow-up commits.
  `423b4118` ("check lower rollout targets after a budget block") fixes F1 by changing the
  budget-exhausted branch from `return` to `continue`; proven by the author's modified
  contract test (fails on base, passes on fix, deterministic in isolation). `2efff36a`
  ("resume early stateful rollbacks") adds a distinct rollback-resume behavior with its own
  passing tests (scope expansion, noted, not a blocker). P0 premise + fix re-confirmed live
  on the new head. The round-1 reviewer canary is **retired** (it was order-dependent).
  Verdict: **COMMENT**.

## Premise (P0) — Confirmed (re-confirmed round 2)

> A Stateful RoleInstanceSet rollout stalls when fresh-unhealthy instances exhaust the
> maxUnavailable budget, because the base controller schedules no requeue after the 10s
> unhealthy window expires.

- **Unit (against base `c500c963`):** the PR's own test, grafted onto base *without* the fix,
  fails with `retry = 0s, want a positive delay of at most 4s` (round-2 wording; round-1 said
  "at most 10s"). The base budget-exhausted branch only logs + returns; no `durationStore.Push`.
  → premise mechanism is real.
- **Live (against base binary on the cluster, round 1 + round 2):** `Rolling update budget
  exhausted` at `stateful_instance_set_control.go:618` with **no `RequeueAfter`**;
  `updated=0` for the full 35s observation window — the rollout never resumes past the 10s
  window. `results/live-base-timeline.log`, `results/live-base-signals.log`.

## Fix (HEAD) — Confirmed live (re-confirmed round 2 on `2efff36a`)

On the PR head binary: the budget-exhausted branch (now `continue`, line 626 on head) pushes
`RequeueAfter: 9200854475` ns ≈ **9.2s** (the remaining unhealthy window). The requeue fires
~9s later; the RIs become stably-unhealthy → `isFree` → replaced with rev2 pods (no failing
probe) → `ready=2`, `currentRev=rev2`. Round 1 observed `RequeueAfter≈7.2s` on `ec577f76`;
the value varies with the remaining window at apply time — the key signal is a positive
`RequeueAfter` and a resumed rollout. `results/live-head-signals.log`, `results/live-head-timeline.log`.

## Observed-vs-expected table

| id | claim | layer | polarity | verdict | evidence |
| --- | --- | --- | --- | --- | --- |
| P0 | base stalls (no requeue after 10s window) | unit + live | contract | **Confirmed** | base test `retry=0s`; live `updated=0` × 35s, no RequeueAfter |
| F1 | fix only requeues from the first budget-exhausted target; healthy blocker + different fresh-unhealthy target still stalls | unit | canary | **Fixed (round 2)** | commit `423b4118` `return`→`continue`; author's contract test fails on base (`retry=0s, want ≤4s`) / passes on fix (`wait=4s`), 3/3 isolated runs; round-1 canary retired (order-dependent) |
| F2 | 1ns floor in `max(...)` is dead code | review | n/a | **Confirmed (cosmetic)** | `isStablyUnhealthy` flips `isFree` before the branch once the window expired |
| O1 | scope expansion: `2efff36a` rollback-resume broadens `inRollout` | review | n/a | **Noted (not a blocker)** | new `hasStaleBaseInstance` + surge-protects-unready-base; rollback tests (197 lines) pass; live repro recovers cleanly |

## F1 — fixed by the author's follow-up commit (round 2)

`buildUpdateTargets` iterates **highest-ordinal first**. On the round-1 head (`ec577f76`) the
fix pushed a requeue only for *the first target that tripped*
`!isFree && initialBaseUnavail+newlyUnavail >= effectiveBudget`, and only if *that* target had
an `instanceUnhealthySince` entry. A healthy blocker has its entry deleted by
`observeInstanceHealth`, so when it was the first exhausted target the code pushed **nothing**
even if a lower ordinal was freshly-unhealthy with a pending 10s window — the mixed-config
rollout stalled.

Commit `423b4118` ("check lower rollout targets after a budget block") changes the
budget-exhausted branch from `return status, nil` to `continue`, so the loop keeps scanning
lower targets and lets the earliest-expiring pending window schedule the retry. The author's
modified `TestUpdateStatefulInstanceSetRetriesUnhealthyRollout` now asserts
`assertWait(reconcile(), 4*time.Second)` for the healthy-blocker + lower-unhealthy case;
it **FAILS on base** (`retry = 0s, want a positive delay of at most 4s`,
`stateful_instance_set_control_test.go:1012`) and **PASSES on the fix head** (`wait=4s`),
deterministic across 3 isolated runs.

The round-1 reviewer canary (`TestF1MixedHealthyBlockerStillStalls`) is **retired**. It was
order-dependent — it relied on leaked global `instanceUnhealthySince`/`durationStore` state
from other tests in the same binary. In isolation it yielded `wait=0` on **both** base and
fix head, so it did not reliably isolate F1; its round-2 "flip" on the full suite was a false
positive from test-order pollution. The author's contract test subsumes the guard.

## Layers & how to run

### L1 — unit (deterministic)

```bash
cd <rbg checkout>
GOFLAGS=-mod=vendor go test -count=1 \
  -run 'TestUpdateStatefulInstanceSetRetriesUnhealthyRollout|TestProgressUpdateBudget' \
  ./pkg/reconciler/roleinstanceset/statefulmode/
```

Round-2 F1 proof (author's contract test, fails on base / passes on fix):

```bash
git checkout 2efff36a   # PR head (round 2)
GOFLAGS=-mod=vendor go test -count=1 -run TestUpdateStatefulInstanceSetRetriesUnhealthyRollout ./pkg/reconciler/roleinstanceset/statefulmode/   # expect PASS (wait=4s)
git checkout c500c963 && git checkout 2efff36a -- pkg/reconciler/roleinstanceset/statefulmode/stateful_instance_set_control_test.go
GOFLAGS=-mod=vendor go test -count=1 -run TestUpdateStatefulInstanceSetRetriesUnhealthyRollout ./pkg/reconciler/roleinstanceset/statefulmode/   # expect FAIL: retry=0s, want ≤4s
git checkout .   # restore
```

P0 premise on base (graft the PR's test onto the merge-base *without* the fix):

```bash
git checkout c500c963dca6823ef505694a535f08249528e997
git checkout fix/requeue-unhealthy-rollouts -- pkg/reconciler/roleinstanceset/statefulmode/stateful_instance_set_control_test.go
GOFLAGS=-mod=vendor go test -count=1 -run TestUpdateStatefulInstanceSetRetriesUnhealthyRollout ./pkg/reconciler/roleinstanceset/statefulmode/
# expect: FAIL  retry = 0s
```

### L3 — live (real cluster)

Requires a kubeconfig with a cluster running the RBG v1alpha2 CRDs. Build both binaries:

```bash
GOFLAGS=-mod=vendor CGO_ENABLED=0 go build -o /tmp/rbgs-head ./cmd/rbgs   # on PR head
git checkout c500c963 && GOFLAGS=-mod=vendor CGO_ENABLED=0 go build -o /tmp/rbgs-base ./cmd/rbgs && git checkout -
```

Run the scenario (see `scripts/live-scenario.sh` for the exact sequence):

1. Scale the in-cluster `rbgs-controller-manager` to 0 (so the local binary is the sole
   reconciler; RIS creation does not hit the RBG validating webhook, only `rolebasedgroups`
   does). **Restore it to its original replica count after.**
2. `KUBECONFIG=<kc> /tmp/rbgs-head --enable-webhooks none --metrics-bind-address=0 --health-probe-bind-address=:18082`
3. `bash scripts/live-scenario.sh /tmp/rbgs-verify/head.log head` — expect
   `result={"Requeue":false,"RequeueAfter":~7e9}` then `updated=2` / `ready=2` ~t+11s.
4. Repeat with `/tmp/rbgs-base` — expect `Rolling update budget exhausted` at line 618, **no
   RequeueAfter**, `updated=0` for 35s+.

The scenario creates a RoleInstanceSet (`/tmp/rbgs-verify/ris-rev1.yaml`): 2 replicas,
`maxSurge 0`, `maxUnavailable 2`, `RecreatePod`, a single component of `size: 1` running
`nginx:1.27` with an always-failing exec readinessProbe. rev2
(`/tmp/rbgs-verify/ris-rev2.yaml`) removes the probe. rev2 is applied the moment both
RoleInstances first report `RoleInstanceReady=False` so they are *freshly* unhealthy (< 10s).

## Continuing after the fix

- After the author extends the fix (or on a new commit), run
  `bash scripts/re-verify.sh` (no sha needed — it fetches the current PR head via the
  manifest's `pr` URL). It grafts the harness onto that ref and re-runs L1.
- **P0** contract test: green on the fixed code (already green on head).
- **F1** is a *canary*: fixed only when it **flips to fail**. If the author's fix schedules a
  requeue for the mixed config, invert it into a contract test asserting a positive
  `wait <= remaining window`.
- **L3** is cluster-dependent; re-run `scripts/live-scenario.sh` against the head binary.

## Harness-bites check (done this round)

A naive "scan all stalled targets for the earliest window" patch was applied to
`stateful_instance_set_control.go`, the F1 canary re-run, and it **flipped to fail** with
`wait=4.9998s`; the patch was then reverted and the production diff confirmed empty.

## Cluster used

ACK, 3 nodes (`cn-hongkong.*`), KUBECONFIG `~/.kube/config`, CRDs at v1alpha2 (stored
`v1alpha2`). In-cluster `rbgs-controller-manager` (image `rolebasedgroup/rbgs-controller:v0.8.0-0c00546d`,
2 replicas) was scaled to 0 during the test and restored to 2 after. Test namespace
`pr470-verify` was deleted. No cluster-wide destructive actions.
