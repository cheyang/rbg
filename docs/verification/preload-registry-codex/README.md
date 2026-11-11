# preload-registry-codex — bug verification (PR #567)

Reproducible evidence for the findings raised while reviewing
https://github.com/sgl-project/rbg/pull/567
("ci: pick e2e image registry by environment (docker.io / aliyun)", head `ec9c88cf`).

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit | `hack/preload-e2e-images.sh` against stubbed `docker`/`kind`; coverage of the e2e-referenced image set derived from `test/` sources; retry/validation behavior; frozen base fixture for the premise | `go test ./test/verification/preload/ -count=1 -v` |
| 2. Integration (CI differential) | the real GitHub Actions environment: PR-head `E2E rollout` job vs same-day base `E2E rollout` job | captured, not re-runnable offline: `results/ci-differential-evidence.txt` |
| 3. Live | skipped (per round policy; the CI run IS the live env) | — |

> Polarity: contract tests FAIL on the reviewed head and PASS once fixed. The premise
> canary asserts the frozen base behavior and must keep PASSING.

## Problem premise (P0)

| | |
|---|---|
| Claimed symptom | "Release Test run 42213206716 failed in `e2e-test-helm` at the 'Pre-load e2e test images' step … connection reset by peer … The three inline copies in `release-test.yml` had no retry either, so a single transient reset failed the whole job." (quoted from the PR body) |
| Linked issue | none |
| Reported component | CI pre-load steps of `release-test.yml` / `setup-e2e-cluster` |
| Patched component | same (new `hack/preload-e2e-images.sh` + wiring) |
| Component match | Yes |
| **Verdict** | **Confirmed** for the no-retry flake (fixtures/base-release-test-preload.sh fails on the first transient pull; the head script survives it). The original failed attempt is not inspectable (job was retried green), but the mechanism is proven. |
| Sub-claim | **Refuted**: "the e2e pods reference the short name `nginx:latest` … The pre-loaded images were never used". False — `test/utils/utils.go` sets `DefaultImage = "registry.cn-hangzhou.aliyuncs.com/acs-sample/nginx:latest"` (26 usage sites via the shared builders, `PullIfNotPresent`); the old preload loaded exactly that name and the pods used it. Only `test/e2e/testcase/v1alpha2/ris.go` uses short `nginx:latest`. |

## Summary of results

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| P0 | base release-test inline pre-load has no retry → one transient reset fails the job | 1 | Confirmed | `TestBaseInlinePreloadFailsOnFirstTransientPull` PASS (base fixture fails after exactly 1 pull); head survives (`TestNewScriptSurvivesTransientPullFailure` PASS) |
| F1 | new preload never loads `registry.cn-hangzhou.aliyuncs.com/acs-sample/nginx:latest` (the e2e DefaultImage) into kind — not in docker.io mode (never pulled), not in aliyun mode (re-tagged to `nginx:latest`; `docker image save name:tag` drops sibling tags, moby v25 `image/tarexport/save.go parseNames`) → pods must pull the intermittently-unreachable Hangzhou registry at runtime | 1 + 2 | **Confirmed** | unit: `TestPreloadCoversE2EReferencedImages_DockerIO`, `TestPreloadCoversDefaultImage_Aliyun` FAIL; CI: PR run 43031453847 `E2E rollout` FAILURE (pods `ContainerCreating` ≥7.5 min, suite 2400s timeout) vs base run 43028395429 SUCCESS in 12m56s — `results/ci-differential-evidence.txt` |
| F2 | docker.io mode drops `registry-cn-hangzhou.ack.aliyuncs.com/dev/patio-runtime:v0.2.0` (preloaded by every old step; injected into pods by the engine-runtime tests via `utils.CreatePatioRuntime`) → same runtime-pull exposure | 1 | **Confirmed** (code path); runtime impact not observed this run (those jobs got lucky) | `TestPreloadCoversPatioRuntime_DockerIO` FAIL |
| F3 | aliyun mode maps `nginx:1.25` ← `acs-sample/nginx:latest`, serving latest-content as 1.25; the registry has no 1.25 tag at all (`/v2/acs-sample/nginx/tags/list` → `["latest"]`, checked 2026-11-12) | 1 | Confirmed | `TestAliyunMirrorCarriesSameTag` FAIL |

Observed vs expected on head `ec9c88cf`:

```
--- FAIL: TestPreloadCoversE2EReferencedImages_DockerIO   (acs-sample + patio missing)
--- FAIL: TestPreloadCoversPatioRuntime_DockerIO
--- FAIL: TestPreloadCoversDefaultImage_Aliyun            (acs-sample name missing)
--- FAIL: TestAliyunMirrorCarriesSameTag                  (latest tagged as 1.25)
--- PASS: TestBaseInlinePreloadFailsOnFirstTransientPull  (premise canary)
--- PASS: TestNewScriptSurvivesTransientPullFailure       (retry works)
--- PASS: TestPullRetryExhaustionFailsJob                 (exhaustion still fails)
--- PASS: TestInvalidRegistryModeRejected                 (bad mode rejected)
```

Harness-bites check: applying a candidate fix (add `acs-sample/nginx:latest|-` and
`patio-runtime|-` entries, map `nginx:1.25` to `-`) flips all four contract tests to
PASS; reverting restores the failures and leaves the production tree byte-identical
(`results/harness-bites.txt`).

## Proposed fixes (NOT applied to production here)

- **F1**: the preloaded set must contain the names the pods reference. Any of:
  (a) keep pulling `acs-sample/nginx:latest` (and pause mirror etc.) and load it under
  its own full name in both modes — the Hangzhou dependency stays but moves back to the
  retried preload step;
  (b) pull `nginx:latest` from docker.io and `docker tag` it to the acs-sample name
  before saving, loading both tags explicitly (removes the Hangzhou dependency
  entirely; content-equivalent mirror);
  (c) repoint `test/utils.DefaultImage` at a docker.io name (touches test code;
  breaks China-side runners that motivated the aliyun name in the first place).
  (b) matches this PR's stated intent best.
- **F2**: preload patio-runtime in docker.io mode too (option (a) entry with `-`),
  or accept and document the residual ACK-reachability flake for the engine-runtime
  and warmup tests.
- **F3**: map `nginx:1.25` to `-` (pull from docker.io in aliyun mode) — no 1.25
  mirror exists on acs-sample.

## Continuing after the fix (possibly on another machine)

The harness is on branch `verify/preload-registry-codex` (production code untouched).

```bash
git fetch https://github.com/cheyang/rbg.git verify/preload-registry-codex
git checkout verify/preload-registry-codex
bash docs/verification/preload-registry-codex/scripts/re-verify.sh   # no args: auto-fetches current PR head
```

Expected after a correct fix: F1/F2/F3 rows report FIXED (contract tests pass);
`TestBaseInlinePreloadFailsOnFirstTransientPull` keeps passing (frozen base fixture).
The CI differential is re-observed by the PR's own `E2E rollout` check turning green.

Kickoff prompt for a fresh agent:

> Continue verification of sgl-project/rbg PR #567. Check out
> https://github.com/cheyang/rbg.git branch verify/preload-registry-codex, read
> docs/verification/preload-registry-codex/README.md and verify-manifest.json, run
> scripts/re-verify.sh (no args) and report per-finding Fixed/Still-broken.
