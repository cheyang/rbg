# KEP-502 workload status model (PR #528) — review verification harness

Reproducible evidence for the findings raised while reviewing
https://github.com/sgl-project/rbg/pull/528 (docs-only KEP PR; adds
`keps/502-workload-status-model/README.md` + `kep.yaml`).

Layers run against the **code under review** (`f05e1dd83d2691e4157bf1bd3548fe405b2a49d5`):

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit | doc-consistency + premise checks (`keps/502-workload-status-model/doc_consistency_test.go`) | `go test ./keps/502-workload-status-model/ -v` |
| 2. Integration | author's claimed verification replayed: markdownlint with repo config; YAML parse of all 3 examples + kep.yaml (the YAML parses are also embedded as Go guards) | `npx --yes --offline markdownlint-cli2 --config .github/.markdownlint.json keps/502-workload-status-model/README.md` (recorded in `results/markdownlint.txt`) |
| 3. Live | n/a — docs-only PR, skipped per round policy | — |

> Test polarity: every test is a CONTRACT test. Premise (P0) and hygiene tests
> PASS on the reviewed head and guard the KEP's factual claims about main.
> Finding tests (F1–F5) FAIL on the reviewed head — red IS the reproduction —
> and PASS once the KEP text is corrected.

## Problem premise (P0)

