# pod-cache-union-tools — verification (PR #545)

Reproducible evidence for the review of
https://github.com/sgl-project/rbg/pull/545 (`test(podcache): preserve KEP-284
reproduction tools`, head `db46c943298ca1693d3f8c717c18cd5684a2cadc`, base
branch `kep-284-e2e-test` == `main` @ `09accf63747177f12544d8738cab13ea07910a25`).

This PR is unusual: it adds only a build-tagged test-tooling package
(`//go:build podcacheunion`) to a dedicated side branch. The "code under
review" is itself a test harness, so verification = running the harness on an
independent platform (linux/amd64, Go 1.27.0; the author validated darwin/arm64
+ linux/arm64-container on Go 1.26.0) plus additive contract tests pinning the
semantics the KEP evidence relies on.

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit (contract) | admission table, transform mutation/idempotency, union boundary merge, compareRV | `KUBEBUILDER_ASSETS=$PWD/bin/k8s/1.31.0-linux-amd64 go test -race -mod=vendor -tags=podcacheunion ./test/podcacheunion -run 'TestVerify' -count=1 -v` |
| 2. Integration | full correctness suite against an isolated envtest apiserver (k8s 1.31.0/etcd 3.5.15), both WatchList configs | the two README commands, see below |
| 2b. Campaign smoke | measurement harness end-to-end (sampler compile, mode subprocesses, 410 relist, summary) | `GOMAXPROCS=4 SHAPES=48:1000 ROUNDS='0' bash test/podcacheunion/scripts/transform-campaign.sh <new-dir>` |
| 3. Live | skipped per debate setup (no KUBECONFIG layer) | — |

envtest assets (one-time, needs network): `make setup-envtest && bin/setup-envtest use 1.31.0 --bin-dir "$PWD/bin" -p path`

> Polarity: all added tests are contract tests — they PASS on correct tooling
> and FAIL on a regression. Verified by harness-bites checks (below).

## Problem premise (P0)

| | |
|---|---|
| Claimed problem | Maintainer request (sgl-project/rbg#530 comment 6568640028, 2026-11-02, cheyang): "there's now an upstream branch for it, `kep-284-e2e-test` ... please: 1. Open a PR from your fork into `kep-284-e2e-test` that adds only `test/podcacheunion`, with the R2-F1 and R2-F4 fixes." |
| Reported component | `test/podcacheunion` KEP-284 harness from PR #530 |
| Patched component | exactly that directory, 14 new files, nothing else |
| Component match | Yes |
| **Verdict** | **Confirmed** — the maintainer explicitly requested this exact PR; tests-only carve-out applies to deeper "why" analysis |
| Evidence | R2-F1 present: `UseExistingCluster: ptr.To(false)` in TestMain + `export USE_EXISTING_CLUSTER=false` in the campaign script (isolation run: results/isolation-use-existing-cluster.txt passes with `USE_EXISTING_CLUSTER=true` and `KUBECONFIG=/nonexistent` inherited). R2-F4 present: `medianRange` averages the two middle values for even groups; `TestMedianRange` passes. PR-body byte-identity claim verified: all 13 Go/script/C files sha256-identical to PR #530 head `d1c331a`; only README links differ. |

## Summary of results

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| V1 | Package compiles and vets clean under `-tags=podcacheunion` | 1 | Confirmed | `go vet` exit 0; `go test -c` OK |
| V2 | Correctness suite passes with `-race` in both WatchList configurations | 2 | Confirmed | results/correctness-wl-off.txt (7.4s), results/correctness-wl-on.txt (16.9s); re-run with harness present: results/full-wl-{off,on}-with-harness.txt |
| V3 | R2-F1 isolation: inherited `USE_EXISTING_CLUSTER=true` + bogus KUBECONFIG cannot leak onto a real cluster | 2 | Confirmed | results/isolation-use-existing-cluster.txt: `ok` |
| V4 | R2-F4 even-sized median averaging | 1 | Confirmed | `TestMedianRange` PASS (part of both suite runs) |
| V5 | Measurement campaign produces asserted per-mode results on both server configs, incl. streaming fallback | 2b | Confirmed | results/campaign-smoke.txt, results/campaign-smoke-summary.md, results/campaign-smoke-provenance.txt; per-mode assertions (lists/streaming counts, retained whole/stub counts) all held: e.g. fallback row `lists=2 streaming=2`, streaming rows `lists=0 streaming=2`, union `retained=48` of 1048 |
| V6 | Opt-in heap diagnostic runs | 2 | Confirmed | results/diag-smoke.txt |
| F1 | Union prototype boundary merge (selector-exit GET, stale replay, watermark prune, overflow resnapshot) is correct | 1 | Confirmed by added contract test | `TestVerifyUnionBoundaryResolution`; results/verify-contract-tests.txt. NOTE: in the PR itself this logic is only covered indirectly by campaign invariants (see findings F1 in the review) |
| F2 | Admission labels match production constants | 1 | Confirmed | `TestVerifyAdmissionLabelWiring` (group/instance vs `api/workloads/constants`, warmup pinned by value, managed = KEP literal) |
| F3 | Admission table matches KEP-284 rule | 1 | Confirmed | `TestVerifyAdmissionContract` (13 cases incl. managed=false+legacy, empty value, non-controller RBG ref, foreign-only) |
| F4 | Transform mutation contract (in-place for admitted, new object for stubs, fixed point) | 1 | Confirmed | `TestVerifyTransformMutationContract` |

## Harness-bites check (step 4)

Production code was temporarily broken, harness observed red, then reverted
(`git diff` on PR files empty afterwards):

- `admitted()` ignoring `warmupLabel` → `TestVerifyAdmissionContract/warmup_label` FAILS (exit 1).
- `resolve()` skipping the NotFound watermark → `TestVerifyUnionBoundaryResolution` FAILS ("deleted Pod must become a watermark").

## Limitations

- darwin sampler (`memory_sampler_darwin.c`) not exercised (no macOS here); linux sampler verified.
- The full default campaign (5 shapes × 3 rounds × 2 batches) was not re-run; a 1×1×2 smoke was.
- Go 1.27.0 vs the author's 1.26.0: results are directionally consistent; absolute numbers are platform/toolchain-dependent, as the README states.

## Continuing after changes (another machine)

```bash
git fetch https://github.com/cheyang/rbg.git verify/pod-cache-union-tools-codex
git checkout <ref-to-check>
git checkout FETCH_HEAD -- docs/verification/pod-cache-union-tools test/podcacheunion/verify_reviewer_codex_test.go
bash docs/verification/pod-cache-union-tools/scripts/re-verify.sh   # resolves the PR head itself
```

or, from a checkout of this branch: `bash docs/verification/pod-cache-union-tools/scripts/re-verify.sh [ref]`.
The script grafts the harness onto the target ref and reports per finding
Fixed / Still-broken / Harness-update. All findings here are polarity
`contract`: PASS means the tooling still satisfies the contract.
