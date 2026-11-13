# restart-wave-guard — bug verification

Reproducible evidence for the review of [PR #573](https://github.com/sgl-project/rbg/pull/573)
(fixes [#563](https://github.com/sgl-project/rbg/issues/563): the `Restarting` guard blocked
any further whole-instance recreation until Ready).

Layers run against the **code under review** (`145cbadd` = PR head):

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit | wave-guard scoping, release, persisted fallback, residual limitations | `go test ./pkg/reconciler/roleinstance/sync/ -run 'TestHarness' -v` |
| 2. Integration (envtest) | full controller + real API server: #563 second-recreation scenario | `KUBEBUILDER_ASSETS=$(ls -d ~/.local/share/kubebuilder-envtest/k8s/1.36.2-linux-amd64) go test ./test/envtest/testcase/restart_policy/... -ginkgo.focus 'Restart wave completion' -timeout 600s` |
| 3. Live | not run (no cluster authorized this round) | see `liveNote` in the manifest |

> Test polarity: contract tests (assert intended behavior) FAIL on buggy code / PASS when
> fixed. Bug-canary tests (assert current behavior) PASS now / FLIP to red when fixed.

## Problem premise (P0)

Run against the **base** branch (`ea5e82ae`) without the patch, because the question is
whether the problem exists today.

| | |
|---|---|
| Claimed symptom | "no second recreation; `Restarting` stays True and the instance stays NotReady until the RoleInstance is deleted" (issue #563) |
| Linked issue | #563, OPEN, created 2026-11-11, label `bug` — still valid |
| Reported component | RoleInstance controller restart-policy guard (`shouldRecreateInstanceGuarded` / `isAlreadyRestarting`) |
| Patched component | same files (`pkg/reconciler/roleinstance/sync/instance_scale.go`, `sync/api.go`, `instance_reconciler.go`, `instance_status.go`) |
| Component match | Yes |
| **Verdict** | **Confirmed** |
| Evidence | The PR's own envtest spec, grafted onto base, FAILS after the first recreation: `the completed wave must persist Restarting=False / Expected True to equal False` — the guard never releases pre-Ready. `results/envtest-563-base.txt`. Same spec passes on head: `results/envtest-563-head.txt`. |

## Summary of results

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| FIX-563 | After a completed wave (pre-restart Pods gone), a pre-Ready failure of the rebuilt group triggers a second recreation; `RestartCount` reaches 2 | 2 | Confirmed (contract green on head, red on base) | `results/envtest-563-{head,base}.txt` |
| GUARD-INTACT | The guard still absorbs cascading MemberTerminating triggers while pre-restart Pods exist; controller-restart fallback blocks/releases correctly | 1 | Confirmed | `results/unit-harness-head.txt` |
| W1 | Residual: a pre-restart Pod stuck Terminating (finalizer/dead node) keeps the guard armed, so a failed replacement cannot recreate | 1 | Confirmed (canary — documents current behavior) | `results/unit-harness-head.txt` |
| W2 | Residual: controller-restart fallback treats a same-second Pod as a replacement → early guard release | 1 | Confirmed (canary, acknowledged in code + docs) | `results/unit-harness-head.txt` |

## Per-finding detail

### FIX-563 (the PR's core contract)
Mechanism: the wave marker (`restartingCache` keyed by instance, value = set of pre-restart
Pod UIDs) replaces the old boolean-until-Ready latch. `releaseCompletedRestartWave` runs at
the top of `calculateDiffsWithExpectation`; once no recorded Pod is present, it deletes the
marker and flips `Restarting` to `False`/`RestartCompleted` on both the revision-patched copy
and the fresh API copy, so the same reconcile's `checkRestartBackoff` and
`shouldRecreateInstanceGuarded` observe the release.
Differential: envtest spec red on base (`Restarting=True` persists 60s post-recreation),
green on head (second `ReCreateInstance`, `RestartCount=2`). This also doubles as the
harness-bites check for the contract test.

### GUARD-INTACT (no regression of the guard's original purpose)
H1: a desired member observed Terminating is a trigger (`shouldRecreateInstance` true), but
`shouldRecreateInstanceGuarded` returns false while the wave marker contains that Pod — the
cascading-deletion protection the guard was built for still works.
H5/H4: with the marker lost (controller restart), `isAlreadyRestarting` blocks via the
persisted condition + `hasPreRestartPods` (creation before `LastRestartTime`) and releases
once those Pods are gone.

### W1 (minor — residual limitation, same shape as base)
A pre-restart Pod that never finishes deleting (finalizer, dead node holding the Pod object
Terminating) keeps `wavePodsRemaining` true forever, so the guard stays armed and a failed
replacement cannot start a second recreation. The instance also stays NotReady (an expected
Pod is missing/terminating), so the Ready path cannot release it either. Same as base
behavior, so not a regression, but #563's symptom persists for deletion-stuck waves. A
deadline-based release (or bounding by deletion age) would close it.

### W2 (minor — acknowledged)
`hasPreRestartPods` uses strict `CreationTimestamp.Before(LastRestartTime)` with
second-granularity timestamps: a Pod created in the same second as the restart counts as a
replacement, so the persisted fallback can release the guard while that Pod is still
terminating. Acknowledged in the code comment and `doc/features/failure-handling.md`; impact
is one extra backoff-bounded recreation.

## Harness-bites check

- Contract layer: the #563 envtest spec FAILS on base and PASSES on head (differential
  evidence above) — the test exercises the changed path.
- Unit layer: H1/H4/H5 assert both directions (blocked while pre-restart Pods exist,
  released when gone), so a guard that never released or never blocked would fail them.

## Proposed fixes (NOT applied to production here)

- **W1**: release the wave after `max(grace period, N minutes)` of deletion age, or track
  per-Pod deletion start times, so a stuck pre-restart Pod cannot hold the guard forever.

## Continuing after the fix (possibly on another machine)

The harness lives on branch `verify/restart-wave-guard-claude` (production code untouched —
the only code-file addition is the harness test file).

1. Graft it onto the fixed code:
   ```bash
   git fetch https://github.com/cheyang/rbg.git verify/restart-wave-guard-claude
   git checkout <fixed-branch>
   git checkout FETCH_HEAD -- docs/verification/restart-wave-guard pkg/reconciler/roleinstance/sync/wave_guard_harness_test.go
   ```
2. Re-run:
   ```bash
   bash docs/verification/restart-wave-guard/scripts/re-verify.sh   # resolves current PR head itself
   # or, against an explicit ref:
   bash docs/verification/restart-wave-guard/scripts/re-verify.sh <fixed-ref>
   ```
   Requires: git, go, jq, envtest assets at
   `~/.local/share/kubebuilder-envtest/k8s/1.36.2-linux-amd64` (or edit the manifest path).
3. Polarity table for future rounds:
   | test | now | after a W1 fix (stuck-release deadline) | after a W2 fix (same-second) |
   |---|---|---|---|
   | FIX-563 envtest spec | pass | pass | pass |
   | TestHarnessStuckTerminatingPreRestartPodHoldsGuard (canary) | pass | **flips red — invert it** | pass |
   | TestHarnessSameSecondPodCountsAsReplacement (canary) | pass | pass | **flips red — invert it** |
   | H1/H4/H5 contract tests | pass | pass | pass |
4. Kickoff prompt for a fresh agent: "Checkout the PR #573 head, graft
   `verify/restart-wave-guard-claude` per docs/verification/restart-wave-guard/README.md,
   run `scripts/re-verify.sh`, and report per finding Fixed / Still-broken / Partial /
   Harness-update with the polarity table applied."
