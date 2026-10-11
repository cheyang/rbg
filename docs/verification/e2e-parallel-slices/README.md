# e2e-parallel-slices — PR #494 verification

Reproducible evidence for the findings raised while reviewing
https://github.com/sgl-project/rbg/pull/494 (test(ci): split full E2E suite into
parallel slices), head `ecd1120a3dbbc2f0d6bc4399e7b1bbe59c07eaee`, base
`798e5d6f5eda5f8f67bba35831381921c88d1e3f`.

Harness lives on branch `verify/e2e-parallel-slices-claude`; **production code is
untouched** — only this docs tree, `scripts/re-verify.sh` and the additive test
package `test/e2e/cislices/` were added.

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit (deterministic) | label→slice routing via ginkgo `--dry-run` (no cluster needed), workflow static structure | `go test ./test/e2e/cislices/ -v -timeout 15m` |
| 2. Integration | (folded into 1) ginkgo JSON reports parsed exactly as CI would select specs | same command |
| 3. Live (GitHub Actions) | real workflow run | skipped this round (debate policy: unit+integration only); see liveNote in verify-manifest.json |

> Polarity: `TestMatrixSlicesCoverLegacyDefaultSuite` is a **contract** test (the
> guard — fails if a spec loses coverage). The other three are **bug-canaries**:
> they pass on the current (defective) code and flip red when the author fixes
> the finding.

## Problem premise (P0)

| | |
|---|---|
| Claimed symptom | "The current E2E workflow is monolithic and expensive: multiple jobs rebuild the same controller and CRD-upgrader images, repeat the same Kind/LWS/Helm setup, and run long suites sequentially." |
| Linked issue | none |
| Reported component | CI workflow |
| Patched component | same |
| Component match | Yes |
| **Verdict** | **Confirmed** — base has 5 e2e jobs each rebuilding images and repeating setup; default job runs 106 specs in one gulp |

The second half of the premise — *"keeps full E2E coverage on every eligible PR"*
— is **partially refuted**: default-scheduler coverage is fully preserved
(union of the 8 matrix slices == the legacy 106-spec default suite, measured),
but the manifest-installation job drops from the full 106 specs to 12 (F1).

## Summary of results

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| P0 | monolithic, image-rebuilding CI exists on base | 1 | Confirmed | results/static-checks.txt; base workflow has 5 duplicated setup jobs |
| F1 | manifest-install e2e coverage cut 106 → 12 specs | 1 | Confirmed (canary green = defect present) | results/coverage-summary.txt, results/base-manifest-coverage.txt |
| F2 | fail-closed label routing, no automated guard | 1 | Confirmed (guard passes today; bite check shows it catches drift) | results/coverage-summary.txt; bite check below |
| F3 | matrix job timeout == go-test timeout, no headroom → hang loses all diagnostics | 1 | Confirmed (static, canary) | results/coverage-summary.txt (8× "no setup headroom") |
| F4 | volcano diagnostics query the wrong PodGroup CRD | 1 | Confirmed (static, canary) | results/coverage-summary.txt |
| F5–F7 | weaker caBundle gate / duplicated specs / local default change | — | Reasoning only (nits) | findings doc |

Measured slice sizes (ginkgo dry-run): lifecycle 12, workload 9, update 16,
rollout 21, feature 17, webhook-rbac 6, scheduler 3, v1alpha1 28 → union 106 ==
legacy default suite 106. Volcano 8 + scheduler-plugins 8. Manifest slice 12.
6 specs run in 2 slices each (workload+rollout overlap). LOST: 0.

## Harness-bites check (run 2026-10-11)

Mutated `filter: "lifecycle && …"` → `"lifecycle-nonexistent && …"` in the
workflow, re-ran the guard: **FAIL with 12 LOST specs** — the guard bites.
Reverted (`git checkout -- .github/workflows/e2e-test.yml`); `git status` clean
except the harness itself. See `TestWorkflowBiteGuardSelfCheck` for the recipe.

## Proposed fixes (NOT applied here)

- **F1 (major)**: either widen the manifest job's `E2E_LABEL_FILTER` (e.g. run
  the full matrix union on the manifest cluster, accepting the wall-clock), or
  add a periodic/scheduled full-coverage manifest job, or explicitly document
  the reduction in `test/e2e/ci-optimization-plan.md` and the PR body (which
  currently claims full coverage is kept).
- **F2 (major)**: land the guard — add `go test ./test/e2e/cislices/` (or an
  equivalent `ginkgo` dry-run check) as a fast CI job/step so a future unlabeled
  spec cannot silently drop out of CI. The old default filter was fail-open;
  the matrix is fail-closed and needs this tripwire.
- **F3 (minor)**: give each matrix slice headroom, e.g. job
  `timeout_minutes: N` and `E2E_TIMEOUT=$((N-5))m`, and change the diagnostics
  steps to `if: failure() || cancelled()` so a job-timeout cancellation still
  collects controller logs and Kind logs.
