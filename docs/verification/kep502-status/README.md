# KEP-502 status model — reviewer verification (PR #572)

Reviewer: Claude (Reviewer A). Branch: `verify/kep502-status-claude` on the reviewer fork
(`cheyang/rbg`). Round 1 reviewed PR head `95ddf06a`; round 2 (this) reviews head
`effc2f8e` (delta `95ddf06a..effc2f8e`: four commits addressing the round-1 findings).
Production code on this branch is **untouched** — the diff is the harness only.

## Premise (P0) — Confirmed

KEP-502's motivation is real on the merge base (`c2896cc0`): `RoleBasedGroupProgressing`
is a declared-but-never-written condition type, RIS/RI write transient failures as
lasting `FailedScale`/`FailedUpdate` facts (10 write sites), and `RoleInstance` has no
`status.phase`. The KEP was merged in the direct base commit of this PR, so the
component match holds: the PR touches exactly the RBG/RIS/RI status paths the KEP
defines.

Evidence: `results/premise-and-suite.txt`.

## Observed vs expected

### Round 1 (head `95ddf06a`) — archived

| ID | Finding | Layer | Verdict | Evidence |
| --- | --- | --- | --- | --- |
| F1 | Established RIS missing-instance classification flaps `Recovering` → `ScalingUp` | unit (contract) | Confirmed | `results/unit-ris-ri.txt` |
| F2 | RIS `InvalidSpec` blocker surfaces at RBG as benign activity | unit (contract) | Confirmed | `results/unit-rbg.txt` |
| F3 | Mixed-backend RBG keeps (and re-applies) a stale `Progressing` condition | unit (contract) | Confirmed | `results/unit-rbg.txt` |
| F4 | RI layer keeps the first-appended blocker instead of family precedence | unit (contract) | Confirmed | `results/unit-ris-ri.txt` |

### Round 2 (head `effc2f8e`) — current

| ID | Finding | Layer | Verdict | Evidence |
| --- | --- | --- | --- | --- |
| F1 | Missing-instance classification flap | unit (contract) | **Fixed** (sticky persisted `Recovering` reason; PR also added the persisted-loss test) | `results/round2-contract-ris.txt` |
| F2 | `InvalidSpec` not propagated to RBG | unit (contract) | **Fixed** (`risReasonToRBG` maps it; configuration family ranks first) | `results/round2-contract-rbg.txt` |
| F3 (ledger F3) | RBG confirmed blocker family lost on the roleErr path | unit (contract, controller-level) | **Fixed** (entry-blocker snapshot + finalize; new test `TestRBGBlockerFamilySurvivesRecomputeAndUnclassifiedRetry`) | `results/round2-contract-rbg.txt` |
| F3 (mixed backend, ledger F4) | Stale controller-owned `Progressing` condition on mixed-backend RBGs | unit (contract) | **Still broken** — no removal path exists; the condition still rides the SSA patch payload | `results/round2-contract-rbg.txt` |
| F4 | RI blocker append order | unit (contract) | **Fixed** (family priority) | `results/round2-contract-ri.txt` |
| F5 (ledger F5) | RI family precedence | unit | **Fixed** | `results/round2-contract-ri.txt` |
| F9 | Dependency-readiness status vs execution gate | code-path | **Fixed** (`unreadyDependencies` now calls `CheckWorkloadReady`, the same predicate as `CheckDependencyReady`) | source inspection |
| F10 | Ungated gang PodGroup reads | code-path | **Fixed** (gated on gang strategy + controller-ownership check) | source inspection |
| **F11** | **NEW (revision regression):** established set member loss during a readiness flap reports `Creating` instead of `Recovering` — `effc2f8e` removed `InstancesNotReady` from `setEstablishmentReasons` | unit (contract, differential) | **Broken on `effc2f8e`, passing on `95ddf06a`** | `results/round2-contract-ris.txt`, `results/round2-differential-previous-head.txt` |
| **F12** | **NEW:** own-operation blocker interlude destroys establishment evidence; post-blocker recovery reports `Creating` | unit (contract) | **Broken on both heads** (pre-existing within the PR, discovered this round) | `results/round2-contract-ris.txt`, `results/round2-differential-previous-head.txt` |
| F6 (ledger) | Legacy unconfirmable RI phase defaults to `Creating` | code-path | Still open (unchanged) | `progressing.go` `""` case |
| F7 (ledger) | RIS preflight read failures skip the status write | code-path | Still open (both modes; RBG layer got the `Unknown` branch, RIS did not) | `stateful_instance_set_control.go:231/256`, `statelessmode/reconciler.go:147/154` |
| F8 (ledger) | `FailedScale` marker not cleaned once `InvalidSpec` is persisted | code-path | Still open (dedup early return still precedes the cleanup loop) | `roleinstanceset_controller.go` `validateCommittedMembership` |

