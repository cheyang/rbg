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
| F4 | KEP upgrade/downgrade: "When an RBG no longer uses RIS for all roles, clean up controller-owned new-model output" | contract | unit | `TestVerifyF4StaleProgressingConditionRemovedWhenNoLongerAllRIS` FAILS: stale `Progressing` condition survives the transition to a mixed-backend RBG | controller-owned `Progressing` removed on the transition | **Confirmed** (severity minor: reachable only via role backend annotation edits) |

Harness-bites checks (fix applied temporarily, then reverted; production diff empty afterwards):

- F1: holding the persisted current-generation `Recovering` reason in `establishedInstanceMissing` flips the contract test green; all 17 subtests of the PR's own `TestComputeSetProgressingCondition` still pass.
- F2: snapshotting the persisted `Progressing` condition before the step-4 rewrite and retaining from the snapshot flips the contract test green; the full `internal/controller/workloads` package still passes.
- F4: `apimeta.RemoveStatusCondition` on the non-all-RIS early return flips the contract test green; PR-owned RBG progressing tests still pass.

Baseline on head: every PR-owned unit test passes, and both envtest packages the PR touched
pass (`test/envtest/testcase/stateful`, `test/envtest/testcase/rbg` — see `results/integration-envtest.txt`).

## Files

- `pkg/reconciler/roleinstanceset/progressing_verify_test.go` — F1 (+controls).
- `internal/controller/workloads/progressing_retention_verify_test.go` — F2 (+control).
- `internal/controller/workloads/gang_classify_verify_test.go` — F3, F4 (+controls).
- `results/` — captured outputs.

## Re-running

```bash
go test ./pkg/reconciler/roleinstanceset/ -run 'TestVerifyF1' -v
go test ./internal/controller/workloads/ -run 'TestVerifyF2|TestVerifyF3|TestVerifyF4' -v
```

or `bash docs/verification/kep-502-status-model-impl/scripts/re-verify.sh` from this branch.

## Continuing after a fix

All four findings are contract tests: they FAIL on the reviewed head and must PASS when fixed
(no canaries to invert). `scripts/re-verify.sh` fetches the current PR head, grafts this
harness onto it, and reports Fixed / Still-broken per finding.
