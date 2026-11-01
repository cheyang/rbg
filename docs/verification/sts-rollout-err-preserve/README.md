# sts-rollout-err-preserve — bug verification

Reproducible evidence for the review of https://github.com/sgl-project/rbg/pull/540
(`fix(sts): preserve rollout errors to prevent scale-to-zero`, fixes issue #539).

Layers run against the **code under review** (head `cf4bff4c`, base `4a0765f7`):

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit | `rollingUpdateParameters` error propagation with injected List failures (fake client + `interceptor.Funcs`) | `go test -mod=vendor ./pkg/reconciler -run 'TestVerifyRollingUpdateParametersPreserves' -count=1 -v` |
| 2. Integration | full `reconcileStatefulSet` against a real API server (envtest), Pod List denied | `KUBEBUILDER_ASSETS=$(bin/setup-envtest use 1.31.0 --bin-dir bin -p path) go test -mod=vendor -tags=envtest_verify ./pkg/reconciler -run '^TestVerifyStatefulSetNotScaledToZeroOnPodListError$' -count=1 -v -timeout=180s` |
| 3. Live | skipped per review instructions (no KUBECONFIG layer this round) | — |

> Test polarity: all tests are CONTRACT tests (assert intended behavior). They FAIL on the
> buggy base and PASS on the fixed head. No bug-canaries in this harness.

## Problem premise (P0)

| | |
|---|---|
| Claimed symptom | "A failed Pod List request during reconciliation of a StatefulSet-backed role can cause the controller to silently apply `spec.replicas: 0`, even though the role still requests four replicas." (issue #539) |
| Linked issue | #539, OPEN, filed 2026-11-01 against main@4a0765f7 — still valid |
| Reported component | `pkg/reconciler/sts_reconciler.go` — `rollingUpdateParameters` deferred partition conversion overwriting named return `err` |
| Patched component | same file, same defer — exact match |
| Component match | Yes |
| **Verdict** | **Confirmed** |
| Evidence | On base `4a0765f7`: second reconcile returns `<nil>` and the real (envtest) API server stores `spec.replicas: 0` (was 5). On head `cf4bff4c`: error `pods is forbidden` propagates, replicas stay 5. See `results/base-envtest.out`, `results/head-envtest.out`. |

## Summary of results

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| B1 | Pod List / ControllerRevision List failures during an in-progress rollout are swallowed by the defer and surface as `(0, 0, nil)` from `rollingUpdateParameters` | 1 | Confirmed on base (both tests FAIL with `replicas=0, err=nil`); fixed on head (PASS, error propagates) | `results/base-unit.out`, `results/head-unit.out` |
| B2 | The swallowed error makes `reconcileStatefulSet` apply `spec.replicas=0` to the live StatefulSet | 2 | Confirmed on base (replicas 5→0, err nil); fixed on head (err `pods is forbidden`, replicas stay 5) | `results/base-envtest.out`, `results/head-envtest.out` |

## Per-finding detail

### B1 — deferred partition conversion overwrites the named return `err`

Mechanism: `rollingUpdateParameters` has named returns `(stsPartition, replicas, err)`. On the
base, the deferred closure assigns `partition, err = intstr.GetScaledValueFromIntOrPercent(...)`.
Any error return in the body (Pod List failure, ControllerRevision List failure, missing
revision, bad role-size annotation) is overwritten with `nil` when the conversion succeeds,
which it does for a valid `Partition` — so the caller sees success with `replicas=0`.

- Unit run on base: both tests fail, reporting `(partition=0, replicas=0, err=nil)`.
- Unit run on head: both PASS, `err` is the injected Forbidden error.

### B2 — end-to-end: live StatefulSet scaled to zero

Mechanism: `reconcileStatefulSet` treats the nil error as success and applies the returned
zero replicas via server-side apply.

- envtest on base: `second reconcile error: <nil>`, `replicas after second reconcile: 0`.
- envtest on head: `second reconcile error: pods is forbidden`, `replicas after second reconcile: 5`.

Envtest runs no native StatefulSet controller, so pod deletion is not exercised; the persisted
zero-replica write is the verified boundary (same scope as the issue's reproducer).

### Harness-bites check

The base-vs-head differential *is* the bites check: identical test files fail on base
(`4a0765f7`, run in a worktree) and pass on head (`cf4bff4c`). Production code untouched;
`git diff` of the harness branch vs the PR head contains only the two additive test files and
this docs tree.

### Regression sweep

`go test -mod=vendor ./pkg/reconciler/... -count=1` on head: all packages ok
(`results/head-package-regression.out`), including the pre-existing
`TestStatefulSetReconciler_rollingUpdateParameters` table.

## Proposed fixes (NOT applied to production here)

The PR's fix (guard `if err != nil { return }` at defer entry; separate `partitionErr`
variable) is verified correct. Remaining gap: the PR adds no regression test — the two
contract tests here are exactly the coverage issue #539's "Suggested fix" section asked for
("Add regression coverage asserting both error propagation and no StatefulSet write on failed
reads") and can be uplifted as-is.

## Continuing after the fix (possibly on another machine)

The harness is on branch `verify/sts-rollout-err-preserve-codex` (production code untouched).

1. Fetch and re-verify against any ref (defaults to the current PR head):
   ```bash
   git fetch https://github.com/cheyang/rbg.git verify/sts-rollout-err-preserve-codex
   git checkout verify/sts-rollout-err-preserve-codex
   bash docs/verification/sts-rollout-err-preserve/scripts/re-verify.sh            # current PR head
   bash docs/verification/sts-rollout-err-preserve/scripts/re-verify.sh <some-ref> # explicit ref
   ```
2. Prereqs: Go + vendor tree (Layer 1). Layer 2 needs envtest assets: `bin/setup-envtest`
   (repo-local, from `make setup-envtest`) or `setup-envtest` on PATH; k8s 1.31.0 is used.
3. All tests are contract polarity: green = fixed. If a future refactor flips them red, the
   bug is back.
4. Harness-bites: `git worktree add /tmp/rbg-base 4a0765f7 && cp pkg/reconciler/sts_rollout_errpreserve_*_verify_test.go /tmp/rbg-base/pkg/reconciler/ && cd /tmp/rbg-base && <layer commands>` — expect red.

### Kickoff prompt for a fresh agent
```text
Continue a verification task on branch verify/sts-rollout-err-preserve-codex (remote
https://github.com/cheyang/rbg.git). Background: review of sgl-project/rbg PR #540 confirmed
issue #539 (rollingUpdateParameters' defer swallows read errors -> STS scaled to zero) on base
4a0765f7 and verified the fix on head cf4bff4c. Read
docs/verification/sts-rollout-err-preserve/README.md ("Continuing after the fix") and follow
it: run scripts/re-verify.sh to graft the harness onto the current PR head, mind that all
tests are contract polarity, and report an observed-vs-expected table. Layer 2 needs envtest
assets (setup-envtest, k8s 1.31.0). No live/cluster layer this round.
```