All four contract tests are **contract polarity**: they FAIL on the PR head (that is
the reproduction) and PASS once fixed. The control case
`TestScaleUpAfterConvergenceStillReportsScalingUp` guards F1's fix: genuine target
growth must stay `ScalingUp`.

## Suite status on PR head `effc2f8e`

- `go build ./...` — OK
- PR-owned unit suites all green (`results/round2-package-suites.txt`; the only
  failures with the harness grafted are the contract tests for F3-mixed-backend,
  F11, F12 — i.e. the open findings)
- envtest (`KUBEBUILDER_ASSETS` set, 1.37.0): `./test/envtest/testcase/rbg/` ok
  (100s), `./test/envtest/testcase/stateful/` ok (42s) — `results/round2-envtest.txt`
- CI rollup on `effc2f8e`: all unit/envtest/lint/build/e2e checks SUCCESS except two
  CANCELLED e2e jobs ("E2E lifecycle", "E2E update") in the same workflow run whose
  sibling e2e jobs succeeded — looks like runner cancellation, not a product failure.
- Live layer: skipped (no KUBECONFIG), see `liveNote` in the manifest.

## How to run

From a checkout of this branch:

```bash
bash docs/verification/kep502-status/scripts/re-verify.sh          # current PR head
bash docs/verification/kep502-status/scripts/re-verify.sh <sha>    # a fixed ref
export KUBEBUILDER_ASSETS=<envtest-assets>                          # optional L2
```

Exit 0 iff all contract-test findings are fixed and the package suites pass.

## Continuing after the fix

Polarity table:

| Test | On buggy head `effc2f8e` | On fixed code |
| --- | --- | --- |
| `TestEstablishedSetMissingInstanceStaysRecovering` | PASS (fixed in this round) | PASS |
| `TestScaleUpAfterConvergenceStillReportsScalingUp` | PASS (control) | PASS |
| `TestRBGAggregatesRISInvalidSpecAsConfigurationBlocker` | PASS (fixed in this round) | PASS |
| `TestRBGBlockerFamilySurvivesRecomputeAndUnclassifiedRetry` | PASS (fixed in this round) | PASS |
| `TestInstanceBlockerSelectionFollowsFamilyPriority` | PASS (fixed in this round) | PASS |
| `TestMixedBackendRBGCleansUpProgressing` | **FAIL** (open) | PASS |
| `TestEstablishedSetReadinessFlapThenMemberLossStaysRecovering` | **FAIL** (open, regression) | PASS |
| `TestEstablishedSetRecoveryAfterBlockerInterruptionStaysRecovering` | **FAIL** (open, pre-existing) | PASS |

F6/F7/F8 (legacy phase default, RIS preflight status write, FailedScale cleanup
ordering) have no executable repro; re-check by inspection against the mechanism in
the manifest.

Copy-paste kickoff for a fresh agent:

> You are re-verifying PR https://github.com/sgl-project/rbg/pull/572. Check out the
> branch `verify/kep502-status-claude` from the reviewer fork `cheyang/rbg`, read
> `docs/verification/kep502-status/README.md` and `verify-manifest.json`, then run
> `bash docs/verification/kep502-status/scripts/re-verify.sh` (set `KUBEBUILDER_ASSETS`
> for the integration layer). Report per finding Fixed / Still-broken / Harness-update.
