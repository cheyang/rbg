# PR #470 verification — retry stateful rollouts after the unhealthy window

PR: https://github.com/sgl-project/rbg/pull/470 (`fix: retry stateful rollouts after the unhealthy window`, fixes #464)
Branch: `verify/pr470-requeue-unhealthy-rollout` (based on PR head `ec577f76`). Production code untouched; the only added file is the F1 canary test.

## Premise (P0) — Confirmed

> A Stateful RoleInstanceSet rollout stalls when fresh-unhealthy instances exhaust the
> maxUnavailable budget, because the base controller schedules no requeue after the 10s
> unhealthy window expires.

- **Unit (against base `c500c963`):** `TestUpdateStatefulInstanceSetRetriesUnhealthyRollout`
  (the PR's own test, grafted onto base *without* the fix) fails with `retry = 0s, want a
  positive delay of at most 10s`. The base budget-exhausted branch only logs + returns; no
  `durationStore.Push`. → premise mechanism is real.
- **Live (against base binary on the cluster):** `Rolling update budget exhausted` at
  `stateful_instance_set_control.go:618` with **no `RequeueAfter`**; `updated=0` for the full
  35s observation window — the rollout never resumes past the 10s window.

## Fix (HEAD) — Confirmed live

On the PR head binary: the budget-exhausted branch pushes
`RequeueAfter: 7211329726` ns ≈ **7.2s** (the remaining unhealthy window). The requeue fires
~7s later; the RIs are now stably-unhealthy → `isFree` → replaced with rev2 pods (no failing
probe) → `ready=2`, `currentRev=rev2` at **t+11s**.

## Observed-vs-expected table

| id | claim | layer | polarity | verdict | evidence |
| --- | --- | --- | --- | --- | --- |
| P0 | base stalls (no requeue after 10s window) | unit + live | contract | **Confirmed** | base test `retry=0s`; live `updated=0` × 35s, no RequeueAfter |
| F1 | fix only requeues from the first budget-exhausted target; healthy blocker + different fresh-unhealthy target still stalls | unit | canary | **Confirmed** | `TestF1MixedHealthyBlockerStillStalls` PASS on head (wait=0); flips to fail under a "scan all targets" patch |
| F2 | 1ns floor in `max(...)` is dead code | review | n/a | **Confirmed (cosmetic)** | `isStablyUnhealthy` flips `isFree` before the branch once the window expired |

## F1 — the fix is narrower than the general stall class

`buildUpdateTargets` iterates **highest-ordinal first**. The fix pushes a requeue only for
*the first target that trips* `!isFree && initialBaseUnavail+newlyUnavail >= effectiveBudget`,
and only if *that* target has an `instanceUnhealthySince` entry. A healthy blocker has its
entry deleted by `observeInstanceHealth`, so when it is the first exhausted target the code
pushes **nothing** even if a lower ordinal is freshly-unhealthy with a pending 10s window.

`TestF1MixedHealthyBlockerStillStalls` (canary) constructs exactly that: ord 1 healthy at
currentRev, ord 0 freshly unhealthy, `maxUnavailable=1`. On the PR head `Pop(key)==0` (no
requeue) — the mixed-config rollout still stalls.

This is **not a regression** (base behaves identically) and is outside the issue #464 repro
(all-unhealthy), so it does not block the PR. A more robust fix would scan all stalled targets
for the earliest expiring window; the canary flips to fail under such a patch and should then
be inverted into a contract test.

## Layers & how to run

### L1 — unit (deterministic)

```bash
cd <rbg checkout>
GOFLAGS=-mod=vendor go test -count=1 \
  -run 'TestUpdateStatefulInstanceSetRetriesUnhealthyRollout|TestF1MixedHealthyBlockerStillStalls' \
  ./pkg/reconciler/roleinstanceset/statefulmode/
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
