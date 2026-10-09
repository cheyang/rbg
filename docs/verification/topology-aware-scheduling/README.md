# topology-aware-scheduling — bug verification (PR #473, KEP-473)

Reproducible evidence for the findings raised while reviewing
https://github.com/sgl-project/rbg/pull/473 (head `2b65779b`, docs-only: adds
`keps/473-topology-aware-scheduling/{README.md,kep.yaml}`).

Because the PR ships no runtime code, the harness verifies (a) the KEP's factual
claims about the base tree and the upstream scheduler APIs, and (b) the exact
document text behind each finding. **All six finding tests are bug-canaries**:
they PASS while the defective text is present and must FLIP to red once the
author fixes the KEP (or the PR body). Green here means "finding reproduced",
not "all clear".

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

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| F1 | Finalizer release ("only after the RBG has been deleted") is name-scoped while policy↔RBG binding is name+namespace only: delete + same-name recreate wedges the terminating policy and strands the new RBG under stale topology | 1 | Confirmed (text + binding mechanism) | `TestKEP473_F1_FinalizerWedgeUnaddressed` + `TestBaseFact_CoordinatedPolicyBoundByName` |
| F2 | Volcano renderability of gang-parent + cross-role topology child (the KEP's own Story 2 composed with gang) is unspecified; the KEP only commits to "parent topology + per-instance child topology" and punts the rest to `SchedulerUnsupported` | 1 | Confirmed (gap present in text) | `TestKEP473_F2_VolcanoGangParentTopologyChildUnspecified` |
| F3 | "RBGSet immutability guard ... lists children from the informer cache" contradicts "RBGSet template ... requires no cross-resource child lookup"; base validator does no cross-resource reads at all | 1 | Confirmed | `TestKEP473_F3_RBGSetAdmissionContradiction` + `TestBaseFact_RBGSetAdmissionDoesNoChildLookup` |
| F4 | PR body drift: (a) claims reconcile validates "preferred level not broader than required" while the KEP says the relative order is "Not validated by RBG"; (b) lists conditions `PlacementPlanReady`/`TopologyConstraintActive` that exist nowhere in the KEP; (c) claims "The Chinese translation is kept in-tree" — no Chinese file exists under `keps/` | 1 | Confirmed | `TestKEP473_F4a/F4b/F4c_*` (PR body frozen in `fixtures/pr-body-473.md`) |
| F5 | Risks table puts `TopologyTranslated=False` "on the declaring object (CoordinatedPolicy for rules...)"; Observability + Graduation Criteria put it on the RBG | 1 | Confirmed | `TestKEP473_F5_ConditionSurfaceInconsistent` |
| F6 | "Generated Group Names" specifies only Volcano; KAI example silently reuses legacy `rbg.Name`, Koordinator example introduces per-instance member PodGroup names | 1 | Confirmed | `TestKEP473_F6_NamingSchemeDialectsUnspecified` |

Non-finding contract checks (all PASS): all 38 TOC anchors resolve
(`TestKEP473_TOCAnchorsResolve`); the generated-name budget math is internally
consistent at 26+1+22+1+12=62 < 63 (`TestKEP473_GroupNameBudget`).

## Per-finding detail

- **F1** — KEP §Mutability: "the controller protects the active policy with a
  finalizer and releases it only after the RBG has been deleted." Base code binds
  a CoordinatedPolicy to its RBG "by identical namespace/name"
  (`internal/controller/workloads/rolebasedgroup_controller.go`), no UID.
  Sequence: user deletes RBG+policy and a GitOps tool immediately re-creates the
  RBG under the same name → the release condition ("RBG has been deleted") never
  holds again → old policy stuck terminating, new policy of that name can never
  be created, and the new RBG is governed by the stale terminating policy's old
  topology — the exact bypass the finalizer exists to prevent, inverted.
  Expected: release keyed on RBG UID/generation, plus specified treatment of
  terminating policies by the planner. Observed: neither is in the text.
- **F2** — §Translation Channels / §Scope Composition: gang over all roles with
  a topology rule over {prefill,decode} is a containment scope (gang parent).
  The KEP states Volcano "supports the common parent topology + per-instance
  child topology case, but not arbitrary cross-role containment; such plans
  return `SchedulerUnsupported`". Whether the gang-parent/topology-child case
  renders on Volcano (e.g. a `subGroupPolicy` entry with a multi-value
  `role-name In [...]` selector carrying `networkTopology`) or returns
  `SchedulerUnsupported` decides whether Phase 1 serves the KEP's motivating
  scenario for gang-scheduled workloads. Observed: the text does not say.
- **F3/F4/F5/F6** — see the test names; each asserts the exact quoted strings
  above and flips red when they are reconciled.

Harness-bites evidence: `results/bites-check.txt` (each canary was flipped red
by a candidate fix, then the KEP was reverted; one harness bug — missing `(?m)`
regex flag — was found and fixed by this step).

## Proposed fixes (NOT applied anywhere)

- **F1**: key the finalizer release on the RBG's UID (or record it in an
  annotation/finalizer payload), state that terminating policies are ignored by
  the planner, and document the escape hatch for a wedged policy.
- **F2**: add one sentence to §Translation Channels stating how gang-parent +
  cross-role topology-child renders on Volcano, or list it as explicitly
  unsupported in Phase 1 with the workaround (put gang and topology on the same
  scope).
- **F3**: delete the stale "lists children from the informer cache" sentence or
  reconcile it with §Mutability.
- **F4**: update the PR body to match the KEP (validation list, condition names)
  and drop or fulfill the Chinese-translation claim.
- **F5**: pick one condition surface (KEP-430 precedent = RBG) and align the
  Risks table.
- **F6**: extend §Generated Group Names to KAI and Koordinator.

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

Expected: every F-canary **flips to fail** (= fixed) once the corresponding text
is corrected; then invert the canaries or drop them. Contract tests
(`TestBaseFact_*`, TOC, name budget) must stay green. Note: the F4 canaries
compare against the frozen PR-body fixture; if the author edits the PR body on
GitHub, refresh `fixtures/pr-body-473.md` with
`gh pr view 473 --repo sgl-project/rbg --json body -q .body` before re-running.

Kickoff prompt for a fresh agent: "Continue verification of sgl-project/rbg
PR #473 from branch verify/topology-aware-scheduling-codex on
https://github.com/cheyang/rbg.git. Run
docs/verification/topology-aware-scheduling/scripts/re-verify.sh, apply canary
polarity (a flipped red canary = fixed), refresh the PR-body fixture, review the
last-reviewed..head delta printed by the script, then advance .last-reviewed and
push the branch back to the same fork."
