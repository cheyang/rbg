# Verification — PR #474 "feat: support RoleBasedGroupSet rolling update"

Reviewer: Codex (debate pipeline, Reviewer B). Reviewed head: `44241903`
(`.last-reviewed`). Production code untouched — this branch adds only
`docs/verification/**` and `*_verify_test.go` files.

## Premise (P0)

> "when the template changes, every outdated child is updated within a single
> reconcile, with no ordering and no availability gating" — PR body.

**Verdict: Confirmed** (run against base `0821cb5b`, results/p0-base.txt): one
`Reconcile` moved all 3 children to the new template at once. The same test passes
on the PR head with `rolloutStrategy` unset, which also confirms the
backward-compatibility claim (static path unchanged).

## Findings

| id | claim | polarity | layer | status on 44241903 |
|----|-------|----------|-------|--------------------|
| F1 | v1alpha1↔v1alpha2 conversion drops `spec.rolloutStrategy` and the rollout status fields (`currentRevision`, `updateRevision`, counters); v1alpha1 is still served, so a full-object v1alpha1 write silently reverts a rolling set to the un-paced static path mid-rollout | contract | unit | **RED = confirmed bug** (`TestVerify_ConversionPreservesRolloutStrategy` fails) |
| F3 | webhook rejects integer `maxUnavailable: 0` + `maxSurge: 0` but accepts `"0%"`/`"0%"` (resolves to 0/0 for any replica count); the controller then silently clamps maxUnavailable to 1, rewriting explicit user intent | canary | unit | canary passes (pins current behavior); flip when fixed |

## Guard-rail tests (no finding — the PR's core claims, verified)

| id | claim | layer | status |
|----|-------|-------|--------|
| G1 | paced recreate: never deletes ready groups beyond `ready-(replicas-maxUnavailable)`; converges; `RolloutComplete`; steady state does NOT requeue | unit | green, converged in 12 steps |
| G2 | `maxUnavailable: 0` + `maxSurge: 1`: serving never drops below replicas; surge created then reclaimed | unit | green |
| G3 | `partition: 2`: ordinals 0,1 keep old template and identity; a deleted held-back group is rebuilt at the OLD revision | unit | green |
| G4 | template flip-flop A→B→A converges | unit | green |
| G5 | replicas-only template diff scales in place, no recreation | unit | green |
| G6 | scale-in during a rollout is budget-gated and converges | unit | green |
| G7 | same drive against a REAL API server (envtest, real CRDs/defaulting): converges, each child recreated exactly once (no defaulting-induced recreate loop), foreground delete + UID precondition work | integration | green (results/l2-envtest.txt) |

## Harness-bites check (Step 4)

Two production perturbations were applied temporarily and reverted
(results/harness-bites.txt):

1. Removing the budget gate in `deleteGroupSetOutdatedChildren` → G1/G2 fail
   ("deleted 3 ready groups with ready=3 replicas=3 maxUnavailable=1").
2. Removing the rebuild-at-current rule in `fillMissingGroupSetBaseOrdinals` →
   G3 fails ("must come back on the previous template").

Production diff is empty after the revert.

## How to run

```bash
# unit layer (fake client)
go test ./internal/controller/workloads/ ./api/workloads/v1alpha1/ ./api/workloads/v1alpha2/ -run 'TestVerify_' -count=1 -v

# integration layer (needs envtest assets)
KUBEBUILDER_ASSETS=$(ls -d $HOME/.local/share/kubebuilder-envtest/k8s/*-linux-amd64 | head -1) \
  go test ./internal/controller/workloads/ -run 'TestVerify_Envtest' -count=1 -v -timeout 8m
```

Note: on the reviewed head the unit run FAILS on
`TestVerify_ConversionPreservesRolloutStrategy` — that red IS the F1 reproduction.
Expected state after the author fixes the conversion: all green, and the two F3
canaries flip red (invert or promote them then).

## Continuing after the fix

```bash
git fetch https://github.com/cheyang/rbg.git verify/rbgs-rolling-update-codex
git checkout verify/rbgs-rolling-update-codex
bash docs/verification/rbgs-rolling-update/scripts/re-verify.sh   # resolves the current PR head itself
```

re-verify.sh grafts the harness onto the current PR head, runs unit+integration, and
prints per finding Fixed / Still-broken / Harness-update (F1 fixed iff its contract
test goes green; F3 fixed iff its canaries flip). Live e2e (L3) is covered by the
repo's own kind-based suite (`make test-e2e`), which is green in CI.

Kickoff prompt for a fresh agent: "Continue verifying sgl-project/rbg PR #474 from
branch verify/rbgs-rolling-update-codex on the cheyang fork. Read
docs/verification/rbgs-rolling-update/README.md and run scripts/re-verify.sh."

