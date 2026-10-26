# warmup-target-waits — bug verification

Reproducible evidence for the findings raised while reviewing
https://github.com/sgl-project/rbg/pull/478 (reviewer B / codex).

- Round 1: head `9f61d4f7` — findings F1a/F1b (gate blocks terminal accounting), F2 (dual
  timeout clocks), F3 (untested timeout branch); premise P0 confirmed on base `35e5d029`.
- Round 2: head `60b1b7cb` — delta `9f61d4f7..60b1b7cb`. F1/F2/F3 verified **fixed**; two new
  mid-run-churn regressions found and confirmed: **F4** (false `Completed/NoNodesMatched`
  with in-flight Pods) and **F5** (global timeout no longer bounds jobs with an active Pod on
  a non-desired node).
- Round 3: head `b0915b21` — delta `60b1b7cb..b0915b21` after a rebase onto
  `ecdfd6e6`. F4a/F4b/F5/F6/F7/F8 verified **fixed**; one new minor semantics finding **F9**
  (collapsed-set settle bypasses `maxFailedNodes` tolerance), pinned as a canary.
- Round 4 (this round): head `92cb7ef1` — delta `b0915b21..92cb7ef1` (commits `3bd283a5` +
  `92cb7ef1`). **F9 canary flipped → inverted to a 4-leg contract test (fixed)**; round-3 open
  questions **Q4** (cross-role surplus masks a pending Pod) and **Q2** (terminal
  `TargetReady=False` contradiction) are fixed in code and pinned as contract tests. N2 (dead
  duplicate settle branch) removed, N3 (RBGSA RBAC marker) added, Q1 answered in the KEP —
  verified by code reading.

Layers run against the code under review:

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit | `reconcileUnfinished` target-wait gate, timeout clocks, mid-run churn (fake client) | `go test ./internal/controller/workloads/ -run 'TestVerify_' -count=1 -v` |
| 1b. Premise (unit, **base** branch `35e5d029`) | base semantics the PR claims to fix | `VERIFY_PREMISE_ON_BASE=1 go test ./internal/controller/workloads/ -run 'TestVerify_Premise' -count=1 -v` (on a base worktree; the file is base-compatible) |
| 2. Integration | real API server (envtest), CRD upgrade A/B of the #466 CEL rule | `KUBEBUILDER_ASSETS="$(setup-envtest use 1.31.0 -p path)" go test ./test/envtest/warmupcrdverify/ -count=1 -v` (also run with `1.33.0` for the positive control) |
| 3. Live | — | skipped by scope decision; see `liveNote` in verify-manifest.json |

> Test polarity: contract tests (assert intended behavior) FAIL on buggy code / PASS when
> fixed. Round-1 canaries F1b/F2 flipped on head `60b1b7cb` and were **inverted to contract
> tests** (`TestVerify_CompletedWorkSurvivesExpiredGlobalTimeout`,
> `TestVerify_GlobalTimeoutIsSingleCreationAnchoredBudget`); on the old head they fail,
> documenting the fix.

## Problem premise (P0) — round 1, unchanged

| | |
|---|---|
| Claimed symptom (1) | "A missing target `RoleBasedGroup` is an unrecoverable one-shot failure" — `Failed/InvalidTarget` even if the target is created moments later |
| Claimed symptom (2) | "An existing target RBG whose Pods are not scheduled yet reports `Completed/NoNodesMatched`" — a silent false success |
| Claimed symptom (3) | "#466's customized-container-image CEL rule freezes pre-existing objects" on Kubernetes <= 1.32 (status strategy runs CEL without ratcheting) |
| Linked issue | none closing; follow-up to merged PR #466 |
| Reported component | `internal/controller/workloads/rolebasedgroupwarmup_controller.go` + Warmup CRD |
| Patched component | same |
| Component match | Yes |
| **Verdict** | **Confirmed** (all three) |
| Evidence | `results/unit-premise-base.txt`; `results/envtest-1.31.txt`; `results/envtest-1.33.txt` |

## Round 2 results (head `60b1b7cb`, delta `9f61d4f7..60b1b7cb`)

