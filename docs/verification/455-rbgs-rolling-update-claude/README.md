# Verification — PR #491 (KEP-455: RBGSet rolling update) — Reviewer A (Claude)

First round, 2026-10-11. Branch `verify/455-rbgs-rolling-update-claude` (fork `cheyang/rbg`).

- PR: https://github.com/sgl-project/rbg/pull/491 — **docs-only**: adds `keps/455-rbgs-rolling-update/` (README + kep.yaml), no code changes.
- Base `0821cb5b` … head `719851a6` (single commit, 396 added lines, 2 files).
- Live layer: **skipped by debate-round configuration** (unit + integration only). Probe result for the record: a cluster *was* reachable (`kubectl get no` → 2 aliyun v1.36.2 nodes); it was not used.

## Premise (P0)

Issue #455 "[Feature] RBGS-level RollingUpdate implementation" — **OPEN**, requests set-level rolling update for RoleBasedGroupSet, citing #112/#18. Matches the KEP's subject and the only files the PR touches. The KEP also honestly discloses that the propagation fix (whole `groupTemplate.spec` incl. `roleTemplates`) changes the legacy path too — and that claim is real (see E-P0 below). Premise **valid**.

## Observed vs expected

| ID | Claim under test | Layer | Observed | Expected | Verdict |
|----|------------------|-------|----------|----------|---------|
| P0 | Set controller drops `groupTemplate.spec.roleTemplates` on children today (KEP's propagation-gap premise) | unit (`TestVerify491P0PropagationGap`) | `newRBGForSet` and `updateExistingRBGs` copy `Roles` only; child `Spec.RoleTemplates` stays empty | child lacks `roleTemplates` → `templateRef` unresolvable | **confirmed** |
| E1 | `RoleBasedGroupSpec` has exactly `Roles` + `RoleTemplates` (KEP's "blast radius bounded" claim) | unit (`TestVerify491E1SpecBlastRadiusBounded`, reflection) | exactly those two fields | bound holds today | **confirmed** |
| E2 | "RBG publishes `RollingUpdateInProgress` …" reads like existing behavior | script (`E2-no-writer`) | 0 non-test writers in `internal/`/`cmd/`/`pkg/`; condition type only *declared* (api v1alpha2 rolebasedgroup_types.go:580); RBG controller sets only `Ready`/`GangConfigured` | if the KEP means a barrier that exists, ≥1 writer | **refuted as existing behavior** → F2 |
| E3 | Scale subresource interaction with redefined `status.replicas` | script (`E3a/b/c`) | scale subresource served on `.status.replicas` in both API versions + CRD; KEP text mentions scale subresource/HPA **0 times** | design that changes `status.replicas` semantics must cover the surface that reads it | **gap confirmed** → F1 |
| E4 | Controller-owned annotation exclusion is load-bearing | script (`E4a/b`) | RBG controller writes `discovery-config-mode` onto children; set's `syncRBGMetadata` replaces child annotations wholesale | exclusion list must be explicit and shared by hash / comparison / sync | **confirmed** → F5 |
| E5 | Naming surface | script (`E5`) | role-level enum has legacy `Recreate` spelling + `InPlaceUpdateStrategy` field | set-level `Recreate`/`InPlaceUpdate` coexists with them | **confirmed** → F4 |

## Test layers

- **L1 unit**: `go test ./internal/controller/workloads/` — green (3.6 s), incl. the two harness tests above.
- **L2 integration (envtest)**: `go test ./test/envtest/testcase/...` with `KUBEBUILDER_ASSETS=~/.local/share/kubebuilder-envtest/k8s/1.36.2-linux-amd64` — green: `rbg` 47 s, `restart_policy` 201 s, `webhook` 32 s. Confirms the PR changes no behavior (docs-only) and the touched component's suites pass at head.

## Harness bites

`results/bite-check.txt` + `results/bite-a-with-propagation-fix.txt`:

- **Bite A**: implementing the KEP's propagation fix in `newRBGForSet` (copy `RoleTemplates`) flips `TestVerify491P0` red — the harness genuinely detects the fact it pins. Code reverted after (`git diff` clean).
- **Bite B**: inserting "scale subresource" into the KEP's Status heading flips E3c's grep 0→1 (FAIL) — the silence check genuinely detects KEP text changes.

## Re-verify

```bash
bash docs/verification/455-rbgs-rolling-update-claude/scripts/re-verify.sh
```

Resolves head from `origin/pr/491`; prints PASS/FAIL per check; full logs in `results/`.
