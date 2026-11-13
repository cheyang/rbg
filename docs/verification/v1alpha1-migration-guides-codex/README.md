# Verification — PR #586: docs(migration): add v1alpha1-to-v1alpha2 guides

Reviewer: codex. Round: 1 (initial). Reviewed head: `c79216cae851168cb751bd17c0eac631d4d3694a`.
Base: `2d31318890685f6c0c71bcf6261522f79bf9af17`. Harness adds test files only;
production code and the PR's doc changes are untouched.

## Premise (P0) — confirmed

Docs-only PR (the skill's premise carve-out applies), checked anyway: the
migration guides did not exist on base (`git cat-file -e 2d313188:doc/migration-v1alpha1-to-v1alpha2.md` →
exit 128), matching roadmap item #454. The substantive risk of a docs PR is
inaccuracy, so the harness pins every load-bearing claim the guides make about
0.9.0 behavior against the code.

## Verified claims (all contract polarity, all PASS at PR head)

| Guide claim | Where the code says so | Test |
| --- | --- | --- |
| Compatibility is type-exact; existing roles keep their own deprecated type | `validateNoNewDeprecatedWorkloadTypes` keys the exemption on role name+type (`api/workloads/v1alpha2/rolebasedgroup_validation.go:327`) | `TestDocClaimGateKeepsExistingDeprecatedRole` |
| Renaming a role / switching deprecated types is a new use, rejected | same function | `TestDocClaimGateRejectsRenamedRole`, `TestDocClaimGateRejectsDeprecatedTypeSwitch` |
| Rollback to a deprecated selector is rejected even with the gate enabled | update path calls `validateNoNewDeprecatedWorkloadTypes(new, old)` with no template exemption (`rolebasedgroup_admission.go:215`) | `TestDocClaimGateRejectsRollbackToDeprecatedType` |
| InstanceSet selector is not covered by the compatibility toggle | `isDeprecatedWorkloadType` matches only Deployment/StatefulSet/LWS | `TestDocClaimInstanceSetIsNotGateProtected` |
| v0.7.0+ controller has no InstanceSet reconciler; the role fails preflight and blocks the whole RBG | `NewWorkloadReconciler` default → "unsupported workload type" (`pkg/reconciler/workload_reconciler.go:67`); `preCheck` aborts `Reconcile` at Step 0 (`rolebasedgroup_controller.go:195`) | `TestDocClaimInstanceSetHasNoReconciler` |
| Not reconciled since v0.7.0; RoleInstanceSet first ships in v0.7.0 | v0.6.0 tree has `pkg/reconciler/instanceset_reconciler.go`; removed by #188/#201 (first tag v0.7.0); RoleInstanceSet added by #191 (first tag v0.7.0) | git archaeology (see results/) |
| Old Deployment/StatefulSet/LWS deleted as orphan after the role moves to RoleInstanceSet | per-type `CleanupOrphanedWorkloads` (`pkg/reconciler/deploy_reconciler.go:265` etc.), invoked only while the gate is enabled (`rolebasedgroup_controller.go:746`) | `TestDocClaimOrphanCleanupDeletesAbandonedDeployment` (+ `KeepsActiveDeployment` control) |
| Orphan cleanup does not cover InstanceSet | no InstanceSet cleanup exists in `deleteOrphanRoles` | code reading (nothing to call) |
| v1alpha1 `spec.roles[].workload` is authoritative; annotation recreated from it by conversion; annotation-only v1alpha1 edits are discarded; v1alpha1 round-trip reintroduces the annotation | `convertRoleV1alpha1ToV2` writes the annotation from `src.Workload`; `convertRoleV1alpha2ToV1alpha1` always populates `dst.Workload` (`api/workloads/v1alpha1/rolebasedgroup_conversion.go:147,363`) | code reading |
| v1alpha1 defaults `workload` to apps/v1 StatefulSet | `+kubebuilder:default={apiVersion:"apps/v1", kind:"StatefulSet"}` (`rolebasedgroup_types.go:341`) | code reading |
| RoleInstanceSet is the v1alpha2 default | `RoleSpec.GetWorkloadType()` (`rolebasedgroup_types.go:345`) | `TestDocClaimV1alpha2DefaultWorkloadIsRoleInstanceSet` |
| RBGSet template ordering (controller copies template roles into children; update-path restore is rejected) | `needsUpdate`/`updateExistingRBGs` (`rolebasedgroupset_controller.go:474,534`); create-path exemption `legacySetTemplateRoles` is create-only | code reading |
| Inventory command detects both API encodings, correct precedence | CRDs serve v1alpha1+v1alpha2 (v1alpha2 storage); shortnames rbg/rbgs | `TestGuideInventoryDetectsAllDocumentedShapes` (runs the guide's exact jq, extracted from the doc) |
| Pod label selector `group-name`/`role-name` | `GetCommonLabelsFromRole` (`api/workloads/v1alpha2/helper.go:39`) feeds every workload's pod template | code reading |
| Gate defaults to enabled in the chart | `deploy/helm/rbgs/values.yaml` `deprecatedWorkloadTypes.enabled: true`; RBAC/watches/NOTES gated | `TestChartGateDefaultsToEnabled` + `go test ./test/chart/` |
| Conversion-webhook CRDs are exactly RoleBasedGroup + RoleBasedGroupSet | `config/crd/patches/webhook_in_rolebasedgroup{,sets}.yaml` only | code reading |
| Links added by the PR resolve | — | `TestGuideLinksResolve` |
| EN/ZH guides have matching structure and commands | — | `TestGuideChineseParity` |
| markdownlint clean on the new guides | CI: `.github/workflows/docs-check.yml` | `results/markdownlint.out` (0 issues) |

## Findings

| ID | Severity | Status | Evidence |
| --- | --- | --- | --- |
| F1 | minor | open (canary active) | Guide's interpretation table promises an "Any other value" row, but the shipped jq `select()` silently filters unknown selectors — the row is unreachable. `TestGuideInventoryUnknownValueUnreachableRow_Canary` PASSES today and flips to FAIL once fixed (bites check: removing the row flips it). |
| F2 | nit | open (canary active) | `doc/dev/how_to_release.md` checklist item 2 requires chart NOTES to "link the English and Chinese migration guides"; `deploy/helm/rbgs/templates/NOTES.txt` describes the gate but links nothing. `TestReleaseChecklistNotesLinkGap_Canary` PASSES today and flips once NOTES links the guide or the checklist is reworded (bites check: adding a link flips it). |

No blockers. Every load-bearing statement in the guides was traced to the code
and holds at the PR head; CI-relevant checks the PR lists (`helm lint`,
`make test-chart`, markdownlint on the two new guides) were re-run locally and
pass (see `results/`).

## How to run

From a checkout of this branch:

```bash
bash docs/verification/v1alpha1-migration-guides-codex/scripts/run-all.sh
```

Prerequisites: go, jq (inventory tests skip without it), helm (lint step only).

## Continuing after a fix

```bash
bash docs/verification/v1alpha1-migration-guides-codex/scripts/re-verify.sh        # auto-fetches the current PR head
```

Polarity: all `TestDocClaim*`/`TestGuide*`/`TestChartGate*` tests are contracts
(must stay green). The two `*_Canary` tests are bug-canaries documenting F1/F2:
when a fix makes one flip to FAIL, invert its assertion (or promote the fixed
behavior to a contract). `.last-reviewed` records the reviewed head; advance it
after each round.

Kickoff prompt for a fresh machine: "Check out
https://github.com/cheyang/rbg.git branch
verify/v1alpha1-migration-guides-codex and run
docs/verification/v1alpha1-migration-guides-codex/scripts/re-verify.sh for PR
https://github.com/sgl-project/rbg/pull/586; report Fixed/Still-broken per
finding and review the delta since .last-reviewed."
