# topology-aware-scheduling — bug verification (PR #473, KEP-473)

Reproducible evidence for the findings raised while reviewing
https://github.com/sgl-project/rbg/pull/473 (round 1 head `2b65779b`, round 2
head `6894a2e`; docs-only: adds `keps/473-topology-aware-scheduling/{README.md,kep.yaml}`).

Because the PR ships no runtime code, the harness verifies (a) the KEP's factual
claims about the base tree and the upstream scheduler APIs, and (b) the exact
document text behind each finding.

**Round-2 state (head 6894a2e):** the round-1 canaries F1-F6 all flipped (=
fixed) and were inverted into contract tests asserting the corrected text; a
regression of any fix turns them red again. Round-2 review of the
`2b65779b..6894a2e` delta added four new bug-canaries (F7-F10): they PASS while
the defective text is present and must FLIP to red once fixed. Green on a
canary means "finding reproduced", not "all clear".

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit | KEP text + base-tree facts (`test/verification/kep473_doc_test.go`) | `go test ./test/verification/ -run 'TestBaseFact\|TestKEP473' -count=1` |
| 2. Integration | upstream scheduler APIs the KEP cites (network fetch) | `go test ./test/verification/ -run 'TestExternalDialect' -count=1` (set `RBG_VERIFY_NO_NETWORK=1` to skip) |
| 3. Live | n/a — docs-only PR | — |

## Problem premise (P0)

| | |
|---|---|
| Claimed symptom | "RBG workloads are increasingly topology-sensitive: (1) Multi-node instance packing ... (2) Prefill–decode co-location ... Volcano, Koordinator, and KAI all model this, but each uses a different topology dialect." (PR body / issue #473, OPEN, created 2026-09-16) |
| Linked issue | #473 — referenced with `ref #473`, explicitly "does not close the tracking issue"; no auto-close risk |
| Reported component | design/API surface only (KEP) |
| Patched component | `keps/473-topology-aware-scheduling/` — matches |
| Component match | Yes |
| **Verdict** | **Confirmed** — the three dialects exist upstream with exactly the names the KEP cites |
| Evidence | `results/integration.json`: Volcano `HyperNode.spec.tier`/`tierName` (volcano-sh/apis@master), Koordinator `gang.scheduling.koordinator.sh/network-topology-spec` + `MustGather`/`PreferGather` (koordinator@master), KAI `minSubGroup` (singular) / `subGroups` / `parent` / `topologyConstraint.{requiredTopologyLevel,preferredTopologyLevel,topology}` (NVIDIA/KAI-scheduler@main) all verified live on 2026-10-10. Base-tree facts the KEP leans on (`ResolveGangStrategy`, `IncompatibleGangConfig`, `GangConfigured`, `SchedulingCoordinationStrategy.Gang`, KEP-430 PodGroup named `rbg.Name`, `group-name`/`role-name`/`role-instance-name` label keys, vendored Volcano `networkTopology`/`highestTierName`/`subGroupPolicy.matchLabelKeys`) verified by `TestBaseFact_*` in `results/unit.json`. |

## Summary of results

### Round 3 (head 99b991c5, `.last-reviewed` advanced)

The round-3 delta (`6894a2e..99b991c5`) rewrites the KEP around a new
`ClusterNetworkTopology` vocabulary CR (Managed/Imported), adds the alpha PRR
questionnaire, the instance-binding handshake, and the RBG/topology drain
protocols, and re-bases onto `79f7e585` — picking up a duplicate of the
already-merged #495 `GetSelector` fix (`19acd93d`, content-identical to main's
`7b384f4d`; zero net diff against the round-3 frozen base `d88b3186`).

