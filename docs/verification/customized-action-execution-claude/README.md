# Verification — customized-action-execution (PR #488, Reviewer A / Claude, round 1)

PR: https://github.com/sgl-project/rbg/pull/488
Code under review: `origin/pr/488` = `d260dc6e` (base `0821cb5b`)
Branch: `verify/customized-action-execution-claude` (production code untouched — harness only)

## Premise (P0) — verdict: **Confirmed**

Issue #486 claims `CustomizedAction` has no per-action timeout, no explicit completion
semantics, no exit-code/termination-message exposure in status, no Events, and forces
users to inspect generated Pods manually. Checked on **base** (`0821cb5b`):

- `CustomizedAction` struct = `Containers` + `Volumes` only — no `timeoutSeconds`, no
  `completionPolicy` (results/premise-base.txt §1).
- `RoleBasedGroupWarmupStatus` has no result fields beyond counters + generic conditions (§2).
- Controller has zero references to `ActiveDeadlineSeconds`, container statuses, attempt
  labels, or customized-action reasons/events (§3); events are only phase/pod-lifecycle ones (§4).

Component match: the files the issue names (`api/workloads/v1alpha2/rolebasedgroupwarmup_types.go`,
`internal/controller/workloads/rolebasedgroupwarmup_controller.go` + tests) are exactly what the
PR touches. Premise valid; one API-shape deviation from the issue's acceptance criteria is filed
as F2 (condition type `CustomizedActionComplete`/`CustomizedActionFailed` vs the issue's single
`CustomizedAction` type).

## Findings → tests

| id | severity | claim (one sentence) | polarity | layer | test | observed on PR head |
|----|----------|----------------------|----------|-------|------|---------------------|
| F1 | minor | `failWarmupJob` with `desiredNodes == nil` finalizes customized-action results to Failed in status but emits **no** per-node transition Events, because `oldCustomizedActionResults` aliases the slice mutated in place | contract (RED on buggy code) | unit | `TestVerifyFailWarmupJobFinalizationEmitsEvents_NilDesiredNodes` | **RED** — only the job-level `InvalidWarmupSpec` event fires, no `node=node-1` warning (results/unit-head.txt) |
| F1-ctl | — | control: same finalization with `desiredNodes != nil` does emit the event | contract | unit | `TestVerifyFailWarmupJobFinalizationEmitsEvents_WithDesiredNodes` | GREEN (control proves harness bites) |
| F3 | nit | two roles contributing an identical container (same spec **and** same name) yield duplicated `containerNames` entries and duplicated per-container status results | canary | unit | `TestVerifyBuildWarmupPodDuplicateIdentityAcrossRoles` | GREEN (duplicates observed; flips if deduped) |
| F4 | nit | aggregate `ContainerStartFailed` message comes raw from the container status and skips the 1024-byte truncation applied per-container | canary | unit | `TestVerifyAggregateStartFailureMessageNotTruncated` | GREEN (4096-byte message observed in `result.Message`, per-container message truncated to 1024) |
| F5 | minor | a stuck `ImagePullBackOff`/`CreateContainer*Error` container (RestartPolicy=Never) never terminalizes; without `timeoutSeconds`/`globalTimeoutSeconds` the job never finishes — pre-existing liveness gap, now surfaced as `Pending` | evidence | unit | `TestVerifyImagePullBackOffStaysPending` (documents contract; reasoning in findings doc) | GREEN (`Pending`/`ImagePullFailed`, never Failed) |
| F2 | minor (question) | condition-type naming deviates from issue #486 acceptance criteria | — | reasoning | — | n/a |
| F6 | nit | `CustomizedActionComplete` may be True while status results were truncated (condition computed pre-limit) | — | reasoning | — | n/a |

## How to run

```bash
go test ./internal/controller/workloads/ -run 'TestVerify' -v
```

Expected on the unfixed PR head: `TestVerifyFailWarmupJobFinalizationEmitsEvents_NilDesiredNodes`
FAILS (that failure *is* the F1 reproduction); everything else PASSes.
After F1 is fixed, all five PASS; if F3/F4 are fixed, their canaries FLIP to FAIL and must be inverted.

## Harness-bites proof (Step 4)

Applied the one-line F1 fix (snapshot old results before in-place finalization:
`oldCustomizedActionResults := append([]workloadsv1alpha2.CustomizedActionResult(nil), warmup.Status.CustomizedActionResults...)`)
temporarily, then reverted:

- with fix: F1 contract test + full package suite GREEN (results/unit-head-with-tempfix-F1.txt)
- after revert: F1 RED again, production diff empty — the red result is the finding, not a broken test.

## Layers

- **Unit**: run above (Go 1.27, fake client). Complete.
- **Integration (envtest-style)**: covered by the fake-client reconcile tests in the harness and
  the PR's own `internal/controller/workloads` suite (all green on head; see results/unit-full-suite-head.txt —
  only the F1 reproduction is red).
- **Live (L3)**: a cluster is reachable from this machine, but this debate round was instructed to
  run unit + integration only. Skipped by operator instruction, not by probe failure. The PR's own
  e2e label filter `customized-action` covers success/exit/timeout/pull/start-failure against Minikube.

## Proposed fixes

- F1: in `failWarmupJob`, snapshot `oldCustomizedActionResults` by value before the finalization
  loop mutates the shared slice (one line, verified above). Optionally also make the in-place
  status mutation explicit instead of relying on slice aliasing.
- F3: dedupe `mapping.ContainerNames` (and emit one result per unique name).
- F4: route the aggregate `terminated.Message`/`pod.Status.Message` through
  `truncateCustomizedActionMessage`.
- F5: document that Waiting-state failures only terminalize via a timeout, or add a grace period.
