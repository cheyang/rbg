# Verification — PR #488 "feat: enhance CustomizedAction execution reporting" (Reviewer B / Codex)

- PR: https://github.com/sgl-project/rbg/pull/488
- Head under review: `060ef1b7310ef9d79b80b7d0a5aaffee34de5c2d`
- Harness branch: `verify/pr488-customized-action-reporting-codex` (additive test files only; production diff is empty)
- Layers run: L1 unit (in-package, fake client) + L2 integration (envtest, real API server `1.36.2` assets). L3 live skipped per debate setup.

## Premise (P0) — Confirmed

Issue #486 is an open feature request: `CustomizedAction` has no per-action timeout,
no explicit completion policy, and no per-container exit code / termination message
in status or Events. On base `0821cb5b` none of `CustomizedActionResult`,
`timeoutSeconds`, or `completionPolicy` exist in `api/workloads/v1alpha2` or the
warmup controller (grep over a base worktree). The report names exactly the files
this PR touches, so the component matches. The feature gap is real; the PR is the
right place to close it.

## Observed vs expected (head `060ef1b7`)

| Finding | Layer | Test | Polarity | Expected (correct behavior) | Observed on head | Verdict |
| --- | --- | --- | --- | --- | --- | --- |
| F1 (P1) | unit | `TestVerifyPR488F1SucceededResultKeepsContainersWithinBudget` | contract | 1-node success keeps `containers[0]` and `truncated=false` | `containers=nil`, `truncated=true` | **Confirmed (bug)** |
| F1 (P1) | unit | `TestVerifyPR488F1CanaryTinySuccessStrippedAndFlaggedTruncated` | canary | flips when fixed | passes (pins buggy behavior) | — |
| F1 (P1) | integration | `successful customized action keeps per-container results` | contract | stored status keeps `containers[0]` after real reconcile of a Succeeded pod | `result.Containers` nil in the stored CR status | **Confirmed (bug)** |
| F1 (P1) | integration | `tiny successful status is flagged truncated` | canary | flips when fixed | passes (`customizedActionResultsTruncated=true` on a ~200-byte status) | — |
| F2 (P2) | unit | `TestVerifyPR488F2WaitingFailureIdentitySurvivesBounding` | contract | waiting failure keeps both mapped container names + `ImagePullBackOff` reason/message after bounding | passes | **Refutes P2** |
| F2 (P2) | integration | `image-pull waiting failure keeps container identity and reason` | contract | stored status keeps `pull-check` identity + waiting reason | passes | **Refutes P2** |
| F3 (new) | unit | `TestVerifyPR488F3TerminalFailureLeavesNoStaleRunningResult` | contract | no `Running`/`Pending` customized result survives in a terminally Failed job | node-b result stays `Running` after `MaxFailedNodesExceeded` deleted its pod | **Confirmed (bug)** |
| F4 (new) | unit | `TestVerifyPR488F4CanaryGlobalTimeoutClobbersSpecificReason` | canary | flips when fixed | passes (result reason rewritten `ImagePullFailed` → `GlobalTimeoutExceeded`) | — |

Raw output: `results/unit-head.txt`, `results/integration-head.txt`.

## Notes on each finding