| | |
|---|---|
| Claimed symptom | "RBG / RIS / RI lack `status.phase`, requiring users to infer the lifecycle … RBG declares `Progressing` and `RollingUpdateInProgress`, but the built-in controllers on the main branch do not currently write them. … RIS / RI `FailedScale` and `FailedUpdate` record individual operation failures" (KEP Motivation; mirrors issue #502 "Current limitations") |
| Linked issue | #502 "[Feature] Improve workload status reporting" — OPEN (created 2026-10-19), referenced not closed; still valid (`results/issue-502.json`) |
| Reported component | RBG / RIS / RI status model, `workloads.x-k8s.io/v1alpha2` |
| Patched component | `keps/502-workload-status-model/` (design doc only — matches the issue's "develop a KEP and submit the detailed design as a PR" next step) |
| Component match | Yes |
| **Verdict** | **Confirmed** |
| Evidence | `TestPremise*` all pass at the reviewed head; the PR diff touches only `keps/`, so head == base (`885d655…`) for every file read. Verified: no `Phase` field in any of the three status structs; `RoleBasedGroupProgressing`/`RoleBasedGroupRollingUpdateInProgress` declared in `api/workloads/v1alpha2/rolebasedgroup_types.go` but never referenced by `internal/` or `pkg/`; RIS/RI reconcilers write `FailedScale`/`FailedUpdate`; RIS/RI condition structs lack `observedGeneration`; RBG Ready computed from role replica counts (`AllRolesReady`/`RoleNotReady`) and the controller early-returns on `deletionTimestamp` without rewriting Ready. `results/unit-head-f05e1dd.txt` |

## Summary of results

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| P0 | Premise claims about main branch | 1 | Confirmed | 5/5 `TestPremise*` pass; `results/unit-head-f05e1dd.txt` |
| F1 | RBG `Progressing=True` candidate reason `Preparing` is used nowhere else in the KEP (not in the reason-family table, activity table, phases, or prose) | 1 | Confirmed | `TestRBGCandidateReasonsDefined` fails: "Preparing (row Progressing=True)" |
| F2 | Reporting-priority rule contradicts the RBG example: `ChildReadiness` is in the blocker priority list ("a current blocker outranks an activity reason"), yet the example reports `ScalingOrUpdating` while a child-readiness fact holds (Ready=False, decode 2/3) | 1 | Confirmed | `TestExampleConsistentWithPriorityRule` fails |
| F3 | `Progressing=False` covers "configuration rejected" but its candidate reasons omit `TemplateInvalid` and `DependencyCycle` (both are `Accepted=False` rejection reasons) | 1 | Confirmed | `TestProgressingFalseCoversConfigRejection` fails |
| F4 | KEP marks RBG `Ready` "[Retained]" but adds "or the object is terminating" as a new `Ready=False` trigger; today the controller early-returns on deletion and never rewrites Ready — an unflagged behavior change | 1 | Confirmed | `TestReadyTerminatingChangeAcknowledged` fails; code evidence `internal/controller/workloads/rolebasedgroup_controller.go` (`if !rbg.DeletionTimestamp.IsZero() { return }`, no `Terminating` writes) |
| F5 | TOC omits the `Reason Taxonomy and Aggregation` section | 1 | Confirmed | `TestTOCComplete` fails |
| G1 | kep.yaml well-formed; all 3 YAML examples parse | 1 | Pass (guard, no finding) | `TestKepYAMLWellFormed`, `TestYAMLExamplesParse`; `results/markdownlint.txt` (0 issues) |

## Harness-bites proof

`scripts/bites-check.sh` applies candidate fixes for F1–F5 to the KEP README,
runs the suite (all 12 tests green — `results/bites-check.txt`), then reverts.
Production diff (everything except the additive test file and this directory)
is empty.

## Per-finding detail

- **F1 (minor)** — `Preparing` appears once, in the RBG candidate-reasons table
  (`README.md` line ~275). The KEP's own definition point for activity reasons
  is the "Activity and child-readiness reasons" table; `Creating`,
  `ScalingOrUpdating`, `Recovering` are anchored there, `Preparing` is not, and
  no prose or example mentions it. Implementers cannot tell when to use it vs
  `Creating`.
- **F2 (minor)** — the fenced priority list after "highest reporting priority"
  ends with `ChildReadiness`, and the text says a current blocker outranks an
  activity reason. The RBG example holds a child-readiness fact (decode 2/3
  ready, Ready=False) yet reports the activity reason `ScalingOrUpdating`. As
  written, the rule selects `RolesNotReady`; the example selects
  `ScalingOrUpdating`. One sentence ("child readiness does not outrank an
  activity reason while a workflow is in progress") resolves it.
- **F3 (minor)** — the `Progressing` condition's False state covers
  "configuration rejected", and `Accepted=False` has five rejection reasons;
  only three are mapped to `Progressing=False`. `TemplateInvalid` and
  `DependencyCycle` have no defined Progressing=False mapping.
- **F4 (minor)** — current code: `rolebasedgroup_controller.go` returns
  immediately when `deletionTimestamp` is set and computes Ready purely from
  role replica counts, so a Ready object keeps `Ready=True` until it is gone.
  The KEP's conditions table adds "or the object is terminating" to Ready=False
  while the example marks the condition `[Retained]`, and the
  upgrade/downgrade + risks sections never mention the new False trigger.
  Consumers (release waits, dependency startup, alerts) see a new False
  transition during normal deletion; the KEP should flag it `[Changed]` and
  assess it in the compatibility analysis.
- **F5 (nit)** — TOC entry missing for `Reason Taxonomy and Aggregation`
  (`###` under Design Details).

## Continuing after the fix

On any machine:

```bash
git fetch https://github.com/cheyang/rbg.git verify/kep-502-status-model-codex
git checkout FETCH_HEAD   # or the branch itself
bash docs/verification/kep-502-status-model-codex/scripts/re-verify.sh
```

`re-verify.sh` resolves the current PR head from `manifest.pr`, grafts the
harness onto it, and prints per finding FIXED / STILL-BROKEN / PARTIAL /
HARNESS-UPDATE. All findings are contract polarity: FIXED == the corresponding
KEP text gap is closed. `.last-reviewed` records the reviewed head; advance it
after each round. No cluster, kubeconfig, or envtest assets are needed; the
live layer is intentionally absent.

Kickoff prompt for a fresh agent: "Check out
https://github.com/cheyang/rbg.git branch verify/kep-502-status-model-codex and
run docs/verification/kep-502-status-model-codex/scripts/re-verify.sh; report
the per-finding table."
