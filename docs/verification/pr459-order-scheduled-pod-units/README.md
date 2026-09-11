# Verification — PR #459: count scheduled Pods in role replica units

**PR:** https://github.com/sgl-project/rbg/pull/459
**Topic:** `pr459-order-scheduled-pod-units`
**Branch:** `verify/pr459-order-scheduled-pod-units` (reviewer fork `cheyang/rbg`)
**Reviewed head:** `b4f10e085ab5ff709cd21c2c94a119a68f0ef960` (PR `feat/fix-order-scheduled-units`)

## Premise verdict (P0): CONFIRMED

> `OrderScheduled` compares `ScheduledReplicas` (a raw scheduled Pod count) against
> `RoleStatus.Replicas` (a replica count), so for multi-Pod-per-replica roles the gate passes
> as soon as a fraction of the current batch is scheduled. (PR §Ⅰ.)

- **Mechanism on base** (`07fc643f`): `getScheduledReplicas` returns the count of Pods with
  `nodeName` set; that raw Pod count is stored in `ScheduledReplicas`; the `OrderScheduled`
  branch of `canProceedToNextBatch` compares it against `CurrentReplicas` (replicas). For a
  4-Pod-per-replica role with 2 current replicas, 4 scheduled Pods satisfy `4 >= 2` and the
  next batch advances even though only half the current batch is scheduled.
- **Reproduction**: the PR's own contract test
  `TestCalculateScalingForAllCoordination_OrderScheduled`, grafted onto the merge-base and
  run, FAILS for every multi-Pod partial-scheduling case (leader_worker, custom_components,
  custom_component_with_default_size, negative_component_size, component_sizes_cancel) and for
  the zero-Pod-per-replica cases (only_negative_component, empty_custom_components,
  zero_sized_custom_component), which on base are *permanently stuck* (`0 < currentReplicas`
  can never be satisfied when a role creates no Pods). Boundaries (`none scheduled`,
  `all scheduled`) pass. → `results/L1-base-bites-fails.txt`.
- **Live layer**: not attempted and not needed. The defect is a count-unit mismatch in a pure
  calculation (`CalculateScalingForAllCoordination`); the unit layer exercises the real
  `getPodSchedulingCounts` against a controller-runtime fake client and fully decides it. The
  ACK cluster is additionally blocked by pre-existing legacy RoleInstance `restartPolicy`
  data unrelated to this PR.

## Observed-vs-expected table

| ID | Claim | Polarity | Layer | Base | Head | Verdict |
| --- | --- | --- | --- | --- | --- | --- |
| P0 | OrderScheduled gate passes prematurely for multi-Pod-per-replica roles | contract | unit | FAIL | PASS | **Confirmed** |
| F1 | Fix: gate now waits for all expected Pods of the current batch (`ScheduledPods < ExpectedPods`, `ExpectedPods = currentReplicas * ComputeSubGroupSize`); single-Pod preserved; zero-Pod satisfies naturally; terminating Pods excluded | contract | unit | FAIL | PASS | **Proven** |
| F2 | Latent blast-radius fix: `ComputeSubGroupSize` clamps non-positive `InstanceComponent.Size` to 0 (matches RI pod-build loop; fixes gang sizing). Negative sizes are API-reachable (no `Minimum=0` marker) | contract | unit | FAIL | PASS | **Proven** |
| F3 | Guard: new `GetRole` error path for a removed role is swallowed (`0,0,nil`); remaining roles still scale | contract | unit | PASS | PASS | **Preserved** (guard, not a bite) |

Evidence: `results/L1-base-bites-fails.txt` (base), `results/L1-head-pass.txt` (head).

## What changed (factual)

