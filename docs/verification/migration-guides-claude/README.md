# migration-guides-claude — verification harness for PR #586

Reproducible evidence for the review of [sgl-project/rbg#586](https://github.com/sgl-project/rbg/pull/586)
("docs(migration): add v1alpha1-to-v1alpha2 guides"), head `c79216ca`, base `2d31318`.

PR #586 is **docs-only** (8 markdown files; no production code, chart templates, or CRDs
touched). The review premise is therefore not a bug reproduction but **content accuracy**:
a migration guide is an operator-facing runbook, so every behavioral claim was traced to the
code that implements it, and the guide's one executable artifact (the jq role-inventory
command) is tested deterministically.

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit (gotest) | admission-validator behavior the guide describes; doc-content contract for F1 | `go test ./api/workloads/v1alpha2/ -run 'TestMigrationGuide' -v` |
| 1b. Standalone | the guide's jq inventory pipeline against fixtures | `bash docs/verification/migration-guides-claude/scripts/inventory-jq-check.sh` |
| 2. Integration | — (not applicable: no controller/CRD behavior changed by this PR) | — |
| 3. Live | skipped per round policy (unit + integration only) | — |

> Polarity: contract tests (assert intended behavior) FAIL on the defective artifact and
> PASS when fixed. `TestMigrationGuide_F1_RollbackDocClaim` is a **doc-contract** test: it
> is RED on the PR head — that red *is* the reproduction of F1 — and goes GREEN when both
> guides stop claiming the validator rejects an InstanceSet restore. The InstanceSet
> behavior subtests are code-pins documenting what the code actually does.

## Premise (P0)

Docs/tests-only PR — premise reproduction is carved out per the review rubric. The claim
structure reviewed instead: the guides document the 0.9.0 compatibility gate
(`controller.deprecatedWorkloadTypes.enabled`), the type-exact rule, per-role migration to
`RoleInstanceSet`, the v1alpha1 `InstanceSet` special case, and the
`status.storedVersions` storage-version rewrite. All of these exist in the code on the PR
head (validator: `api/workloads/v1alpha2/rolebasedgroup_validation.go` + admission; orphan
cleanup: `internal/controller/workloads/rolebasedgroup_controller.go` `deleteOrphanRoles`;
conversion: `api/workloads/v1alpha1/*_conversion.go`; CRDs: conversion-strategy Webhook for
the two CRDs, v1alpha1 still served, v1alpha2 storage). The InstanceSet history claims were
verified against git tags: the InstanceSet branch of `NewWorkloadReconciler` exists at
`v0.6.0` and is gone at `v0.7.0`, which is also where `RoleInstanceSet` first appears.

## Summary of results

| ID | Claim under test | Layer | Verdict | Evidence |
|----|------------------|-------|---------|----------|
| F1 | Guide Rollback section: "restoring the saved … or InstanceSet selector is a new deprecated use. The validator rejects it even while `…enabled=true`" | 1 | **Confirmed (doc defect)** — the validator accepts an InstanceSet restore in both compat modes; the write succeeds and the whole RBG only fails later at reconcile ("unsupported workload type") | `results/unit-summary.txt`; code-pin subtest PASS + doc-contract test FAIL |
| GUIDE-ROLLBACK-DSL | Same sentence for Deployment/StatefulSet/LeaderWorkerSet | 1 | Accurate — rejected with compat enabled (`new uses are not accepted`) | `TestMigrationGuide_RollbackRestore_DeprecatedTypesRejected` PASS |
| GUIDE-TYPEEXACT | "Compatibility is type-exact … switching a role from one deprecated type to another is a new use and is rejected" | 1 | Accurate — `validateNoNewDeprecatedWorkloadTypes` keys the exemption on (role name, type) | `TestMigrationGuide_TypeExactCompat` PASS |
| GUIDE-INVENTORY-JQ | The §1 inventory command handles both API shapes and both selector encodings | 1b | Accurate — exact expected output on the fixture set (v1alpha2 RBG/RBGSet, v1alpha1 RBG/RBGSet, direct-workload precedence over a stale annotation, InstanceSet, RIS negative controls) | `results/inventory-jq.out` |

Claims verified by code/git inspection only (no runnable harness; mechanism noted in the
review findings):

- v1alpha1 `spec.roles[].workload` defaults to `apps/v1 StatefulSet` (kubebuilder default,
  `api/workloads/v1alpha1/rolebasedgroup_types.go:341-343`).
- `rbg.workloads.x-k8s.io/role-workload-type` annotation key and
  `rbg.workloads.x-k8s.io/{group-name,role-name}` pod labels match the constants.
- An unsupported workload type fails the whole RBG's role-declaration validation
  (preflight), not just one role.
