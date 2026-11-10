# Verification — PR #560 (maxGroupRestarts budget + FromCreation policy)

Reviewer: Codex (Reviewer B). Head under review: `ef35b48109068fb57b4ad7648e530b45ebee9c74`, base `fd160cc964b0e1438bead4ad7324d0a8f0bf5eac`.
Harness-only branch: no production code changes.

## P0 premise — CONFIRMED (runs against BASE)

Claim (PR body): RBG may automatically rebuild a RoleInstance an unbounded number of
times per Pod-template revision; backoff may reset after a quiet period.

On base `fd160cc` (`results/unit-premise-base.txt`):
- `TestVerifyP0EveryReadyFaultCycleTriggersRecreate`: 50 consecutive Ready→fault cycles each
  return `shouldRecreateInstance=true` — no bound exists in the decision path.
- `TestVerifyP0BackoffCounterSelfResets`: after a 1h quiet period `updateRestartTracking`
  resets `RestartCount` 42→0 — backoff never becomes a bound either.

Component match: issue #542 (referenced by the PR as "Refs #542") names
`pkg/reconciler/roleinstance/sync/instance_scale.go`; that is exactly the file the PR
changes. Symptom match: member-deletion / pre-Ready faults never trigger group recovery
(#542) ↔ the PR's new `RecreateRoleInstanceOnPodRestartFromCreation` policy.

## Findings matrix (head ef35b481)

| ID | Claim | Polarity | Layer | Test / script | Head result |
|----|-------|----------|-------|---------------|-------------|
| F1 | Stale status write must not roll back live external conditions (PR's own invariant + its failing CI test `TestStaleStatusPreservesLiveRestartBudget`) | contract | unit | `TestVerifyStaleStatusCannotRollbackLiveExternalCondition`, `TestStaleStatusPreservesLiveRestartBudget` | **FAIL** (stale `CustomReady` overwrites live) |
| F2 | `calculateDiffsWithExpectation` must satisfy the repo gocyclo threshold (30) | contract | lint | `scripts/check-gocyclo.sh` | **FAIL** (complexity 33; base ≤30) |
| F3 | Resize must not reset the budget (PR doc: "resizing cannot be used to bypass exhaustion") | contract | unit | `TestVerifyResizeMustNotResetBudget` | **FAIL** (RIS-injected `component-size` template label is in the hash input) |
| F4 | Never-Ready crashloop must eventually exhaust / signal Degraded | canary | unit | `TestVerifyNeverReadyCrashloopNeverExhausts` | PASS (documents gap: budget never fires, no Degraded) |
| F5 | Recovery during the drain window must not re-trigger/re-exhaust | canary | unit | `TestVerifyRecoveryDuringDrainRetriggers` | PASS (documents gap: zero budget re-exhausts on the still-terminating old member) |
| F5b | Failed drain must not burn an attempt without rebuild | canary | unit | `TestVerifyFailedDrainBurnsBudgetAttempt` | PASS (attempt consumed, drain never retried, exhaustion blocked by Restarting latch) |
| ACC | Exact accounting / exhaustion / retention / recovery contracts | contract | unit | `TestVerifyBudgetExactAccountingContract`, `TestVerifyBudgetRevisionIdentityContract` | PASS |
| ACC2 | Full lifecycle + CEL rejection on a real API server | contract | integration | `test/envtest/testcase/restart_policy`, `test/envtest/testcase/deprecationv1a1` | PASS (216s + 6s, k8s 1.31.0 envtest) |

## Harness-bites proof

- F1: in a scratch worktree, deleting `RoleInstanceCustomReady`/`RoleInstanceInPlaceUpdateReady`
  from `controllerOwnedConditionTypes()` and sourcing them from the live object (same pattern
  as Degraded) flips both failing tests green; `./pkg/reconciler/roleinstance/...` stays green.
  Reverted afterwards.
- F3: excluding the `ComponentSizeLabelKey` template label from `groupRestartRevision` flips
  `TestVerifyResizeMustNotResetBudget` green; the author's sync tests still pass. Reverted.

## Re-running

```bash
# unit + lint layers (from this branch)
go test ./pkg/reconciler/roleinstance/ ./pkg/reconciler/roleinstance/sync/ -run 'TestVerify|TestStaleStatusPreservesLiveRestartBudget' -count=1
bash docs/verification/maxgrouprestarts-budget/scripts/check-gocyclo.sh

# integration (envtest, real apiserver)
KUBEBUILDER_ASSETS=$HOME/.local/share/kubebuilder-envtest/k8s/1.31.0-linux-amd64 \
  go test ./test/envtest/testcase/restart_policy/... ./test/envtest/testcase/deprecationv1a1/... -count=1 -timeout 15m

# after a fix lands on the PR
bash docs/verification/maxgrouprestarts-budget/scripts/re-verify.sh
```

Polarity note: F4/F5/F5b are canaries — green today; when the fix lands they must FLIP
to red and then be inverted. F1/F3 are contracts — red today, must go green. F2 is a
script contract — exits 1 today, must exit 0.

## L3 (skipped per review constraints)

No live-cluster layer was run. What to observe live after fixes: exhaust a budgeted RI on a
real cluster and confirm kubelet releases resources while Pod objects are retained; resize the
role and confirm the budget marker survives; recover during the grace window and confirm no
re-exhaustion.