- `rolebasedgroup_controller.go`: `getScheduledReplicas` → `getPodSchedulingCounts`, returning
  `(scheduledPods, expectedPods int64)`. `expectedPods = currentReplicas * ComputeSubGroupSize(role)`;
  if `0`, returns early so zero-Pod roles pass the gate naturally. Pods with `DeletionTimestamp`
  set are skipped (terminating Pods don't satisfy the gate). A removed role (`GetRole` error)
  yields `(0,0,nil)`.
- `scaler.go`: `RoleScalingState` drops `ScheduledReplicas`, adds `ScheduledPods`/`ExpectedPods`
  (`int64`). `OrderScheduled` branch: `if state.ScheduledPods < state.ExpectedPods { return false }`.
- `helper.go`: `ComputeSubGroupSize` clamps each component: `total += max(*c.Size, 0)`.
- Tests: new `TestCalculateScalingForAllCoordination_OrderScheduled` (multi-Pod patterns ×
  partial/full/missing/terminating scheduling) + `TestCalculateScalingForAllCoordination_MissingRole`;
  `scaler_test.go` mechanically migrated to the new fields.

## Findings & review notes (no blockers / majors)

- **Premise Confirmed, fix Proven.** The count-unit correction is correct and the strictness
  (require *all* current-batch Pods scheduled before advancing) is the intended `OrderScheduled`
  semantics — the old permissive behavior was the bug. Stricter gate does not stall a
  previously-working path: a Pod that can never schedule is a real resource problem the operator
  must see, not a reason to keep scaling onto an overloaded cluster.
- **F2 — bundled blast-radius fix (positive).** The `max(*c.Size, 0)` clamp also corrects
  gang sizing (`pkg/scheduler/common/gang_strategy.go`, `pkg/scheduler/volcano/scheduler.go`)
  which previously under-counted on negative `InstanceComponent.Size`. Reachable: the field is
  `*int32` with no `+kubebuilder:validation:Minimum=0` marker, so the API accepts negatives.
  *Nit (out of scope):* `pkg/reconciler/roleinstance/instance_status.go:152`
  (`componentSize += *component.Size`) remains unclamped on a separate RI-status path — not this
  PR's job, but the clamp is inconsistent across the codebase.
- **Suggested (minor):** add `+kubebuilder:validation:Minimum=0` to `InstanceComponent.Size` so
  the clamp becomes defensive-only and the API rejects nonsensical sizes at admission.
- **Test coverage (minor):** the unit test is thorough (multi-Pod patterns, partial scheduling,
  terminating Pods, missing Pods, zero-Pod roles, removed roles) and proves the fix at the
  calculation layer. No e2e covers the multi-Pod `OrderScheduled` progression end-to-end; the
  existing `test/e2e/.../coordinated_policy.go` uses standalone (1-Pod) roles + `OrderReady`.
  Given the change is a pure-function count-unit fix with thorough unit coverage, e2e is a
  nice-to-have, not a merge blocker.
- **OrderReady asymmetry (unchanged, out of scope):** `OrderReady` still compares
  `ReadyReplicas < CurrentReplicas` (replica units). `ReadyReplicas` comes from
  `RoleStatus.ReadyReplicas` (replica count), so it remains consistent. Not touched by this PR.

**Suggested review verdict:** `COMMENT` (no blocker/major). The fix is correct and proven.

## How to re-verify (one line)

```bash
bash docs/verification/pr459-order-scheduled-pod-units/scripts/re-verify.sh
```

With no argument, `re-verify.sh` resolves the current PR head from `manifest.pr`
(`git fetch https://github.com/sgl-project/rbg.git pull/459/head`), grafts the harness
(`docs/verification/pr459-order-scheduled-pod-units/` + `api/workloads/v1alpha2/helper_verify_test.go`)
onto it, runs the unit layer, and prints per-finding **Fixed / Still-broken / Partial /
Harness-update**, applying polarity (contract tests should go green). Exit 0 iff all fixed.

To re-verify a specific ref: `bash scripts/re-verify.sh <ref>`.

### Prerequisites

- `go` (1.27 used here) and `jq` on `PATH`.
- No cluster / no envtest assets needed — unit layer only.

### Continuing after the author pushes a fix

```bash
git switch verify/pr459-order-scheduled-pod-units && git pull
bash docs/verification/pr459-order-scheduled-pod-units/scripts/re-verify.sh
# review the last-reviewed..head delta printed by the script, then advance the marker:
#   echo <head-sha> > docs/verification/pr459-order-scheduled-pod-units/.last-reviewed
#   git add docs/verification/pr459-order-scheduled-pod-units/.last-reviewed
#   git commit -m "review: advance last-reviewed"
```

Polarity: all three findings are `contract` tests — on fixed code they must go **green**
(`FIXED`). F1/F2 are red on base (the reproduction); F3 is green on both (a guard). If a future
fix changes a test's shape, the script reports `HARNESS-UPDATE` — adjust the test, then re-run.

## Copy-paste kickoff (fresh agent, any machine)

```
You are resuming a review-pipeline verification for PR https://github.com/sgl-project/rbg/pull/459.
The harness lives on branch verify/pr459-order-scheduled-pod-units of cheyang/rbg.
1. git clone https://github.com/cheyang/rbg && cd rbg && git switch verify/pr459-order-scheduled-pod-units && git pull
2. Install jq (static binary: curl -sL https://github.com/jqlang/jq/releases/download/jq-1.7.1/jq-linux-amd64 -o /tmp/jq && chmod +x /tmp/jq; export PATH=/tmp:$PATH)
3. bash docs/verification/pr459-order-scheduled-pod-units/scripts/re-verify.sh
4. Read the printed Fixed/Still-broken/Partial table and the last-reviewed..head delta.
   Premise P0 is Confirmed; F1/F2 are contract tests (red on base, green on fixed); F3 is a guard.
5. Commit+push the advanced .last-reviewed marker after reviewing the delta.
```
