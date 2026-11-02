# Verification — PR #545 (`test(podcache): preserve KEP-284 reproduction tools`)

Reviewer A (Claude) harness. PR head `db46c943` (base `09accf6`, branch `kep-284-e2e-test`).
This branch adds **only** this harness directory; production code and the PR's
14 files under `test/podcacheunion` are untouched.

## Premise (P0)

**Confirmed (process-level).** The PR is the split the collaborator requested on
PR #530 (comment 6568640028): *“Open a PR from your fork into `kep-284-e2e-test`
that adds only `test/podcacheunion`, with the R2-F1 and R2-F4 fixes.”* The PR
adds exactly the 14 files; the 13 non-README files are **byte-identical** to PR
#530 head `d1c331ae` (verified by `git diff`); the README differs only in link
targets. Tests-only PR, so no base-branch symptom repro is owed.

## Observed vs expected

| Finding | Claim | Verdict | Evidence |
| --- | --- | --- | --- |
| R2-F1 (carried) | Campaign/fixture could escape to the kubeconfig cluster via `USE_EXISTING_CLUSTER` | **FIXED** | `UseExistingCluster: ptr.To(false)` in `TestMain` + `export USE_EXISTING_CLUSTER=false` in the campaign script. Race suite green under `USE_EXISTING_CLUSTER=true`, `~/.kube/config` present, `KUBECONFIG=/nonexistent/kubeconfig`; “UNION_ENVTEST: isolated…” banner. Harness-bites: reverting the pin → `isolation-check.sh` exit 1 |
| R2-F4 (carried) | `medianRange` returned the upper-middle value for even-sized groups | **FIXED** | Even groups now average the two middle values; `TestMedianRange` covers 1–4 samples, green |
| F1 (carried R2-F2, adapted) | No CI coverage: zero checks run on PRs to `kep-284-e2e-test`; the tagged package is never compiled by CI anywhere | **OPEN (minor)** | check-runs total 0, statusCheckRollup empty; all 7 workflows filter `pull_request`→`main`; no `podcacheunion` tag reference in workflows/Makefile/.golangci.yml. `ci-coverage-check.sh` RED on head |
| F2 | README links point at the author’s fork (QIW9/rbg @ d1c331ae) instead of upstream | **OPEN (nit)** | Link diff only; correct today, less durable than upstream once PR #530 merges |
| F3 | union.go comparison machinery has no correctness test (campaign count assertions only) | **OPEN (nit/coverage)** | grep: only `transform_measure_test.go` (campaign mode) touches the union |

## How to run

```bash
# unit + integration (from a checkout of this branch):
bash docs/verification/545-podcacheunion-tools-claude/scripts/isolation-check.sh
bash docs/verification/545-podcacheunion-tools-claude/scripts/ci-coverage-check.sh   # RED on head by design (F1)
CGO_ENABLED=1 KUBEBUILDER_ASSETS=/tmp/rbg-assets/k8s/1.31.0-linux-amd64 \
  KUBE_FEATURE_WatchListClient=false KUBE_CACHE_MUTATION_DETECTOR=true \
  go test -race -mod=vendor -tags=podcacheunion ./test/podcacheunion -count=1 -v -timeout=10m
# repeat with KUBE_FEATURE_WatchListClient=true RBG_POD_UNION_APISERVER_FEATURE_GATES=WatchList=true

# perf layer (optional, ~2 min):
GOMAXPROCS=4 SHAPES=48:1000 ROUNDS=0 KUBEBUILDER_ASSETS=<assets> \
  bash test/podcacheunion/scripts/transform-campaign.sh /tmp/fresh-run

# re-verify everything against the current PR head (any machine):
bash docs/verification/545-podcacheunion-tools-claude/scripts/re-verify.sh
```

Environment used this round: linux/amd64, Go 1.27.0, envtest binaries
`/tmp/rbg-assets/k8s/1.31.0-linux-amd64` (a third platform config beyond the
author’s macOS arm64 and Linux arm64 container).

## Polarity table (continuing after a fix)

| Check | Kind | On buggy code | On fixed code |
| --- | --- | --- | --- |
| `isolation-check.sh` | contract | exit 1 (envtest escapes/fails under hostile env) | exit 0 + isolated banner |
| `TestMedianRange` | contract | fail | pass |
| `ci-coverage-check.sh` | contract | RED = F1 reproduced | green once CI covers the branch/tag — retire with a recorded maintainer decision if the branch stays manual-only |
| Campaign child assertions | contract | child `t.Fatalf` → campaign exit 1 | exit 0 + summary.md |

## Handoff / resume prompt

> You are continuing a PR review verification. Checkout branch
> `verify/545-podcacheunion-tools-claude` of `cheyang/rbg` (fork of
> sgl-project/rbg). Read `docs/verification/545-podcacheunion-tools-claude/`
> (README, verify-manifest.json, .last-reviewed). Run
> `bash docs/verification/545-podcacheunion-tools-claude/scripts/re-verify.sh`
> (no sha — it fetches the current head of PR #545), report per finding
> Fixed / Still-broken / Partial / Harness-update with polarity from the table
> above, then review the `last-reviewed..head` delta incrementally and advance
> `.last-reviewed`. Envtest assets: `KUBEBUILDER_ASSETS` or
> `/tmp/rbg-assets/k8s/1.31.0-linux-amd64`; `make setup-envtest` otherwise.
> Never push anywhere but `verify/*` on the fork.
