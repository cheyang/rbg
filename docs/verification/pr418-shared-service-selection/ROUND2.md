# Round 2 — re-verify at `926778e7`

Reviewed range: `f8f2a59f..926778e7`, one commit:
`fix: scope sharedServiceSelection default to RoleInstanceSet roles`.

## Verdict per finding

| # | Round 1 | Round 2 | Evidence |
|---|---------|---------|----------|
| **F1** | CONFIRMED | **FIXED** | canary flipped at L1 (`results/round2/l1-reverify.txt`) and L2. Selector for a StatefulSet role is back to `{group, role}` |
| **F1b** | CONFIRMED | **FIXED** | helper now returns `All` outside `RoleInstanceSet`, so the controller no longer applies a policy the CEL rule rejects |
| **F6** | CONFIRMED | **FIXED** | author added `TestServiceReconciler_reconcileHeadlessService_StatefulSetLeaderWorkerKeepsAllSelector`, asserting literal labels |
| **F3** | CONFIRMED | **FIXED** | envtest `All -> LeaderOnly` context plus an e2e round trip asserting worker pods are recreated without a network identity |
| **F7** | CONFIRMED | **FIXED** | KEP re-synced, `### Validation` restored, `+kubebuilder:default` snippet dropped, PR description rewritten |
| **F2** | CONFIRMED | **documented, not reverted** | deliberate. New `### Upgrade Considerations` names it "the breaking case for endpoints" and states every example leaves the field unset |
| **F5** | CONFIRMED | **documented** | same section: "**every worker Pod is replaced**", on the first reconcile after the image rolls, per the role's rollout strategy |
| **N1** | — | **NEW, blocks merge** | `lint` fails: `goconst`. See below |

## How the fix works

`GetSharedServiceSelection` moved from `*LeaderWorkerPattern` to `*RoleSpec` so it can read the
workload type, and now resolves:

- no `leaderWorkerPattern`, or any workload type other than `RoleInstanceSet` -> `All`
- otherwise the stored value when set
- otherwise `LeaderOnly`

That is the scope check this review asked for. It also ignores a stored `LeaderOnly` outside the
supported scope, so a cluster whose CRDs lag the controller stays safe rather than relying on
admission alone.

## N1 — lint regression (blocks merge)

```
test/e2e/testcase/v1alpha2/component_ordering.go:166:8:
  string `leader` has 3 occurrences, make it a constant (goconst)
```

`lint` passed at `f8f2a59f` and fails at `926778e7`, so this commit caused it. The reported file is
untouched by the PR: `goconst` counts per package, and the new e2e round-trip test raised the count
of the literal `"leader"` in `test/e2e/testcase/v1alpha2/` from 13 to 16, which is what pushed the
package over the threshold. The linter then reports the first occurrence, in a file nobody edited.

`constants.LeaderComponentType` already exists (`api/workloads/constants/constants.go:85`, value
`"leader"`), so the fix is to use `string(constants.LeaderComponentType)` in the new e2e code.

Reproducing locally needs golangci-lint `v1.63.4` (`Makefile:289`). A v2.x binary cannot read this
repo's `.golangci.yml` and exits with "unsupported version of the configuration".

## Harness changes this round

`GetSharedServiceSelection` moved receivers, so the F1b probe stopped compiling: a
**Harness-update**, not a result. Updated to `role.GetSharedServiceSelection()`, after which both
F1 and F1b flipped.

F1 and F1b are now canaries asserting behaviour that has been fixed, so they are red by
construction. Next round should invert them into contract tests (assert the selector is NOT narrowed
for a StatefulSet role, and that the helper returns `All` there) so they become regression guards
instead of standing failures.

## Other test state

The PR's own suites pass with the harness removed: unit tests across `./pkg/reconciler/...`,
`./api/workloads/v1alpha2/`, `./pkg/inplace/...`, and all 6 `SharedServiceSelection` envtest specs.
CI is green on everything except `lint`.