| ID | Claim | Layer | previous `9f61d4f7` | head `60b1b7cb` | base `35e5d029` | Verdict |
|----|-------|-------|--------------------|-----------------|-----------------|---------|
| F1a | Pending-Pod gate must not block completion of fully-warmed work | 1 | fail (bug present) | **pass** | — | **Fixed** |
| F1b | Fully-warmed job past the deadline must complete, not fail | 1 | fail (bug present) | **pass** | — | **Fixed** (canary flipped → inverted) |
| F2 | `globalTimeoutSeconds` is a single creation-anchored budget | 1 | fail (additive clocks) | **pass** | — | **Fixed** (canary flipped → inverted; API comment + KEP updated) |
| F3 | Wait-phase `GlobalTimeoutExceeded` branch has coverage | 1 | pass | pass (PR added `TestReconcile_PendingTargetPodsFailAfterGlobalTimeout`) | — | **Fixed** (harness fixture updated for expected-count readiness) |
| P0-3 | CEL rule freezes stored invalid objects on ≤1.32; PR CRD restores status writes | 2 | pass | pass | — | Still green |
| **F4a** | Target RBG deleted mid-run with active warmup Pods must not complete as `NoNodesMatched` | 1 | pass | **FAIL** (`Completed/NoNodesMatched`, `Active=1`) | pass | **Confirmed regression (this delta)** |
| **F4b** | All selected target Pods unscheduled mid-run (drain gap) must not complete as `NoNodesMatched` | 1 | pass (gate held) | **FAIL** (`Completed/NoNodesMatched`, `Active=1`) | fail (no gate existed) | **Confirmed regression vs previous head** (base was broken differently) |
| **F5** | Expired global timeout must fail the job + delete active Pods even when the only active Pod is on a non-desired node | 1 | pass | **FAIL** (stuck `Running`, no requeue, Pod kept) | pass | **Confirmed regression (this delta)** |

Raw output: `results/unit-round2-head-60b1b7cb.txt`, `results/unit-round2-previous-head-9f61d4f7.txt`,
`results/unit-round2-base-35e5d029.txt`, `results/unit-round2-harness-bites.txt`,
`results/reverify/` (re-verify.sh run).


## Round 3 results (head `b0915b21`, delta `60b1b7cb..b0915b21`, base moved `35e5d029` -> `ecdfd6e6`)

The PR was rebased onto `ecdfd6e6` (main gained #488 customized-action reporting;
orthogonal to the wait/completion logic) and added three commits: `e313d507`
(active-gating + terminal-gate accounting + scaling-adapter replicas + CRD guard),
`82ce44bc` (extract `updateWarmupPhase`), `b0915b21` (collapsed-set settle semantics).

| ID | Claim | Layer | previous head `60b1b7cb` | head `b0915b21` | Verdict |
|----|-------|-------|--------------------------|------------------|---------|
| F1a/F1b/F2/F3 | round-1 contracts (regression suite) | 1 | pass | **pass** | Still fixed |
| F4a | Target deleted mid-run with active Pods must not complete | 1 | FAIL | **pass** | **Fixed** (`updateEmptyDesiredPhase` keeps `Running` while active; settles `Completed/WarmupCompleted` with a truthful reason once Pods are terminal) |
| F4b | All target Pods unscheduled mid-run must not complete; replacement node must be warmed | 1 | FAIL | **pass** | **Fixed** (stays `Running` + 10 s poll while `pending>0 && desired==0`; PR test `TestReconcile_MidRunTargetGapWaitsAndWarmsReplacementNode` proves the replacement node is warmed) |
| F5 | Expired global timeout must fire with an active Pod on a non-desired node | 1 | FAIL | **pass** | **Fixed** (`warmupReachedTerminalState` blocks on any active Pod; PR test `TestReconcile_ActivePodOutsideDesiredSetFailsOnGlobalTimeout` asserts the orphan Pod is deleted) |
| F6 (Claude) | Stale terminal node on a dropped node must not bypass the expired timeout | 1 | FAIL | pass (code-verified: terminal accounting intersected with desired; PR test `TestReconcile_StaleTerminalNodeDoesNotBypassGlobalTimeout`) | **Fixed** |
| F7 | `desired==0` completions must report a truthful reason and preserve `startTime` | 1 | FAIL | pass (code + PR test `TestReconcile_TargetDeletedAfterSuccessKeepsStartTimeAndReason`) | **Fixed** |
| F8 | CRD guard must pin the container *item* schema | 1 | — | pass (`crd_test.go` now asserts `containers.Items.Schema.XValidations` is empty) | **Fixed** |
| P0 premise | re-confirmed on the NEW base `ecdfd6e6` (base moved) | 1 | — | base: both premise bugs still reproduce (`VERIFY_PREMISE_ON_BASE=1`) | Confirmed |
| P0-3 | CEL/status A/B on envtest 1.31.0 | 2 | pass | **pass** | Still green |
| **F9** | Collapsed-set settle should honor `maxFailedNodes` tolerance | 1 | n/a (path did not exist) | **CANARY pinned**: within-tolerance job settles `Failed/InvalidTarget` when the target is deleted pre-settle | Open (minor — see findings) |

Raw output: `results/reverify/` (round-3 run against `b0915b21`).

