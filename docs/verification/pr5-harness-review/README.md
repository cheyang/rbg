# PR #5 (cheyang/rbg) harness review — verification by Reviewer B (Codex)

Subject under review: https://github.com/cheyang/rbg/pull/5
("test: verification harness reproducing PR #394 restart-backoff bugs"),
head `da0e366d`, base `pr394-base` (`0c0fcc11` = sgl-project/rbg#394 round-1 head).
PR #5 is itself a *verification harness* PR for the already-merged upstream
sgl-project/rbg#394 (merged 2026-07-23, final head `d0db8af0`).

## P0 — premise of PR #5: Confirmed

Claim: the harness reproduces 4 real bugs (B1, B2, B4, B5) in PR #394 round-1
code, and its round-5 verdict table matches the merged head.

Reproduced independently (unit + integration layers; live layer skipped per
debate instructions):

| Layer | Run | Observed | Expected per PR #5 | Verdict |
|-------|-----|----------|--------------------|---------|
| L1 unit, round-1 harness (`f592f533`) on base `0c0fcc11` | `results/l1-base-round1-harness.txt` | `calc(30,600,58)=600`, `calc(30,600,59)=0` (B1 FAIL); first realized delay `60s` with base 30 (B5 canary PASS); `checkRestartBackoff(-30)=0s` (B4a FAIL) | exactly as claimed | **Confirmed** |
| L2 envtest, round-1 harness on base `0c0fcc11` | `results/l2-base-round1-harness.txt` | seeded RestartCount=5 + LRT 11min ago + crash → count stuck at 5 for full 60s window (B2 FAIL); apiserver rejects `-30` on RBG, accepts on RoleInstance (B4b FAIL) | exactly as claimed | **Confirmed** |
| L1+L2, current harness (PR #5 head) grafted onto merged head `d0db8af0` | `results/l1-merged-head.txt`, `results/reverify-vs-merged-head.txt` | B1 pass=2, B5 pass=1, B2 pass=1, B4b pass=1, B4a fail=1 (`checkRestartBackoff(-30)=0s`) | matches README round-5 table exactly | **Confirmed** |

## Findings on PR #5 itself

| ID | Sev | Claim | Evidence |
|----|-----|-------|----------|
| F1 | major | The branch does not compile its own test packages against its own base: `go vet` fails with `undefined: workloadsv1alpha2.RestartPolicyConfig` in both added test files (they were adapted to PR #394 round-3+ API while the base stayed at round-1 head). Merging breaks `go test ./...` on `pr394-base`; the README's per-layer quick-start fails from this branch; the "harness still bites on pre-fix code" claim no longer holds — `re-verify.sh 0c0fcc11` now reports HARNESS-UPDATE (compile), not STILL-BROKEN. | `results/l1-pr5-branch-vet.txt`, `results/reverify-vs-round1-head.txt` |
| F2 | minor | Polarity doc drift: test-file headers still say "expected to FAIL on the PR head" (B1/B5 pass since round 3, B2 since round 4); README section-4 table still lists B5 tests as bug-canaries although commit `ecf0e69e` promoted them to contract tests and the manifest says `contract`. | `results/stale-docs.txt` |
| F3 | minor | Evidence gaps: README references `results/live-offbyone-evidence.log` (absent) and `results/reverify/` for rounds 2-5 raw output (gitignored → never committed). Only round-1 artifacts are auditable from the branch. | `results/stale-docs.txt` |
| F4 | minor | L3 scripts (`rbg-backoff.yaml`, `20-live-negative-delay.sh`) use the round-1 flat schema (`restartPolicy: <scalar>`, top-level `baseDelaySeconds`); merged CRDs nest `restartPolicy` as an object on both RBG leaderWorkerPattern and RoleInstance, so the applies now fail schema validation for the wrong reason. | `results/crd-schema-vs-l3-scripts.txt` |
| F5 | minor | B4a is kept as a hard contract test although the reviewer's own posted round-4 comment says it is "fine to leave as-is"; `re-verify.sh` therefore can never exit 0 on the merged code (`RESULT: not all findings fixed`), permanently diluting the automation signal. | `results/reverify-vs-merged-head.txt` |
| F6 | nit | Counting inconsistency: PR body lists 4 bugs (B1,B2,B4,B5); README/round-4 draft count 5 (B4 split into a/b); DRAFT-round2 says "1 of 4 resolved" while README round-2 table says "1 of 5 fixed". | docs inspection |

## Re-run

- F1 contract check: `bash docs/verification/pr5-harness-review/scripts/re-verify.sh`
  from a checkout of this branch (exit 0 once the branch compiles its harness).
- P0 replay: check out `0c0fcc11`, `git checkout f592f533 -- pkg/reconciler/roleinstance/sync/restart_backoff_verify_test.go test/envtest/testcase/restart_policy/backoff_bug_verify_test.go`, then
  `go test ./pkg/reconciler/roleinstance/sync/ -run RestartBackoffVerify -v` and
  `KUBEBUILDER_ASSETS=$(bin/setup-envtest use 1.31.0 --bin-dir bin -p path | xargs echo $PWD/) go test ./test/envtest/testcase/restart_policy/... -run TestRestartPolicy -ginkgo.focus PR394`.
- Fixed-state replay: from a clean checkout of `origin/pr/5`,
  `bash docs/verification/pr394-restart-backoff/scripts/re-verify.sh` (auto-fetches the merged head).

Production code untouched by this branch; only `docs/verification/pr5-harness-review/` is added.
