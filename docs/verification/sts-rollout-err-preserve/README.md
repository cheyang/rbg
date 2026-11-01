# sts-rollout-err-preserve — bug verification

Reproducible evidence for the findings raised while reviewing
https://github.com/sgl-project/rbg/pull/540 (fixes #539).

Layers run against the **code under review** (`cf4bff4c`, PR #540 head; base
`4a0765f` for the premise check):

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit | `rollingUpdateParameters` / `reconcileStatefulSet` error propagation with a fake client + list-denying interceptors | `go test -mod=vendor ./pkg/reconciler -run 'TestRollingUpdateParameters_\|TestReconcileStatefulSet_NoScaleToZero' -count=1 -v` |
| 2. Integration | `reconcileStatefulSet` against a real local Kubernetes API server (controller-runtime envtest), Pod List denied mid-rollout | `KUBEBUILDER_ASSETS=<dir> go test -mod=vendor -tags verification_envtest ./pkg/reconciler -run '^TestEnvtest_ReconcileStatefulSet_NoScaleToZeroOnPodListError$' -count=1 -v` |
| 3. Live | real cluster | not run this round (debate setup limits stage ② to unit + integration) |

> Test polarity: **contract** tests (assert intended behavior) FAIL on buggy
> code / PASS when fixed. **Bug-canary** tests (assert current behavior) PASS
> now / FLIP to red when fixed. Every test below is a contract test except
> `TestRollingUpdateParameters_PanicOnShrunkLiveSts_CANARY`.

Harness files (additive only — production code untouched on this branch):
- `pkg/reconciler/sts_rollout_error_preserve_test.go` (L1, no build tag, CI-safe)
- `pkg/reconciler/sts_rollout_error_preserve_envtest_test.go` (L2, build tag
  `verification_envtest` so default CI does not need envtest assets)

## Problem premise (P0)

Run against the **base** branch (`4a0765f`) without the patch.

