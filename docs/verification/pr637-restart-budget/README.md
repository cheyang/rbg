# Verification harness — PR #637 (rbg restart budget lifecycle hardening)

- PR: https://github.com/sgl-project/rbg/pull/637
- Head reviewed: `7b9b47c279e7d24eb386230c668397205785b9d1`
- Base: `45e6e1c208e1c0526f8b1f2d2fc26c6934ab389a` (merge-base with main)
- Reviewer: A (Claude), branch `verify/pr637-restart-budget-claude`
- Harness is **additive only**; production code in this branch is identical to
  the PR head (`git diff 7b9b47c2 -- ':!docs/verification' ':!pkg/reconciler/roleinstance/sync/restart_budget_upgrade_test.go'` is empty).

## Premise (P0) — runs against the BASE branch

Issue #617 claims two release-critical problems with #560's restart budget. Both
reproduce on the base branch (unit layer, deterministic fake-client fixtures):

| verdict | claim | evidence |
| --- | --- | --- |
| **Confirmed** | P0-a: a healthy, never-faulted budgeted RoleInstance acquires `rbg.workloads.x-k8s.io/group-restart-budget-cleanup` on its first reconcile (issue #617 item 2) | `TestPremiseP0aHealthyBudgetedInstanceAcquiresCleanupFinalizerOnBase` — `results/base-premise-run.log` |
| **Confirmed** | P0-b: a persisted `groupRestartRevision` that the controller no longer computes (serializer/encoding drift across controller builds) releases an exhausted group without `recover=true`, deletes its retained Pods and resets the count (issue #617 item 1 mechanism) | `TestPremiseP0bRevisionMismatchReleasesExhaustedGroupOnBase` — `results/base-premise-run.log` |
| **Control (base=pass)** | P0-c: a plain controller upgrade whose persisted revision still matches keeps exhaustion on base — F1's regression is introduced by this PR's format change, not present on base | `TestPremiseP0cPlainUpgradeKeepsExhaustionOnBase` — `results/base-premise-run.log` |

The premise tests are not committed to this branch (they call the base
signatures and do not compile at the head); their source is preserved at
`results/base-premise_test.go` and runs against a worktree of the base:
`git worktree add <dir> 45e6e1c2 && cp results/base-premise_test.go <dir>/pkg/reconciler/roleinstance/sync/ && go test -mod=vendor -run TestPremiseP0 ./pkg/reconciler/roleinstance/sync/`.

Component match: the issue names `pkg/reconciler/roleinstance/sync/restart_budget.go`
(and the CRD/webhook for item 4); the PR touches exactly those. Symptom match: yes.

## Findings

### F1 — missing migration for the budget-revision format change (confirmed, major)

`status.groupRestartRevision` persisted by builds up to and including the
**v0.9.0-alpha.5 tag (which contains #560)** is a bare sha256 hex digest.
This PR changes the identity to `<ControllerRevision-name>/<size-digest>`.
Issue #617 item 1 states that landing this after a release requires a migration
"that rewrites the old hash format in place without resetting the count or
exhaustion; otherwise upgrading to the fix triggers this bug". No such migration
exists in the PR, and no doc note covers the upgrade.

Mechanism: on the first reconcile after upgrade, `revisionChanged` is true for
every budgeted RoleInstance (old format never equals the new format):

- exhausted groups: `drainExhaustedBudgetPods` → `resetRestartBudget` → the
  group is released **without** `rbg.workloads.x-k8s.io/recover=true`, retained
  Pods deleted, new group created — the exact invariant the budget exists to
  enforce, broken by the upgrade itself;
- healthy groups with consumed attempts: `groupRestartCount` is reset to 0,
  granting extra restarts beyond `maxGroupRestarts`.

Harness (contract polarity — fails on the PR head, passes once fixed):

- `TestBudgetUpgradeFromOldRevisionFormatPreservesExhaustion`
- `TestBudgetUpgradeFromOldRevisionFormatPreservesCount`
- in `pkg/reconciler/roleinstance/sync/restart_budget_upgrade_test.go`

Observed vs expected:

| run | result |
| --- | --- |
| PR head `7b9b47c2` | both tests FAIL — exhausted group released without recover, count reset (`results/f1-head-red.log`) |
| head + 6-line migration sketch | both tests PASS and the PR's whole sync suite still passes (`results/f1-with-proposed-fix-green.log`, `results/f1-with-proposed-fix-full-sync-suite.log`) |

The migration sketch used for the harness-bites check (reverted afterwards; do
not ship as-is): in `reconcileRestartBudget`, when the persisted revision
contains no `/`, rewrite it to `groupRestartRevision(fresh, selectedRevision)`
via `patchBudgetStatus` **without** resetting `GroupRestartCount` or
`RestartBudgetExhausted`, then treat `revisionChanged` as false. Alternative
acceptable fix: a documented operator procedure (tear down budgeted workloads
before upgrading from v0.9.0-alpha.5), if maintainers declare alpha-to-<this>
upgrades out of support — but then the doc must say so.

### F2 — redundant uncached reads on the non-exhausted revision-change path (not verified by harness; code-path reasoning)

`reconcileRestartBudget` calls `releaseBudgetPods` at restart_budget.go:334 and
then `resetRestartBudget` → `releaseBudgetOwner` → `releaseBudgetPods` again
(restart_budget.go:412): two uncached Pod LISTs plus, per Pod, one GET inside
`mutateBudgetPodFinalizer` even for Pods that never carried the finalizer — the
per-Pod-read skip that `terminateBudgetPods` received was not applied to
`releaseBudgetPods`/`drainExhaustedBudgetPods`. Perf-only; does not affect the
steady-state counters the PR documents.

## Layers and how to run

- Unit (L1): `go test -mod=vendor -count=1 -run 'TestBudgetUpgradeFromOldRevisionFormat' ./pkg/reconciler/roleinstance/sync/`
- Full PR-relevant suites (already run this round):
  - `go test -mod=vendor -count=1 ./api/... ./internal/... ./pkg/...` — 38 packages ok (`results/full-unit-suite.log`; the only red is F1's contract tests by design)
  - `go test -mod=vendor -race -count=1 -run 'TestRestartBudget|TestBudget' ./pkg/reconciler/roleinstance/sync/` — no data races; only F1 red (console evidence)
  - `KUBEBUILDER_ASSETS=<envtest 1.31.0> go test -mod=vendor -count=1 ./test/envtest/testcase/restart_policy/...` — PASS incl. the new CEL table (`results/envtest-restart-policy.log`)
- Integration envtest binaries via
  `go run sigs.k8s.io/controller-runtime/tools/setup-envtest@latest use 1.31.0 --bin-dir <dir> -p path`.
- Live layer: skipped for this round (no cluster), per the round instructions.

## Re-verify after a fix

From a checkout of this branch:

```bash
bash docs/verification/pr637-restart-budget/scripts/re-verify.sh
```

It fetches the current PR head, grafts this harness onto it, runs the unit
layer and prints per finding **Fixed / Still-broken / Partial /
Harness-update**. F1 is a contract finding: it is *fixed* only when
`TestBudgetUpgradeFromOldRevisionFormat*` goes green.

## Continuing after the fix (handoff)

1. `git clone https://github.com/cheyang/rbg.git && cd rbg && git checkout verify/pr637-restart-budget-claude`
2. `bash docs/verification/pr637-restart-budget/scripts/re-verify.sh` (or pass an explicit ref)
3. Interpret with the polarity table: F1 contract tests green → fixed; still
   red → still broken. P0 base-side tests are reference-only (they prove the
   premise, not the fix).
4. Raw evidence lives under `docs/verification/pr637-restart-budget/results/`.

Kickoff prompt for a fresh agent: "Checkout branch
verify/pr637-restart-budget-claude of cheyang/rbg, read
docs/verification/pr637-restart-budget/README.md, run scripts/re-verify.sh
against the current head of https://github.com/sgl-project/rbg/pull/637 and
report per-finding Fixed/Still-broken with the raw logs."
