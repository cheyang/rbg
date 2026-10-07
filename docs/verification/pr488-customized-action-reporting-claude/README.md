# pr488-customized-action-reporting (Reviewer A / Claude) — bug verification

Reproducible evidence for the findings raised while reviewing
https://github.com/sgl-project/rbg/pull/488 (head `060ef1b7`).

Harness branch: `verify/pr488-customized-action-reporting-claude` on the reviewer's fork
(cheyang/rbg). **Production code is untouched on this branch** — the only source change is the
additive test file `internal/controller/workloads/verify_pr488_claude_test.go`.

Layers run against the code under review (`origin/pr/488` = `060ef1b7`):

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit | pure logic (`limitCustomizedActionResults`, `evaluateCustomizedActionPod`, `recordCustomizedActionEvents`) | `go test ./internal/controller/workloads/ -run 'TestVerifyC[1-6]' -v` |
| 2. Integration (fake client, real controller code) | `updateStatus` / `Reconcile` end-to-end against a controller-runtime fake client with status subresource | same command (C2/C3/C4) |
| 3. Live | skipped for this debate round (per pipeline instructions, unit + integration only) | — |

> Test polarity: C1–C3 and C6 are **contract** tests (assert intended behavior) — they are RED on
> the head under review and PASS once the corresponding defect is fixed. C4 and C5 are
> **contract** guards that are already GREEN on the head (they pin correct behavior the prior
> reviewers' claims implied was broken — see the P2 row).

## Problem premise (P0)

Answered against the **base** branch (`0821cb5b`) without the patch.

| | |
|---|---|
| Claimed symptom | issue #486 (OPEN, 2026-09-25): "Container exit codes and termination messages are not clearly exposed in the RoleBasedGroupWarmup status. Users must inspect the generated Pod manually to understand why a customized action failed. There is no per-CustomizedAction timeout." |
| Linked issue | #486, OPEN, ~2 weeks old, still valid |
| Reported component | `api/workloads/v1alpha2/rolebasedgroupwarmup_types.go`, `internal/controller/workloads/rolebasedgroupwarmup_controller.go` |
| Patched component | exactly those files (+ CRD manifests, unit/e2e tests) — component match |
| **Verdict** | **Confirmed** (feature gap) |
| Evidence | `results/premise-base-branch-evidence.txt` — base `CustomizedAction` has only `Containers`/`Volumes`, no `TimeoutSeconds`, no `CustomizedActionResult`, and the base controller has no execution reporting |

## Summary of results

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| C1 | A successful result keeps per-container detail when the budget is not exhausted (prior finding P1, minimal pure-function repro) | 1 | **Confirmed (red)** | containers=0, truncated=true for a single ~200-byte result; `results/unit-l1-red-at-head.txt` |
| C2 | `updateStatus` reports container detail for a single-node success (e2e mirror, warmup.go:245) | 2 | **Confirmed (red)** | `results/unit-l1-red-at-head.txt` |
| C3 | Merged/deduplicated container identities survive into a successful result (second affected path) | 2 | **Confirmed (red)** | mapping recorded `[decode-check prefill-check]`, result reports 0 containers |
| C4 | Global timeout emits per-node transition events (guard; suspected aliasing bug — disproven) | 2 | **Green on head** | per-node Warning `node=node-1 … GlobalTimeoutExceeded` fires; `results/unit-green-guards.txt` |
| C5 | Waiting failures record the failing pod-container identity + waiting reason in per-container results (refutes the status half of prior finding P2) | 1 | **Green on head (refutation)** | `custom-0`→`ImagePullBackOff`/`pull-check`, `custom-1`→`ContainerCreating`; `results/unit-green-guards.txt` |
| C6 | The waiting-failure aggregate message / Warning event names the failing container (refined residual of P2) | 1 | **Confirmed (red)** | message is `"back-off pulling image"` — no container identity |

## Per-finding detail

### C1/C2/C3 — prior finding P1 confirmed (blocker), with new evidence

Mechanism: `limitCustomizedActionResults` (rolebasedgroupwarmup_customized_action.go:360-376)
upgrades summaries back to full detail for every result **except** `State == Succeeded`, so
successful results are unconditionally stripped of `containers` — even when the status is
tiny and the 512 KB budget is untouched.

New evidence beyond the maintainer's / Copilot's record (their e2e failure + fake-client
contract test + live run):

1. **Minimal pure-function repro (C1)**: one succeeded result, no client, no scale —
   `limitCustomizedActionResults` returns it with `Containers: nil`. This pins that the drop
   is *unconditional*, not a size decision.
2. **Second affected path (C3)**: two roles with identical container specs are merged by
   `buildWarmupPod` into one pod container; the annotation mapping records
   `ContainerNames: [decode-check, prefill-check]`, but after a successful run the reported
   result has **zero** containers — the PR's headline feature ("preserve original container
   identities across merged/deduplicated Warmup Pods") is entirely nullified for successful
   runs, not just single-container detail.
3. **`CustomizedActionResultsTruncated` is a permanent false positive (C1 log)**: the same
   unconditional drop makes the flag `true` for a single ~200-byte success. Every successful
   warmup will report "one or more results or per-container details were omitted", so the
   flag can no longer tell an operator whether the bounding actually bit.
4. **The suggested fix does not require updating the scale test** (bite-check experiment):
   applying the maintainer's suggested fix — spend the remaining budget on results in the
   existing priority order *including successes* — makes C1/C2/C3 green **and**
   `TestUpdateStatusBoundsCustomizedActionResultsAtScale` still passes unchanged
   (`results/bite-check-upstream-suite-with-fixes.txt`): at 1000-node scale the 500 failed
   results consume the budget first, so successes stay summary-only exactly as the scale test
   expects. The prior record assumed the scale test "would need updating"; it does not — the
   fix only changes behavior when there is spare budget, which is precisely the e2e-covered
   case.

### C4 — suspected new bug (global-timeout event suppression) — disproven, kept as a guard

While reviewing `failWarmupJob` I suspected the in-place global-timeout marking loop mutates
the slice that `oldCustomizedActionResults` (the event dedup baseline) points into, silently
suppressing per-node transition events. C4 tests exactly that scenario (previous status
result `Running` for node-1, global timeout flips it to `Failed/GlobalTimeoutExceeded`).
**Result: green on head** — because `allCustomizedActionResults` is reassigned from
`evaluateCustomizedActionResults(...)` (a fresh slice) *before* the mutation whenever
`desiredNodes != nil`, and the global-timeout path always passes non-nil `desiredNodes`. The
aliasing only exists when `desiredNodes == nil`, which never coincides with
`reason == GlobalTimeoutExceeded`. No upstream test covers this event path, so the test stays
in the harness as a regression guard.

### C5/C6 — prior finding P2: status half refuted, residual refined (minor)

Copilot's claim (inline @060ef1b7, `rolebasedgroupwarmup_customized_action.go:201`): "for
waiting failures the code drops both the failing mapping identity and `waiting.Reason`; every
mapped container reports only `Waiting`, so with multiple customized containers neither
status nor Events can identify which container hit the image-pull/start error."

C5 (green on head) **refutes the status half**: `evaluateCustomizedActionContainer`
(rolebasedgroupwarmup_customized_action.go:142-145) copies `waiting.Reason`/`waiting.Message`
into each container result's `terminationReason`/`terminationMessage`. With two pod
containers, the status reports `custom-0`→`terminationReason: ImagePullBackOff,
containerName: pull-check` and `custom-1`→`ContainerCreating` — the failing container is
identifiable, and `terminationReason` is retained through limiting because pending results
are non-successful and get the detail upgrade. (The PR's own
`TestEvaluateCustomizedActionPodPreservesWaitingFailureDetails` already pins the
single-container case.)

C6 (red on head) **refines what actually remains**: the *aggregate* `message` — and therefore
the Warning Event (`node=node-1, pod=two-containers: back-off pulling image`) — does not name
the failing container. With N>1 customized containers, an operator watching Events must
cross-reference `status.customizedActionResults[].containers` to find the culprit. #486 asks
for "container name … when available"; the mapping has it. Minor.

## Live run notes

Not run this round (unit + integration layers only, per the debate setup). The maintainer's
own live evidence (successful two-container action reporting zero container details on a real
cluster) is on record in the PR thread and matches the C2/C3 mechanism.

## Proposed fixes (NOT applied to production here)

Both were applied temporarily for the harness-bites check and reverted (production diff empty).

- **P1 / C1-C3**: in the second pass of `limitCustomizedActionResults`, drop the
  `State == Succeeded` skip so the remaining budget is spent on results in the existing
  priority order, successes included. Verified: C1/C2/C3 green, full upstream unit suite
  green, scale test green unchanged.
- **P2 residual / C6**: in `aggregateCustomizedActionState`, prefix the waiting-failure
  message with the failing pod container, e.g.
  `fmt.Sprintf("container=%s: %s", mapping.PodContainerName, waiting.Message)`.
  Verified: C6 green, upstream suite green.

## Continuing after the fix (possibly on another machine)

The harness is on branch `verify/pr488-customized-action-reporting-claude` (production code
untouched), so it grafts onto whatever the fixed code is.

1. Get it onto the fixed code:
   ```bash
   git fetch https://github.com/cheyang/rbg.git verify/pr488-customized-action-reporting-claude
   git checkout <fixed-branch>
   git checkout FETCH_HEAD -- internal/controller/workloads/verify_pr488_claude_test.go docs/verification/pr488-customized-action-reporting-claude
   ```
2. Run it:
   ```bash
   go test ./internal/controller/workloads/ -run 'TestVerifyC[1-6]' -v
   ```
   Expected after the P1 + P2-residual fixes: **all six PASS**. All six are contract tests;
   there are no bug-canaries to invert. C1/C2/C3/C6 flipping green is the fix confirmation.
3. Or simply `bash scripts/re-verify.sh` from a checkout of this branch — it resolves the
   current PR head from the manifest's `pr` URL, grafts the harness, runs the layers, and
   prints Fixed / Still-broken per finding.

Kickoff prompt for a fresh agent: "You are re-verifying PR https://github.com/sgl-project/rbg/pull/488.
Checkout branch verify/pr488-customized-action-reporting-claude from
https://github.com/cheyang/rbg.git, read docs/verification/pr488-customized-action-reporting-claude/README.md,
run `bash docs/verification/pr488-customized-action-reporting-claude/scripts/re-verify.sh` (no
args — it fetches the current PR head), and report per-claim Fixed / Still-broken with the
observed-vs-expected table. Do not modify production code."
