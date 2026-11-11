# Verification — PR #567 "ci: pick e2e image registry by environment (docker.io / aliyun)"

Reviewer: Claude (Reviewer A). Head reviewed: `ec9c88cfb14f04ba5af1abfe6c17de7c0cb55665`
(merge-base `73e05db4213950a5dd1f230279e9344a4ffe8435`). Production code is
**untouched** on this branch — the diff is this harness only.

## Premise (P0) — verdict: CONFIRMED (runs against base)

The PR claims Release Test run 42213206716 failed in `e2e-test-helm` at the
"Pre-load e2e test images" step because `docker pull
registry.cn-hangzhou.aliyuncs.com/acs-sample/nginx:latest` was reset by a
transient network failure, with no retry.

Evidence: run 42213206716's `e2e-test-helm` job is at `run_attempt=2`; **attempt
1 failed** and attempt 2 passed after a re-run (GitHub jobs API, see
`results/ci-evidence.txt`). On the merge-base, `release-test.yml`'s three inline
pre-load steps `docker pull` both cn-hangzhou images unconditionally with **no
retry** (`results/base-preload-images.txt`). The premise is real and the
component matches the patch.

The step-level fix itself **works**: in `docker.io` mode the pre-load step never
contacts an aliyuncs.com registry (`P0fix_no_aliyun_pull_in_preload_step` =
pass), and pulls are retried. What does *not* hold is the stronger claim — see
F1/F2.

## Observed vs expected

| ID | claim (condition → expected vs suspected) | verdict | evidence |
| --- | --- | --- | --- |
| P0 | base pre-load step pulls cn-hangzhou unconditionally, no retry → one reset fails the job | **Confirmed** (on base) | attempt-1 failure of run 42213206716 + `results/base-preload-images.txt` |
| F1 | docker.io mode pre-loads every aliyun-hosted image referenced by e2e pods (PR: "removes the overseas-runner dependency on the Hangzhou registry entirely") | **Confirmed broken**: `testutils.DefaultImage` = `registry.cn-hangzhou.aliyuncs.com/acs-sample/nginx:latest` (referenced by ~30 test sites) is *not* in the loaded set in **either** mode; pods must runtime-pull it from Hangzhou | `results/preload-coverage-*.txt`; head CI run 43031453847 shows pods pulling it at test runtime (`results/ci-evidence.txt`) |
| F2 | `patio-runtime` is "China-side only" | **Confirmed broken**: `testutils.CreatePatioRuntime` (called by every suite: rbg, revision, leader_worker_role v1alpha1+v1alpha2, upgrade) injects `registry-cn-hangzhou.ack.aliyuncs.com/dev/patio-runtime:v0.2.0` sidecars; it is skipped entirely in docker.io mode (the new default for **all** GitHub-hosted jobs) | `results/preload-coverage-docker.io.txt`; base pre-loaded it (`results/base-preload-images.txt`) — tag `v0.2.0` ⇒ kubelet defaults to IfNotPresent, so the base pre-load actually worked |
| F3 | aliyun mode: `nginx:1.25` should be nginx 1.25 | **Observed** (canary): both `nginx:latest` and `nginx:1.25` are re-tagged from the *same* mirror blob `acs-sample/nginx:latest` | `results/stub-log-aliyun.txt` |
| F4 | retry loop sleeps only *between* attempts | **Observed** (canary): the final failed attempt also sleeps the full backoff before erroring | `F4_backoff_sleeps_after_final_attempt` = pass (documents the wasteful sleep) |
| F5 | script hardening: invalid `E2E_IMAGE_REGISTRY` rejected; retry exhaustion exits 1 | **Pass** (script is correct on these) | `results/head-run.txt` |

## How to run

```bash
bash docs/verification/preload-e2e-images/scripts/run-all.sh            # all layers, go-test JSON
bash docs/verification/preload-e2e-images/scripts/audit-image-coverage.sh docker.io   # L1 audit + coverage table
bash docs/verification/preload-e2e-images/scripts/script-behavior-tests.sh            # L2 stubbed docker/kind
bash docs/verification/preload-e2e-images/scripts/re-verify.sh          # graft onto current PR head, per-finding verdicts
```

Requires: bash, git, coreutils. No docker/kind/cluster needed (stubs under
`scripts/stub-bin/`). Layers run: unit (static differential audit) +
integration (script behavior against a stubbed container runtime). No live
layer was run (see `liveNote` in `verify-manifest.json`).

## Proposed fixes

- **F1** — make docker.io mode produce the *names the pods reference*, e.g.
  pull `nginx:latest` from docker.io and `docker tag` it to
  `registry.cn-hangzhou.aliyuncs.com/acs-sample/nginx:latest` (plus
  `nginx:1.25`), or change `testutils.DefaultImage` to a docker.io name. The
  bites-check (`results/bites-check.txt`) shows the re-tag variant flips F1 to
  pass, so the fix is one entry away.
- **F2** — `patio-runtime` has no docker.io counterpart; options: keep pulling
  it (with retry) in every mode, mirror it, or make the tests' runtime profile
  image configurable per environment. Author decision required — the current
  state silently drops it from all GitHub-hosted e2e runs.
- **F4** — `if [ "${attempt}" -lt "${PULL_ATTEMPTS}" ]; then sleep ...; fi`.

## Continuing after the fix

Re-run `scripts/re-verify.sh` (auto-resolves the PR head from
`verify-manifest.json`'s `pr` URL). Polarity table:

| test | kind | goes green when |
| --- | --- | --- |
| `F1_DefaultImage_loaded_in_docker.io_mode` | contract | DefaultImage name is loaded in docker.io mode |
| `F2_patio_runtime_loaded_in_docker.io_mode` | contract | patio-runtime is loaded (or the tests stop referencing it — then update the audit's referenced-set extraction) |
| `G1_all_referenced_aliyun_images_loaded_docker.io_mode` | contract | no referenced aliyun-hosted image is left to runtime pulls |
| `P0fix_no_aliyun_pull_in_preload_step` | contract | stays green unless the pre-load step regresses to pulling from cn-hangzhou in docker.io mode |
| `F3_aliyun_mode_retag_aliases_versions` | canary | flips to fail when version-specific mirrors (or no aliasing) are used — invert it then |
| `F4_backoff_sleeps_after_final_attempt` | canary | flips to fail when the trailing sleep is dropped — invert it then |
| `F5a`/`F5b` | contract | stay green; guard the script's hardening |

Kickoff prompt for a fresh agent: "Checkout branch `verify/preload-e2e-images-claude`
of the reviewer fork for rbg PR #567, read
`docs/verification/preload-e2e-images/README.md`, run `scripts/re-verify.sh`,
and report the per-finding verdicts honoring the polarity table above."
