# KEP-473 topology-aware scheduling — verification harness (Reviewer A: Claude)

PR: https://github.com/sgl-project/rbg/pull/492
Head reviewed: `63b402f27ef9fe1289275cb4d66850ff865f1371` (`.last-reviewed`)
Base: `0821cb5b5a93960efd9a607393def80fdee8347a`
Harness branch: `verify/kep473-topology-claude` (additive only — production code untouched;
verified with `git status` after the bites check revert).

## Premise (P0) — CONFIRMED

The PR implements KEP-473. Claimed problem: RBG workloads need topology placement
constraints (instance packing, prefill/decode co-location) that no current component
provides. Evidence on **base** (`results/premise-base-grep.out`): no `TopologyConstraint`
type, no Volcano `networkTopology` compilation, no HyperNode reads anywhere in
`pkg/`, `api/`, `internal/`. Greenfield feature — the capability genuinely does not
exist, and the touched components (scheduler placement, API, controller) are the ones
the KEP names. Component match holds.

## Per-finding observed-vs-expected

| ID | Claim | Polarity | Layer | Test | Observed on head | Expected | Verdict |
|----|-------|----------|-------|------|------------------|----------|---------|
| P0 | Topology support absent on base | contract | grep | `results/premise-base-grep.out` | no topology code on base | capability missing | **Confirmed** |
| F1 | Subset gang keeps `schedulerName: volcano` on uncovered roles (KEP-430) | contract | unit | `TestVerifyF1GangSubsetPlanKeepsVolcanoSchedulerForUncoveredRole` | **FAIL (red)** — router role gets no schedulerName | volcano for every role while a gang is configured | **Confirmed (regression)** |
| F1-diff | Legacy injector still injects (differential evidence) | canary | unit | `TestVerifyF1DifferentialLegacyInjectorStillInjects` | PASS — legacy path injects volcano | documents the divergence | Divergence proven |
| F2 | Gang superset + subset topology rule rejected as SchedulerUnsupported | canary | unit | `TestVerifyF2GangSupersetWithSubsetTopologyRuleRejected` | PASS — rejected, no PodGroup written, misleading "cross-role" wording | current design per KEP | Documented |
| C-compose | Equal-scope gang+topology composes into one PodGroup | contract | unit | `TestVerifyGangTopologyEqualScopeComposesOnePodGroup` | PASS — minMember>0, networkTopology set, pod annotations match | composition promise | Guarded |

Supporting sweeps (all green): `results/unit-prsuite.out` (pkg/scheduler/..., api/workloads/...,
internal/controller/workloads), `results/envtest-topology.out` (envtest topology suite:
1 spec passed — finalizer lifecycle + delete/recreate).

Note: `go test ./...` also shows 3 failures in `test/chart`
(`TestManagerFlagReflectsToggle` etc.); they reproduce **identically on the base
commit** (local Helm rendering issue) — pre-existing environment noise, not PR-caused.
See findings doc.

## F1 mechanism (the reproduction)

- Base contract (base `pkg/scheduler/volcano/scheduler.go:118`): *"schedulerName is
  injected for every role: Volcano must own the scheduling decision for the whole group,
  otherwise excluded roles would be placed by a different scheduler and could starve the
  gang of resources."* Base wired this via `pod_reconciler.go:152` →
  `InjectPodSchedulingFields`.
- Head replaces the call site with `InjectPlacementSchedulingFields`
  (`pkg/scheduler/volcano/placement.go:149`), which returns early when the role is not in
  any top-level placement group. For a CoordinatedPolicy gang rule scoped to
  `roles: [prefill]` while the RBG also has `router`, the router role loses
  `schedulerName: volcano` and falls back to the default scheduler.
- The sibling backend (`pkg/scheduler/k8s-scheduler-plugin/placement.go`) still delegates
  gang-only plans to the legacy injector, so the two backends now disagree for the same
  configuration.
- Harm: uncovered-role pods are no longer placed by Volcano while the gang's PodGroup
  still competes for the same nodes — the exact starvation scenario the base comment
  describes. Rolling updates migrate existing pods to the default scheduler silently.

### Harness-bites check (Step 4 of the verifier skill)

Applied the proposed fix (inject schedulerName for uncovered roles whenever the plan
carries a gang) to `placement.go`, re-ran: F1 contract test went **green**, full
`pkg/scheduler/...` + `internal/controller/workloads` suites stayed green. Fix reverted;
`git status` shows only the additive harness files. A fix exists and is cheap.

## F2 mechanism

