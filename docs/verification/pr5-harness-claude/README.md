# PR #5 harness verification — Reviewer A (Claude)

First-round independent verification of
[cheyang/rbg#5](https://github.com/cheyang/rbg/pull/5)
("test: verification harness reproducing PR #394 restart-backoff bugs"),
run 2026-09-29. Unit + integration layers only (no live/KUBECONFIG layer).

PR #5 is **tests/docs-only**: it adds the pr394 restart-backoff verification
harness (2 Go test files, live scripts, captured results, review state) on top
of `pr394-base` (= upstream PR #394 round-1 head `0c0fcc11`). Production code
is untouched — confirmed: `git diff pr394-base..pr/5` touches only harness
paths.

## Premise verdict

The PR's stated purpose — "layered, reproducible evidence" for the bugs found
in sgl-project/rbg#394 — is **valid and independently reproducible**:

- P1 (unit): the round-1 harness commit `f592f533`, grafted onto the PR base,
  regenerates `results/layer1-gotest.txt` exactly — B1_Overflow FAIL,
  B1_NoCapOverflow FAIL, B5_OffByOne PASS (2×base canary), B4_NegativeDelayBypass FAIL.
- P2 (integration): the round-1 envtest specs fail exactly as recorded —
  B2 stuck at `RestartCount=5` (120 observations, never 1), B4b RoleInstance
  accepts `baseDelaySeconds=-30` (err = nil) while RBG rejects it.
- The bug mechanisms are all visible in the base production code
  (`int64(base)<<(rc)` overflow in `calculateRestartDelay`, the monotonic
  `liveRestartCount > newStatus.RestartCount` preserve in `updateStatus`,
  negative delay falling through `checkRestartBackoff`).

The harness's *substance* is sound. The findings below are about the branch's
own consistency, not about the bugs it proves.

## Observed vs expected

| ID | Finding | Severity | Expected (fixed) | Observed (2026-09-29) | Verdict |
|----|---------|----------|------------------|-----------------------|---------|
| F1 | Harness Layers 1–2 do not compile against the PR's own base | major | both packages build at head | `go test -c` fails: `undefined: workloadsv1alpha2.RestartPolicyConfig` (sync test:165, envtest test:238), `checkRestartBackoff` ctx/signature mismatch (sync test:197,203); base WITHOUT the harness files builds rc=0 | **CONFIRMED** |
| F2 | README references evidence not present on the branch | minor | referenced files exist | `results/live-offbyone-evidence.log` never committed (README:191); `results/reverify/` (rounds 2–5 raw output) gitignored | **CONFIRMED** |
| F3 | PR body describes round-1 state, head is round-3–5 | minor | body matches head | body says "B5 tests are bug-canaries … flip to red", head test asserts `firstRealizedDelay == base` (contract); body table "all confirmed", README says 4/5 FIXED upstream | **CONFIRMED** (mechanical via `gh pr view`) |
| F4 | L3 scripts are round-1-vintage vs. the fixed CRDs they'd run against | nit | scripts match target CRD shape | `20-live-negative-delay.sh` posts `spec.baseDelaySeconds`; round-5 CRD (`d0db8af0`) nests it under `spec.restartPolicy.baseDelaySeconds` with `minimum: 0` → field would be pruned, probe passes for the wrong reason | reasoning only |
| P1 | round-1 unit evidence regenerates | — | REPRODUCED while base = round-1 head | all 5 test outcomes match the committed log | **REPRODUCED** |
| P2 | round-1 integration evidence regenerates | — | REPRODUCED while base = round-1 head | B2 stuck at 5, B4b err=nil | **REPRODUCED** |

## Why F1 matters (and its blast radius)

The PR head mixes harness vintages: the Go tests were adapted in rounds 3–5 to
the *upstream* PR #394 API (`RestartPolicyConfig`, ctx-less
`checkRestartBackoff(instance, fresh, pods, inactive)`), but this branch's base
is pinned to the round-1 head, where `RoleInstanceSpec` has bare
`RestartPolicy RestartPolicyType` + top-level delay fields and
`checkRestartBackoff(ctx, instance, pods, inactivePods)`. Consequences:

1. `go test ./pkg/reconciler/roleinstance/sync/` fails to **build** at the PR
   head — the package's *entire* test suite (including the pre-existing
   `instance_scale_test.go`) cannot run. Same for the envtest package.
2. The README's round-1 "how to run" commands fail on a fresh checkout of this
   branch; the committed round-1 evidence is regenerable only by digging out
   the round-1 harness commit (`f592f533`) from history.
3. Mitigation (why this is major, not blocker): the documented one-command
   path (`re-verify.sh <fixed-ref>`) grafts the harness onto *other* refs,
   where it compiles — and reports `HARNESS-UPDATE` on build failure rather
   than a false verdict. The branch is reviewer state, not production code.

Suggested fix: rebase the harness's base to the current upstream PR #394 head
(`pr394-base` → `d0db8af0`), or carry per-round harness variants
(e.g. `restart_backoff_verify_test.go` behind a build tag per API shape).

## Files

```
scripts/10-compile-at-head.sh      F1: build check at head + control at base
scripts/20-round1-unit-repro.sh    P1: round-1 unit evidence regeneration
scripts/30-round1-envtest-repro.sh P2: round-1 envtest evidence regeneration
scripts/40-evidence-files.sh       F2: evidence availability
scripts/re-verify.sh               one command: all checks + verdict table
results/                           captured outputs (committed)
verify-manifest.json               findings/layers map (pr = PR #5 URL)
.last-reviewed                     da0e366d (PR #5 head at review time)
```

## Re-verify after the author updates the branch

```bash
bash docs/verification/pr5-harness-claude/scripts/re-verify.sh
```

Exit code 0 iff F1/F2 are REFUTED (branch fixed). P1/P2 are expected to stay
REPRODUCED while the base is the round-1 PR #394 head — they stop mattering
once the harness is rebased onto fixed code.
