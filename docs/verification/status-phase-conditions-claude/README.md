# status-phase-conditions-claude — bug verification

Reproducible evidence for the findings raised while reviewing
https://github.com/sgl-project/rbg/pull/510 (head `42e4479ea0b67840f1dd8d011c6b194ecf15d762`,
base `a0b11e6ab95ca6dfcfdf6fca713352c8482ebd41`).

Harness branch: `verify/status-phase-conditions-claude` (based on the PR head; production code
untouched — the diff is only test files plus this docs tree).

Layers run against the **code under review** (`42e4479e`):

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit | `roleWorkloadStatus` (RBG role aggregation), stateless-mode status updater | `go test ./internal/controller/workloads/ ./pkg/reconciler/roleinstanceset/statelessmode/ -run 'TestClaude' -v` |
| 2. Lint (differential) | golangci-lint v2.13.1 with the repo's `.golangci.yml` | `docs/verification/status-phase-conditions-claude/scripts/20-lint-diff.sh` |
| 3. Live (E2E kind) | **not run here** — no cluster in the review sandbox; CI evidence used instead | see "Live run notes" |

> Test polarity: contract tests (assert intended behavior) FAIL on buggy code / PASS when
> fixed. There are no bug-canaries in this harness.

## Problem premise (P0)

Answered before the findings, and run against the **base** branch without the patch.

| | |
|---|---|
| Claimed symptom | issue #502 (OPEN, 2026-10-19): "There is no queryable lifecycle phase … `Progressing` and `RollingUpdateInProgress` are declared but never written … `Ready` is based mainly on replica counts and does not require revision convergence … `FailedScale` and `FailedUpdate` … persist reconcile errors as conditions" |
| Linked issue | #502, OPEN, referenced from the PR body ("Partially addresses #502") |
| Reported component | RBG / RoleInstanceSet / RoleInstance / RoleBasedGroupWarmup status reporting |
| Patched component | exactly those: `internal/controller/workloads/*`, `pkg/reconciler/roleinstance{,set}/**`, `pkg/inplace/instance/**`, `api/workloads/**` |
| Component match | Yes |
| **Verdict** | **Confirmed** (design gap confirmed by code inspection on base; not a runtime symptom) |
| Evidence | On base `a0b11e6`: `RoleBasedGroupProgressing` is only an API constant, never written by any controller; no `status.Phase` field on RBG/RoleInstanceSet; RBG `Ready` only compared `role.Replicas/rs.Replicas/rs.ReadyReplicas`. See `results/premise-base.txt`. |

## Summary of results

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| F-lws-ready | A converged LeaderWorkerSet child never makes the owning RBG role ready | 1 | **Confirmed** | `TestClaudeRoleWorkloadStatusLWSReady` FAILS on head; PASSES with the one-line `fresh` fix; `results/f-lws-ready-head.txt` |
| F-lint-gocyclo | The PR's new functions trip the repo's gocyclo>30 gate (CI `lint` FAILURE) | 2 | **Confirmed** | 3 issues on head, 0 on base; `results/lint-head.txt`, `results/lint-base.txt` |
| F-no-churn (negative control) | RIS Ready/Progressing transition times must be preserved and no-op status writes skipped | 1 | **Confirmed healthy** (concern dismissed) | `TestClaudeStatelessNoChurnOnSecondReconcile` PASSES; `results/f-no-churn-head.txt` |

## Per-finding detail

### F-lws-ready (blocker)

`internal/controller/workloads/rolebasedgroup_controller.go:939` (LWS branch of
`roleWorkloadStatus`):

```go
available := apimeta.FindStatusCondition(s.Conditions, string(lwsv1.LeaderWorkerSetAvailable))
fresh = available != nil && available.ObservedGeneration >= child.Generation
```

LWS v0.7.0 (`sigs.k8s.io/lws@v0.7.0`) has **no `status.observedGeneration` field** and its
condition writer `makeCondition()` (pkg/controllers/leaderworkerset_controller.go:779) sets only
Type/Status/Reason/Message/LastTransitionTime — never `ObservedGeneration`. So
`available.ObservedGeneration` is always 0 and `fresh` is `0 >= child.Generation` = false for
every LWS child with Generation ≥ 1. `ready := fresh && converged && …` is therefore
permanently false → RBG `Ready=False Reason=WaitingForInstances` forever for **any RBG with a
LeaderWorkerSet role**.

Unit reproduction (this harness):
- `TestClaudeRoleWorkloadStatusLWSReady` — converged LWS child (Available=True/AllGroupsReady,
  Replicas 1/1, UpdatedReplicas 1, revision label matching, owned by the RBG) → observed
  `ready=false`, expected `ready=true`. **FAILS on head.**
- `TestClaudeRoleWorkloadStatusLWSObservedGenerationSet` — same fixture with
  `ObservedGeneration=1` → `ready=true`. **PASSES.** Isolates the ObservedGeneration gate as the
  only blocking term.
- `TestClaudeRoleWorkloadStatusDeploymentReady` — Deployment control (status.observedGeneration
  is populated for Deployments) → `ready=true`. **PASSES.** Rules out test-setup error.