| | |
|---|---|
| Claimed symptom | "A failed Pod List request during reconciliation of a StatefulSet-backed role can cause the controller to silently apply `spec.replicas: 0`, even though the role still requests four replicas." (issue #539) |
| Linked issue | #539, OPEN, created 2026-11-01 (1 day before this PR) — still valid, no commits to `pkg/reconciler/sts_reconciler.go` since |
| Reported component | `StatefulSetReconciler.rollingUpdateParameters` defer, `pkg/reconciler/sts_reconciler.go:204-221` |
| Patched component | same function, same file — exact match |
| Component match | Yes |
| **Verdict** | **Confirmed** — reproduced on base at both layers |
| Evidence | envtest on base: 2nd reconcile returned `nil` and the real API server stored `spec.replicas: 0` (expected: error returned, replicas left at 5); unit layer on base: `rollingUpdateParameters` returned `(0, 0, nil)` on a denied Pod List. `results/integration/base-verbose.txt`, `results/unit/base-verbose.txt` |

Additional blast-radius evidence found while verifying (base only): after the
bug zeroes the StatefulSet, the **next** reconcile (after the outage recovers)
panics in `rollingUpdatePartition` with `index out of range` (states slice is
sized by the now-zero live replica count while `rollingStep` goes negative).
controller-runtime v0.22.1 recovers reconcile panics by default, so in
production this is a permanent requeue/panic loop: the RoleBasedGroup never
converges. The PR's fix removes the route into this state; see finding F8 for
the residual pre-existing panic reachable by other means.

## Summary of results

Observed on head `cf4bff4c` (fixed) vs base `4a0765f` (buggy):

| ID | Claim | Layer | Base | Head | Verdict |
|----|-------|-------|------|------|---------|
| P0 | Pod List failure mid-rollout scales the live STS to 0 on base | 1+2 | FAIL (replicas=0, err=nil) | PASS | **Confirmed** |
| F1 | Pod List error is preserved (not swallowed by the defer) | 1 | FAIL (err=nil) | PASS | **Confirmed fixed** |
| F2 | ControllerRevision List error is preserved | 1 | FAIL (err=nil) | PASS | **Confirmed fixed** |
| F3 | Missing ControllerRevision error is preserved | 1 | FAIL (err=nil) | PASS | **Confirmed fixed** |
| F4 | Malformed/missing role-size annotation error is preserved | 1 | FAIL (err=nil) | PASS | **Confirmed fixed** |
| F5 | Partition conversion error propagates via `partitionErr` | 1 | PASS | PASS | Guard (unchanged path, both green by design) |
| F6 | `reconcileStatefulSet` returns the error and does not write replicas (fake client) | 1 | FAIL (replicas 5→0) | PASS | **Confirmed fixed** |
| F7 | Same contract against a real API server (envtest) | 2 | FAIL (replicas 5→0 stored) | PASS | **Confirmed fixed** |
| F8 | Pre-existing: `rollingUpdatePartition` panics when live STS replicas < role replicas − maxUnavailable − ready (canary) | 1 | PASS (panics) | PASS (panics) | **Pre-existing, not addressed by this PR** |

## Per-finding detail

### P0 / F6 / F7 — scale-to-zero on swallowed read error (the PR's target)

Mechanism (base): `rollingUpdateParameters` has named return `err`; its defer
assigned the partition-conversion result directly to that named return, so a
successful conversion nil'd out an error the body had returned. The caller then
applied `(partition=0, replicas=0)` via server-side apply.

Observed (base, envtest): `second reconcile error: <nil>`, `replicas after
second reconcile: 0`. Observed (head): error returned, replicas stay 5, and the
post-recovery reconcile converges again. Artifacts:
`results/integration/{base,head}-verbose.txt`.

### F1–F4 — the other `return 0, 0, err` paths

The issue listed ControllerRevision List errors, a missing ControllerRevision,
and a malformed/missing role-size annotation as additional paths reaching the
same defer. All three confirmed on base (err nil'd), all fixed on head. The
fix's `if err != nil { return }` guard preserves every body error uniformly, so
no path was missed. Artifacts: `results/unit/{base,head}-verbose.txt`.

### F5 — `partitionErr` branch

Behavior when the body succeeds and the deferred partition conversion fails:
error propagates. Identical on base and head (on base the old code assigned the
conversion error to the same named return). Kept as a regression guard while
the defer is edited.

### F8 — pre-existing latent panic (NOT introduced by this PR)

`rollingUpdatePartition` (pkg/reconciler/sts_reconciler.go:398-421) computes
`rollingStepPartition` from live STS state and then indexes `states[idx]` for
`idx < rollingStepPartition`, but `states` is sized by `*sts.Spec.Replicas`.
When the live replica count is more than `maxUnavailable` +
continuously-ready replicas below `role.Replicas` (externally shrunk workload,
or a partially-terminated STS), `rollingStep` goes negative and
`rollingStepPartition` exceeds `len(states)` → `index out of range` panic.
Reproduced on both base and head with a shrunk live STS (role wants 4, live 1,
maxUnavailable 1). controller-runtime v0.22.1 recovers panics (RecoverPanic
defaults to true), so the user-visible effect is a RoleBasedGroup that never
reconciles. This is pre-existing (`introducedByPR: false`) and does not gate
this PR, but it is adjacent to the fixed path: the PR removes the main route
into it (the scale-to-zero state). Canary:
`TestRollingUpdateParameters_PanicOnShrunkLiveSts_CANARY`.

## Proposed fixes (NOT applied to production here)

- **F8 (pre-existing, follow-up issue suggested)**: clamp the
  `rollingStepPartition` loop with `min(rollingStepPartition, len(states))`, or
  size `states` by `max(stsReplicas, roleReplicas)`, or return an error when
  the live replica count is inconsistent with the role's. A tracking issue
  should be filed separately from this PR.

## Harness-bites check

Proven by the base runs: every contract test that the fix is supposed to
satisfy goes red on the un-fixed code (`results/*/base-verbose.txt`) and green
on the PR head (`results/*/head-verbose.txt`). No test was trusted that cannot
fail for the right reason.

## Continuing after the fix (possibly on another machine)

The harness is on branch `verify/sts-rollout-err-preserve-claude` (production
code untouched), so it grafts onto whatever the fixed code is.

1. Get it onto the fixed code:
   ```bash
   git fetch https://github.com/cheyang/rbg.git verify/sts-rollout-err-preserve-claude
   git checkout <fixed-branch>
   git checkout FETCH_HEAD -- docs/verification/sts-rollout-err-preserve \
     pkg/reconciler/sts_rollout_error_preserve_test.go \
     pkg/reconciler/sts_rollout_error_preserve_envtest_test.go
   ```
2. Prereqs: Layer 1 = Go toolchain + vendored deps only; Layer 2 =
   `KUBEBUILDER_ASSETS` pointing at envtest binaries (kube-apiserver + etcd,
   e.g. via `setup-envtest use 1.31.0`); Layer 3 = a real cluster (not used).
3. Re-run:
   ```bash
   bash docs/verification/sts-rollout-err-preserve/scripts/re-verify.sh   # no arg = current PR head
   ```
   or the two `go test` commands from the layer table.
4. Read results via the polarity table: F1–F7 (contract) should be GREEN;
   F8 (canary) stays green while the pre-existing panic remains — when it
   flips red, the latent panic was fixed and the canary must be inverted into
   a contract test.
5. Harness-bites: re-run Layer 1 against the merge-base to confirm it still
   goes red there.

### Kickoff prompt for a fresh agent

```text
Continue a verification task on branch verify/sts-rollout-err-preserve-claude
(fork cheyang/rbg). Background: a review of sgl-project/rbg PR #540 produced
findings P0, F1-F8; a harness reproduced them (docs/verification/sts-rollout-err-preserve).
The PR may have new commits. Read docs/verification/sts-rollout-err-preserve/README.md
("Continuing after the fix"), run scripts/re-verify.sh (it auto-resolves the PR head
from the manifest), review the last-reviewed..head delta, and report an
observed-vs-expected table per finding. Mind polarity: F8 is a canary. Clean up
scoped test resources; do not run cluster-wide destructive actions; never push
to upstream or the PR author's fork.
```
