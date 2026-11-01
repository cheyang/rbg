# Verification — PR #536 (lifecycle readiness gates for RoleInstance Pods)

Reviewer: **Claude (Reviewer A)** — debate round 1 (initial), 2026-11-02.
Harness branch: `verify/lifecycle-readiness-claude` (based on PR head `d9d7c4cf`).
PR: https://github.com/sgl-project/rbg/pull/536 — head `d9d7c4cf`, base `4a0765f`.

## Premise (P0) — CONFIRMED

Issue #534 Task 2 claims that on base, a lifecycle hook with `markPodNotReady=true`
does not make a RoleInstance (or its Pods) NotReady when the instance lacks the
custom readiness gate. **Confirmed** at the unit layer: `TestP0PremiseBaseCanary`
passes on base `4a0765f7` (no gate injected, no condition written) and fails on
head `d9d7c4cf` (gate + `False` condition written). See
`results/unit-base.txt` / `results/unit-head.txt`.

Component match: the issue names `pkg/inplace/instance/readiness` and
`pkg/reconciler/roleinstance` — exactly the files the PR patches.

## Observed vs expected

| ID | claim (falsifiable) | layer | polarity | base | head | verdict |
|----|---------------------|-------|----------|------|------|---------|
| P0 | `UpdateRoleInstanceLifecycle(PreparingDelete, markPodNotReady=true)` leaves readiness untouched on an instance without the custom gate | unit | canary | PASS (bug present) | FAIL (fixed) | premise Confirmed; fix works |
| F1 | after PreparingDelete(true) → Normal(false) (scale.go:185 regret path), the lifecycle block must be released: custom condition True again, `RoleInstanceReady=True`, Pod `InstancePodReady=True` | unit | contract | SKIP (premise bug: no block written) | **FAIL** | Confirmed PR-introduced defect |
| F1 | same, with the real RoleInstance controller in envtest | integration | contract | n/a | **FAIL** (RI Ready=False, want True; 25s timeout) | Confirmed |
| F2 | repo-pinned golangci-lint is clean on the changed packages | lint script | contract | n/a | **FAIL** (`prealloc` at `pkg/reconciler/roleinstance/lifecycle_readiness_test.go:45`) | Confirmed — matches the failing `lint` CI job |

Raw evidence: `results/unit-base.txt`, `results/unit-head.txt`,
`results/envtest-head.txt`, `results/envtest-bites-check.txt`,
`results/lint-head.txt`, `results/reverify/` (produced by `re-verify.sh`).

## Findings detail

### F1 — regretting a PreparingDelete scale-down leaves the instance and its Pods NotReady forever

Mechanism (traced to the controller entry points):

1. `statelessmode/sync/scale.go:266 deleteInstances()` puts an instance into
   `PreparingDelete` with `markNotReady=true`. This PR's `ensureReadinessGate()`
   adds `RoleInstanceCustomReady` to `spec.readinessGates` and
   `addNotReadyKey()` writes a `False` condition whose message carries the
   `preDeleteHook` key.
2. The scale-down is regretted (scale back up) and the set no longer has
   `MarkNotReady` on its hooks, so `scale.go:185 managePreparingDelete()`
   transitions the instance to `Normal` with `markPodNotReady=false`.
3. `executeRoleInstanceNotReadyPolicy()` case `Normal` only removes the
   `preparingUpdateHookKey` — and only when `markPodNotReady=true`. Nothing in
   the repo ever removes `preparingDeleteHookKey` (grep: the only
   `RemoveNotReadyKey` call for instances is the preUpdate one), and the
   injected gate is never removed from spec.
4. `customReadinessBlocked()` therefore stays `true` forever: every
   `updatePodsLifeCycle()` reconcile re-marks the component Pods
   `InstancePodReady=False` and `getInstanceReadyCondition()` keeps
   `RoleInstanceReady=False`. The workload stays out of service endpoints
   permanently; only manual status surgery or instance deletion recovers.