Harness-bites check: applying the proposed fix
(`fresh = available != nil && (available.ObservedGeneration == 0 || available.ObservedGeneration >= child.Generation)`)
makes `TestClaudeRoleWorkloadStatusLWSReady` green; the fix was then reverted
(`git checkout -- internal/controller/workloads/rolebasedgroup_controller.go`), production diff
confirmed empty.

Cluster-level corroboration (CI, not run locally): on head, E2E jobs `lifecycle`, `workload`,
`rollout`, `feature`, `v1alpha1`, `Manifest installation` all FAIL at
`test/e2e/framework/rbg_v2_expect.go:86` (RBG Ready wait); the same jobs are green on base
`a0b11e6`. Every failing spec contains an LWS role, and the dumped diagnostics show converged
children (e.g. `[LWS] Status: Replicas=1, ReadyReplicas=1, UpdatedReplicas=1 … Available=True`)
while the RBG stays `Ready=False Reason=WaitingForInstances`. Raw log excerpts:
`results/e2e-evidence.md`.

### F-lint-gocyclo (blocker — required CI check)

`golangci-lint run` (v2.13.1, repo `.golangci.yml`) reports on head:
- `updateRBGStatus` cyclomatic complexity 33 (> 30) — rolebasedgroup_controller.go:774
- `roleWorkloadStatus` cyclomatic complexity 57 (> 30) — rolebasedgroup_controller.go:868
- `SetStatusConditions` cyclomatic complexity 45 (> 30) — pkg/reconciler/roleinstanceset/status.go:33

Base `a0b11e6`: 0 issues. This matches the CI `Project Check / lint` FAILURE on the head and
SUCCESS on base. Fixes are mechanical (split per-workload-type helpers / extract condition
write helpers).

### F-no-churn (reviewer concern, dismissed with evidence)

During review I suspected the new `MergeConditions`/`setCondition` path resets
`LastTransitionTime` on every reconcile (which would defeat the no-op status skip and cause
status-write churn). The harness disproves it: at the **real call shape** (fresh `newStatus`,
as `syncInstanceSet` builds), `TestClaudeStatelessNoChurnOnSecondReconcile` shows a second
reconcile performs **0 status writes** and both Ready/Progressing keep their first-write
transition times. `MergeConditions` keeps the live Ready/Progressing entries in `out`, and
`putCondition` preserves `LastTransitionTime` when the status is unchanged. Recording this so
the cross-review doesn't re-derive the same false positive.

## Live run notes

No cluster was available in the review sandbox (the debate setup also skips the live layer).
Instead, the head-vs-base CI differential plus the failing jobs' diagnostic dumps are recorded
in `results/e2e-evidence.md`, including the stuck RBG status lines that pin the LWS root cause.

## Proposed fixes (NOT applied to production here)

- **F-lws-ready**: don't gate LWS freshness on a field LWS never writes. Either
  `fresh = available != nil` (rely on `converged`/replica terms), or tolerate a zero
  ObservedGeneration as shown in the harness-bites check. Follow-up: add unit coverage for the
  Deployment/StatefulSet/LWS branches of `roleWorkloadStatus` (the PR only unit-tests the
  RoleInstanceSet branch).
- **F-lint-gocyclo**: split `roleWorkloadStatus` per workload type; extract the paused/reason
  override tail of `SetStatusConditions` into a helper; split the reason-resolution block of
  `updateRBGStatus`.

## Continuing after the fix (possibly on another machine)

1. Get the harness onto the fixed code:
   ```bash
   git fetch https://github.com/cheyang/rbg.git verify/status-phase-conditions-claude
   git checkout <fixed-branch>
   git checkout FETCH_HEAD -- docs/verification/status-phase-conditions-claude \
     internal/controller/workloads/rolebasedgroup_lws_status_claude_test.go \
     pkg/reconciler/roleinstanceset/statelessmode/status_churn_claude_test.go
   ```
   or run `docs/verification/status-phase-conditions-claude/scripts/re-verify.sh` with no
   argument — it fetches the current PR head, grafts the harness, and runs the layers.
2. Prereqs: Go toolchain (repo's go.mod version) plus `golangci-lint` v2.13.1 for the lint
   layer (`scripts/20-lint-diff.sh` locates it at
   `/home/ecs-user/.pr-debate/homes/claude/tools/golangci-lint` or `$(go env GOPATH)/bin`).
3. Re-run: `bash scripts/re-verify.sh` (or `[<fixed-ref>]`).
4. Read results: `TestClaudeRoleWorkloadStatusLWSReady` must be GREEN (contract). The other
   two Claude tests must stay GREEN (regression guards).
5. Harness-bites: already performed this round (fix applied → green → reverted).

### Kickoff prompt for a fresh agent
```text
Continue a verification task on branch verify/status-phase-conditions-claude of
https://github.com/cheyang/rbg.git (harness for sgl-project/rbg PR #510). Background: the
review found RBG Ready never becomes True for LeaderWorkerSet roles because
roleWorkloadStatus gates freshness on available.ObservedGeneration, which LWS v0.7.0 never
sets; plus a gocyclo lint gate failure. Read
docs/verification/status-phase-conditions-claude/README.md ("Continuing after the fix") and
follow it: graft the harness onto the current PR head (or the fixed ref given), run
scripts/re-verify.sh, and report Fixed / Still-broken / Partial per finding. All tests are
contract tests — green means fixed. No cluster needed.
```
