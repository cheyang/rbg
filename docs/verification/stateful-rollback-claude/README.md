# Verification — stateful-rollback-claude (PR #508, issue #482)

Reviewer A (Claude) verification harness for https://github.com/sgl-project/rbg/pull/508
(`fix: persist stateful rollback replacement state`, fixes #482).

- PR head verified: `a1d164a95d146bc209d73627f3a14cdb877b83d4`
- Base: `ecdfd6e601de64adc0d0892c7ad05f75e85873a8` (merge-base with main)
- Harness branch: `verify/stateful-rollback-claude` (this branch; production code untouched)

## Premise (P0) — CONFIRMED on base

Claim under test (issue #482): early-rollback replacement state lives in
controller process memory; after a restart mid-replacement the topology treats
the rollout as complete and condemns the existing surge while the replacement
base is still unavailable.

Evidence (`results/premise-base.out`, from `scripts/run-premise-base.sh`):
on the base branch, `recordEarlyRollbackReplacementSignal` keeps the surge in
range while the signal is alive; after simulating a restart (the process-local
signal map is the only carrier of that state — clearing it is exactly what a
process restart does), `computeTopologyForReconcile` returns `inRollout=false,
endOrdinal=1, activeSurge=0`, so the only Ready instance (the surge) falls out
of the managed range and is condemned while the replacement base is unready —
an availability violation with `maxUnavailable: 0`. The PR's premise is valid
and the affected component matches the patched files.

The premise test uses base-only symbols (removed by the PR), so it is compiled
with the `rbg_premise_base` build tag inside a throwaway worktree of the base
sha. It is never compiled into normal head runs.

## Observed vs expected

| id | claim | layer | polarity | verdict | evidence |
|----|-------|-------|----------|---------|----------|
| P0 | premise symptom on base | unit (base) | canary (documents the bug) | CONFIRMED | results/premise-base.out |
| C1 | persisted state protects surge after restart | unit | contract | PASS | results/unit-head.out |
| C2 | protection released once scope converged | unit | contract | PASS | results/unit-head.out |
| C3 | superseded revision invalidates state | unit | contract | PASS | results/unit-head.out |
| C4 | scale-down overlap semantics | unit | contract | PASS | results/unit-head.out |
| C5 | intent persisted before base deletion | unit | contract | PASS | results/unit-head.out |
| I1 | envtest: recovery from persisted API state | integration | contract | PASS | results/envtest-head.out |
| I2 | envtest: full A→B→A rollback with partition drop | integration | contract | PASS | results/envtest-head.out |

Full sweep also re-run by the reviewer on the PR head, all green:
`go test -mod=vendor -race -count=1 ./pkg/reconciler/roleinstanceset/... ./api/workloads/...`
and `go test -mod=vendor ./test/envtest/testcase/stateful/...` (with
`KUBEBUILDER_ASSETS` pointing at local envtest binaries, k8s 1.37.0).

## Harness bites check

Two temporary sabotages of production code, both reverted afterwards
(`git diff` on production files confirmed empty):

1. `rolloutReplacementStateActive` forced to return `false` →
   `TestClaudeRestartSurgeProtectedByPersistedState` and
   `TestClaudeScaleDownOverlapRetainsFormerBaseAsSurge` FAIL.
2. The persist call in `applyTargetUpdate` moved to AFTER the base deletion →
   `TestClaudePersistIntentPrecedesBaseDeletion` FAIL
   ("replacement intent must be persisted before deleting the stale base").

## How to re-run

From a checkout of this branch:

```bash
# unit layer (head)
go test -mod=vendor -count=1 -run 'TestClaude' -v ./pkg/reconciler/roleinstanceset/statefulmode/
# premise against the frozen base
bash docs/verification/stateful-rollback-claude/scripts/run-premise-base.sh
# integration layer (needs envtest binaries)
KUBEBUILDER_ASSETS=<envtest-bin-dir> go test -mod=vendor ./test/envtest/testcase/stateful/... -count=1 -timeout 15m
# everything at once against the current PR head
bash docs/verification/stateful-rollback-claude/scripts/re-verify.sh
```

`re-verify.sh` resolves the current PR head from the manifest's `pr` URL,
grafts this harness onto it, runs the unit + integration layers, and prints
per-finding Fixed / Still-broken / Partial / Harness-update (contract tests
must pass; canaries must flip).

## Continuing after the fix / next round

The harness is the PR's regression suite: if the author pushes new commits,
run `bash scripts/re-verify.sh` (no args) from this branch. Interpretation:
C1–C5, I1–I2 are contract tests — "FIXED" means they pass; a failure means the
protection regressed. P0's base-only canary is unaffected by head changes.
If a finding comes back `Harness-update`, the code shape changed — adjust the
test, then re-run.

Polarity table:

| test | on buggy code | on fixed code |
|------|---------------|----------------|
| C1–C5, I1–I2 (contract) | FAIL | PASS |
| P0 base canary | PASS (documents the base bug) | n/a (base ref is frozen) |

Kickoff prompt for a fresh agent: "You are resuming the review verification of
https://github.com/sgl-project/rbg/pull/508. Checkout branch
verify/stateful-rollback-claude, read docs/verification/stateful-rollback-claude/README.md,
run scripts/re-verify.sh, report the per-finding table, then review the
last-reviewed..head delta and advance .last-reviewed."

## Notes and residual risks (not regressions of this PR)

- The set-cache wait (`rolloutStatusExpectations`) is process-local; a
  set-informer lag that spans a leader handover is not covered (acknowledged
  in the PR description).
- With an outdated CRD and `maxSurge > 0`, early-rollback replacement stalls
  with a Warning event until the CRD is upgraded (detected via a post-patch
  deep-equal check; see `PersistRolloutReplacementState`).

## Audit round (2026-10-22) — re-attestation

Round mode `audit`: the PR head is unchanged (`a1d164a`), base unchanged, empty
review delta `a1d164a..a1d164a`; the round exists because the previous round's
Codex cross-review timed out (phase1 rc=124), not because the code moved. No
author commits, comments, or CI changes since the initial round (PR discussion
re-checked via `gh` this round).

All evidence below was **re-executed** this round (not copied from the initial
round's outputs):

- `scripts/run-premise-base.sh` → premise re-CONFIRMED on base `ecdfd6e6`
  (`results/premise-base.out`, 2026-10-22).
- `scripts/re-verify.sh` (no ref; auto-resolved PR head via
  `git fetch ... pull/508/head`) → C1–C5, I1, I2 all FIXED/pass, exit 0
  (`results/reverify/`, 2026-10-22; integration layer ran the real controller
  against a real API server, 35s).
- Full race sweep on the head:
  `go test -mod=vendor -race -count=1 ./api/workloads/...
  ./pkg/reconciler/roleinstanceset/... ./internal/controller/workloads/...`
  → all green (`results/race-sweep-audit.out`).

`.last-reviewed` stays at `a1d164a` (nothing to advance — delta empty).