`buildPlacementGroups` nests a topology rule's group under a containing gang scope;
`buildTopologySubGroups` (placement.go:622) only renders single-role per-instance
children, so any other nested topology scope returns `SchedulerUnsupportedError`, which
the controller turns into `TopologyTranslated=False` + role-creation gating. The KEP
sanctions this ("containment scopes form a parent/child placement tree **when a backend
can render it**"), so it is a documented limitation, not a defect — but the rejection
message calls a single-role child "cross-role topology child group roles [prefill]",
which is misleading. Canary-locked so a future rendering improvement flips it.

## How to re-run

```bash
bash docs/verification/kep473-topology/scripts/re-verify.sh            # grafts onto current PR head
bash docs/verification/kep473-topology/scripts/re-verify.sh <fixed-ref> # or a specific fix commit
```

Unit layer: `go test ./pkg/scheduler/volcano/ -run 'TestVerify' -v`
Integration layer: `KUBEBUILDER_ASSETS=$(setup-envtest use -p path 1.34.x) go test ./test/envtest/testcase/topology/ -v`

Interpretation after a fix:
- F1 fixed ⇔ `TestVerifyF1GangSubsetPlanKeepsVolcanoSchedulerForUncoveredRole` passes.
- F1-diff canary: if the legacy injector is removed/changed, invert or drop.
- F2 canary: if Volcano learns to render nested scopes, the canary flips red — promote
  it to a contract test then.
- C-compose must stay green forever.

## Continuing after the fix (handoff)

On any machine with git+go+jq:
```bash
git clone https://github.com/sgl-project/rbg.git && cd rbg
git fetch https://github.com/cheyang/rbg.git verify/kep473-topology-claude && git checkout FETCH_HEAD
bash docs/verification/kep473-topology/scripts/re-verify.sh
```
Copy-paste kickoff prompt: "You are resuming a PR-492 verification. Checkout branch
verify/kep473-topology-claude, run docs/verification/kep473-topology/scripts/re-verify.sh,
apply polarity from the README table, report Fixed/Still-broken per finding, then
advance .last-reviewed to the new head."

## Round 2 (head `dab8726249aa6193d2db15b701df84f12a3787a0`, base `79f7e5858fdc30ffaeadef4aea12c3809dcb348d`)

The PR was rebased and extended with review-fix commits (contract freeze, UID-keyed
policy release, flat cross-role subGroup rendering). Re-verify results on the new head:

| ID | Claim | Polarity | Verdict on `dab87262` | Evidence |
|----|-------|----------|------------------------|----------|
| P0 | Topology support absent on base | contract | Confirmed (unchanged) | `results/premise-base-grep.out` |
| F1 | Uncovered gang roles lost `schedulerName: volcano` | contract | **FIXED** — gang-only plans route through the legacy KEP-430 injector (`placement.go` InjectPlacementSchedulingFields) | `TestVerifyF1GangSubsetPlanKeepsVolcanoSchedulerForUncoveredRole` green |
| F1-diff | Placement vs legacy injector divergence | contract (converted from canary) | **FIXED** — both paths now agree for every role | `TestVerifyF1GangOnlyPlanInjectionMatchesLegacy` green |
| F2 | Cross-role containment rejected as SchedulerUnsupported | contract (promoted from canary) | **FIXED** — whole-scope topology child renders as a flat `subGroupPolicy` entry (`role In [...]` + child networkTopology); gang parent keeps the KEP-430 `rbg.Name` | `TestVerifyF2CrossRoleTopologyChildRendersFlatSubGroupPolicy` green |
| F3 (NEW) | Topology-contract freeze hashes the whole-group gang scope's role set, so an admission-allowed role add/remove changes the contract ID → `TopologyPolicyLate` gates role reconciliation of a live RBG | contract | **STILL-BROKEN (red, 2/2 tests)** — plan layer: removing `router` changes the ID `e15ade6e…` → `36ee84b0…`; controller layer: `ensureTopologyContract` then returns `TopologyPolicyLate` ("delete and recreate the RoleBasedGroup") for the live RBG | `TestVerifyF3TopologyContractStableAcrossNonTopologyRoleChanges` + `TestVerifyF3AdmissionAllowedRoleRemovalFreezesLiveRBG` red on head |
| F5 (NEW) | Contract-ID preimage concatenation is ambiguous | contract | **Refuted as a practical bug** — every alias needs a NUL byte in `topologyName`; kept as encoding-distinctness guard | `TestVerifyF5TopologyContractEncodingIsAmbiguous` green |
| C-compose | Equal-scope gang+topology composes one PodGroup | contract | Guard, green | `TestVerifyGangTopologyEqualScopeComposesOnePodGroup` |

Envtest layer (2 specs: late-policy rejection, policy protection + delete/recreate):
`results/reverify/integration.out` — SUCCESS, 2/2 on the new head.

### F3 harness-bites check

Applied the proposed fix (gang-only nodes contribute no roles to the contract preimage)
to `topology_plan.go`, re-ran: F3 went green; `./pkg/scheduler/...` and
`./internal/controller/workloads/...` (incl. the author's `TestTopologyContractID`) all
stayed green. Fix reverted; production diff empty again.

### F4 (open, minor — code-path evidence, no unit repro)

`resolvePlacement` records the contract (`ensureTopologyContract`) BEFORE
`reconcilePlacement` renders it. A plan that resolves but fails to render
(SchedulerUnsupported) is nevertheless frozen, so removing/fixing the offending
topology then reports `TopologyPolicyLate` — backing out of a launch-time
misconfiguration requires deleting the whole RBG. Arguably intended fail-closed
behavior; raised as a question for the author.