- **F1 (confirms P1, with sharper evidence).** `limitCustomizedActionResults`
  (internal/controller/workloads/rolebasedgroupwarmup_customized_action.go:361-364)
  skips `Succeeded` results in the detail-upgrade pass, so successful results lose
  per-container detail *unconditionally* — even at 1-node scale with ~512 KiB of
  budget free. Two pieces of evidence beyond what prior rounds recorded:
  1. the false-positive `customizedActionResultsTruncated=true` on every
     all-success warmup (the flag claims details were dropped "to keep the status
     size bounded", which never happened);
  2. an envtest run through a real API server reproducing exactly the failure the
     PR's own e2e asserts against at `test/e2e/testcase/v1alpha2/warmup.go:245`
     (this head cannot pass its own e2e).
- **Fix direction verified (harness-bites check).** Dropping the `Succeeded` skip
  so the remaining budget is spent in the existing priority order (failures first)
  turns the F1 contract tests green, flips the canary, and — contrary to the prior
  note that the scale test "would need updating" — keeps
  `TestUpdateStatusBoundsCustomizedActionResultsAtScale` and all other pre-existing
  tests green, because at 1000 nodes the 512 KiB budget is exhausted by failures
  before any success is reached. Verified by patching, re-running, and reverting.
- **F2 (refutes P2).** Copilot claimed waiting failures drop the failing
  container's identity and `waiting.Reason`. The per-container results carry
  `TerminationReason`/`TerminationMessage` from the waiting state, and non-success
  results keep their containers through bounding — both at unit level (including a
  deduplicated two-identity mapping) and through a real API server. What is true:
  the *aggregate* `message`/Event text does not name the container; that is a
  cosmetic gap, not a data loss, since `status.customizedActionResults[].containers[]`
  identifies it. The PR also already ships
  `TestEvaluateCustomizedActionPodPreservesWaitingFailureDetails`.
- **F3 (new).** `failWarmupJob` deletes active pods and, unless the reason is
  `GlobalTimeoutExceeded`, never flips their non-terminal customized-action
  results. A node killed mid-flight by `MaxFailedNodesExceeded` (or an invalid
  spec discovered late) keeps `State: Running`/`Pending` in the final status of a
  terminally `Failed` job, frozen there forever (`reconcileFinished` never
  recomputes). The global-timeout path already demonstrates the intended flip.
  Verified fix: flip all non-terminal results to `Failed` on any terminal job
  failure, preserving any specific reason already recorded.
- **F4 (new, minor).** The global-timeout loop in `failWarmupJob` rewrites the
  per-result reason of *specific* failures (e.g. `ImagePullFailed`) to
  `GlobalTimeoutExceeded` and replaces the kubelet message with the generic
  timeout text. Container-level detail survives, but the result-level signal is
  degraded exactly when an operator needs to know whether nodes were stuck on
  image pulls vs genuinely slow actions. Canary pinned; verified fix preserves the
  original reason/message when present.

## Reproduce

```bash
# unit layer
go test ./internal/controller/workloads/ -run 'TestVerifyPR488' -v
# integration layer (needs envtest assets; adjust path or use setup-envtest)
KUBEBUILDER_ASSETS=$HOME/.local/share/kubebuilder-envtest/k8s/1.36.2-linux-amd64 \
  go test ./test/envtest/testcase/warmupverify/ -timeout 10m
```

## Continuing after the fix

From a checkout of this branch:

```bash
bash docs/verification/pr488-customized-action-reporting-codex/scripts/re-verify.sh
```

(no ref needed — the manifest's `pr` URL resolves the current PR head). Polarity:
contract tests must go green; the F1/F4 canaries are *fixed only when they flip to
fail* — then invert them (or drop them and keep the contract tests). The
integration layer needs envtest assets (`setup-envtest use 1.31.0` or any local
KUBEBUILDER_ASSETS). Copy-paste kickoff for a fresh machine:

> Review https://github.com/sgl-project/rbg/pull/488 : fetch branch
> `verify/pr488-customized-action-reporting-codex` from https://github.com/cheyang/rbg.git,
> read docs/verification/pr488-customized-action-reporting-codex/README.md, run
> scripts/re-verify.sh, then review the delta since `.last-reviewed`.

## Proposed fixes (validated by the harness-bites check, then reverted)

1. F1: remove the `Succeeded` skip in the detail-upgrade pass of
   `limitCustomizedActionResults` so remaining budget is spent in priority order
   (failures first, successes last).
2. F3+F4: in `failWarmupJob`, flip every non-terminal customized-action result to
   `Failed` on any terminal job failure, keeping any specific `Reason`/`Message`
   already recorded (only falling back to the job-level reason/message).