| ID | Claim (ledger ID) | Layer | Round-3 verdict | Evidence |
|----|-------------------|-------|-----------------|----------|
| F7 | (ledger F1, round-2 major) Contract freeze read the policy through the cache-backed client; blessed "policy before RBG" ordering could freeze `none` and wedge the RBG | 1 | **Fixed** (canary flipped -> contract): freeze now reads the policy through the uncached `apiReader`, non-NotFound read errors block acceptance, and immutable `spec.topologyPolicyRequired` provides a deterministic wait | `TestKEP473_F7_ContractFreezeReadsUncached` (fails on 6894a2e, passes on 99b991c5) |
| F8 | (ledger F2) "controller resync" listed as a drift trigger the predicates filter out | 1 | **Fixed** — trigger removed everywhere; drift now discovered via topology/native-resource watches + workload events | `TestKEP473_F8_ContractNoResyncTrigger` (bites on 6894a2e) |
| F9 | (ledger F3) Koordinator cross-role-only example contradicted the partition rule | 1 | **Fixed** — example rewritten as an explicitly gated Phase-2 illustration with the correct 2 role-level members; inspected baseline reports `SchedulerUnsupported` | `TestKEP473_F9_ContractKoordinatorExampleGatedAndConsistent` (bites on 6894a2e) |
| F10 | (ledger F4) webhooks-disabled sentence contradicted the contract freeze | 1 | **Fixed** — admission bypass no longer authorizes overwriting an accepted record | `TestKEP473_F10_ContractWebhookBypassStillGated` (bites on 6894a2e) |
| F11 | (ledger F6) KAI subGroup names had no length budget vs the 63-char binding label | 1 | **Fixed** — kind-prefixed, 20-char-truncated, hash-suffixed names; worst case 42 < 63 | `TestKEP473_F11_ContractKAISubGroupNamesBounded` (bites on 6894a2e) |
| F12 | (ledger F7, open) Naming-safety rule "Role names ... validated at admission as DNS-1123 labels" is still unscoped (RoleSpec.Name carries no pattern validation today) | 1 | **Still present** (canary green on both heads) | `TestKEP473_F12_RoleNameRuleUnscoped` |
| F13 | (ledger F9, open) The new topology controller needs Node list/watch, HyperNode/Topology CRUD and topology-CR status/finalizer writes; still no RBAC/ClusterRole section | 1 | **Still present** (canary green on both heads) | `TestKEP473_F13_RBACSectionMissing` |

Harness-updates this round: the round-1 contract assertions that pinned
round-2 wording (F1/F2/F3/F4a/F5) were updated to the round-3 text with
contract semantics preserved (e.g. the gang-parent + cross-role topology
child case is now specified as explicitly `SchedulerUnsupported` in Phase 1 —
the round-2 labelSelector rendering was found unworkable upstream, see below);
`headingRE` now accepts the H1 self-link the regenerated TOC uses. The F4b/F4c
failures on the previous-head differential are fixture artifacts (the
refreshed round-2 PR-body fixture shares assertions with the fix), unchanged
from round 2. `TestBaseFact_GetSelectorUsesSelectorFromSet` pins that the
tree under review carries the #495 selector fix (fails on 6894a2e, passes on
99b991c5).

Round-3 integration layer (all PASS, live-fetched 2026-10-19): the new
Backend Capability Gate claims verified against the cited upstream sources —
KAI v0.18.6 `calcDomainId` joins label values with `.` without escaping;
Volcano v1.14.4 `allocate` skips empty-request (BestEffort) tasks;
`getOrCreateSubJob` honors only the first policy with non-empty match values
(selector-only policies never form a subgroup); the default subjob inherits
the parent `networkTopology`; `recoverAllocatedHyperNode` skips topology-free
subjobs; Koordinator@81e01347's topology workflow is documented upstream as
supporting only jobs with no Bound members and total membership equal to the
minimum.

### Round 2 (head 6894a2e, `.last-reviewed` advanced)