## Round 2 — head `9e423e86` (delta `44241903..9e423e86`: v1alpha1 round-trip fix, envtest suite, InPlaceUpdate strategy)

Reviewed head advanced to `9e423e86` (`.last-reviewed`). All round-1 guard rails
(G1-G7) still pass unmodified on the new head (results/l1-unit-round2.txt,
results/l2-envtest-round2.txt).

### Round-1 findings re-verified

| id | status on 9e423e86 | evidence |
|----|--------------------|----------|
| F1 (blocker: conversion dropped rolloutStrategy) | **FIXED** — annotation stash in `rolebasedgroupset_conversion.go` | contract test now green, extended to InPlaceUpdate + full status block (`TestVerify_ConversionPreservesInPlaceStrategy`) |
| F3 (minor: "0%"/"0%" admission gap) | **FIXED** — declared 0/0 rejected in every spelling for Recreate; InPlace rules enforced; clamp is now only the documented fallback for positive percents resolving to 0 | flipped canary promoted to contract tests (`TestVerify_WebhookRejectsPercentZeroZero`, `TestVerify_WebhookInPlaceUpdateRules`, `TestVerify_ControllerClampsPercentZeroZero_Contract`) |
| F8 (minor: no envtest drive) | **FIXED** — author added `test/envtest/testcase/rbgs_rolling` running real controllers | CI envtest job green on 9e423e86 |
| F4 / F5 / F6 / F7 | unchanged (F6 shrinks: InPlaceUpdate, now the default, syncs metadata-only changes in place; Recreate still recreates) | code inspection |

### Round-2 additions

| id | claim | polarity | layer | status |
|----|-------|----------|-------|--------|
| G8 | InPlaceUpdate pacing: never recreates a base child (UID/name stable), at most one child in flight under maxUnavailable=1, a stalled in-flight child holds the whole budget (no further child touched, Rolling stays True), steady state quiet | contract | unit | green |
| G8b | `groupSetChildReady` barrier semantics: Ready + RollingUpdateInProgress=True at current generation is NOT serving; stale-observation barrier is NOT serving; absent barrier (old RBG controller) is serving | contract | unit | green |
| G9 | InPlaceUpdate against a real API server: UIDs stable, stalled child holds the budget, converges under real CRD defaulting, exactly one generation bump per child for the template change and no churn afterwards | contract | integration (envtest) | green (results/l2-envtest-round2.txt) |
| F9 | replicas-only template diff under InPlaceUpdate is budget-paced (one child per readiness window) instead of immediate like in Recreate mode; the PR body claims scaling is never paced | canary | unit | canary passes = behavior pinned; intent questioned |

Harness-bites round 2 (results/harness-bites-round2.txt): disabling the budget gate
turns G8's stall test red; dropping the stale-observation guard turns G8b red.
Production diff empty after reverts.

### Note on the fake client

The controller-runtime fake client never manages `metadata.generation`, which would
hide the generation-lag barrier the InPlace budget relies on. The round-2 unit drive
(`rolebasedgroupset_inplace_verify_test.go`) emulates the API server: it bumps a
child's generation itself when the controller rewrites the child spec, and plays the
child RBG controller by flipping `status.observedGeneration` after a cooldown. The
envtest layer (G9) needs no emulation and is the authoritative check.

## Round 3 — head `98720938` (delta `9e423e86..98720938`: example manifest fix + PR body rewrite)

The code delta is exactly one file: `examples/basic/rbgs/rbgs-rolling-update.yaml`
— the stale "InPlaceUpdate is reserved; the CRD enum rejects it" comments are
replaced with accurate descriptions (closes Copilot finding C4242488204). The PR
body was rewritten and is now accurate on every point checked: strategy field table
(type default InPlaceUpdate, maxUnavailable/maxSurge semantics), no paused
field/condition, ControllerRevision-based design, per-strategy scale-only behavior,
all 22 unit + 10 e2e test names exist in the tree, webhook rules match
`validateGroupSetRolloutStrategy`, and the controller-behavior narrative matches
`rolebasedgroupset_rolling.go`.

Re-verify (results/reverify/ round 3): all contract tests green on the new head;
F9 canary still passes (budget-paced replicas-only under InPlaceUpdate — now
correctly documented in the body).

### Round-3 addition

| id | claim | polarity | layer | status |
|----|-------|----------|-------|--------|
| F10 | body claims held-back ordinals are "excluded from ... currentReplicas"; `updateRollingGroupSetStatus` counts them (no partition filter on the CurrentReplicas branch; author's own `TestSyncRollingGroupSet_StrategiesRecoverMissingOrdinals` expects CurrentReplicas=2 with partition=3) | canary | unit | passes = body clause inaccurate (one-line body fix) |
