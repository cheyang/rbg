# Verification — PR #508: persist stateful rollback replacement state (Reviewer B / Codex)

PR: https://github.com/sgl-project/rbg/pull/508 (fixes #482)
Reviewed head: `a1d164a95d146bc209d73627f3a14cdb877b83d4`
Base (merge-base): `ecdfd6e601de64adc0d0892c7ad05f75e85873a8`
Mode: audit (previous Codex passes timed out mid-verification; this commit completes the round)

## Audit-round completion (2026-10-22, head unchanged `a1d164a`)

Everything below was re-run fresh this round via `scripts/re-verify.sh` after one
harness fix (KUBEBUILDER_ASSETS must be absolute inside the grafted worktree, and
setup-envtest now falls back to $HOME/go/bin — a relative path made the first
integration attempt fail to find etcd; harness bug, not a PR issue). Final
coherent run: premise REPRODUCED on base, C1-C7 FIXED on head, I1 PASS (3/3
specs), plus a full `-race` sweep over api/workloads/...,
pkg/reconciler/roleinstanceset/..., internal/controller/workloads/... (all ok,
results/unit-sweep-head.out). Review delta this round is empty
(a1d164a..a1d164a); `.last-reviewed` is unchanged.

Findings carried into the debate document (findings-codex.md): F1 (minor,
pre-existing, leader-handover informer-lag window), F2 (minor, CRD co-upgrade
documented only in the PR body), F3 (minor, no literal process-restart test),
F4 (nit, whole-scope surge retention). No new findings; no blockers/majors.

## Premise (P0) — CONFIRMED on base

Issue #482 claims: the early-rollback replacement signal is process-local
(`earlyRollbackReplacementSignals` sync.Map), so a controller restart after the
stale base is deleted but before the replacement is Ready collapses the rollout
topology and condemns the only Ready surge instance, violating maxUnavailable=0.

Reproduced at unit level against the merge-base with
`harness/zz_codex_premise_base_test.go` (contract polarity — asserts the desired
behavior, so it FAILS on base). Scenario: replicas=1, maxSurge=1,
maxUnavailable=0, current==update==A, stale B base already deleted, replacement
base (ord 0) at A unready, surge (ord 1) Ready at A.

| step | observed on base `ecdfd6e6` |
| --- | --- |
| signal recorded, pre-restart | `endOrdinal=2, activeSurge=1` — surge retained (sanity) |
| after simulated restart (map reset) | `endOrdinal=1, activeSurge=0, inRollout=false` — surge falls into `condemned` and is deleted while the replacement is unready |

Raw output: `results/premise-base.out` (`--- FAIL: TestCodexP0RestartLosesReplacementSignal`
with the collapse values). Component match: issue names the stateful
RoleInstanceSet controller; the patch touches exactly
`pkg/reconciler/roleinstanceset/statefulmode/*` plus the CRD status schema. Premise
valid, component matches, fix direction (persist intent in status) matches the
issue's own suggestion.

## Claims and results (head `a1d164a`)

All tests below are **contract** tests (must PASS on fixed code) except P0,
which is a contract test evaluated on the base (must FAIL there).

| ID | claim | layer | result | evidence |
| --- | --- | --- | --- | --- |
| P0 | restart loses replacement signal on base | unit (base) | reproduced (FAILs on base, as expected) | results/premise-base.out |
| C1 | persisted state alone (fresh controller) retains surge while replacement unready | unit | PASS | results/unit-contracts-head.out |
| C2 | surge released and state cleared once the replacement converges | unit | PASS | results/unit-contracts-head.out |
| C2b | terminating replacement does not count as converged | unit | PASS | results/unit-contracts-head.out |
| C3 | intent persisted BEFORE stale-base deletion; failed persist leaves target untouched | unit | PASS | results/unit-contracts-head.out |
| C4 | target-revision change invalidates the scope | unit | PASS | results/unit-contracts-head.out |
| C5 | replicas/partition changes restrict completion to the overlap; empty overlap ends protection | unit | PASS | results/unit-contracts-head.out |
| C6 | externally-terminating stale base is recorded; maxSurge=0 never writes state | unit | PASS | results/unit-contracts-head.out |
| C7 | RV gate blocks reconcile until the set cache observes the write; recreated UID not blocked | unit | PASS | results/unit-contracts-head.out |
| I1 | PR envtest suite (real API server): seeded-state recovery, full A→B→A rollback with partition decrease, stale-rollout recovery | integration | PASS (3/3 specs) | results/envtest-head.out |
| S1 | full unit sweep `-race` over affected packages | unit | PASS | results/unit-sweep-head.out |
| S2 | `make manifests generate` produces no tracked diff | codegen | PASS | this README (run interactively) |

## Harness-bites check (production code reverted afterwards)

| sabotage | expected | observed |
| --- | --- | --- |
| `rolloutReplacementStateActive` forced to `return false` | C1/C2b/C5 red | `--- FAIL` on exactly those three |
| persist-before-delete block removed from `applyTargetUpdate` | C3 red | `--- FAIL: TestCodexPersistIntentPrecedesDelete` |

## How to re-run

From a checkout of this branch:

```bash
bash docs/verification/stateful-rollback-codex/scripts/re-verify.sh            # resolve PR head automatically
bash docs/verification/stateful-rollback-codex/scripts/re-verify.sh <sha>      # pin a ref
```

The script: (1) checks the premise against the merge-base in a throwaway
worktree, (2) grafts `zz_codex_contracts_test.go` onto the target ref and runs
the unit contracts with `-race`, (3) runs the envtest stateful suite when
`KUBEBUILDER_ASSETS` (or `bin/setup-envtest`) is available, (4) prints per-finding
verdicts and the incremental review delta from `.last-reviewed`.

Live layer (L3, intentionally skipped this round per round instructions): a
literal process kill + leader re-election mid-replacement. Expected signal:
surge instance UID unchanged while the replacement base is unready; see the PR
body's own delayed-watch experiment for the timing-dependent variant.

## Continuing after the fix

The contract tests assert behavior, not internals, except C4–C6 which call
`rolloutReplacementStateActive` / `persistTerminatingRolloutReplacement`
directly (package-internal). If a fix revision renames those, re-verify reports
Harness-update — adjust the calls, keep the assertions. No test needs inversion:
all head-side tests are plain contracts.
