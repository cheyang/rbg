# Verification: PR #572 — KEP-502 workload status model implementation

Reviewer: Codex (Reviewer B). Round: initial. Head under review: `95ddf06a88747bf2180a67a36e299dbe459e8627` (base `c2896cc0d9ccb8b72de0dda4838f52d8807c9ff1`).

## Premise (P0) — CONFIRMED

The PR implements KEP-502 (merged in #528, which is the base commit). On the base branch
no `Progressing` condition, RI `status.phase`, or reason constants exist
(`grep -rn "RoleInstanceSetProgressing|ProgressingReason" pkg/ internal/ api/` at base → empty),
so the feature is genuinely absent and the premise — "implement the KEP-502 status model" — is valid.
This is a feature PR: the contract under test is the KEP itself (`keps/502-workload-status-model/README.md`).

## Findings and observed-vs-expected

| ID | Claim | Polarity | Layer | Observed on head | Expected (KEP-502) | Verdict |
|----|-------|----------|-------|------------------|--------------------|---------|
| F1 | RIS `establishedInstanceMissing` requires `set.Status.Replicas >= target`, so once the reduced replica count is persisted, an established set missing an instance flips from `Recovering` to `ScalingUp` for the rest of the recovery window | contract | unit | `TestVerifyF1PersistedLossMisclassifiedAsScalingUp` FAILS: reason=`ScalingUp` | KEP: "missing instances of an established set classify as `Recovering`"; test plan: "fault recovery must not be misclassified as scaling" | **Confirmed** |
| F2 | `reportRBGProgressingBlocker` retains a previously confirmed current-generation blocker family across unclassifiable errors (KEP retention rule) | contract | unit (fake client) | `TestVerifyF2RetentionLostAfterProgressingRecompute`: after step-4's `updateRBGProgressingCondition` rewrites the in-memory condition, retention sees nothing; persisted reason becomes `UnclassifiedBlocker` | confirmed family (`WaitingForQuota`) retained until a fresh successful observation | **Confirmed** |
| F3 | `classifyGangIncompatibility` string-matches "not support"/"unsupported" and would classify the subGroupPolicy-backend config error (`pkg/scheduler/volcano/scheduler.go` "is not supported for role ... backed by workload type") as `SchedulerUnsupported` instead of `IncompatibleGangConfig` | contract | unit | FAILS on that message; control (genuine capability gap) passes | config-family split per KEP reason taxonomy | **Confirmed, but unreachable today**: all-RIS RBGs can't produce that message (`emitsRoleInstanceLabel` is true for RIS roles) and mixed-backend RBGs skip the report. Kept as a fragility nit: classification by message substring where a typed error exists |
| F5 | KEP upgrade table: "The current phase cannot be confirmed -> Leave phase absent; do not default to initial creation or Running" | contract | unit | `TestVerifyF5UnconfirmableLegacyInstanceStaysAbsent` FAILS: legacy instance with persisted status, no readiness latch, and missing Pods initializes `Creating` | phase stays absent until facts confirm a workflow | **Confirmed** (severity minor: reporting-only, affects legacy instances that never latched readiness) |
| F4 | KEP upgrade/downgrade: "When an RBG no longer uses RIS for all roles, clean up controller-owned new-model output" | contract | unit | `TestVerifyF4StaleProgressingConditionRemovedWhenNoLongerAllRIS` FAILS: stale `Progressing` condition survives the transition to a mixed-backend RBG | controller-owned `Progressing` removed on the transition | **Confirmed** (severity minor: reachable only via role backend annotation edits) |

Harness-bites checks (fix applied temporarily, then reverted; production diff empty afterwards):

- F1: holding the persisted current-generation `Recovering` reason in `establishedInstanceMissing` flips the contract test green; all 17 subtests of the PR's own `TestComputeSetProgressingCondition` still pass.
- F2: snapshotting the persisted `Progressing` condition before the step-4 rewrite and retaining from the snapshot flips the contract test green; the full `internal/controller/workloads` package still passes.
- F5: threading a `legacyStatus` fact (persisted status exists) into `nextRoleInstancePhase` flips the contract test green while keeping the genuinely-new control and the PR's own `TestNextRoleInstancePhase` green.
- F4: `apimeta.RemoveStatusCondition` on the non-all-RIS early return flips the contract test green; PR-owned RBG progressing tests still pass.

Baseline on head: every PR-owned unit test passes, and both envtest packages the PR touched
pass (`test/envtest/testcase/stateful`, `test/envtest/testcase/rbg` — see `results/integration-envtest.txt`).

## Files

- `pkg/reconciler/roleinstanceset/progressing_verify_test.go` — F1 (+controls).
- `internal/controller/workloads/progressing_retention_verify_test.go` — F2 (+control).
- `internal/controller/workloads/gang_classify_verify_test.go` — F3, F4 (+controls).
- `pkg/reconciler/roleinstance/phase_init_verify_test.go` — F5 (+control).
- `results/` — captured outputs.

## Re-running

```bash
go test ./pkg/reconciler/roleinstanceset/ -run 'TestVerifyF1' -v
go test ./internal/controller/workloads/ -run 'TestVerifyF2|TestVerifyF3|TestVerifyF4' -v
go test ./pkg/reconciler/roleinstance/ -run 'TestVerifyF5' -v
```

or `bash docs/verification/kep-502-status-model-impl/scripts/re-verify.sh` from this branch.

## Continuing after a fix

All five findings are contract tests: they FAIL on the reviewed head and must PASS when fixed
(no canaries to invert). `scripts/re-verify.sh` fetches the current PR head, grafts this
harness onto it, and reports Fixed / Still-broken per finding.

## Round 2 (incremental) — head effc2f8ee983d6c5a95be12f07ec4a19d94aec14

Delta reviewed: `95ddf06a..effc2f8e` (+1218/-166, 18 files). Manifest finding IDs are now the
debate-ledger IDs (see `idMapping` in verify-manifest.json; test function names keep their
round-1 codex-local numbering).

### Round-2 verdicts (re-verify.sh, unit layer; integration = repo envtest suites)

| Ledger ID | Verdict | Evidence |
|-----------|---------|----------|
| F1 | **Fixed** | 3/3 pass: persisted loss stays `Recovering` via the persisted-reason hold; fresh-loss and genuine-scale-up controls green |
| F2 | **Fixed** | 2/2 pass: `risReasonToRBG` maps `InvalidSpec`; `rbgCurrentBlocker` collects it; stale-generation control green |
| F3 | **Fixed** | 3/3 pass: entry snapshot carried through step-4 recompute; retained over unclassifiable step-8 error; fresh success (`finalizeRBGProgressingCondition`) clears |
| F4 | **Still broken** | mixed-backend early return still never removes the stale `Progressing` condition |
| F5 | **Fixed** | 2/2 pass: `instanceProgressingBlocker` selects by family precedence regardless of append order |
| F6 | **Still broken** | `nextRoleInstancePhase("")` still defaults to `Creating` for the unconfirmable legacy instance |
| F7, F8 | **Still open** | `roleinstanceset_controller.go` untouched by the delta; code-path findings stand |
| F9 | **Fixed (code-path)** | `unreadyDependencies` reuses `CheckWorkloadReady` incl. the maxSkew carve-out; PR adds `TestUnreadyDependencies` |
| F10 | **Fixed (cost half)** | `observeGangWaiting` gated on `GetGangStrategy` + controller-ref ownership; coverage half was refuted in round 1 |
| T2 | **Fixed** | PR suite now pins "persisted recovering reason keeps replacement stable after counters decay" |
| O3 | **Still broken** | `classifyGangIncompatibility` unchanged |

### New findings this round (both revision regressions; contract passes on 95ddf06a, fails on effc2f8e)

- **F11** (`pkg/reconciler/roleinstanceset/progressing_establishment_verify_test.go`): removing
  `InstancesNotReady` from `setEstablishmentReasons` makes "established" depend on the single
  persisted Progressing slot. Any established set whose last persisted reason is
  `InstancesNotReady` (readiness flap, scale-up readiness window, post-recovery readiness window)
  reports `Creating` on member loss — initial-establishment semantics for a long-established set.
  Bites: restoring the reason flips N1/N2 green; it also flips the author's pinned never-ready
  case red, proving the real fix needs durable establishment evidence (e.g. an establishment
  generation marker), not a reason-list edit.
- **F12** (`pkg/reconciler/roleinstanceset/progressing_partition_verify_test.go`): `updateActive`
  dropped the `UpdatedReplicas < ExpectedUpdatedReplicas` conjunct, but `shouldAdvanceCurrentRevision`
  never advances `CurrentRevision` while `partition > 0`, so a partition-scoped rollout whose scope
  is complete reports `RoleInstanceUpdated` forever; via child-activity aggregation the RBG never
  reaches `Converged`, contradicting the PR's own partition-aware completion
  (`rbgOwnTargetComplete` finalPartition logic + PR body). Bites: revision-inequality AND
  (`UpdatedReplicas < ExpectedUpdatedReplicas || observedReplicas > target`) flips F12 green while
  keeping the author's surge-gap and RecreatePod-gap cases green.

Also recorded (code-path only): an optimistic-lock Conflict from the resourceVersion-pinned SSA
spec patch reaches the `else if entryBlocker != nil` finalize branch, clearing a confirmed blocker
family without a successful observation (F13, minor); the admission ratchet compares error strings
and therefore no longer rejects becoming-RBGSet-managed with a pre-existing excluded-ordinals
annotation (F14, minor).

Round-2 results: `results/round2-verdicts.txt`, `results/reverify/`, `results/unit-round2-head.jsonl`.
Integration: envtest rbg (100.5s) + stateful (42.3s) green on head; see results/round2-verdicts.txt.
