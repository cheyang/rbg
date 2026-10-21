# PR #508 verification — persist stateful rollback replacement state

- PR: https://github.com/sgl-project/rbg/pull/508 (fixes #482)
- Base at review: `main@ecdfd6e6` · Head at round 1: `a1d164a9`
- Reviewer branch: `verify/pr508-rollback-replacement-state` (cheyang fork)

## Premise P0 — Confirmed (base, unit)

Issue #482's mechanism reproduces on base: after a simulated controller
restart mid early-rollback (in-memory `earlyRollbackReplacementSignals` empty,
both revisions resolve to A, stale base already deleted, healthy A surge at
ordinal 1, `replicas=1 maxSurge=1 maxUnavailable=0`), main's reconcile deletes
the surge while the replacement base has only just been created and is not
Ready — the maxUnavailable=0 availability guarantee is broken.

Evidence: `results/premise-p0-base.txt` (repro source:
`results/premise_p0_base_repro_test.go`, run from a detached worktree at
`ecdfd6e6`; the file does not compile on the PR head and is kept here only as
the base-side record).

## Findings

| ID | Severity | Claim | Verdict | Test |
| --- | --- | --- | --- | --- |
| P0 | premise | Restart loses surge protection on base | **Confirmed** (base) | `results/premise-p0-base.txt` |
| V1 | canary | Persisted state retains surge through restart, releases it after the replacement base is Ready, and clears the state | **Confirmed** (head) | `TestVerifyPR508_V1_RestartRetainsSurgeUntilReplacementReady` |
| V2 | contract | Changed target revision ends the recorded scope's protection | **Confirmed** (head) | `TestVerifyPR508_V2_RevisionChangeEndsProtection` |

Full round-1 sweep (unit + envtest, harness included, all green):
`results/full-sweep-round1.txt`. CI on the PR head is 22/22 green.

## Review findings from the code read (not runtime defects)

- `[minor]` No e2e coverage restarts the controller process; envtest seeds
  API-only state instead (the PR body discloses this honestly). A follow-up
  issue for a controller-restart e2e (if feasible without timing injection)
  would close the gap. Unit + envtest coverage is unusually thorough
  (Reconcile-level separate caches, persistence-ordering, stale-RV, pruned-CRD,
  scope overlap, surge retention across reconstruction).
- `[nit]` `Reconcile`'s NotFound path cleans `updateExpectations` but not
  `rolloutStatusExpectations`; it relies on the informer DeleteFunc. A missed
  delete event leaks one UID-keyed map entry (bounded, recreated sets get a new
  UID so the gate is unaffected).
- `[nit]` `persistTerminatingRolloutReplacement` can emit a
  `FailedPersistRolloutReplacementState` Warning event for a set that already
  has a deletion timestamp (updater returns a plain error for a deleting set).
- `[nit]` `inheritedRolloutReplacementState` re-evaluates
  `rolloutReplacementStateActive` with values identical to the
  `computeTopologyForReconcile` call a few lines earlier (topo.partition /
  topo.replicas always equal the spec values there); passing the flag through
  topology would avoid the duplicate scan.

## Re-verify

```bash
bash docs/verification/pr508-rollback-replacement-state/scripts/re-verify.sh
```

Resolves the current PR head from `verify-manifest.json` and the delta start
from `.last-reviewed`.
