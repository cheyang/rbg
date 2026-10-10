# warmup-target-waits — bug verification

Reproducible evidence for the findings raised while reviewing
https://github.com/sgl-project/rbg/pull/478 (reviewer B / codex; head `9f61d4f7`).

Layers run against the **code under review** (`9f61d4f73d72bb47e181c81f1d6a9a6de12f0d69`):

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit | `reconcileUnfinished` target-wait gate, timeout clocks (fake client) | `go test ./internal/controller/workloads/ -run 'TestVerify_' -count=1 -v` |
| 1b. Premise (unit, **base** branch `35e5d029`) | base semantics the PR claims to fix | `VERIFY_PREMISE_ON_BASE=1 go test ./internal/controller/workloads/ -run 'TestVerify_Premise' -count=1 -v` (on a base worktree; the file is base-compatible) |
| 2. Integration | real API server (envtest), CRD upgrade A/B of the #466 CEL rule | `KUBEBUILDER_ASSETS="$(setup-envtest use 1.31.0 -p path)" go test ./test/envtest/warmupcrdverify/ -count=1 -v` (also run with `1.33.0` for the positive control) |
| 3. Live | — | skipped by scope decision; see `liveNote` in verify-manifest.json |

> Test polarity: contract tests (assert intended behavior) FAIL on buggy code / PASS when
> fixed. Bug-canary tests (assert current behavior) PASS now / FLIP to red when fixed.

## Problem premise (P0)

Three claims from the PR body, verified independently:

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

- **P0-1 confirmed**: on base, missing target → `Failed/InvalidTarget`, no requeue
  (base's own `TestReconcile_MissingTargetRBGFailsWithoutRequeue` +
  `TestVerify_Premise_MissingTargetFailsTerminallyOnBase`).
- **P0-2 confirmed**: on base, target RBG with only unscheduled Pods →
  `Completed/NoNodesMatched`, `desired=0` (`TestVerify_Premise_UnscheduledTargetPodsCompleteOnBase`).
