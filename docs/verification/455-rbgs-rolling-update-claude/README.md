# Verification — PR #491 (KEP-455: RBGSet rolling update) — Reviewer A (Claude)

First round, 2026-10-11; re-run 2026-10-12 (added E6 check, PR-head guard, E4 unit canary `TestVerify491E4ControllerOwnedAnnotationWipe`, bite C); **round 2, 2026-10-14** (PR force-pushed: head `719851a6` → `b03a7e67`, a KEP amendment answering round 1; harness rebased onto the new head, E3c inverted after the F2 fix, R2-* contract checks added for every fix, new R2-F9 contract reproduction, bites D and E). Branch `verify/455-rbgs-rolling-update-claude` (fork `cheyang/rbg`).

- PR: https://github.com/sgl-project/rbg/pull/491 — **docs-only**: adds `keps/455-rbgs-rolling-update/` (README + kep.yaml), no code changes. Code at head `b03a7e67` is still byte-identical to merge-base `0821cb5b` for everything outside `keps/`.
- Round-2 delta: `keps/455-rbgs-rolling-update/README.md` +114/−5 (new *Child Rollout Barrier* section, scale-subresource paragraph, 4-key exclusion list, Appendix with five one-sentence answers, test-plan entries).
- Base movement since round 1 (main `0821cb5b` → `375fdfd5`, PRs #484/#488/#494/#495): **no finding-referenced file touched** (`rolebasedgroup_controller.go`, `rolebasedgroupset_controller.go`, `rolebasedgroupset_conversion.go`, `pkg/utils/revision_utils.go`, RBGSet CRD unchanged) — verified by `git diff --stat 0821cb5b origin/main`; round-1 code-path contracts remain valid.
- Live layer: **skipped by debate-round configuration** (unit + integration only).

## Premise (P0)

Issue #455 "[Feature] RBGS-level RollingUpdate implementation" — **OPEN**, requests set-level rolling update for RoleBasedGroupSet, citing #112/#18. Matches the KEP's subject and the only files the PR touches. The KEP also honestly discloses that the propagation fix (whole `groupTemplate.spec` incl. `roleTemplates`) changes the legacy path too — and that claim is real (see E-P0 below). Premise **valid**.

## Observed vs expected

| ID | Claim under test | Layer | Observed | Expected | Verdict |
|----|------------------|-------|----------|----------|---------|
| P0 | Set controller drops `groupTemplate.spec.roleTemplates` on children today (KEP's propagation-gap premise) | unit (`TestVerify491P0PropagationGap`) | `newRBGForSet` and `updateExistingRBGs` copy `Roles` only; child `Spec.RoleTemplates` stays empty | child lacks `roleTemplates` → `templateRef` unresolvable | **confirmed** |
| E1 | `RoleBasedGroupSpec` has exactly `Roles` + `RoleTemplates` (KEP's "blast radius bounded" claim) | unit (`TestVerify491E1SpecBlastRadiusBounded`, reflection) | exactly those two fields | bound holds today | **confirmed** |
| E2 | "RBG publishes `RollingUpdateInProgress` …" reads like existing behavior | script (`E2-no-writer`) | 0 non-test writers in `internal/`/`cmd/`/`pkg/`; condition type only *declared* (api v1alpha2 rolebasedgroup_types.go:580); RBG controller sets only `Ready`/`GangConfigured` | if the KEP means a barrier that exists, ≥1 writer | **refuted as existing behavior** → F2 |
| E3 | Scale subresource interaction with redefined `status.replicas` | script (`E3a/b/c`) | scale subresource served on `.status.replicas` in both API versions + CRD; KEP text mentions scale subresource/HPA **0 times** | design that changes `status.replicas` semantics must cover the surface that reads it | **gap confirmed** → F1 |
| E4 | Controller-owned annotation exclusion is load-bearing | unit + script (`TestVerify491E4ControllerOwnedAnnotationWipe`, `E4a/b`) | RBG controller writes `discovery-config-mode` onto children; `needsUpdate` reports drift from that annotation alone; `updateExistingRBGs`/`syncRBGMetadata` wipes it (annotations → nil); child then looks converged until the RBG controller re-adds it — oscillation | exclusion list must be explicit and shared by hash / comparison / sync | **confirmed (deterministic unit repro)** → F3 |
| E5 | Naming surface | script (`E5`) | role-level enum has legacy `Recreate` spelling + `InPlaceUpdateStrategy` field | set-level `Recreate`/`InPlaceUpdate` coexists with them | **confirmed** → F4 |
| E6 | Revision-hash exclusion of controller-injected template keys | script (`E6`) | v1alpha1→v2 conversion injects `conversion.workloads.x-k8s.io/v1alpha1-coordination` into `groupTemplate.annotations` (rolebasedgroupset_conversion.go:61); KEP's exclusion example names only `discovery-config-mode` | conversion-injected keys must be enumerated as excluded, or a v1alpha1 round-trip of an unchanged template changes revision identity | **gap confirmed** → F6 |

## Test layers

- **L1 unit**: `go test ./internal/controller/workloads/` — green (3.6 s), incl. the two harness tests above.
- **L2 integration (envtest)**: `go test ./test/envtest/testcase/...` with `KUBEBUILDER_ASSETS=~/.local/share/kubebuilder-envtest/k8s/1.36.2-linux-amd64` — green: `rbg` 47 s, `restart_policy` 201 s, `webhook` 32 s. Confirms the PR changes no behavior (docs-only) and the touched component's suites pass at head.

## Harness bites

`results/bite-check.txt` + `results/bite-a-with-propagation-fix.txt`:

- **Bite A**: implementing the KEP's propagation fix in `newRBGForSet` (copy `RoleTemplates`) flips `TestVerify491P0` red — the harness genuinely detects the fact it pins. Code reverted after (`git diff` clean).
- **Bite B**: inserting "scale subresource" into the KEP's Status heading flips E3c's grep 0→1 (FAIL) — the silence check genuinely detects KEP text changes.
- **Bite C** (2026-10-12): mutating `needsTemplateAnnotationUpdate` to skip the controller-owned `discovery-config-mode` key (the exclusion F3 asks for) flips `TestVerify491E4ControllerOwnedAnnotationWipe` red (`results/bite-b-annotation-fix.txt`). Code reverted after; production diff clean.

## Round 2 (2026-10-14, head `b03a7e67`) — observed vs expected

The amendment answers every round-1 MUST/SHOULD. Status per prior finding:

| ID | Round-2 check | Observed | Verdict |
|----|---------------|----------|---------|
| F1 (child barrier) | `R2-F1-barrier-section`, `R2-F1b-ordering-matches-code`, `R2-F1c-api-reader-exists` | New *Child Rollout Barrier* section: producer (RBG controller, uncached `apiReader` reads, per-role workload checks), ordering invariant (status written Step 4 before Step 8 mutates roles), consumer (`groupSetChildReady`, absent-or-False@ObservedGeneration≥Generation). The Step 4/Step 8 claim **maps onto the real controller**: `Step 4: Construct role statuses` (rolebasedgroup_controller.go:221) calls `updateRBGStatus` inside `constructAndUpdateRoleStatuses` (line 689), before `Step 8: Reconcile roles` (line 276); `apiReader` exists (line 121) | **fixed** (design; implementation PR still owes E2's writer) |
| F2 (scale subresource) | `E3c-kep-addresses-scale` (inverted round-1 canary) | New *Scale subresource interaction* paragraph: surge visible in `.status.replicas`, HPA-during-surge declared **not a supported configuration**, scoped to manual/set-aware scaling | **fixed** (scoped out with reason, as asked) |
| F3+F4 (controller-owned keys) | `R2-F3-key-enumeration` | One enumerated 4-key list (`discovery-config-mode` + three `conversion.workloads.x-k8s.io/*` keys) governing revision hash, drift comparison, and metadata sync; `syncRBGMetadata` carries the keys over | **fixed** at design level; the E4 unit canary stays green (code unchanged — docs-only PR), flips when implementation lands |
| F5 (checkpoint storage) | `R2-F5-checkpoint-storage` | Appendix names the annotation `rbg.workloads.x-k8s.io/groupset-observed-replicas` on the RBGSet | **fixed** |
| F6 (numeric revision) | `R2-F6-numeric-informational` | Appendix: field is informational only; name equality decides A→B→A | **fixed** |
| F7 (partition timing) | `R2-F7-partition-timing` | Appendix: resolved against current `spec.replicas` at reconcile time; completed batches not re-evaluated | **fixed** |
| Q1 (maxUnavailable 0) | `R2-Q1-zero-verdict` | Appendix: the webhook rejects it | **fixed** |
| Q2 (downgrade stance) | `R2-Q2-downgrade-stance` | Appendix: downgrade stance written down (legacy path resumes, strategy preserved by webhook) | **fixed** |
| **F9 (NEW)** | `R2-F9-bootstrap-window` (contract, expected-FAIL repro) | Absent-as-ready is accepted for *all* children, including ones the set has just spec-bumped whose RBG controller has not reconciled yet (post-upgrade bootstrap window). The producer invariant does not cover that window: nothing has published the barrier yet on a child the set touched first. Fix = one sentence restricting absence-acceptance to children not yet updated in the current rollout | **open** (minor/question; reproduction at `results/R2-F9-bootstrap-window.txt`; bite D proves the check detects the fix) |
| M1 (test-plan gaps) | diff + residual scan | Entries added for barrier set/clear, bootstrap semantics, scale-subresource behavior, controller-owned annotation survival — the four round-1 gaps. The two Reviewer C wording catches remain (line 434 "old template, not the current one"; the :401 in-place ordering sentence still gates the higher ordinal on its own completion) | **mostly fixed**; two advisory wording nits open |
| F8 / O1 / C4238820879 (nits) | residual-open scan | No vocabulary cross-reference, no Graduation/Release Signoff sections, lowercase "rbg" at line 478 | **open** (nits, non-gating) |

## Round-2 harness bites

- **Bite D** (`results/bite-d-f9-simulated-fix.txt`): appending the one-sentence F9 restriction to the KEP text (temp commit via commit-tree; ref restored) flips `R2-F9` 0→1. The check detects the fix it asks for.
- **Bite E** (`results/bite-e-barrier-section-removed.txt`): stripping the *Child Rollout Barrier* section (round-1 state simulated) makes `R2-F1` fail (no matches). The fix-pinning checks are real regression tests.
- Round-1 bites A/B/C remain recorded in `results/`.

## Re-verify

```bash
bash docs/verification/455-rbgs-rolling-update-claude/scripts/re-verify.sh
```

Resolves head from `origin/pr/491`; prints PASS/FAIL per check; full logs in `results/`.

## Continuing after the fix (polarity)

When the KEP's implementation lands, graft this harness onto the new head (`git checkout <new-head> -- .` is *not* the way: rebase or cherry-pick this branch's harness commit onto the PR head, or fetch `refs/remotes/origin/pr/491` and re-run the script) and read:

- `TestVerify491P0PropagationGap`, `E2`–`E6`, `E1` are **contract checks pinning base facts**: they keep passing while those facts hold. `TestVerify491P0` flips red the moment `roleTemplates` propagation lands — then invert it into the fixed-behavior contract.
- `TestVerify491E4ControllerOwnedAnnotationWipe` is a **bug-canary**: it passes while the annotation wipe exists. When propagation preserves controller-owned keys, it flips red — invert the three assertions (drift=false, annotation survives, convergence without deletion) and promote it to a contract test.
- E3c was a **text canary** in round 1 (passed while the KEP was silent on the scale subresource). The round-2 amendment covered it, the canary flipped (as predicted), and it has been **inverted into a contract check** (`E3c-kep-addresses-scale`) pinning the F2 fix.
