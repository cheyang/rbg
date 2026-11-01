# Verification — PR #538 "feat: implement stateful selective scale-down" (Reviewer B / Codex)

- PR: https://github.com/sgl-project/rbg/pull/538
- Reviewed head: `07ae1194c0481b2f39db2d41c32c08a6b2123b1d` (base `4a0765f78b0930114a4b73a64433ee8f27c34372`)
- Harness branch: `verify/selective-scale-down-codex` (additive only; production code untouched — `git diff 07ae1194 -- ':(exclude)docs/verification/**' ':(exclude)*_verify_test.go'` is empty)

## P0 — Premise (runs against BASE, not the patch)

Issue #499 (OPEN): "Stateful mode does not expose persistent ordinal retirement."
Base `4a0765f7` contains zero references to `excluded-ordinals` / `committed-skip-ordinals`
outside `keps/` and `doc/` (see `results/premise-base.txt`); the base commit is the
docs-only KEP PR #513. **Verdict: Confirmed** — the feature gap exists; component named
by the issue (Stateful RIS membership) matches the patched component.

## Layer results (head 07ae1194)

| Layer | Command | Result |
|---|---|---|
| Unit + race | `go test -race -count=1 ./api/workloads/ordinals/... ./api/workloads/v1alpha2/... ./api/workloads/v1alpha1/... ./pkg/utils/... ./pkg/discovery/... ./pkg/reconciler/... ./internal/controller/workloads/...` | PASS (`results/unit-race.txt`) |
| Integration (real apiserver via envtest 1.31) | `go test ./test/envtest/testcase/selective_commit/... ./test/envtest/testcase/webhook/... ./test/envtest/testcase/rbg/...` | PASS (`results/envtest.txt`) |
| Live (kind cluster) | skipped per debate policy | — |

## Findings

| ID | Severity | Claim | Evidence |
|---|---|---|---|
| F1 | blocker (CI gate) | `make lint` fails on PR-added code: gocyclo 45>30 on `reconcileSelectiveMembership`, goconst on `"InvalidCommittedOrdinals"` (4 occurrences), prealloc ×3 in new test files. CI `lint` job 124766651800 is red because of this PR. | `results/lint-repro.txt` (local gocyclo repro + CI log lines) |
| F2 | minor | `validateCommittedMembership` drops *all* `FailedScale` conditions (any reason) when the committed record is invalid, replacing unrelated scale-failure history. | canary `TestVerifyF2FailedScaleConditionsReplacedOnInvalidRecord` (`results/f2-canary.txt`) |

F1 is a CI-gate finding: it does not map to a runtime contract test, so it is not in
`verify-manifest.json`'s `findings`; re-verification is `make lint` going green in CI.

## Harness-bites check (F2 canary)

Scoped the condition filter to `condition.Reason == "InvalidCommittedOrdinals"` (the
proposed fix) → canary flipped to FAIL ("unrelated FailedScale condition survived").
Reverted; production diff empty again; canary green. See `results/f2-canary.txt`.

## Continuing after a fix revision

```bash
git fetch https://github.com/cheyang/rbg.git verify/selective-scale-down-codex
git checkout verify/selective-scale-down-codex
bash docs/verification/selective-scale-down-codex/scripts/re-verify.sh   # no sha needed; resolves PR head from manifest.pr
```

Polarity: F2 is a canary — green means "current replace semantics still in effect",
red means the semantics changed (invert the assertions if the change is the scoped
filter). The PR's own suites (ordinals property tests, selective_commit envtest,
webhook admission envtest, rbg selective scale-down envtest) are all contract tests
and must stay green; `re-verify.sh` runs the unit layer plus the harness canary and
reports per-finding Fixed/Still-broken.