- **P0-3 confirmed, with version boundary**: envtest A/B. With the PR CRD installed, store a
  Warmup whose customized-action container has no `image`; status writes succeed. Upgrade the
  CRD to the base (#466) version carrying the item-level CEL rule; fresh invalid creates are
  rejected (control), and a **status write on the pre-existing invalid object is rejected**
  on apiserver **1.31.0** (`... is invalid: spec.targetNodes.customizedAction.containers[0]:
  Invalid value: "object": customized action container image must not be empty`) but
  **accepted on 1.33.0** (status ratcheting). Restoring the PR CRD makes status writable
  again. The claimed ≤1.32 freeze and the 1.33 boundary are both real.

## Summary of results

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| F1a | A Warmup whose desired-node work is fully terminal cannot complete while a selected-role target Pod is pending (gate runs before completion accounting) | 1 | **Confirmed** (contract test red) | `results/unit-pr-head.txt` |
| F1b | With `globalTimeoutSeconds`, the same stall turns 100%-warmed work into terminal `Failed/GlobalTimeoutExceeded` | 1 | **Confirmed** (canary green on current behavior) | `results/unit-pr-head.txt` |
| F2 | Wait-phase and run-phase timeout clocks differ (creation vs startTime): total budget can reach ~2× `globalTimeoutSeconds`; field comment contradicts wait-phase clock | 1 | **Confirmed** (canary green) | `results/unit-pr-head.txt` |
| F3 | The `GlobalTimeoutExceeded`-while-pods-pending branch has no test in the PR | 1 | Confirmed gap; harness adds it (green) | `results/unit-pr-head.txt` |
| P0-3 | CEL rule freezes stored invalid objects on ≤1.32; PR CRD restores status writes | 2 | **Confirmed** (+1.33 control) | `results/envtest-1.31.txt`, `results/envtest-1.33.txt` |

## Per-finding detail

### F1 — pending-gate placement blocks terminal accounting (`major`)

`reconcileUnfinished` runs the pending-Pod gate on **every** reconcile, before
`updateStatus` (the only place `Completed` is computed). Reproduction
(`TestVerify_MidWarmupPendingPodBlocksCompletion`): Warmup targets role `worker`; target Pod
on `node-1` → reconcile creates the warmup Pod; mark it `Succeeded`; then a new selected-role
target Pod appears with `nodeName=""` (scale-up, or recreation after a node failure).

- Control (no pending Pod): second reconcile → `Completed` (`1/1 nodes warmed up successfully`).
- With the pending Pod: reconcile → `Running`, `TargetReady=False/TargetPodsNotScheduled`,
  10s requeue, forever if no `globalTimeoutSeconds` — even though `1/1` desired nodes are done.

Timeout leg (`TestVerify_MidWarmupPendingPodFailsCompletedWorkOnTimeout`, canary): with
`globalTimeoutSeconds=60` and the gate blocking past the deadline, the job ends
`Failed/GlobalTimeoutExceeded`: *"Warmup job timed out waiting for 1 selected target Pod(s)
to be scheduled"* — with `status.succeeded=1`. A job that did all its work reports failure.

Design tension (explicitly called out for the maintainers): the pre-start gating was
requested in review ("wait for all selected target pods") and is correct; the finding is only
about the gate also blocking **completion** of work already done. Candidate fixes (not applied):
evaluate completion before the gate, or apply the gate only while no warmup Pods exist yet.

Harness-bites check: a temporary patch gating only when
`len(activePods)==0 && len(succeededPods)==0 && len(failedPods)==0` turns F1a green and flips
F1b red (see `results/unit-harness-bites.txt`); patch reverted afterwards; production diff empty.

### F2 — two timeout clocks (`minor`)

`targetWaitExpired` bounds the pre-start wait from `creationTimestamp`; once the first Pod
exists, `status.startTime` (set at Pod creation) anchors both the wait gate and the run-phase
timeout. Canary `TestVerify_WaitAndRunTimeoutBudgetsAreAdditive`: `creation=now-120s`,
`startTime=now-30s`, `globalTimeoutSeconds=60`, pending target Pod → the job keeps waiting
(creation-anchored elapsed 120s > 60s). Total wall time from creation can approach
2× the configured "global" timeout. Also, the API field comment still says the timeout is
"measured from the time the first Pod is created (status.startTime)", which no longer
describes the wait phase.

### F3 — untested timeout branch (`minor`)

`Failed/GlobalTimeoutExceeded` for still-unscheduled target Pods (the second
`targetWaitExpired` call site) has no test in the PR. Harness adds
`TestVerify_PendingPodsFailAfterGlobalTimeout` (contract, green on PR head).

### P0-3 — CRD freeze A/B (premise, integration)

See "Problem premise" above. The test installs the PR CRD from `config/crd/bases/`, stores an
invalid object, swaps in the base CRD from
`docs/verification/warmup-target-waits/assets/warmup-crd-base-with-cel-rule.yaml` (extracted
from merge-base `35e5d029`), probes admission, then writes status. Version-dependent
assertion: reject on ≤1.32, accept on ≥1.33 (both observed).

## Proposed fixes (NOT applied to production here)

- **F1**: let terminal accounting win — either compute completion before the pending gate, or
  skip the gate once any warmup Pod exists (`if pending > 0 && len(activePods) == 0 &&
  len(succeededPods) == 0 && len(failedPods) == 0`, the bites-check shape). Keeps the requested
  pre-start semantics intact.
- **F2**: document the two clocks (KEP + `GlobalTimeoutSeconds` field comment), or anchor the
  whole job at creation.
- **F3**: absorb `TestVerify_PendingPodsFailAfterGlobalTimeout` (or equivalent) into the PR.

## Continuing after the fix (possibly on another machine)

The harness is on branch `verify/warmup-target-waits-codex` (fork `cheyang/rbg`), production
code untouched, so it grafts onto whatever the fixed code is.

1. Get it onto the fixed code:
   ```bash
   git fetch https://github.com/cheyang/rbg.git verify/warmup-target-waits-codex
   git checkout <fixed-branch>
   git checkout FETCH_HEAD -- docs/verification/warmup-target-waits \
     internal/controller/workloads/warmup_target_waits_verification_test.go \
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
4. Polarity: F1a and F3 are contract tests — green when fixed. F1b and F2 are canaries — they
   must FLIP to red when fixed; then invert them (or promote the new behavior to contract).
   P0-3 is version-dependent by design and should stay green.
5. Premise legs (P0-1/P0-2) intentionally skip on PR head; they run only on base with
   `VERIFY_PREMISE_ON_BASE=1`.

### Kickoff prompt for a fresh agent
```text
Continue a verification task on branch verify/warmup-target-waits-codex (fork
https://github.com/cheyang/rbg.git). Background: a review of
https://github.com/sgl-project/rbg/pull/478 produced findings F1a/F1b (pending-pod gate
blocks completion of fully-warmed jobs), F2 (dual timeout clocks), F3 (missing timeout-branch
test); premise P0 was confirmed on base and via envtest 1.31/1.33 A/B. Read
docs/verification/warmup-target-waits/README.md ("Continuing after the fix") and follow it:
graft the harness onto the fixed code (or run scripts/re-verify.sh from the verify branch),
re-run unit + integration layers, mind the polarity table (F1b/F2 are canaries — invert when
they flip), and report an observed-vs-expected table. Production code must stay untouched.
```
