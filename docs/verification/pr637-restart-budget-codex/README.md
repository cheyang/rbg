# pr637-restart-budget-codex — bug verification

Reproducible evidence for the findings raised while reviewing
https://github.com/sgl-project/rbg/pull/637 (head `7b9b47c2`, base `45e6e1c2`).

Layers run against the **code under review** (`7b9b47c2`); P0 runs against the **base**:

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit | budget lifecycle + upgrade path (`pkg/reconciler/roleinstance/sync`) | `go test -mod=vendor ./pkg/reconciler/roleinstance/sync/ -run 'TestVerifyF1' -count=1 -v` |
| 2. Integration | real API server via envtest (CRD CEL + budget lifecycle) | `KUBEBUILDER_ASSETS=<1.31 assets> go test -mod=vendor ./test/envtest/testcase/restart_policy/ -count=1 -timeout 20m` |
| 3. Live | skipped this round (debate setup: unit + integration only) | — |

> Test polarity: F1a/F1b are **contract** tests — they FAIL on the reviewed head and PASS once a
> migration is added. The P0 premise tests run against the base and PASS there by demonstrating
> the reported bug.

## Problem premise (P0)

| | |
|---|---|
| Claimed symptom | "After such an upgrade, `reconcileRestartBudget` sees a revision change on every budgeted RoleInstance at once. `groupRestartCount` goes back to 0, and exhausted groups are released without `rbg.workloads.x-k8s.io/recover=true`" (issue #617 item 1) |
| Linked issue | #617, OPEN, filed 2026-11-19, labels bug + high priority — still valid |
| Reported component | `pkg/reconciler/roleinstance/sync/restart_budget.go` (restart budget on RoleInstance) |
| Patched component | same |
| Component match | Yes |
| **Verdict** | **Confirmed** (real problem on base) |
| Evidence | `results/p0-premise-base.txt` — on base `45e6e1c2`, `groupRestartRevision` is exactly `sha256(json.Marshal(sorted components))` (encoding-dependent), and a simulated previous-build hash releases an exhausted group without the recover annotation |

## Summary of results

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| F1a | Upgrading v0.9.0-alpha.5 → this PR resets a consumed budget (`groupRestartCount` 1→0) because the stored revision uses the pre-PR format and no migration re-stamps it | 1 | **Confirmed** (contract red on head) | `results/f1-head.txt` — `count=0` after one reconcile |
| F1b | Same upgrade releases an exhausted group without `recover=true`: retained Pods are drained and `restartBudgetExhausted` flips to false | 1 | **Confirmed** (contract red on head) | `results/f1-head.txt` — `exhausted=false` after one reconcile |
| F1-control | Never-restarted budgeted RIs (no stored revision) are untouched by the upgrade | 1 | Confirmed (passes everywhere) | `results/f1-head.txt` |
| PR's own tests | All PR unit + status tests pass on head; envtest restart_policy package passes | 1+2 | Pass | `results/unit-head.txt`, `results/envtest-restart-policy-head.txt` |

## Per-finding detail

### F1 — missing old-format migration for `status.groupRestartRevision` (blocker)

Mechanism: v0.9.0-alpha.5 (tagged 2026-11-21, contains #560, `restart_budget.go` identical to the
review base) persists `groupRestartRevision` as the bare `sha256` of the JSON-encoded components.
The PR changes the format to `<selected ControllerRevision name>/<size-hash>`. On the first
reconcile after an upgrade, `revisionChanged` is true for every budgeted RI with a stored
revision: non-exhausted RIs get `groupRestartCount` reset to 0; exhausted RIs are drained and
reset without the recover annotation. Issue #617 item 1 explicitly requires an in-place migration
("rewrites the old hash format in place without resetting the count or exhaustion") when the fix
lands after a release — and the release now exists. The repo's upgrade e2e only covers
v0.7.0/v0.8.0 → current, which never contained the old format, so CI cannot see this.

- F1a command: `go test -mod=vendor ./pkg/reconciler/roleinstance/sync/ -run TestVerifyF1aUpgradeFromAlpha5MustNotResetConsumedBudget -count=1 -v`
  - Expected (contract): `groupRestartCount` stays 1. Observed on head: reset to 0. **FAIL (red)**
- F1b command: `go test -mod=vendor ./pkg/reconciler/roleinstance/sync/ -run TestVerifyF1bUpgradeFromAlpha5MustNotReleaseExhaustion -count=1 -v`
  - Expected (contract): exhaustion and retained Pods survive the upgrade. Observed: exhaustion released, Pods deleted, no recover annotation. **FAIL (red)**

Harness-bites check: applying `premise/migration-fix.patch` (re-stamp legacy bare-hash revisions
in place, preserving count/exhaustion) flips both tests green and keeps the whole
`pkg/reconciler/roleinstance/...` suite green; evidence in `results/f1-with-migration-fix.txt`.
The patch was then reverted — production code on this branch is untouched.

## Proposed fixes (NOT applied to production here)

- **F1**: add the legacy-format re-stamp from `premise/migration-fix.patch` (or equivalent:
  detect a bare 64-hex revision, rewrite it to the new format in place, preserve
  `groupRestartCount`/`restartBudgetExhausted`), plus a release-note line. Alternative, if
  maintainers explicitly waive the alpha.5 → next upgrade path: document the one-time budget
  reset/exhaustion release in `doc/features/failure-handling.md` and the release notes, and have
  a maintainer record that decision on #617.

## Continuing after the fix (possibly on another machine)

The harness is on branch `verify/pr637-restart-budget-codex` (production code untouched), so it
grafts onto whatever the fixed code is.

1. Get it onto the fixed code:
   ```bash
   git fetch https://github.com/cheyang/rbg.git verify/pr637-restart-budget-codex
   git checkout <fixed-ref>
   git checkout FETCH_HEAD -- docs/verification/pr637-restart-budget-codex \
     pkg/reconciler/roleinstance/sync/restart_budget_upgrade_verify_test.go
   ```
2. Run the unit layer: `go test -mod=vendor ./pkg/reconciler/roleinstance/sync/ -run 'TestVerifyF1' -count=1 -v`
   — F1a/F1b are contract tests: green = fixed.
3. Or one shot from a checkout of this branch: `bash docs/verification/pr637-restart-budget-codex/scripts/re-verify.sh`
   (auto-fetches the current PR head, grafts, runs, reports Fixed/Still-broken per finding).
4. Integration layer: `KUBEBUILDER_ASSETS=$(bin/setup-envtest use 1.31.0 --bin-dir bin -p path) go test -mod=vendor ./test/envtest/testcase/restart_policy/ -count=1`.

Kickoff prompt for a fresh agent: "Fetch branch `verify/pr637-restart-budget-codex` from
https://github.com/cheyang/rbg.git, read `docs/verification/pr637-restart-budget-codex/README.md`,
run `scripts/re-verify.sh` against the current head of https://github.com/sgl-project/rbg/pull/637,
and report per-finding Fixed/Still-broken."
