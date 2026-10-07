# stateful-rollout-stall-recovery — verification of PR sgl-project/rbg#484

Reproducible evidence for the review of <https://github.com/sgl-project/rbg/pull/484>
("fix: recover stateful rollout stalls"), head `90dd9068`, base `9b22f4a8`.

Layers run:

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit | full reconcile path `updateStatefulInstanceSet` with fake object manager (premise + fix behavior) | `go test -mod=vendor -count=1 -run 'TestP0Codex' -v ./pkg/reconciler/roleinstanceset/statefulmode/` |
| 1b. Regression | whole `roleinstanceset` tree + `requeueduration`, incl. `-race` | `go test -mod=vendor -race -count=1 ./pkg/reconciler/roleinstanceset/... ./pkg/utils/requeueduration` |
| 2. Integration | existing envtest suites (real API server) against PR head | `KUBEBUILDER_ASSETS=<envtest 1.31.0> go test -mod=vendor -count=1 ./test/envtest/testcase/... -timeout 20m` |
| 3. Live | skipped this round (see manifest `liveNote`) | — |

Polarity: all harness tests are **contract** tests — RED on the base branch (premise), GREEN on
the PR head (fix). The base run doubles as the harness-bites proof: the tests fail on unfixed
code for the expected reason and pass on the patched code with production code untouched.

## Problem premise (P0)

| | |
|---|---|
| Claimed symptom | "A Stateful RoleInstanceSet rollout may stop progressing when its Pod template is updated again while the Pods from the previous update are still NotReady. The rollout does not automatically resume unless another event triggers reconciliation." (issue #464) + PR-stated gaps after #470: OrderedReady stops before the retry path; early A→B→A rollback skipped when revision names match; OrderedReady cannot recycle an in-range stale-rev surge instance |
| Linked issue | #464 OPEN (2026-09-12, still valid); #482 OPEN — the process-local-signal restart gap, explicitly **out of scope** here |
| Reported component | Stateful RoleInstanceSet controller |
| Patched component | `pkg/reconciler/roleinstanceset/statefulmode` |
| Component match | Yes |
| **Verdict** | **Confirmed** — every claimed stall reproduced on the base branch |
| Evidence | `results/unit-p0-base.txt` (4 RED) vs `results/unit-p0-pr-head.txt` (4 GREEN) |

Base-branch observations (expected-vs-observed):

- **P0a OrderedReady**: first update target unhealthy → reconcile schedules **no** requeue
  (`durationStore.Pop == 0`); after the 10s window the target is still never replaced.
  Rollout waits for an unrelated event — exactly the #464 symptom.
- **P0b early A→B→A**: CurrentRevision == UpdateRevision name with base instances at B →
  `inRollout=false`, stale instances never deleted; the rollback is silently skipped.
- **P0c Parallel**: unhealthy non-target at updateRev consumes maxUnavailable=1, the healthy
  target is budget-blocked, and **no** requeue is scheduled.
- **P0d surge bridge**: with the stale B base present and a ready A surge, base code condemns
  the **surge** (`deleted [test-set-1]`) and keeps the stale base — the availability-worst
  ordering.

## Summary of results (PR head `90dd9068`)

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| P0a | OrderedReady stalled target: retry scheduled at window end, replaced after expiry, later ordinals untouched | 1 | Fix confirmed | `results/unit-p0-pr-head.txt` |
| P0b | Early A→B→A same-name rollback resumes; both stale base instances replaced | 1 | Fix confirmed | same |
| P0c | Budget-blocked parallel rollout schedules a requeue (≤10s); budget not relaxed (no wrongful delete) | 1 | Fix confirmed | same |
| P0d | Surge retained across terminating→missing→pending-replacement gap, condemned once replacement Ready | 1 | Fix confirmed | same |
| REG | Full package + race sweep | 1b | Pass | `results/unit-full-pr-head.txt`, `results/unit-race-pr-head.txt` |
| REG | Existing envtest suites (rbg / restart_policy / webhook) | 2 | Pass (48s / 202s / 28s) | `results/envtest-pr-head.txt` |

## Notes

- The harness file `premise_p0_codex_test.go` intentionally references only symbols that exist
  on the base branch so the same file grafts onto both refs. It therefore cannot call
  `resetEarlyRollbackReplacementUIDs()` (PR-only symbol); the residue is proven harmless by the
  full-package green run (P0* sorts before `stateful_*` test files, and every signal-sensitive
  PR test resets the map first).
- The PR's own tests `TestUpdateStatefulInstanceSetOrderedReadyRetriesUnhealthyTarget`,
  `TestUpdateStatefulInstanceSetResumesEarlyRollback`, `TestOrderedReadyRetriesUnhealthyStaleSurge`
  store into the global `earlyRollbackReplacementUIDs` without cleanup (review finding F1).

## Continuing after the fix (possibly on another machine)

Harness branch: `verify/stateful-rollout-stall-recovery-codex` on the reviewer's fork
(`https://github.com/cheyang/rbg.git`). Production code untouched (diff = harness only).

```bash
git fetch https://github.com/cheyang/rbg.git verify/stateful-rollout-stall-recovery-codex
git checkout verify/stateful-rollout-stall-recovery-codex
bash docs/verification/stateful-rollout-stall-recovery/scripts/re-verify.sh   # auto-fetches PR head
```

All four tests are contract tests: they must stay GREEN on any further PR revision. A flip to
red means the recovery behavior regressed. `.last-reviewed` records `90dd9068`; the script
prints the incremental review range.

Kickoff prompt for a fresh agent: "Check out
verify/stateful-rollout-stall-recovery-codex from https://github.com/cheyang/rbg.git and run
bash docs/verification/stateful-rollout-stall-recovery/scripts/re-verify.sh for PR
https://github.com/sgl-project/rbg/pull/484; then review the delta printed by the script."