## Round 4 results (head `92cb7ef1`, delta `b0915b21..92cb7ef1`)

| ID | Claim | Layer | previous `b0915b21` | head `92cb7ef1` | Verdict |
|----|-------|-------|--------------------|-----------------|---------|
| F1a..F5, P0-3 | all round-1/2/3 contracts | 1+2 | pass | **pass** | Still fixed (regression suite green) |
| **F9** | Collapsed-set settle honors `maxFailedNodes` (within/unset → `Completed` with `status.failed` recorded; beyond → `Failed/MaxFailedNodesExceeded`) | 1 | **FAIL** (`Failed/InvalidTarget` or `/TargetNoLongerSelectsNodes` regardless of tolerance) | **pass** (4 legs) | **Fixed** (canary flipped → inverted) |
| **Q4** | Surplus scheduled Pods in one selected role must not mask a missing/Pending Pod in another | 1 | **FAIL** (`TargetReady=True/RoleBasedGroupReady`, warmup Pods created) | **pass** (waits `TargetPodsNotScheduled`, 0 Pods) | **Fixed** (per-role deficits) |
| **Q2** | Started job losing its target settles with `TargetReady=Unknown/RoleBasedGroupNotFound`, not `False` beside the terminal condition | 1 | **FAIL** (`Complete=True` + `TargetReady=False`) | **pass** | **Fixed** (`setTargetUnknown`) |

PR's own new tests green at head: `TestReconcile_CollapsedDesiredSetHonorsMaxFailedNodes`
(3 legs), `TestReconcile_CrossRoleSurplusDoesNotMaskPendingTargetPod`,
`TestReconcile_TargetDeletedMidRunCompletes` + `...KeepsStartTimeAndReason` Unknown
assertions (`results/unit-round4-pr-tests-head.txt`). Full unit sweep
`./internal/... ./api/... ./pkg/...` green at head; repo envtest suite (1.31.0: rbg, restart_policy, stateful, webhook) green at head (`results/envtest-round4-head-92cb7ef1.txt`).

Raw output: `results/unit-round4-head-92cb7ef1.txt`, `results/unit-round4-prev-b0915b21.txt`,
`results/reverify/VERDICTS-round4.txt`, `results/reverify/VERDICTS-round4-at-previous-head-b0915b21.txt`.

## Per-finding detail (round 2)

### F4 — mid-run collapse of the desired-node set reports false `Completed/NoNodesMatched` (`blocker`)

