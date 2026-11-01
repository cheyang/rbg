# Verification — PR #537 "fix: honor InPlaceUpdate hooks for Stateful instances" (Reviewer B / Codex)

- PR: https://github.com/sgl-project/rbg/pull/537 (Task 3 of issue #534)
- Base: `4a0765f78b0930114a4b73a64433ee8f27c34372`  Head: `2d648e5d6b2931883a71ec9d2d076367add3229d`
- Branch: `verify/stateful-inplace-hooks-codex` (based on the PR head; production code untouched — harness is additive test files + this directory)
- Layers run: unit + integration (envtest, k8s 1.31.0 assets). Live layer skipped by debate-pipeline rule.

## Premise (P0) — CONFIRMED

Claim (issue #534 Task 3): the Stateful `inPlaceUpdateInstance()` path invokes the in-place
update control without the lifecycle waiting protocol present in Stateless mode, so a matching
`lifecycle.inPlaceUpdate` label/finalizer handler does not block the update.

`TestVerifyPR537P0_HookedInstanceMustWaitPreparingUpdate` (contract; compiles on both trees via
`applyTargetUpdate`, whose signature is unchanged by the PR):

- **base** `4a0765f7`: FAILS — hooked instance's lifecycle label stays empty, its spec is
  replaced with the update revision immediately. Hook bypassed → premise reproduced
  (`results/unit-base-p0-premise.txt`).
- **head** `2d648e5d`: PASSES — instance is held in `PreparingUpdate`, spec unchanged, no
  deletion (`results/unit-head-verify-tests.txt`).

Component match: report names `stateful_instance_set_control.go`; that is what the PR touches. OK.

## Findings

| ID | Severity | Polarity | Layer | Status on head | Evidence |
|----|----------|----------|-------|----------------|----------|
| P0 | — (premise) | contract | unit | confirmed on base / fixed on head | `results/unit-base-p0-premise.txt`, `results/unit-head-verify-tests.txt` |
| F1 | major | contract | unit + envtest | **reproduced (test red)** | `results/unit-head-verify-tests.txt`, `results/integration-head-f1-stranding.txt` |
| F2 | minor | canary | unit + static | current behavior documented | `results/unit-head-verify-tests.txt`, `results/f2-registry-static-evidence.txt` |

### F1 — PreparingUpdate stranding below a raised partition

Once an instance is admitted to `PreparingUpdate`, the only exits are
`inPlaceUpdateInstance` (only reachable for update targets at ord >= partition, via
`buildUpdateTargets` / `orderedReadyCleanupTarget`) and `refreshInstanceState` (handles only
`Updating`/`Updated`). Raising `spec.updateStrategy.partition` above an admitted ordinal
therefore strands the instance in `PreparingUpdate` — even after the hook owner releases every
handler — with the lifecycle readiness key held (markNotReady => NotReady persists) and the
instance draining `baseUnavail` budget. Verified twice:

- unit `TestVerifyPR537F1_...` (both subtests red on head),
- envtest `...partition stranding...` It-spec (times out on head: label never leaves
  `PreparingUpdate`).

Harness-bites: a temporary, uncommitted rescue in `refreshInstanceState`
(`PreparingUpdate` + `ord < partition` → `Normal`, releasing the readiness key) flips both
tests green and keeps the PR's own suite passing; reverted afterwards
(`results/bites-check-f1-unit-with-fix.txt`, `results/bites-check-f1-integration-with-fix.txt`).

### F2 — lifecycle Expect() writes into a pod-keyed expectations registry

`stateful_instance_set_control.go:1294,1351` call
`instanceutils.ResourceVersionExpectations.Expect(gotRoleInstance)`. That registry
(`pkg/reconciler/roleinstance/utils`) is observed/satisfied/deleted only with **Pod** objects
(`instance_reconciler.go:125-126`, `instance_event_handler.go:93`), keyed by UID — so the
RoleInstance-keyed entries gate nothing and are never cleaned up (one map entry per instance
for the process lifetime). The stateless controller avoids this by using its own registry with
`Observe`/`IsSatisfied` wired for RoleInstances (`statelessmode/reconciler.go:159-170`).
Canary test `TestVerifyPR537F2_...` demonstrates the entry staying unsatisfied across pod
observations. Fix by wiring a stateful-owned registry (observe instances in the stateful
reconciler) or dropping the calls.

## PR's own tests (all pass on head)

- `TestStatefulInPlaceHook*` (7 tests) — `results/unit-head-pr-tests.txt`
- `TestInPlaceUpdatePreservesLifecycleReadinessGate` — `results/unit-head-gate-preservation.txt`
- envtest "Stateful InPlaceUpdate lifecycle hook" (Parallel + OrderedReady; partition, pause,
  UID stability, full handshake) — `results/integration-head-pr-envtest.txt`
- Affected packages: all green except the F1 repro row —
  `results/unit-head-affected-packages.txt`

## Continuing after the fix

```bash
git fetch https://github.com/cheyang/rbg.git verify/stateful-inplace-hooks-codex
git checkout verify/stateful-inplace-hooks-codex
bash docs/verification/stateful-inplace-hooks-codex/scripts/re-verify.sh   # auto-resolves current PR head
```

- F1 contract tests go green when a rescue path exists; F2 canary flips red when the registry
  wiring is fixed (invert it then). P0 stays green as a regression test.
- Integration layer needs envtest assets (`setup-envtest use 1.31.0 -p path` or set
  `KUBEBUILDER_ASSETS`; this machine used `/home/ecs-user/envtest-assets/k8s/1.31.0-linux-amd64`).
- `.last-reviewed` = `2d648e5d…`; advance it after the next round.