On base the same regret sequence is harmless because the block was never
written (no gate → `addNotReadyKey` early return), so this regression is
introduced by this PR (`introducedByPR: true`).

Proposed fix (validated by the harness-bites check, see
`results/envtest-bites-check.txt`): when transitioning to `Normal`, release the
lifecycle blockers (both `preUpdateHook` and `preDeleteHook` keys) even when
the caller passes `markPodNotReady=false` — e.g. in
`UpdateRoleInstanceLifecycle`, run `executeRoleInstanceNotReadyPolicy` when
`state == Normal && readiness.ContainsReadinessGate(instance)`, and make case
`Normal` remove both keys. (The same gap exists for an instance whose
`PreparingUpdate` block is released while the hook's `MarkNotReady` flag was
turned off mid-update.)

### F2 — PR-introduced CI lint failure

`make lint` (CI job "Project Check") fails on this head:
`pkg/reconciler/roleinstance/lifecycle_readiness_test.go:45:5: prealloc`.
Reproduced locally with the repo-pinned golangci-lint v2.13.1
(`scripts/check-lint.sh`, `results/lint-head.txt`). One-line fix:
`objects := make([]client.Object, 0, 1+len(pods))`.

### Non-defect observations (not blocking)

- `UpdateInstanceStatus` now returns early when `updateStatus` fails, so
  `updatePodsLifeCycle` is skipped for that round (base aggregated the errors
  and always ran both). Heals on requeue; deliberate per the PR description.
- The new stale-snapshot guard in `updateStatus` compares only
  `RoleInstanceCustomReady`, not `RoleInstanceInPlaceUpdateReady`; the latter
  can still be rolled back by a stale informer snapshot (pre-existing design).
- The final `c.adp.GetRoleInstance` in `executeRoleInstanceNotReadyPolicy`
  reads through the controller cache, so the "fresh" object may not yet include
  the status write; benign for the patch path used in production.

## How to run

```bash
# unit layer
go test ./pkg/inplace/instance/lifecycle/ ./pkg/reconciler/roleinstance/ -run 'TestP0|TestF1' -v

# integration layer (envtest; needs setup-envtest 1.31.0 assets)
KUBEBUILDER_ASSETS="$(setup-envtest use 1.31.0 --bin-dir /tmp/rbg-assets -p path)" \
  go test ./test/envtest/testcase/stateful/ -ginkgo.focus 'lifecycle regret' -timeout 10m

# lint repro (needs repo-pinned golangci-lint v2.13.1)
bash docs/verification/lifecycle-readiness/scripts/check-lint.sh

# everything, against the current PR head (auto-discovered from manifest.pr)
bash docs/verification/lifecycle-readiness/scripts/re-verify.sh
```

Polarity: P0 is a bug-canary (fails on fixed code = premise fixed; invert it
once the PR lands). F1's tests are contract tests (pass = fixed).

## Continuing after the fix

1. Checkout this branch, run `bash docs/verification/lifecycle-readiness/scripts/re-verify.sh`
   (no args: fetches the current PR head from the manifest `pr` URL, grafts the
   harness onto it, runs unit + integration layers).
2. Expected while F1 is unfixed: `F1 STILL-BROKEN`, exit 1. When the author
   lands the release-path fix: `F1 FIXED`, `P0 FIXED(flipped)`; then invert
   `TestP0PremiseBaseCanary` (or promote `TestP0PremiseContractAfterFix`).
3. Envtest assets are machine-local (`setup-envtest use 1.31.0`); everything
   else lives on this branch.
4. Live layer (L3) skipped this round per the debate setup.

Kickoff prompt for a fresh agent:
> You are re-verifying sgl-project/rbg PR #536. Checkout branch
> `verify/lifecycle-readiness-claude` and run
> `bash docs/verification/lifecycle-readiness/scripts/re-verify.sh`. Read
> `docs/verification/lifecycle-readiness/README.md` first for the polarity
> table; report per finding Fixed / Still-broken / Harness-update.