| ID | Claim | Layer | Round-2 verdict | Evidence |
|----|-------|-------|-----------------|----------|
| F1 | Finalizer release was name-scoped; delete+recreate wedged the policy | 1 | **Fixed** (canary flipped -> contract) — release is now UID-keyed, terminating-policy semantics and escape hatch specified | `TestKEP473_F1_ContractFinalizerReleaseKeyedByUID` |
| F2 | Volcano gang-parent + cross-role topology child rendering unspecified | 1 | **Fixed** — now renders as a `subGroupPolicy` entry with `labelSelector` over the child roles; vendored field pinned by `TestBaseFact_VolcanoSubGroupLabelSelector` | `TestKEP473_F2_ContractVolcanoGangParentTopologyChildSpecified` |
| F3 | RBGSet admission contradiction (informer-cache child lookup vs no cross-resource reads) | 1 | **Fixed** — validation is now stated as self-contained | `TestKEP473_F3_ContractRBGSetAdmissionSelfContained` |
| F4 | PR body drift (validation claim, phantom conditions, Chinese translation) | 1 | **Fixed** — PR body updated to match the KEP (fixture refreshed round 2) | `TestKEP473_F4a/F4b/F4c_Contract*` |
| F5 | Condition surface inconsistent (declaring object vs RBG) | 1 | **Fixed** — consistently on the RBG, naming the declaring rule/role | `TestKEP473_F5_ContractConditionSurfaceIsRBG` |
| F6 | Naming scheme only specified for Volcano | 1 | **Fixed** — per-dialect naming rules for Volcano/Koordinator/KAI | `TestKEP473_F6_ContractNamingSchemeCoversAllDialects` |
| F7 | Contract freeze reads the policy through the cache-backed client; a policy created just before the RBG's first reconcile but not yet in the informer cache freezes the contract as `none`, then TopologyPolicyLate wedges the RBG (recovery = recreate the RBG). KEP specifies no uncached read / settling window at freeze time | 1 | **Still present** (new, round 2) | `TestKEP473_F7_ContractFreezeCacheRaceUnaddressed` + `TestBaseFact_PolicyFetchAndWatch` |
| F8 | Drift discovery lists "controller resync" as a trigger, but RBGPredicate filters spec-equal updates and the policy watch uses GenerationChangedPredicate — resync never enqueues | 1 | **Still present** (new, round 2) | `TestKEP473_F8_ResyncListedAsDriftTrigger` + `TestBaseFact_RBGPredicateFiltersSpecEqualUpdates` |
| F9 | Koordinator worked example: 3-member GangGroup (prefill-0/1 + decode) contradicts the KEP's own partition rule for the cross-role-only input (both roles PartitionBy=none -> 2 role-level members) | 1 | **Still present** (new, round 2) | `TestKEP473_F9_KoordinatorExampleMemberPartition` |
| F10 | Contract-freeze gates any drift from the recorded contract ("Removing all topology ... treated the same way"), but the next paragraph still says webhooks-disabled deployments render "the latest desired topology" — opposite outcomes for the same scenario | 1 | **Still present** (new, round 2) | `TestKEP473_F10_WebhookDisabledContradiction` |
| F11 | KAI subGroup names (`policy-<rule>`, `instance-<role>-<index>`, `role-<role>`) have no length budget while source names admit 63-char DNS-1123 labels; KAI binds pods via the pod label `kai.scheduler/subgroup-name` whose value is limited to 63 chars | 1 | **Still present** (new, round 2) | `TestKEP473_F11_KAISubGroupNameBudgetMissing` + `TestExternalDialect_KAISubGroupLabelKey` |

Non-finding contract checks (all PASS): all 38 TOC anchors resolve
(`TestKEP473_TOCAnchorsResolve`); the generated-name budget math is internally
consistent at 26+1+22+1+12=62 < 63 (`TestKEP473_GroupNameBudget`).

Round-2 integration layer (all PASS): the KEP's new upstream claims verified
live on 2026-10-14 — KAI `TopologyLevel.alias` (kai-scheduler/api@main) and its
v0.18.3 CRD documenting alias as usable in requiredTopologyLevel/
preferredTopologyLevel; Koordinator's `scheduling.sigs.k8s.io` PodGroup CRD
carrying no `minimum` on minMember; the upstream scheduler-plugins
`scheduling.x-k8s.io` CRD requiring `minimum: 1` on minMember. Harness-update:
the KAI Go types moved from `NVIDIA/KAI-scheduler/pkg/apis/...` to the
`kai-scheduler/api` repo; the round-1 URL 404s and the test now fetches the
new location.

## Per-finding detail

- **F1-F6** — fixed at head `6894a2e`; see the round-1 detail below (kept for
  history) and the contract tests for the corrected text.
- **F7** — §Mutability: "The combined RBG/CoordinatedPolicy topology contract
  freezes when the controller first accepts a placement plan for the RBG" and
  "A CoordinatedPolicy may be created before the RBG; that is a normal launch
  ordering and the recorded contract is its topology." The base controller
  fetches the policy via the cache-backed `r.client.Get`
  (`handleCoordinationStrategies`) and watches it with
  `GenerationChangedPredicate` (create events enqueue). A policy applied in the
  same GitOps batch as the RBG can be in etcd but not yet in the informer
  cache at freeze time: the contract freezes as `none`, the policy create event
  then arrives, and the RBG reports `TopologyPolicyLate` and gates role
  updates until the RBG is deleted and recreated. The KEP specifies no guard
  (uncached read at freeze time, settling requeue before recording `none`,
  etc.), so the blessed ordering is only probabilistically safe.
- **F8** — the drift text lists "controller resync" among discovery triggers,
  but `RBGPredicate.UpdateFunc` returns true only on `!reflect.DeepEqual(old,
  new spec)` and the policy watch uses `GenerationChangedPredicate`; informer
  resync delivers old==new/same-generation updates that both filter out. A
  quiescent workload's topology drift is discovered later than claimed (a
  controller *restart* does enqueue via create events; "resync" does not).
- **F11** — KAI subGroup names combine kind prefixes with source names that
  are admitted as DNS-1123 labels (≤63 chars each); `instance-` + a 63-char
  role name + `-<index>` exceeds 63 chars. KAI binds pods to subGroups through
  the pod label `kai.scheduler/subgroup-name` (kai-scheduler/api
  `constants.SubGroupLabelKey`, consumed in
  `pkg/scheduler/api/pod_info/pod_info.go`), and label values are limited to 63
  chars, so pod creation would be rejected for such names. The Volcano form has
  an explicit budget (62 chars); the KAI rule has none.
