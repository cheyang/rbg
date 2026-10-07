# Verification — KEP-465: Continuous Node-Pool Warmup (PR #483)

Reviewer: Claude (Reviewer A, debate round 2026-10-07).
PR: https://github.com/sgl-project/rbg/pull/483 — **documentation-only** (adds
`keps/465-continuous-rbg-warmup/`; no production code).

## Premise (P0) — verdict: CONFIRMED

Claim (from issue #465 / the KEP, quoted):

> "`RoleBasedGroupWarmup` currently behaves as a one-shot job. After it reaches
> `Completed` or `Failed`, nodes that later join an autoscaled inference pool
> are not warmed and may pay the full image, model, or runtime cold-start cost."

and (KEP Motivation):

> "Although the API server currently accepts updates to targets and actions,
> the controller tracks completion only by node name. An update can therefore
> leave running Pods on the old definition, reuse an old successful Pod, and
> create later Pods from the new definition."

Evidence (unit layer, runs against PR head — code identical to base since the
PR touches no production code):

| symptom | test | result |
| --- | --- | --- |
| one-shot: late-joining matching node is never warmed | `TestClaudeVerifyP0OnceIgnoresLateNodes` | PASS (canary — symptom reproduced) |
| spec update reuses the old succeeded Pod for node-1 instead of re-warming it | `TestClaudeVerifyP0SpecUpdateReusesOldSucceededPod` | PASS (canary — symptom reproduced) |

Raw output: `results/unit.txt`, `results/unit.json`.

Component match: the KEP targets exactly the API + controller the issue names.
Issue #465 is OPEN (created 2026-09-12), symptom and component match the PR.

## Findings under verification

The PR is a KEP; the review findings are design-level. The harness pins the
current-behavior facts the KEP builds on, and the behaviors the KEP promises to
preserve.

| id | claim | test | polarity | verdict |
| --- | --- | --- | --- | --- |
| P0-once | Once mode ignores late nodes (premise) | `TestClaudeVerifyP0OnceIgnoresLateNodes` | canary | confirmed (passes = symptom present) |
| P0-update | spec updates reuse old successes by node name (premise) | `TestClaudeVerifyP0SpecUpdateReusesOldSucceededPod` | canary | confirmed (passes = symptom present) |
| F1 | current Once semantics: `spec.paused` does NOT override terminal `Completed`/`Failed`, and TTL deletion still runs while paused | `TestClaudeVerifyF1PausedDoesNotOverrideTerminalPhaseOrTTL` | contract | passes on base — the behavior KEP-465's unscoped phase-ordering step 1 ("Paused when spec.paused is true") would contradict |
| F2 | current controller has no retry backoff: a failed Pod is re-created on the very next reconcile with no delay | `TestClaudeVerifyF2FailedPodRetriedImmediately` | canary | confirmed (passes = no-backoff behavior present) |
| KEP-volume-determinism-claim | current first-wins volume-conflict winner depends on Go map iteration order (role merge), i.e. is nondeterministic across reconciles | `TestClaudeVerifyVolumeFirstWinsIsOrderDependent` | canary | confirmed (passes = both winners observed across 300 rounds, supporting the KEP's sorted-role-order requirement) |

Findings F1/F2 map to the review findings in the debate document
(`findings-claude.md`): F1 = KEP phase-ordering self-contradiction (major),
F2 = unspecified retry timing for Continuous (major).

## How to run

```bash
# from a checkout of this branch (verify/continuous-rbg-warmup-claude)
go test ./internal/controller/workloads/ -run 'TestClaudeVerify' -count=1 -v

# or the full re-verify (unit layer only; grafts harness onto current PR head)
bash docs/verification/continuous-rbg-warmup/scripts/re-verify.sh
```

Integration layer: **not applicable** — the PR contains no production code, so
there is no new controller/API behavior to exercise against a real API server.
All KEP factual claims about existing code are settled at the unit layer.
Live layer: skipped this round (no cluster).

## Harness-bites check

Step 4 of the verifier skill: prove the tests exercise the real code path by
temporarily applying a "fix-like" patch and watching polarity flip.

Applied patch (then reverted, production diff restored to empty):

1. In `Reconcile`, route terminal phases to `reconcileUnfinished` instead of
   `reconcileFinished` (simulating continuous re-evaluation):
   - `TestClaudeVerifyP0OnceIgnoresLateNodes` **flipped to FAIL** (pod created
     for late-joining node-2) — the canary bites.
2. In `getDesiredNodesToWarmup`, replace the `roleToNodes` map iteration with
   sorted role names (the KEP's determinism requirement):
   - `TestClaudeVerifyVolumeFirstWinsIsOrderDependent` **flipped to FAIL**
     (single winner after 300 rounds) — the canary bites.
3. `TestClaudeVerifyF1PausedDoesNotOverrideTerminalPhaseOrTTL`: no patch
   applied; the existing repo test `TestReconcile_PausedSkipsTerminalPhase`
   already pins the same invariant, and the contract test's failure mode is
   exactly "phase flipped / TTL did not delete" — both asserted directly.

Captured in `results/bite-check.txt`. Production code untouched on the branch
(only this harness + docs added; `git diff 50633d00 -- . ':!docs/verification' ':!internal/controller/workloads/verify_kep465_claude_test.go'` is empty).

## Observed vs expected

| finding | expected | observed |
| --- | --- | --- |
| P0 | symptom reproduces on base | reproduced (both canaries pass) |
| F1 (KEP ordering vs existing terminal-phase rule) | base keeps terminal phase + TTL for paused resource | confirmed; KEP text is self-contradictory about this |
| F2 (no retry backoff today) | failed pod retried immediately | confirmed; KEP Failure Semantics specify no retry timing for Continuous |
| volume-conflict nondeterminism | winner varies across reconciles | confirmed; KEP's sorted-role-order fix is necessary |

## Continuing after the (future) implementation

When the KEP implementation PR lands:

```bash
bash docs/verification/continuous-rbg-warmup/scripts/re-verify.sh <impl-sha>
```

Polarity table:

| test | today | after Continuous implementation |
| --- | --- | --- |
| `TestClaudeVerifyP0OnceIgnoresLateNodes` | PASS (canary) | for `mode: Once` must still PASS; add a Continuous variant that FAILS until late nodes are warmed (then promote to contract) |
| `TestClaudeVerifyP0SpecUpdateReusesOldSucceededPod` | PASS (canary) | rejected by the new webhook → rewrite as a webhook envtest; the controller-level canary stays for Once |
| `TestClaudeVerifyF1PausedDoesNotOverrideTerminalPhaseOrTTL` | PASS (contract) | must still PASS (Once unchanged); if it fails, the KEP's step-1 was implemented unscoped — regression |
| `TestClaudeVerifyF2FailedPodRetriedImmediately` | PASS (canary) | must FLIP once retry backoff exists — then invert/promote |
| `TestClaudeVerifyVolumeFirstWinsIsOrderDependent` | PASS (canary) | must FLIP once roles are merged in sorted order — then invert/promote |

## Kickoff prompt (fresh agent, another machine)

> You are resuming verification of PR sgl-project/rbg#483 (KEP-465). Checkout
> branch `verify/continuous-rbg-warmup-claude` from the reviewer fork
> (cheyang/rbg), read `docs/verification/continuous-rbg-warmup/README.md` and
> `verify-manifest.json`, then run
> `bash docs/verification/continuous-rbg-warmup/scripts/re-verify.sh` (unit
> layer only; integration/live skipped). Apply the polarity table above when
> interpreting results, and advance `.last-reviewed` per the script's reminder.