The delta lets the reconcile continue once any warmup Pod exists (`warmupPodsExist`), so a
fully-warmed job can complete (that fixed F1a). But when the desired-node set collapses to
empty mid-run — target RBG deleted (F4a) or every selected-role target Pod momentarily
unscheduled (F4b: eviction, node drain, Recreate-style rollout gap) — `getDesiredNodesToWarmup`
returns `{}` and `updateStatus`'s unconditional `desired == 0` branch fires regardless of
active Pods: phase `Completed`, condition `Complete/NoNodesMatched` ("No nodes matched the
target configuration"), `CompletionTime` set — while `status.Active=1` and a warmup Pod is
still running. Because a terminal Warmup is never reconciled again (`Reconcile` routes
`Completed` to `reconcileFinished`, TTL only):

- the in-flight Pod is never accounted (counters freeze with `Active=1`),
- with `ttlSecondsAfterFinished` the CR — and via owner refs the still-running Pod — is
  deleted mid-work,
- (F4b) when the replacement target Pod lands on a new node, that node is **never warmed**:
  the job already reported success. That is the cold-start the feature exists to prevent.

The PR's stated intent ("terminal accounting continues so a **fully-warmed** job is not
stranded") only covers succeeded work; the code also completes partially-warmed jobs, and
mislabels even the fully-warmed case as `NoNodesMatched`.

Attribution: F4a passes on base and on `9f61d4f7` (missing target → `Running`/wait), fails on
`60b1b7cb` → introduced by this delta. F4b passes on `9f61d4f7` (the readiness gate held
regardless of Pods), fails on base (no gate existed) and again on `60b1b7cb` (gate now skipped
once Pods exist) → regression vs the previous head; the underlying unconditional
`desired==0` completion is older.

Harness-bites: a temporary patch gating the `desired == 0` completion on `active == 0` turns
F4a/F4b green on the head (`results/unit-round2-harness-bites.txt`); reverted afterwards.

### F5 — terminal-state gate lets an orphan active Pod defeat the global timeout (`blocker`)

`warmupReachedTerminalState` counts active Pods only on **desired** nodes, while
`updateStatus` counts active Pods on **any** node. With desired nodes all terminal and one
warmup Pod still active on a node that left the desired set (target Pod migrated during a
rolling update while its warmup Pod is stuck in `ImagePullBackOff`):

- the gate reports "terminal" → the expired `globalTimeoutSeconds` check is skipped;
- `updateStatus` reports `active=1` → not complete → phase stays `Running`;
- `requeueForTimeout` returns no requeue once the deadline passed (remaining ≤ 0).

Net: the job is stuck `Running` forever, the orphan Pod is never deleted, and the documented
contract ("When the timeout is exceeded, all active Pods are deleted and the job is marked as
Failed") no longer holds. Base and `9f61d4f7` enforced the timeout unconditionally → both
pass; `60b1b7cb` fails → introduced by this delta.

Harness-bites: counting active Pods on all nodes in `warmupReachedTerminalState` turns F5
green on the head (same patch run as F4); reverted afterwards.

### Fixed this round

- **F1a** — pending-gate placement: gate now applies only while no warmup Pods exist;
  completion proceeds otherwise. Contract green on head.
- **F1b** — fully-warmed jobs survive an expired deadline: `warmupReachedTerminalState` skips
  the timeout for terminal work. Canary flipped; inverted to contract.
- **F2** — one creation-anchored budget for wait + run phases (`globalTimeoutDeadline`);
  `globalTimeoutSeconds` API comment, generated CRD description, and KEP all updated to
  match. Canary flipped; inverted to contract.
- **F3** — PR added `TestReconcile_PendingTargetPodsFailAfterGlobalTimeout`. The harness test
  needed a fixture update (the readiness gate now derives the expected Pod count from the RBG
  spec, so the fixture RBG must declare the role) — a harness update, not a behavior change.

## Proposed fixes (NOT applied to production here)

- **F4**: do not let `updateStatus` complete while Pods are in flight — e.g. gate the
  `desired == 0` branch on `active == 0` (verified green), or derive desired nodes from the
  Warmup's own Pods when the target snapshot is empty. Also report the fully-warmed
  target-deleted case as `WarmupCompleted` with real counters instead of `NoNodesMatched`.
- **F5**: make `warmupReachedTerminalState` consistent with `updateStatus` — count active
  Pods on all nodes, not only desired ones (verified green).

Both patches together keep the whole `internal/controller/workloads` suite (PR's own tests +
this harness) green.

## Continuing after the fix (possibly on another machine)

The harness is on branch `verify/warmup-target-waits-codex` (fork `cheyang/rbg`), production
code untouched, so it grafts onto whatever the fixed code is.

1. Get it onto the fixed code:
   ```bash
   git fetch https://github.com/cheyang/rbg.git verify/warmup-target-waits-codex
   git checkout <fixed-branch>
   git checkout FETCH_HEAD -- docs/verification/warmup-target-waits \
     internal/controller/workloads/warmup_target_waits_verification_test.go \
     internal/controller/workloads/warmup_midrun_churn_verification_test.go \
     internal/controller/workloads/warmup_premise_verification_test.go \
     test/envtest/warmupcrdverify/warmup_crd_status_verify_test.go
   ```
2. Prereqs: Layer 1 = Go toolchain only; Layer 2 = `setup-envtest` (assets for 1.31.0, and
   1.33.0 for the ratcheting control). No live layer.
3. One-command re-check (from a checkout of the verify branch):
   ```bash
   bash docs/verification/warmup-target-waits/scripts/re-verify.sh
   ```
   (auto-resolves the current PR head from the manifest's `pr` URL; unit + integration layers).
4. Polarity: all findings are contract tests now — green when fixed. F1b/F2 were canaries in
   round 1 and were inverted after head `60b1b7cb` flipped them.
5. Premise legs (P0-1/P0-2) intentionally skip on PR head; they run only on base with
   `VERIFY_PREMISE_ON_BASE=1`.

### Kickoff prompt for a fresh agent
```text
Continue a verification task on branch verify/warmup-target-waits-codex (fork
https://github.com/cheyang/rbg.git). Background: a review of
https://github.com/sgl-project/rbg/pull/478 confirmed round-1 findings fixed at head
60b1b7cb (F1 pending-gate blocking completion, F2 dual timeout clocks, F3 missing timeout
test) and found two new regressions: F4 (mid-run collapse of the desired-node set reports
Completed/NoNodesMatched while warmup Pods are still active) and F5 (an active warmup Pod on
a non-desired node defeats the creation-anchored global timeout; job stuck Running with no
requeue). Read docs/verification/warmup-target-waits/README.md and follow it: graft the
harness onto the fixed code (or run scripts/re-verify.sh from the verify branch), re-run unit
+ integration layers, and report an observed-vs-expected table. All findings are contract
polarity now. Production code must stay untouched.
```
