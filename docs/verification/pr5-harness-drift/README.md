# PR cheyang/rbg#5 — harness/base drift verification

Reproducible evidence for the findings of Reviewer B (Codex) on
[cheyang/rbg#5](https://github.com/cheyang/rbg/pull/5)
("test: verification harness reproducing PR #394 restart-backoff bugs").

PR #5 adds only tests + docs on top of base `pr394-base` (= sgl-project/rbg PR #394
round-1 head `0c0fcc11`). The finding is that the harness files at the PR head
(`da0e366d`) were adapted across review rounds 2–5 to the *evolving upstream* PR #394
code and no longer match the PR's own base.

## Claims and verdicts

| ID | Claim | Layer | Verdict |
|----|-------|-------|---------|
| F1 | Harness test packages do not compile on the PR's own base; README's "still bites against buggy pr-394" claim is stale | unit + integration (compile) | **Confirmed** |
| F2 | README references evidence not tracked in the branch (`results/live-offbyone-evidence.log`, gitignored `results/reverify/`) | static | **Confirmed** |
| F3 | Handoff docs stale: Option B cherry-picks the round-1 harness (fails on post-refactor heads); L3 B4 script uses round-1 RoleInstance schema | unit (compile) + reasoning | **Confirmed** (Option B), reasoning only for L3 |
| F4 | manifest `prHeadFetch.remote="new-origin"` is machine-local dead config | static | Confirmed (nit) |
| F5 | `99-teardown.sh` `pkill -f 'cmd/rbgs/main.go'` kills any matching host process | reasoning only | Not run (nit) |

## F1 — the harness does not compile against `pr394-base`

PR head `da0e366d` = base `0c0fcc11` + harness. On the PR head:

```
$ go test -count=1 -run XXXNONE ./pkg/reconciler/roleinstance/sync/ ./test/envtest/testcase/restart_policy/
pkg/reconciler/.../restart_backoff_verify_test.go:165:38: undefined: workloadsv1alpha2.RestartPolicyConfig
pkg/reconciler/.../restart_backoff_verify_test.go:197:40: cannot use newInstance(30) ... as context.Context value in argument to c.checkRestartBackoff
pkg/reconciler/.../restart_backoff_verify_test.go:203:40: (same)
FAIL sigs.k8s.io/rbgs/pkg/reconciler/roleinstance/sync [build failed]
test/envtest/.../backoff_bug_verify_test.go:238:38: undefined: workloadsv1alpha2.RestartPolicyConfig
FAIL sigs.k8s.io/rbgs/test/envtest/testcase/restart_policy [build failed]
```

Mechanism (proven, see `results/`):

- The base branch has **no** `RestartPolicyConfig` type (added upstream in PR #394
  round 2) and its `checkRestartBackoff` has signature `(ctx, instance, pods,
  inactivePods)`; the PR's test files use the round-2+/round-3+ API.
- The **round-1** harness commit `f592f533` (same base) compiles and reproduces the
  documented round-1 results exactly: B1/B4a unit FAIL, B5 canaries PASS
  (`results/f1-round1-harness-unit.txt`), B2/B4 envtest FAIL, 0/2 passing
  (`results/f1-round1-harness-envtest.txt`). So the drift was introduced by the later
  "adapt harness" commits, not present from the start.
- The PR's own `re-verify.sh` against the base `0c0fcc11` reports **HARNESS-UPDATE
  (did not compile/run) for all five findings** (`results/f1-reverify-against-base.txt`),
  contradicting the README claim "Validated against the buggy pr-394: it reports
  B1/B4a/B2/B4b STILL-BROKEN and B5 STILL-PRESENT — i.e. it still bites."
- The same harness against upstream PR #394 head `d0db8af0` compiles and reproduces
  the README round-5 table exactly (B1/B5/B2/B4b FIXED, B4a STILL-BROKEN;
  `results/f1-reverify-against-upstream-head.txt`) — i.e. the harness now targets the
  upstream head, not this PR's base.

Impact: merging PR #5 leaves `pr394-base` with two test packages that fail to build
(`go test ./...`, `go vet ./...` fail); the documented "run Layer 1/2 from this branch"
flow is broken. (CI workflows trigger only on PRs to `main`, so CI does not catch it.)

## F2 — referenced evidence missing from the branch

`results/f2-evidence-paths.txt`: `results/live-offbyone-evidence.log` (cited in the B5
section) does not exist; rounds 2/4/5 cite `results/reverify/` for raw output, but that
directory is gitignored (`.gitignore` line 1) and untracked, so the round 2–5 verdicts
have no committed raw evidence. Regenerable via `re-verify.sh`, hence minor.

## F3 — stale handoff instructions

README "Option B — `git cherry-pick f592f533`" grafts the **round-1** harness onto the
fixed code; verified it fails to compile against upstream head `d0db8af0`
(`results/f3-optionB-compile.txt`: unknown fields `BaseDelaySeconds`/`MaxDelaySeconds`,
old `checkRestartBackoff` signature). Options A and B silently install two different
harness generations. The L3 B4 script (`20-live-negative-delay.sh`) also targets the
round-1 RoleInstance schema (flat `spec.baseDelaySeconds`); against the round-2+ CRDs
the create is rejected for schema-type reasons and would be misread as "gap fixed".

## How to re-run

From a checkout of this branch (`verify/pr5-harness-drift-codex`):

```bash
bash docs/verification/pr5-harness-drift/scripts/re-verify.sh            # resolves PR #5 head via manifest.pr
bash docs/verification/pr5-harness-drift/scripts/re-verify.sh <sha>      # explicit ref
```

The script checks F1 (harness packages compile), F2 (README-referenced evidence
tracked), F3 (Option B no longer points at the unqualified round-1 commit) against the
given/current PR head and exits 0 iff all pass. All checks are **contract** polarity:
they fail on the reviewed head `da0e366d` and should pass once fixed.

## Continuing after a fix

All durable state is on this branch. `.last-reviewed` records the reviewed head
(`da0e366d`). After the author updates PR #5 (e.g. retargets the base to the upstream
head the harness matches, or restores round-1-compatible harness files), run the
command above with no arguments; it fetches the current PR #5 head machine-independently.
Live layer: none (no cluster needed for these findings).
