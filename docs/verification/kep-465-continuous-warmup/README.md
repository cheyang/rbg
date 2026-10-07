# KEP-465 Continuous Warmup (PR #483) — review verification

Reproducible evidence for the findings raised while reviewing
https://github.com/sgl-project/rbg/pull/483 (docs-only: adds
`keps/465-continuous-rbg-warmup/{README.md,kep.yaml}`).

Because the PR changes no production code, the harness verifies two things:

1. **Codebase-claim contract tests** — every statement the KEP makes about the
   existing API/controller/KEP-129 is asserted against the repository
   (identical on base and head, since the diff is docs-only).
2. **Finding contract tests (`TestKEP465_F*`)** — each review finding is
   encoded as a test of the *intended* doc state; it FAILS on the current PR
   head (that failure is the reproduction) and flips to PASS when the KEP is
   revised.

Layers run against the code under review (`origin/pr/483` = `50633d00`):

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit | KEP-vs-codebase consistency + doc hygiene + finding tests | `go test -mod=vendor -count=1 ./test/verification/kep465/ -run 'TestKEP465_'` |
| 2. Integration | PR diff scope (docs-only), `git diff --check`, kep.yaml/example YAML validity | `go test -mod=vendor -count=1 ./test/verification/kep465/ -run 'TestKEP465Integration'` |
| 3. Live | n/a — docs-only PR, no runtime behavior to exercise | — |

> Polarity: all tests are CONTRACT tests. The 15 claim/hygiene tests PASS on
> the PR head; the 4 finding tests FAIL (that is the reproduction) and go
> GREEN when the KEP text is fixed.

## Problem premise (P0)

