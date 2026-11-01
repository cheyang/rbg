# Verification: PR #536 — fix: honor lifecycle readiness gates for RoleInstance Pods

- PR: https://github.com/sgl-project/rbg/pull/536
- Head under review: `d9d7c4cff8eb507f4aeb36d1de1e2b683d1be3b9`
- Base: `4a0765f78b0930114a4b73a64433ee8f27c34372`
- Reviewer: Codex (Reviewer B). Harness branch: `verify/lifecycle-readiness-gates-codex`.
- Production code untouched by this harness (additive `_test.go` files + this doc tree only).

## Premise (P0) — verdict: CONFIRMED

Claim (sgl-project/rbg#534 Task 2): on base, a lifecycle hook with
`markPodNotReady=true` does not reliably block a RoleInstance: the readiness
helper silently no-ops when the instance lacks the custom readiness gate, the
first condition is created with an empty status, and neither the RoleInstance
`Ready` calculation nor Pod propagation honor the custom gate.

| Claim | Base (4a0765f7) | Head (d9d7c4cf) | Evidence |
|---|---|---|---|
| P0a gateless instance becomes blocked | RED (no gate, no condition) | GREEN | results/l1-base-premise.txt, results/l1-head.txt |
| P0b first condition has Status=False | RED (empty status) | GREEN | same |
| P0c RI Ready honors custom gate | RED (Ready=True) | GREEN | same |
| P0d Pods honor custom gate | RED (pod gate stays True) | GREEN | same |
| P0 (L2, real controllers) | RI Ready=True persists after PreDelete markPodNotReady | RI+pods go False, recover | results/l2-base-premise-envtest.txt (grafted spec, 1 entry), results/l2-head-envtest.txt (PR spec, 4 entries) |

Component match: the report names `pkg/inplace/instance/readiness` and
`pkg/reconciler/roleinstance/instance_status.go`; the PR touches exactly those
plus `pkg/inplace/instance/lifecycle` (the gate initialization). The base/head
flip doubles as the harness-bites check: the PR is the fix, and every contract
test flips exactly between the two revisions.

## Findings verified on head

| Finding | Layer | Test | Expected | Observed | Verdict |
|---|---|---|---|---|---|
| F1: stale-snapshot guard does not cover pod propagation | unit | TestVerifyF1StaleSnapshotClearsPodBlock (contract) | pod block preserved | pod gate flipped to True | **Confirmed** (red on head) |
| F1-control: guard covers the status-write path | unit | TestVerifyF1StaleSnapshotWithStatusWriteIsRejected (contract) | rejected + pod preserved | as expected | Pass — pins the guard boundary |
| F2: lint gate failing (prealloc, PR's own test file) | CI/local | golangci-lint v2.13.1 | clean | `lifecycle_readiness_test.go:45:5 prealloc` | **Confirmed**, PR-attributable |

Race check: `go test -race ./pkg/inplace/instance/... ./pkg/reconciler/roleinstance/`
— no data races; only the expected F1 contract failure (results/l1-head-race.txt).

## Reproducing

```bash
# unit + integration layers (no cluster needed; fake client)
go test -count=1 ./pkg/inplace/instance/lifecycle/ ./pkg/reconciler/roleinstance/ -run 'TestVerify' -v
# PR's own tests
go test -count=1 ./pkg/inplace/instance/... ./pkg/reconciler/roleinstance/
# L2 premise on base: graft test/envtest/testcase/stateful/lifecycle_readiness_test.go
# onto base 4a0765f7 and run with KUBEBUILDER_ASSETS set; entry times out at
# "RI Ready=True, want False".
# One-command re-verification against a future fix push:
bash docs/verification/lifecycle-readiness-gates/scripts/re-verify.sh
```

## Continuing after a fix

All harness tests are CONTRACT polarity: green = correct behavior. F1 stays red
on head d9d7c4cf; a fix that extends the freshness guard to pod propagation
(e.g. re-read the custom condition before `updatePodsLifeCycle`, or skip pod
release when the live object disagrees with the snapshot) flips it green. P0
tests are regression coverage for the premise and must stay green.
`.last-reviewed` records the reviewed head; advance it after each round.