- **F4 (nit)**: in the volcano job's Collect diagnostics, query
  `podgroups.scheduling.volcano.sh` (Volcano's CRD) like the base workflow did
  (keep `scheduling.x-k8s.io` only in the scheduler-plugins job).

## Continuing after the fix (possibly on another machine)

The harness is on branch `verify/e2e-parallel-slices-claude` of the reviewer
fork (cheyang/rbg); it grafts onto whatever the fixed code is.

1. ```bash
   git fetch https://github.com/cheyang/rbg.git verify/e2e-parallel-slices-claude
   git checkout <fixed-ref>
   git checkout FETCH_HEAD -- docs/verification/e2e-parallel-slices test/e2e/cislices scripts/re-verify.sh
   ```
2. Prereqs: Go toolchain only (no cluster; ginkgo dry-run never runs BeforeSuite).
3. `go test ./test/e2e/cislices/ -v -timeout 15m` — or `bash scripts/re-verify.sh`
   (auto-resolves the PR head from `verify-manifest.json`'s `pr` URL).
4. Polarity: the contract guard must stay green; the three canaries
   (F1/F3/F4) going **red** means the corresponding defect is fixed — invert
   them (or promote to contract tests) once red.
5. `.last-reviewed` = `ecd1120a…`; advance it after each review round.

### Kickoff prompt for a fresh agent
```text
Continue a verification task on branch verify/e2e-parallel-slices-claude
(https://github.com/cheyang/rbg). Background: a review of
https://github.com/sgl-project/rbg/pull/494 produced findings F1–F4; a harness
in test/e2e/cislices reproduces them (see docs/verification/e2e-parallel-slices/README.md).
The PR is now fixed at <ref>. Graft the harness onto the fixed code, re-run
`go test ./test/e2e/cislices/ -v -timeout 15m` (no cluster needed), mind the
polarity table (F2 is a contract test that must stay green; F1/F3/F4 are
canaries that must flip red when fixed), run the harness-bites check, and
report an observed-vs-expected table.
```

## Round 2 (2026-10-12) — incremental, head unchanged, base advanced

PR head is still `ecd1120a3dbbc2f0d6bc4399e7b1bbe59c07eaee` (no new commits since
round 1; PR has 0 comments, remains OPEN and mergeable), so every round-1 finding
carries over unchanged. What did change: `origin/main` advanced
`798e5d6f → 79f7e585` (single commit `79f7e585 fix: recover stateful rollout stalls
(#484)`, touching only `internal/…/statefulmode/` and `test/envtest/…` — **no
overlap with this PR's files**, mergeable per GitHub).

Base-change contract re-checks (all pass):
- merge conflict: none (gh `mergeable: MERGEABLE`; disjoint file sets);
- coverage counts (S3 invariant): the base commit adds no `test/e2e` specs — the
  matrix union re-measured at head still equals the 106-spec legacy default suite;
- CRD set (S7): `config/crd/bases` still holds 10 CRDs (base commit touches none);
- release-test.yml (S1): untouched by the base commit — still the bare consumer;
- Q1 branch protection: re-queried — `main` still unprotected, the active
  `Default-rule` ruleset (9876266) still has no `required_status_checks` rule.
- Residual: the green CI run on head (2026-10-10) predates the base advance; the
  merged-tree interaction (rollout slice × stateful rollout recovery) is untested
  — recommend an update-branch/rebase re-run before merge (filed as Q2, advisory).

### Harness extensions this round

Added `test/e2e/cislices/consumers_test.go` — contract repros for the findings
this reviewer carries in the round ledger that previously had tests only on the
*other* reviewer's branch, so this branch alone now re-runs the full set:

| Test | Ledger ID | Polarity | Round-2 result at head |
|------|-----------|----------|------------------------|
| TestReleaseTestBareMakeTestE2eKeepsV1alpha1Suite | S1 | contract | FAIL (defect present: 2 bare call sites, L218/L363) |
| TestWebhookCABundleGateRequiresEveryWebhook | S5 | contract | FAIL (flattening jsonpath gate; semantic demo: 2-webhook fixture with one unpatched sibling yields non-empty `cHVueGVk`) |
| TestSetupGateVerifiesAllCommittedCRDs | S7 | contract | FAIL (3 of 10 CRDs verified) |

Harness-bites (all reverted afterwards; `git status` clean except the harness):
- S1 fix A (E2E_LABEL_FILTER at both release-test.yml call sites) → test green;
- S1 fix B (Makefile default inclusive again) → test green;
- S5 fix (jq `all(.webhooks[]; …)` gate) → test green;
- S7 fix (`for f in config/crd/bases/*.yaml` loop) → test green.

### Round-2 full-suite results

`go test ./test/e2e/cislices/ -v -timeout 15m` (raw: `results/round2/unit.json`):

| Test | ID | Result | Meaning |
|------|----|--------|---------|
| TestReleaseTestBareMakeTestE2eKeepsV1alpha1Suite | S1 | FAIL | release-path v1alpha1 loss still present |
| TestManifestSliceRunsOnlySmokeSubset | S2 | PASS (canary) | manifest 106→12 cut still present |
| TestMatrixSlicesCoverLegacyDefaultSuite | S3 invariant | PASS | union still == legacy suite; the S3 finding (no guard landed in CI) remains open |
| TestMatrixSliceTimeoutEqualsJobTimeout | S4 | PASS (canary) | timeout == job deadline still present |
| TestWebhookCABundleGateRequiresEveryWebhook | S5 | FAIL | any-webhook gate still present |
| TestVolcanoDiagnosticsQueryKubePodGroupCRD | S6 | PASS (canary) | wrong PodGroup group still present |
| TestSetupGateVerifiesAllCommittedCRDs | S7 | FAIL | 3/10 CRD gate still present |

`.last-reviewed` stays `ecd1120a…` (head unchanged — nothing to advance).