- **F9** — the Koordinator example first states the full input is not
  renderable (SchedulerUnsupported, nothing created), then shows a 3-member
  GangGroup for "the cross-role-only portion" justified by "prefill is
  partitioned per instance, but decode has PartitionBy=none". In a
  cross-role-only input prefill has no instance constraint either, so per the
  KEP's own naming rule (per-role members when PartitionBy=none) the members
  should be `prefill` + `decode` (2), not `prefill-0/1` + `decode` (3). The
  example contradicts the stated rule under either reading.
- **F10** — the freeze paragraph gates *any* drift between the recorded
  contract and the currently resolved declarations (`TopologyPolicyLate`;
  "Removing all topology after a non-empty contract is treated the same
  way"), while the next paragraph says deployments with validating webhooks
  disabled "render the latest desired topology". For an in-place topology
  change with webhooks disabled the two sentences give opposite outcomes;
  whichever is intended, the webhooks-disabled sentence needs to be
  reconciled with the freeze.

### Round-1 detail (historical, all fixed at 6894a2e)

- **F1** — was: release "only after the RBG has been deleted" while binding is
  name-scoped. Fixed by UID-keyed release + terminating-policy semantics +
  documented escape hatch.
- **F2** — was: gang-parent + cross-role topology child unspecified on
  Volcano. Fixed by the explicit subGroupPolicy/labelSelector rendering.
- **F3/F4/F5/F6** — see the round-1 test names; the contradictory or drifted
  text is gone and pinned by contract tests.

Harness-bites evidence: `results/bites-check.txt` (round 1: each canary was
flipped red by a candidate fix, then the KEP was reverted; one harness bug —
missing `(?m)` regex flag — was found and fixed by this step). Round 2:
`results/bites-check-round2.txt` — the F7-F11 canaries pass on head `6894a2e`
and all flip red under candidate fixes applied to a scratch copy of the KEP.

## Proposed fixes (NOT applied anywhere)

- **F7**: state that the freeze decision reads the CoordinatedPolicy with an
  uncached client (one direct read per RBG lifecycle), or add a settling
  requeue before recording a `none` contract; alternatively downgrade the
  "normal launch ordering" claim to best-effort and document the recovery.
- **F8**: drop "controller resync" from the drift triggers (or replace with
  "controller restart") in both the Risks table and the Mutability section.
- **F9**: make the Koordinator example consistent — either reduce the input
  and show 2 role-level members, or keep the full input and state that the
  GangGroup rendering applies to a plan in which prefill still partitions per
  instance (and why that plan renders at all despite the instance constraint
  being unsupported).
- **F10**: state explicitly whether the recorded-contract freeze applies when
  validating webhooks are disabled; align the "renders the latest desired
  topology" sentence with the answer.
- **F11**: give KAI subGroup names the same treatment as the Volcano placement
  form (truncate the source portion and/or state a 63-char budget), since pod
  binding goes through the `kai.scheduler/subgroup-name` label.

## Continuing after the fix (possibly on another machine)

The harness lives on branch `verify/topology-aware-scheduling-codex` on the
reviewer's fork (`https://github.com/cheyang/rbg.git`), production code
untouched. To re-verify after the author pushes a fix:

```bash
git clone https://github.com/sgl-project/rbg.git rbg && cd rbg
git fetch https://github.com/cheyang/rbg.git verify/topology-aware-scheduling-codex
git checkout verify/topology-aware-scheduling-codex
bash docs/verification/topology-aware-scheduling/scripts/re-verify.sh   # no args: resolves current PR head
```

Expected: the F7-F10 canaries **flip to fail** (= fixed) once the corresponding
text is corrected; then invert them into contract tests. Contract tests
(`TestBaseFact_*`, TOC, name budget, `TestKEP473_F1..F6_Contract*`) must stay
green — a red contract test means a fixed finding regressed or the ground truth
moved. Note: the F4 contracts compare against the frozen PR-body fixture; if the
author edits the PR body on GitHub, refresh `fixtures/pr-body-473.md` with
`gh pr view 473 --repo sgl-project/rbg --json body -q .body` before re-running.

Kickoff prompt for a fresh agent: "Continue verification of sgl-project/rbg
PR #473 from branch verify/topology-aware-scheduling-codex on
https://github.com/cheyang/rbg.git. Run
docs/verification/topology-aware-scheduling/scripts/re-verify.sh, apply canary
polarity (a flipped red canary = fixed), refresh the PR-body fixture, review the
last-reviewed..head delta printed by the script, then advance .last-reviewed and
push the branch back to the same fork."
