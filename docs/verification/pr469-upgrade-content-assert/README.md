# PR #469 verification — `pr469-upgrade-content-assert`

**PR:** https://github.com/sgl-project/rbg/pull/469
**Title:** test(upgrade): assert expected post-upgrade content instead of counting rewrites
**Kind:** tests-only (e2e upgrade suite refactor). Premise-establishment step skipped per
conductor carve-out; the premise under test is the behavioral claim the suite encodes.
**Base / mergeBase:** `c500c963` (the #463 merge — PR is tests-only, so reconciler logic is
identical at base and head).
**Branch:** `verify/pr469-upgrade-content-assert` (reviewer fork `cheyang/rbg`).
**`.last-reviewed`:** `pr-469` (5fc31fa4).

## Premise P0 — the claim the suite encodes

> The legacy-set child RoleBasedGroup (`up-legacy-set-0`) is healed from the v1alpha1
> `Recreate` spelling to `RecreatePod` on the upgraded controller's first reconcile, via the
> RBGS controller re-applying the child from its normalized `groupTemplate`
> (`updateExistingRBGs`). Phase 3 asserts `child.Type == RecreatePod`; `snapshot.go` records
> `healRoleStrategyTypes` for `RoleBasedGroup/up-legacy-set-0` and `revisionAdds=1` for it.

## Verdict: P0 = **Refuted** (unit-level)

`updateExistingRBGs` is only reached for children where `needsUpdate == true`
(`rolebasedgroupset_controller.go` reconcile loop). `needsUpdate` delegates to `rolesEqual`,
which (#463's convergence fix) deep-copies **both** sides and runs
`normalizeRolloutUpdateTypes` on **both** before `reflect.DeepEqual`. On the upgrade path the
child was written by v0.7.0 with `Recreate` copied verbatim from a legacy template, so parent
and child are spelling-identical and normalize to the same value:

- `needsUpdate(parent=Recreate, child=Recreate)` → **false**
  (`TestPR469_LegacyChildNotReapplied_RefutesPremise`, PASS).
- `normalizedGroupTemplateRoles(parent=Recreate)` → `RecreatePod`, source untouched
  (`TestPR469_NormalizedGroupTemplateDoesHeal`, PASS) — isolating the cause to the closed
  `needsUpdate` gate, not a missing normalize.

So the RBGS reconciler does **not** re-apply the legacy child on its first reconcile, its stored
`rolloutStrategy.rollingUpdate.type` stays `Recreate`, and:

1. The recorded `healRoleStrategyTypes` for `RoleBasedGroup/up-legacy-set-0` would fail:
   `compareWithExpected(before=Recreate, heal→RecreatePod, after=Recreate)` → `cmp.Diff`
   non-empty → `checkOwnersStable` reports a finding → the upgrade e2e spec goes red.
2. The phase-3 hard assertion `child.Type == RecreatePodUpdateStrategyType` fails (child is
   `Recreate`). The PR *regresses* the prior correct phase-3 assertion
   (`child.Type == LegacyRecreateUpdateStrategyType`, "leaves the child alone").
3. `revisionAdds[up-legacy-set-0]=1` is stale: the RBG-layer revision hash moves only if the
   child's spec is healed; it is not, so no new revision is stamped and the
   "recording is stale" branch of `checkNoRevisionExplosion` fires.

### Why this is consistent with #463

#463's convergence fix deliberately makes `rolesEqual` treat spelling-only deltas as equal so a
legacy parent (`Recreate`) and a webhook-healed child (`RecreatePod`) stop re-issuing updates
forever. The legacy-upgrade child (`Recreate`) is exactly such a spelling-only delta after
normalization, so the same fix that converges the post-heal steady state also prevents the
*initial* re-apply that would heal the child. `newRBGForSet` and a direct `updateExistingRBGs`
call *do* normalize (the existing tests prove it), but the reconcile loop only calls
`updateExistingRBGs` for `needsUpdate=true` children — and the legacy child is not one.

## Observed vs expected

| Claim encoded by the PR | Observed (unit) | Verdict |
| --- | --- | --- |
| Legacy child re-applied → healed to `RecreatePod` | `needsUpdate` = false; no re-apply | Refuted |
| Phase 3: `child.Type == RecreatePod` | child stays `Recreate` | Refuted (regression vs prior correct assertion) |
| `revisionAdds[up-legacy-set-0] == 1` | no child heal → no new revision | Refuted (stale) |
| Set template stays `Recreate` (phase 3) | set not written → stays `Recreate` | Confirmed (PR is correct here) |
| RIS heal to `RecreatePod` (webhook) | out of this PR's scope; #463-confirmed elsewhere | n/a |
| Drop per-start LeaderWorkerSet generation allowance | LWS start-patch is content-equal → no finding under content compare | Confirmed (sound) |

## Severity

**Blocker (test-correctness).** This is a tests-only PR; the defect is that the suite encodes a
behavior the reconciler does not produce, so the release-gate upgrade suite (`/release-test`)
would go red. It is not a production regression — but merging a red release gate is blocking.
The generation→content migration itself is sound and the pure functions
(`compareWithExpected`, `healStoredStrategyType`, `describeAddedRevisionChange`,
`revisionDataDiff`, `indentLines`) are correct; only the recorded child-heal premise is wrong.

## Layer coverage

- **Unit:** premise harness in `internal/controller/workloads/pr469_legacy_child_heal_premise_test.go`
  (additive; production code untouched). PASS.
- **Live e2e:** not run. `RunUpgradeSpecs` lives in `release-test.yml` triggered by
  `issue_comment /release-test` against the chart's default published images, **not** per-PR CI
  and **not** the PR head (same coverage gap as #463). A live from-source deploy is further
  blocked on the ACK cluster by legacy `restartPolicy` data. The unit gate is decisive for the
  RBGS-re-apply mechanism the PR cites; live would only add confidence that no *other* writer
  heals the child.

## Reproduce

```bash
bash docs/verification/pr469-upgrade-content-assert/scripts/re-verify.sh
```

## Cluster context

See [[rbg-ack-cluster-state]] and [[rbg-pr463-verification]] (B1 / convergence fix).
