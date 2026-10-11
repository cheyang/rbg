# e2e-ci-parallel-slices — bug verification

Reproducible evidence for the findings raised while reviewing
https://github.com/sgl-project/rbg/pull/494 ("test(ci): split full E2E suite into parallel
slices", head `ecd1120a3dbbc2f0d6bc4399e7b1bbe59c07eaee`, base `798e5d6f`).

The code under review is CI configuration (`.github/workflows/e2e-test.yml`, the new
`.github/actions/setup-e2e-cluster/action.yaml`, `Makefile` e2e targets) plus ginkgo label
annotations on the e2e specs. No production controller code is touched by the PR, so the
"unit layer" here exercises the *CI logic*: real ginkgo label selection via
`go test -ginkgo.dry-run` (no cluster needed — `framework.NewFramework` only requires a
kubeconfig file to exist, it never dials during registration/dry-run), plus static checks of
the workflow/action text. The one thing this harness deliberately does NOT try to reproduce
locally is a full GitHub Actions run (that is what the PR's own CI run demonstrates).

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit (CI logic) | label-filter coverage math, Makefile defaults vs. bare consumers, caBundle gate semantics, workflow/action structure | `go test ./test/ciharness/ -run TestPR494 -v` |
| 2. Static | yamllint on workflow + action; e2e packages compile | `bash docs/verification/e2e-ci-parallel-slices-codex/scripts/static_checks.sh` |
| 3. Live | full GitHub Actions run on the PR (author-reported green on the source fork) | out of scope for this harness (see liveNote in verify-manifest.json) |

All harness tests are **contract** polarity: they FAIL on the PR head while the finding is
present and PASS once fixed. `TestPR494_MatrixUnionCoversOldSuite` is the guard for the PR's
central claim and PASSES already at the PR head (hypothesis cleared).

## Problem premise (P0)

| | |
|---|---|
| Claimed symptom | "The current E2E workflow is monolithic and expensive: multiple jobs rebuild the same controller and CRD-upgrader images, repeat the same Kind/LWS/Helm setup, and run long suites sequentially." (PR body) |
| Linked issue | none (no `fixes #N`; PR body states the problem) |
| Reported component | `.github/workflows/e2e-test.yml` (CI) |
| Patched component | same + new composite action + Makefile e2e targets + e2e spec labels |
| Component match | Yes |
| **Verdict** | **Confirmed** |
| Evidence | base workflow repeats the setup 5x: `kind create cluster` x5, `docker-build-controller` x5, LWS install x5, helm install x4, controller-readiness waits x5 (results/p0-premise.txt). The premise is structural; no runtime repro needed. |

## Summary of results

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| C0 | The 8-slice matrix + 3 standalone label filters still cover every spec the old workflow ran | 1 | Cleared (guard PASSES: old-world 116 specs == new-world 116) | results/harness-pr-head.json |
| C1 | LWS image tag rewrite (`${lws_image/:v/:}`) matches the image in the LWS v0.7.0 manifest | 1 | Cleared (`registry.k8s.io/lws/lws:0.7.0` on both sides) | results/lws-tag-rewrite.txt |
| F1 | Makefile `E2E_LABEL_FILTER` default now excludes v1alpha1, but `release-test.yml` still calls bare `make test-e2e` twice → the 28-spec v1alpha1 compat suite silently drops out of `/release-test` qualification | 1 | **Confirmed** (test FAILS on PR head; 28 dropped specs listed; consumers: release-test.yml L218, L363) | results/harness-pr-head.json |
| F2 | The new caBundle gate passes when ANY webhook in a configuration has a bundle; the base gate required ALL webhooks in BOTH configurations | 1 | **Confirmed** (partial-injection fixture: old gate rejects, new gate passes) | results/harness-pr-head.json |
| F3 | Volcano job diagnostics query `podgroups.scheduling.x-k8s.io` (scheduler-plugins group) instead of `podgroups.scheduling.volcano.sh` | 1 | **Confirmed** (workflow text vs. in-repo authority `pkg/scheduler/volcano/scheduler.go:54`) | results/harness-pr-head.json |
| F4 | Every matrix job sets `timeout-minutes` == the `E2E_TIMEOUT` go-test deadline; setup shares the same clock, so ginkgo's own timeout can never fire and a slow slice dies as an opaque job-level timeout racing diagnostics/report upload | 1 | **Confirmed** (all 8 matrix rows equal) | results/harness-pr-head.json |
| F5 | The setup readiness gate verifies 3 of the 10 committed CRDs (base verified all of `config/crd/bases`) | 1 | **Confirmed** (missing: instances, instancesets, rolebasedgroupscalingadapters, rolebasedgroupsets, rolebasedgroupwarmups, roleinstancesets, clusterengineruntimeprofiles) | results/harness-pr-head.json |

Notes logged by the guard, not failed on: 6 specs carry `{workload, update, rollout}` (or
`{smoke, workload, update, rollout}`) and therefore run in BOTH the `workload` and `rollout`
matrix slices — same cluster configuration, so pure duplicate compute (~6 rolling-update
specs per run). The `workload` filter could exclude `rollout` the way `update`/`feature` do.

## Per-finding detail

### F1 — bare `make test-e2e` consumers lose the v1alpha1 suite
- Mechanism: the PR changes the Makefile default from `!volcano && !scheduler-plugins` to
  `!v1alpha1 && !volcano && !scheduler-plugins` and compensates inside `e2e-test.yml` by
  adding a dedicated `v1alpha1` matrix slice — but `release-test.yml` (the maintainer-triggered
  `/release-test` qualification workflow) invokes bare `make test-e2e` at lines 218 and 363
  and was not updated.
- Command: `go test ./test/ciharness/ -run TestPR494_BareMakeTestE2EConsumersKeepFullSuite -v`
- Observed: 28 v1alpha1 specs selected by the old default, 0 by the new default; 2 bare
  consumers in release-test.yml. Expected: no bare consumer loses specs.
- Fix sketch: pass `E2E_LABEL_FILTER='!volcano && !scheduler-plugins'` at the two
  release-test.yml call sites (or keep the Makefile default inclusive and override only in
  e2e-test.yml).

### F2 — caBundle gate weakened
- Mechanism: base job used jq `(.webhooks | length > 0) and all(.webhooks[]; (.clientConfig.caBundle // "") != "")` per configuration; the action now checks that the
  flattened `{.webhooks[*].clientConfig.caBundle}` output is non-empty, which drops missing
  keys — one patched webhook masks unpatched siblings in the same configuration. (Both
  configurations are still required to be non-empty, so cross-config partial patching is
  still caught; the regression is strictly within one configuration's webhook list.)
- Command: `go test ./test/ciharness/ -run TestPR494_WebhookCABundleGateRequiresEveryWebhook -v`
- Observed: fixture with 2 webhooks (one with caBundle, one without) → old gate false, new
  gate true. Expected: gate requires every webhook to carry a bundle (restore the jq expr).

### F3 — volcano diagnostics query the wrong PodGroup API group
- `kubectl get podgroups.scheduling.x-k8s.io` in the volcano job can never return Volcano
  PodGroups (`podgroups.scheduling.volcano.sh`, per `pkg/scheduler/volcano/scheduler.go:54`
  and the gang e2e specs). Harmless to pass/fail (`|| true`) but the failure diagnostics
  silently lose the Volcano PodGroup dump the base job had.
- Fix sketch: `s/scheduling.x-k8s.io/scheduling.volcano.sh/` in the volcano job only.

### F4 — job timeout == go test deadline
- Setup (checkout, kind create, LWS, helm install, image pulls/loads) consumes the same wall
  clock as the matrix `timeout-minutes`, and the go test deadline `E2E_TIMEOUT` is set to the
  same value. A slice that runs long is killed by GitHub at the job deadline before ginkgo's
  own `-timeout` can fire; the failure()-gated diagnostics and the report upload then race
  the job cancellation instead of running after a clean ginkgo panic report. The base
  workflow had no job-level timeouts, so the go test deadline always won.
- Fix sketch: `timeout-minutes: ${{ matrix.timeout_minutes + 15 }}` (or +20 for volcano/sp).

### F5 — readiness gate checks 3 of 10 CRDs
- The base main job looped over `config/crd/bases/*.yaml`; the composite action hardcodes
  `rolebasedgroups`, `coordinatedpolicies`, `roleinstances`. A chart/manifest regression
  dropping any of the other 7 CRDs now surfaces only as a later, more confusing spec failure.
- Fix sketch: restore the `for f in config/crd/bases/*.yaml` loop in the readiness step.


## Round 2 (incremental, 2026-10-12)

**Delta reviewed:** `ecd1120a..ecd1120a` — the PR head is unchanged since round 1; there
were no new commits to review. The base branch moved `798e5d6f..79f7e585` (PR #484,
stateful-rollout recovery: controller code + envtest only). None of the contract surfaces
(Makefile, `.github/workflows/*`, `.github/actions/*`, `test/e2e/**`, `deploy/**`,
`config/crd/**) appear in that delta, so no round-1 base-side measurement is invalidated
(results/base-change-round2.txt). Post-merge interaction check: #484 adds no e2e specs, so
the fail-closed routing (S3) is not triggered by it; the rollout slice will simply exercise
the new recovery logic after merge.

**Manifest finding IDs now use the shared debate ledger IDs** (S1-S11). Mapping from the
round-1 codex-local IDs: F1->S1, F2->S5, F3->S6, F4->S4, F5->S7.

**Re-verify at head (unchanged):** S1, S4, S5, S6, S7 all STILL-BROKEN (contract tests still
red); coverage guard still green. CI on the head is the same green Actions run 38031782558
(no new runs; PR still open, mergeable, no new comments).

**New runnable tests this round** (additive, `test/ciharness/pr494_round2_test.go`) so every
open blocker/major has a repro on this branch:

| Ledger ID | Test | Polarity | Result at head |
|-----------|------|----------|----------------|
| S2 (AC4)  | TestPR494_ManifestJobKeepsBroadCoverageOrDocumentsCut | contract | FAIL — e2e-manifest selects 12/106 legacy specs, cut undocumented, full-coverage claim present |
| S3 mech.  | TestPR494_UnlabeledSpecGetsZeroCoverageAndStaysGreen | demo (passes by design) | PASS — no-routing-label filter selects 0 specs, `go test` exits 0: silent-zero-coverage mechanism confirmed |
| S3 (AC5)  | TestPR494_MatrixCoverageGuardWiredInCI | contract | FAIL — no union-coverage guard step in e2e-test.yml |

**Harness-bites (round 2):** S2 flips green via BOTH accepted fix routes — (a) widening the
e2e-manifest filter to `!volcano && !scheduler-plugins` (parity), and (b) withdrawing the
full-coverage claim + documenting the manifest subset in ci-optimization-plan.md (docs
route). S3 flips green when a `test-e2e-coverage-guard` step is added to the workflow. All
mutations reverted; production diff vs `origin/pr/494` is empty.

Raw results: results/harness-round2.json, results/reverify/ (layer output of re-verify.sh).

## Harness-bites check (Step 4)

All five fixes above were applied to the working tree simultaneously; the full harness then
went green (6/6 PASS). The fixes were reverted; production diff vs. `origin/pr/494` is empty
and the finding tests are red again. The only files this branch adds are
`docs/verification/e2e-ci-parallel-slices-codex/**` and `test/ciharness/**`.

## Continuing after the fix (possibly on another machine)

The harness is on branch `verify/e2e-ci-parallel-slices-codex` (production code untouched).

1. Graft and re-run in one step:
   ```bash
   bash docs/verification/e2e-ci-parallel-slices-codex/scripts/re-verify.sh   # resolves the current PR head itself
   ```
   or manually:
   ```bash
   git fetch https://github.com/cheyang/rbg.git verify/e2e-ci-parallel-slices-codex
   git checkout <fixed-ref>
   git checkout FETCH_HEAD -- docs/verification/e2e-ci-parallel-slices-codex test/ciharness
   go test ./test/ciharness/ -run TestPR494 -v
   ```
2. Prereqs: Go >= 1.25 toolchain only (layer 1), `yamllint` for the static script. No
   cluster, no KUBECONFIG (the harness writes its own fake one).
3. Polarity: every finding test is contract — green means fixed. The coverage guard
   (`TestPR494_MatrixUnionCoversOldSuite`) must STAY green.
4. If a fix restructures the workflow (renamed jobs, different matrix shape), tests that
   parse the YAML text will report harness-update; adjust the regexes in
   `test/ciharness/pr494_verify_test.go`.

### Kickoff prompt for a fresh agent
```text
Continue a verification task on branch verify/e2e-ci-parallel-slices-codex
(remote https://github.com/cheyang/rbg.git). Background: a review of
https://github.com/sgl-project/rbg/pull/494 produced findings F1-F5; a Go harness
reproduced them. The PR is now fixed at <ref>. Read
docs/verification/e2e-ci-parallel-slices-codex/README.md ("Continuing after the fix") and
follow it: graft the harness onto the fixed code, run `go test ./test/ciharness/ -run
TestPR494 -v` (all contract tests must go green, the coverage guard must stay green),
and report an observed-vs-expected table.
```
