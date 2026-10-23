# Verification — PR #510 `status: add lifecycle phase and aggregated condition reporting`

- PR: https://github.com/sgl-project/rbg/pull/510
- Reviewed head: `42e4479ea0b67840f1dd8d011c6b194ecf15d762` (base `a0b11e6ab95ca6dfcfdf6fca713352c8482ebd41`)
- Reviewer: Codex (Reviewer B), round mode: initial
- Production code is untouched on this branch; the diff vs. the PR head is harness-only.

## P0 — premise

Issue #502 (OPEN, same author) asks for the phase + aggregated-condition status model for
RBG / RoleInstanceSet / RoleInstance / Warmup. The PR implements the status-reporting
subset. Component match: yes (all four kinds touched). The premise needs no runtime
reproduction (it is a design/feature gap, not a bug report), so P0 is **confirmed** by
issue inspection rather than by a base-branch repro.

## Findings and verdicts

| ID | Claim | Layer | Polarity | Result on head | Evidence |
|----|-------|-------|----------|----------------|----------|
| F1 | RBG `Ready` can never become `True` for a role backed by LeaderWorkerSet, because `roleWorkloadStatus` requires `Available.condition.observedGeneration >= lws.generation` and the real LWS controller (vendored sigs.k8s.io/lws v0.7.0, `makeCondition`) never sets `observedGeneration` on conditions; LWS status has no `observedGeneration` field at all | unit + integration (envtest) | contract | **Reproduced**: both contract tests FAIL on head; the stamped-generation control passes | `results/unit-head.txt`, `results/envtest-head.txt` |
| F2 | `golangci-lint` (gocyclo > 30) fails on the three new/rewritten functions: `roleWorkloadStatus` (57), `SetStatusConditions` (45), `updateRBGStatus` (33) | CI + local gocyclo | contract | **Reproduced**: `results/gocyclo-head.txt`; base branch has no function over 30 in these files | `results/gocyclo-head.txt` |

Differential CI evidence for F1 (real Kind clusters, run by the PR's own checks):
main-branch E2E green at run 39897214096; PR head e2e red in 6 suites
(`E2E lifecycle`, `E2E workload`, `E2E rollout`, `E2E feature`, `E2E v1alpha1`,
`Manifest installation e2e`), and every failed test waits for RBG `Ready=True`
(`test/e2e/framework/rbg_v2_expect.go:86` / `rbg_expect.go:114`) on an RBG that
contains an LWS role.

## Harness bites check

Temporary fix applied and reverted (never committed):
`fresh = available != nil && (available.ObservedGeneration == 0 || available.ObservedGeneration >= child.Generation)`
in `roleWorkloadStatus`. With it, all unit contract tests and both envtest specs pass;
reverted, the contract tests fail again. The harness bites on exactly the
observedGeneration gate.

## How to re-run

Unit layer:

```bash
go test ./internal/controller/workloads/ -run 'TestVerifyPR510' -v
```

Integration layer (needs envtest assets; downloads nothing else — the LWS CRD is
resolved from the module cache via `go mod download`):

```bash
KUBEBUILDER_ASSETS="$(bin/setup-envtest use 1.31.0 --bin-dir "$PWD/bin" -p path)" \
  go test ./test/envtest/testcase/verifylws510/ -v -timeout 6m
```

Both layers at once against a fixed ref (grafts this harness onto it):

```bash
bash docs/verification/pr-510-status-phase-conditions/scripts/re-verify.sh
```

All F1 tests are contract tests: they FAIL on the reviewed head and must PASS on a fix.
The `StampedObservedGeneration`/`control:` cases pass on both and are polarity controls,
not finding evidence.

## Continuing after the fix

1. `git fetch` the fork, checkout `verify/pr-510-status-phase-conditions-codex`.
2. Run `bash docs/verification/pr-510-status-phase-conditions/scripts/re-verify.sh`
   (no args: fetches the current PR head from the manifest's `pr` URL).
3. Expected when fixed: unit `TestVerifyPR510_LWSRealWorldStatusReady` and
   `TestVerifyPR510_RBGReadyWithRealWorldLWS` PASS; envtest contract spec passes.
4. Advance `docs/verification/pr-510-status-phase-conditions/.last-reviewed` to the
   re-verified head sha and commit.
