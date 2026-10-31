# pr-533-updatedreplicas-status — bug verification

Reproducible evidence for the review of https://github.com/sgl-project/rbg/pull/533
("fix: update updatedReplicas status", head `4ab4a5ee`, base `7894133d`).

Layers run against the **code under review** (`4ab4a5ee`) and, for premise P0, against the
**base** (`7894133d`):

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit | `updateRBGStatus` merge + SSA-skip gate (fake client, intercepted patch payload) | `go test ./internal/controller/workloads/ -run 'TestVerifyPR533\|TestUpdateRBGStatusUpdatedReplicas' -v` |
| 2. Integration | real API server: SSA status patch persists `updatedReplicas` (envtest) | `KUBEBUILDER_ASSETS=$(bin/setup-envtest use 1.31.0 --bin-dir bin -p path) go test ./internal/controller/workloads/ -run TestVerifyPR533UpdatedReplicasPersistedServerSide -v` |
| 3. Live | skipped this round (debate setup: unit + integration only) | — |

> Test polarity: all tests are CONTRACT tests (assert intended behavior). They FAIL on the
> base branch and PASS on the PR head.

## Problem premise (P0)

| | |
|---|---|
| Claimed symptom | "RBG status synchronization only compared Replicas and ReadyReplicas, so changes to UpdatedReplicas alone were ignored, leaving stale rollout progress even after repeated reconciliations." (PR body, §Motivation) |
| Linked issue | none — PR body says literally `fixes #XXXX` (placeholder). The `#15` auto-detected by tooling comes from the PR-template example text and is a closed, unrelated pull request, not a problem statement. |
| Reported component | RBG status sync (`rolebasedgroup_controller.go` `updateRBGStatus`) |
| Patched component | same |
| Component match | Yes |
| **Verdict** | **Confirmed** (real problem on base) |
| Evidence | unit: `results/unit-base.json` (UpdatedReplicas-only cases FAIL on base, PASS on head `results/unit-head.json`); integration: on base the API server keeps `updatedReplicas = 1` after reconcile — `results/integration-base.json` ("server-side updatedReplicas = 1, want 2") |

Mechanism on base: `updateRBGStatus` merged an observed `RoleStatus` into
`rbg.Status.RoleStatuses[j]` only when `Replicas` or `ReadyReplicas` differed; the skip-SSA
gate (`reflect.DeepEqual(oldStatus, rbg.Status)`) then held, so an `UpdatedReplicas`-only
drift was never written and stayed stale indefinitely (until a replicas change).

## Summary of results

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| P0 | UpdatedReplicas-only drift stays stale on base | 1+2 | Confirmed | `results/unit-base.json`, `results/integration-base.json` |
| F1 | On head, UpdatedReplicas-only drift propagates in memory and is flushed with exactly one SSA status patch whose payload carries `updatedReplicas` explicitly (incl. explicit `0`); a no-change second call does not patch | 1 | Confirmed fixed on head | `results/unit-head.json` |
| F1 | Same, end to end through a real API server: server-side value updates; no-change reconcile does not bump resourceVersion | 2 | Confirmed fixed on head | `results/integration-head.json` |
| F2 | Pre-existing behavior intact: ReadyReplicas drift still propagates and patches | 1 | Confirmed | `results/unit-head.json` (`TestVerifyPR533ReadyReplicasChangeStillPropagates`) |
| — | PR's own test `TestUpdateRBGStatusUpdatedReplicas` | 1 | bites: FAIL on base / PASS on head | `results/unit-base.json`, `results/unit-head.json` |
| — | No regression in the rest of the controller package | 1 | PASS on head | `results/full-package-head.txt` |

## Per-finding detail

### P0 / F1 — stale `updatedReplicas` (the bug this PR fixes)

- Unit command: `go test ./internal/controller/workloads/ -run 'TestVerifyPR533' -v`
  - Base: `TestVerifyPR533UpdatedReplicasOnlyChangePropagates` FAIL — in-memory
    `updatedReplicas` stayed 1, 0 patches issued (expected 1). Same for the reset-to-zero case.
  - Head: both PASS — exactly one SSA status patch; payload contains `"updatedReplicas":2`
    (and explicit `"updatedReplicas":0` for the reset case, so a zero clears a stale value).
- Integration command: `KUBEBUILDER_ASSETS=<envtest 1.31.0> go test ./internal/controller/workloads/ -run TestVerifyPR533UpdatedReplicasPersistedServerSide -v`
  - Base: FAIL — after `updateRBGStatus`, GET from the real API server returns
    `updatedReplicas = 1` (stale), proving no SSA patch was ever sent.
  - Head: PASS — server-side value is 2, and a subsequent no-change reconcile leaves
    `resourceVersion` untouched (skip-gate works against a real server too).

Harness-bites check: running the head harness against the base SHA turns every
UpdatedReplicas contract test red while the ReadyReplicas guard stays green — the harness
exercises exactly the changed lines.

## Proposed fixes (NOT applied to production here)

None needed — the PR itself is the fix and it is correct as far as these layers can tell.
Non-blocking observations are in the review findings (missing integration/e2e coverage in the
PR itself; `fixes #XXXX` placeholder).

## Continuing after the fix (possibly on another machine)

The harness is on branch `verify/pr-533-updatedreplicas-status-codex` (remote
`reviewer-fork` = https://github.com/cheyang/rbg.git), production code untouched.

```bash
bash docs/verification/pr-533-updatedreplicas-status/scripts/re-verify.sh   # auto-fetches current PR head
```

1. Layer 1 needs only the Go toolchain. Layer 2 needs envtest binaries:
   `bin/setup-envtest use 1.31.0 --bin-dir bin -p path` (repo-pinned; downloads on first use).
2. Polarity: every test is a contract test — green means fixed. No canaries.
3. Harness-bites: graft onto base `7894133d` and confirm the UpdatedReplicas tests go red.

### Kickoff prompt for a fresh agent
```text
Continue a verification task on branch verify/pr-533-updatedreplicas-status-codex
(remote reviewer-fork = https://github.com/cheyang/rbg.git). Background: review of
https://github.com/sgl-project/rbg/pull/533 found the premise (stale status.roleStatuses[]
.updatedReplicas) confirmed on base 7894133d and the fix verified at head 4ab4a5ee.
Read docs/verification/pr-533-updatedreplicas-status/README.md ("Continuing after the fix"),
run scripts/re-verify.sh from the repo root of that branch, and report the
observed-vs-expected table. All tests are contract polarity.
```
