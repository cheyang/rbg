# KEP-502 status model — reviewer verification (PR #572)

Reviewer: Claude (Reviewer A). Branch: `verify/kep502-status-claude` on the reviewer fork
(`cheyang/rbg`), based on PR head `95ddf06a`. Production code on this branch is
**untouched** — the diff is the harness only.

## Premise (P0) — Confirmed

KEP-502's motivation is real on the merge base (`c2896cc0`): `RoleBasedGroupProgressing`
is a declared-but-never-written condition type, RIS/RI write transient failures as
lasting `FailedScale`/`FailedUpdate` facts (10 write sites), and `RoleInstance` has no
`status.phase`. The KEP was merged in the direct base commit of this PR, so the
component match holds: the PR touches exactly the RBG/RIS/RI status paths the KEP
defines.

Evidence: `results/premise-and-suite.txt`.

## Observed vs expected

| ID | Finding | Layer | Verdict | Evidence |
| --- | --- | --- | --- | --- |
| F1 | Established RIS missing-instance classification flaps `Recovering` → `ScalingUp` after one reconcile | unit (contract) | **Confirmed (reproduced)** | `results/unit-ris-ri.txt` |
| F2 | RIS `InvalidSpec` blocker surfaces at RBG as benign activity `RoleInstanceSetUpdated`, not a configuration-family blocker | unit (contract) | **Confirmed (reproduced)** | `results/unit-rbg.txt` |
| F3 | Mixed-backend RBG keeps (and re-applies) a stale controller-owned `Progressing` condition forever | unit (contract) | **Confirmed (reproduced)** | `results/unit-rbg.txt` |
| F4 | RI layer keeps the first-appended blocker instead of family precedence (RIS layer does compete) | unit (contract) | **Confirmed (reproduced)** | `results/unit-ris-ri.txt` |
| F5 | RIS/stateful preflight read failures skip the status write (no `Unknown`/`observedGeneration`); RBG layer does write `Unknown` | code-path | Not-reproduced-by-test (evidenced by inspection) | `stateful_instance_set_control.go:243-249` |
| F6 | RI phase exits Creating/Updating one reconcile late (phase computed before `calculateStatus` advances CurrentRevision) | code-path | Not-reproduced-by-test (cosmetic) | `instance_reconciler.go:152` |

All four contract tests are **contract polarity**: they FAIL on the PR head (that is
the reproduction) and PASS once fixed. The control case
`TestScaleUpAfterConvergenceStillReportsScalingUp` guards F1's fix: genuine target
growth must stay `ScalingUp`.

## Harness bites

Each proposed fix (see `verify-manifest.json`) was applied temporarily to the
production code; the corresponding contract test flipped to PASS and the full package
suites stayed green; the fix was then reverted. Production diff on this branch is
empty (verified with `git diff 95ddf06a -- . ':!*_test.go'`).

## Suite status on PR head

- `go build ./...` — OK
- `go test ./pkg/reconciler/roleinstance/... ./pkg/reconciler/roleinstanceset/... ./internal/controller/workloads/... ./api/...` — all green (before adding this harness)
- envtest (`KUBEBUILDER_ASSETS` set, 1.37.0): `./test/envtest/testcase/rbg/` ok (101s), `./test/envtest/testcase/stateful/` ok (42s)
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

| Test | On buggy head | On fixed code |
| --- | --- | --- |
| `TestEstablishedSetMissingInstanceStaysRecovering` | FAIL | PASS |
| `TestScaleUpAfterConvergenceStillReportsScalingUp` | PASS | PASS (control) |
| `TestRBGAggregatesRISInvalidSpecAsConfigurationBlocker` | FAIL | PASS |
| `TestMixedBackendRBGCleansUpProgressing` | FAIL | PASS |
| `TestInstanceBlockerSelectionFollowsFamilyPriority` | FAIL | PASS |

F5/F6 have no executable repro; re-check by inspection against the mechanism in the
manifest.

Copy-paste kickoff for a fresh agent:

> You are re-verifying PR https://github.com/sgl-project/rbg/pull/572. Check out the
> branch `verify/kep502-status-claude` from the reviewer fork `cheyang/rbg`, read
> `docs/verification/kep502-status/README.md` and `verify-manifest.json`, then run
> `bash docs/verification/kep502-status/scripts/re-verify.sh` (set `KUBEBUILDER_ASSETS`
> for the integration layer). Report per finding Fixed / Still-broken / Harness-update.