| | |
|---|---|
| Claimed symptom | "`RoleBasedGroupWarmup` currently behaves as a one-shot job. After it reaches `Completed` or `Failed`, nodes that later join an autoscaled inference pool are not warmed and may pay the full image, model, or runtime cold-start cost." (PR body; issue #465: "it does not cover autoscaled resource pools where new nodes or new workload instances can appear after the warmup has completed") |
| Linked issue | #465 `[Proposal] Discuss once/continuous lifecycle semantics for RoleBasedGroupWarmup`, OPEN, opened 2026-09-12, still valid (mentionedIssues; no `fixes` keyword — docs PR) |
| Reported component | `RoleBasedGroupWarmup` API + controller (`api/workloads/v1alpha2/rolebasedgroupwarmup_types.go`, `internal/controller/workloads/rolebasedgroupwarmup_controller.go`) |
| Patched component | design doc for exactly that API + controller (`keps/465-continuous-rbg-warmup/`) |
| Component match | Yes |
| **Verdict** | **Confirmed** |
| Evidence | One-shot behavior verified on base: terminal phases route only to `reconcileFinished` (TTL cleanup) — `TestKEP465_TerminalPhasesOnlyDoTTL`; no Node/RBG-pod watches in `SetupWithManager`; no webhook/CEL immutability today (`TestKEP465_NoWarmupValidatingWebhookToday`, `TestKEP465_NoCELImmutabilityOnWarmupSpec`). Issue metadata: `results/premise-issue-465.json`. |

Docs-only PR, so "reproducing the symptom on base" reduces to confirming the
limitation the KEP describes is real in the current controller — it is: once a
Warmup reaches `Completed`/`Failed`, `Reconcile` routes to `reconcileFinished`
which only handles TTL deletion, and the controller watches only the Warmup and
its owned Pods, so a late-joining node is never warmed.

## Summary of results

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| C1..C15 | KEP's factual claims about the current codebase (phase enum, labels, generateName, no webhook, no CEL immutability, node-name accounting, TTL-only terminal handling, RBAC markers, KEP-129 retry counter, volume-conflict first-wins, map-iteration merge order, controller-assigned container names, kep.yaml metadata, TOC/links, required sections) | 1 | Confirmed (all PASS) | `results/unit-layer.txt` |
| F1 | Ready/NoNodesMatched ambiguity: operators gate on `Ready` (user story 1) but an empty target set is also `Ready`; Observability section never mentions `NoNodesMatched` | 1 | Confirmed — `TestKEP465_F1_ObservabilityOmitsNoNodesMatched` FAILS | `results/unit-layer.txt` |
| F2 | Requeue interval has contradictory bounds: "no later than five minutes in the future" (≤5m, line 389) vs "capped at one enqueue per Continuous resource per five minutes" (≥5m, lines 621–622) | 1 | Confirmed — `TestKEP465_F2_RequeueIntervalBoundsContradictory` FAILS | `results/unit-layer.txt` |
| F3 | State-machine diagram shows `Paused` only adjacent to `Running`; rule 1 + "Paused takes phase precedence" allow `Ready`/`Degraded` → `Paused` | 1 | Confirmed — `TestKEP465_F3_DiagramOmitsPausedFromReadyOrDegraded` FAILS | `results/unit-layer.txt` |
| F4 | Immutability rationale overgeneralizes: `backoffLimitPerNode`/`maxFailedNodes` are re-read live each reconcile today, so unlike targets/actions their updates are well-defined; KEP lists them as immutable under the "not reliably implemented" rationale without per-field justification | — | Confirmed by code reading (reasoning; not mechanically testable) | controller `computePermanentlyFailedNodes`/parallelism read spec live every reconcile |
| F5 | `kep.yaml` has `approvers: []` — governance path to `implementable` undefined per the repo template | 1 | Confirmed — `TestKEP465_F5_KepYAMLHasNoApprovers` FAILS | `results/unit-layer.txt` |
| F6 | nit: new label prefix `warmup-node-uid` is inconsistent with the existing unprefixed `node-name` label | — | Confirmed by reading | KEP lines 329–331 vs `LabelNodeName` |
| I1 | PR is docs-only (diff scope = the 2 KEP files) | 2 | Confirmed | `TestKEP465Integration_PRDiffScopeIsDocsOnly` |
| I2 | `git diff --check` clean (PR's own verification step) | 2 | Confirmed | `TestKEP465Integration_GitDiffCheckClean` |
| I3 | kep.yaml + embedded example YAML parse; apiVersion/kind/mode consistent | 2 | Confirmed | `TestKEP465Integration_YAMLParses` |
| I4 | Repository unit-test baseline green on PR head (PR body claims "2779 passed in 57 packages") | 2 | Confirmed | `results/unit-tests.txt` |

## Per-finding detail

### F1 — Ready/NoNodesMatched (minor)
User story 1 (KEP line ~111) has the platform gate workload eligibility on
`Ready`. KEP lines 281–282: an available target with **no matched nodes** is
also `Ready` (reason `NoNodesMatched`). A misconfigured selector or a
scaled-to-zero pool therefore reports the same phase as a fully-warmed pool;
only the condition reason / `desired: 0` distinguishes them — and the
Observability section never says so. Fix sketch: document in Observability that
`Ready` + `NoNodesMatched` (or `desired == 0`) means "nothing to warm", not
"warm". Reproduced by `TestKEP465_F1_ObservabilityOmitsNoNodesMatched` (FAIL).

### F2 — contradictory requeue bounds (minor)
Controller Flow step 8 (line 389): "a jittered safety requeue **no later than
five minutes** in the future" (interval ≤ 5m). Scalability (lines 621–622):
"**capped at one enqueue** per Continuous resource **per five minutes**"
(interval ≥ 5m). Pick one bound. Reproduced by
`TestKEP465_F2_RequeueIntervalBoundsContradictory` (FAIL).

### F3 — diagram omits Paused transitions (minor)
The state-machine diagram (lines ~253–270) draws `Paused` reachable only from
`Running`, but rule 1 (line 273: "`Paused` when `spec.paused` is true") and the
note at line 307 ("`Paused` takes phase precedence") apply from `Ready` and
`Degraded` too. The text rules are authoritative and correct; the diagram
understates them. Reproduced by
`TestKEP465_F3_DiagramOmitsPausedFromReadyOrDegraded` (FAIL).

### F4 — blanket immutability of retry-policy fields (question, minor)
KEP line 240 makes `backoffLimitPerNode` and `maxFailedNodes` immutable, with
the motivation (lines ~84–89, risks table) that execution-content updates
"were not reliably implemented". That is true for targets/actions/images
(verified: completion is keyed by node name only), but the two retry-policy
fields are re-read from the live spec on every reconcile
(`computePermanentlyFailedNodes`, parallelism budget), so updating them today
is deterministic. Question for the author: justify making retry policy
immutable, or scope immutability to execution content.

### F5 — empty approvers (nit/question)
`kep.yaml` has `approvers: []`. The repo template ties `implementable`
transitions to approver sign-off; with an empty list the governance path is
undefined. Most merged KEPs here list approvers (e.g. KEP-129:
@syspretor/@cheyang). Reproduced by `TestKEP465_F5_KepYAMLHasNoApprovers`.

### F6 — label prefix nit (nit)
New labels use the `warmup-` prefix (`warmup-revision`, `warmup-attempt`,
`warmup-node-uid`) while the existing node label it complements is the
unprefixed `workloads.x-k8s.io/node-name`. Cosmetic.

## Harness-bites check (Step 4)

The proposed doc fixes (F1: mention `NoNodesMatched` in Observability; F2:
drop the "no later than" bound; F3: "from any phase"; F5: add an approver)
were applied to the KEP temporarily: all four `TestKEP465_F*` tests flipped to
PASS, confirming they are red for the right reason. The KEP files were then
restored byte-identical to the PR head (`git diff origin/pr/483 -- keps/`
empty); production code was never touched (there is none in this PR).

## Repository baseline

`go test -mod=vendor -count=1 ./api/... ./cmd/... ./internal/... ./pkg/...`
on the PR head: see `results/unit-tests.txt` (the PR changes no code, so this
validates the PR body's baseline claim rather than the PR itself).

## Continuing after a KEP revision (possibly on another machine)

The harness lives on branch `verify/kep-465-continuous-warmup-codex` on the
reviewer's fork; the KEP (the "code under review") is untouched by the branch.

1. ```bash
   git fetch https://github.com/cheyang/rbg.git verify/kep-465-continuous-warmup-codex
   git checkout <revised-PR-head-or-main>
   git checkout FETCH_HEAD -- docs/verification/kep-465-continuous-warmup test/verification/kep465
   ```
   or simply run, from a checkout of this branch:
   ```bash
   bash docs/verification/kep-465-continuous-warmup/scripts/re-verify.sh
   ```
   (no ref: the script fetches the current PR #483 head from the PR URL in
   `verify-manifest.json`, grafts the harness onto it, and runs layers 1–2).
2. Prereqs: Go toolchain + `git`; layer 2 also needs `python3`+PyYAML. No
   cluster, no envtest assets.
3. Read results by polarity: the 15 claim tests + 3 integration tests should be
   GREEN; each `TestKEP465_F*` is FIXED only when it flips to PASS.
4. Harness-bites: `git stash` the KEP fix (or check out the old KEP) and watch
   the F tests go red again.

### Kickoff prompt for a fresh agent
```text
Continue a verification task on branch verify/kep-465-continuous-warmup-codex
(remote https://github.com/cheyang/rbg.git). Background: review of
https://github.com/sgl-project/rbg/pull/483 (KEP-465, docs-only) produced
findings F1/F2/F3/F5, each encoded as a failing contract test in
test/verification/kep465. Read docs/verification/kep-465-continuous-warmup/README.md
("Continuing after a KEP revision"), then run
`bash docs/verification/kep-465-continuous-warmup/scripts/re-verify.sh`
and report an observed-vs-expected table. Do not modify the KEP itself except
to trial-fix and revert (harness-bites).
```