- Orphan cleanup covers Deployment/StatefulSet/LeaderWorkerSet while compat is enabled and
  never covers v1alpha1 InstanceSet; InstanceSet has had no reconciler since v0.7.0.
- The storage-version rewrite procedure (read/write through the fully-qualified v1alpha2
  resource, then patch `status.storedVersions` on the two webhook-conversion CRDs) matches
  the shipped CRDs; `status.storedVersions` is a historical record that object rewrites do
  not prune.
- EN and ZH guides have identical section structure and byte-identical command blocks
  (only a comment differs, translated); the guide's jq block is byte-identical to the chart
  README's copy.
- All relative links added by the PR resolve; the only broken link in `doc/TOC.md`
  (`role-temlate` typo) is pre-existing on the base.
- CI `docs-check` (markdownlint-cli2 + autocorrect) is green on the PR head per the status
  rollup.

## Per-finding detail

### F1 — Rollback section misstates enforcement for InstanceSet

`doc/migration-v1alpha1-to-v1alpha2.md:140` (and the ZH twin at
`doc/zh/migration-v1alpha1-to-v1alpha2.md:140`) says restoring an InstanceSet selector is
rejected by the validator even in compatibility mode. In the code, `isDeprecatedWorkloadType`
covers only Deployment/StatefulSet/LeaderWorkerSet, and no other admission validation
constrains the `role-workload-type` annotation value, so an InstanceSet restore (annotation
`workloads.x-k8s.io/v1alpha1/InstanceSet` written back through either API version) is
**accepted**; the failure surfaces only when reconciliation hits
`unsupported workload type` in `NewWorkloadReconciler`, which fails the entire RoleBasedGroup.
During an emergency rollback an operator would see the write succeed and reasonably conclude
the restore worked, while reconciliation of the whole group is now broken.

Reproduction: `go test ./api/workloads/v1alpha2/ -run 'TestMigrationGuide' -v` —
`TestMigrationGuide_F1_RollbackDocClaim` FAILs on both guides; the code-pin subtests in
`TestMigrationGuide_RollbackRestore_InstanceSetNotRejectedByValidator` PASS, proving the
write is accepted (positive control in the same run:
`..._DeprecatedTypesRejected` proves the same validator does reject the three deprecated
types, so the InstanceSet result is not a vacuous pass).

Harness-bites check (per the verifier skill): temporarily correcting both Rollback
paragraphs (dropping InstanceSet from the rejected list and stating the reconcile-time
failure) flips the F1 test to green; the temporary fix was reverted and the production
diff confirmed empty.

## Proposed fix (NOT applied here)

- **F1**: in both guides, scope the Rollback sentence to
  Deployment/StatefulSet/LeaderWorkerSet for validator rejection, and state that
  restoring an InstanceSet selector is *accepted* by the validator and instead breaks
  reconciliation of the whole RoleBasedGroup (fail-late, not write-time rejection).

## Continuing after the fix (possibly on another machine)

The harness lives on branch `verify/migration-guides-claude` (production code untouched).
`scripts/re-verify.sh` (no argument) resolves the current PR head from the manifest's `pr`
URL, grafts these paths onto it, and reports per finding Fixed / Still-broken:

```bash
git fetch https://github.com/sgl-project/rbg.git pull/586/head   # optional sanity
bash docs/verification/migration-guides-claude/scripts/re-verify.sh
bash docs/verification/migration-guides-claude/scripts/inventory-jq-check.sh
```

Polarity table for reading results:

| Test | Now | After the F1 doc fix | If the validator is later extended to reject InstanceSet restores |
|------|-----|----------------------|-------------------------------------------------------------------|
| `TestMigrationGuide_F1_RollbackDocClaim` | FAIL (reproduction) | PASS | PASS (doc presumably also updated) |
| `..._InstanceSetNotRejectedByValidator` | PASS (code-pin) | PASS | **FAIL — invert or promote to contract** |
| `..._DeprecatedTypesRejected`, `..._TypeExactCompat` | PASS | PASS | PASS |

### Kickoff prompt for a fresh agent

```text
Continue a verification task on branch verify/migration-guides-claude of the reviewer fork
(cheyang/rbg). Background: a review of sgl-project/rbg PR #586 (docs-only migration guides)
produced finding F1: the guides' Rollback section claims the admission validator rejects
restoring a v1alpha1 InstanceSet selector, but the validator accepts it (failure happens at
reconcile time instead). Read docs/verification/migration-guides-claude/README.md and
follow it: run scripts/re-verify.sh (resolves the PR head itself) and
scripts/inventory-jq-check.sh, apply the polarity table (the F1 doc-contract test should be
green once the guides are corrected; the InstanceSet code-pin tests flip only if the
validator itself changes), and report an observed-vs-expected table.
```
