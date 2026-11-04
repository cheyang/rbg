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


## Round 2 — revision 6335eac (incremental, 2026-11-03)

The revision (4 commits, README.md only) resolved every round-1 finding and
answered all three parked questions. The harness was updated additively:

- `TestExampleConsistentWithPriorityRule` (F2) was re-anchored structurally
  (the fix removed the sentence the old test anchored on — a Harness-update,
  not a pass); it now locates the priority fence by content and accepts any of
  the three closings (ChildReadiness absent, Activity ranked above it, or
  subsumption prose).
- New contract tests cover the Claude-side consensus findings
  (F5-collision, F6-casing, F8-event-budget, O1-row-ambiguity) and the
  question resolutions (Q1: RI gains `Recovering` phase / RIS deliberately has
  none; Q2: explicit "no Failed phase"; Q3 + Copilot C4431017493: RIS
  phase-display priority independent of condition reason selection, overlap
  recorded as an accepted v1alpha2 trade-off).
- Harness bug fixed: `section()` substring-matched "RoleInstance" to the
  earlier "RoleInstanceSet" heading; added `sectionExact`.

Bite check at previous head f05e1dd: all 12 finding/guard tests FAIL (red IS
the reproduction), 5 premise + 2 hygiene tests pass
(`results/bite-check-previous-head-f05e1dd.json`).

Result at head 6335eac: **17/17 pass** (`results/unit-head-6335eac.txt|.json`);
markdownlint replay clean, 0 issues (`results/markdownlint-head-6335eac.txt`).

| Ledger ID | Round-1 verdict | Round-2 verdict | Evidence |
|-----------|-----------------|-----------------|----------|
| F1-preparing-undefined | Confirmed (minor) | FIXED | `Preparing` dropped from Progressing=True row; `TestRBGCandidateReasonsDefined` green |
| F2-priority-rule-vs-example | Confirmed (minor) | FIXED | `Activity` ranked above `ChildReadiness` in the fence + subsumption prose; `TestExampleConsistentWithPriorityRule` green |
| F3-progressing-false-rejection-mapping | Confirmed (minor) | FIXED | `TemplateInvalid`, `DependencyCycle` added to Progressing=False row + prose mapping; test green |
| F4-ready-terminating-retained-mislabel | Confirmed (minor) | FIXED | Example relabeled `[Retained type; terminating trigger changed]`, prose calls it a behavior change, upgrade table covers it; test green |
| F5-rolesnotready-collision | Confirmed (minor) | FIXED | Explicit distinct-strings acknowledgment sentence; test green |
| F6-inplace-reason-casing | Confirmed (minor) | FIXED | Enumeration now spells `InplaceUpdateNotReady`, matching `instance_status.go:307`; test green |
| F7-toc-missing-reason-taxonomy | Confirmed (nit) | FIXED | TOC gained both new section entries; `TestTOCComplete` green |
| F8-event-budget-risk | Confirmed (minor) | FIXED | New Risks row names the event-budget/spam-filter hazard; test green |
| O1-ready-reason-row-ambiguity | Confirmed (nit) | FIXED | Row split into Ready=True / Ready=False; test green |
| Q1-ris-phase-during-recovery | Open question | ANSWERED in doc | "RIS does not define a Recovering phase … This is intentional"; guard green |
| Q2-no-failure-phase | Open question | ANSWERED in doc | New "Validation Ownership and Failure Semantics" section: "There is no Failed phase"; guard green |
| Q3-ris-concurrent-scaling-visibility | Open question | ANSWERED in doc | Overlap = one dominant phase + one reason, recorded as accepted v1alpha2 trade-off; guard green |
| C4431017493 (Copilot advisory) | Unconfirmed | ADDRESSED in doc | Same RIS paragraph: blockers/pause take precedence over `RollingUpdate` |
| R-rolloutreplacementstate-phantom | Refuted (round 1) | n/a (doc unchanged there) | retained-field inventory untouched by the delta |

Known harness-infra note: the stock `re-verify.sh` persists raw results into the
in-repo `results/reverify/` dir *before* its exit trap restores the harness
ref with `git checkout -f`, so tracked files there are the round-1 committed
copies. Trust the printed verdict table plus `results/unit-head-6335eac.*`
(captured separately) over `results/reverify/`.


## Round 3 — revision f622d642 (incremental, 2026-11-04)

Delta: one commit, 3 changed lines in `keps/502-workload-status-model/README.md`
(`kep.yaml` untouched), targeting exactly the two open round-2 items:

- F9 fix: Goals item 2 (:70) and the RoleBasedGroup intro (:211) now read
  "lower-level phases, conditions, and recovery facts" — both stragglers name
  conditions, matching the authoritative statements (:463, :502).
- F10 fix: the RIS Progressing prose (:358) now enumerates "Activity reasons
  are `Creating`, `ScalingUp`, `ScalingDown`, `RollingUpdate`, and
  `Recovering`" — exactly the RIS activity-reason set derivable from the
  activity/child-readiness family table.

Harness extended additively with the two contracts the Claude harness carried
in round 2 (C8/C9), so both findings are now double-harnessed:

- `TestTransitionInputVocabularyConsistent` (F9): uniformity of the
  transition-input vocabulary across all RBG transition-input statements.
  Polarity/provenance matrix: PASS at f05e1dd (uniform "phases"), FAIL at
  6335eac (mixed), PASS at f622d642 — matching the recorded
  revision-regression attribution. (First version of this guard counted any
  "condition" substring and false-failed at f05e1dd on the trailing "uses
  Accepted / Progressing / Ready conditions" clause; fixed to detect
  `lower-level (phases, )?conditions` within the input enumeration.)
- `TestRISActivityReasonsEnumerated` (F10): the prose enumeration must equal
  the RIS activity-reason set derived from the family table. FAIL at f05e1dd
  and 6335eac (missing Creating, Recovering), PASS at f622d642 — matching the
  recorded pre-existing/late-discovery attribution.

Result at head f622d642: **21/21 pass** (`results/unit-head-f622d642.json`,
`results/reverify-head-f622d642.txt` — re-verify.sh exit 0, all 15 findings
FIXED); markdownlint replay clean (`results/markdownlint-head-f622d642.txt`).
Raw per-ref runs: `results/unit-previous-head-6335eac.json` (both new tests
red — the bite check), `results/unit-r1head-f05e1dd-round3.json` (F9 green /
F10 red — the provenance check). `results/reverify/unit.out|.parsed` were
manually refreshed to the f622d642 run this round (the stock script's
restore-on-exit still clobbers tracked copies — see the round-2 infra note).

| Ledger ID | Round-2 verdict | Round-3 verdict | Evidence |
|-----------|-----------------|-----------------|----------|
| F9-rbg-transition-input-inconsistent | OPEN (minor, revision regression) | FIXED | `TestTransitionInputVocabularyConsistent` pass@f05e1dd / fail@6335eac / pass@f622d642 |
| F10-ris-activity-reasons-unenumerated | OPEN (nit, pre-existing) | FIXED | `TestRISActivityReasonsEnumerated` fail@f05e1dd / fail@6335eac / pass@f622d642 |
| all other ledger items | FIXED/ANSWERED at 6335eac | still FIXED (no regression) | full suite 21/21 green at f622d642 |

## Round 4 — revision da818779 (incremental, 2026-11-05)

Delta: one docs-only commit (`docs: define RBG activity reason precedence`, +11
lines in `keps/502-workload-status-model/README.md`). It is the targeted fix
for the one item round 3 left open — the Copilot advisory C4457744212
(within-Activity tie across heterogeneous role facts).

What changed:

1. A within-Activity precedence declaration after the cross-family rule
   (README.md:184-192): `Creating` > `Recovering` > `ScalingOrUpdating`, with
   per-step rationale, plus "RBG uses the highest-precedence activity reason
   and describes all activities in message".
2. A Test Plan bullet (README.md:495) covering the concurrent scenarios:
   Recovering+updating → `Recovering`; Creating+updating/recovering →
   `Creating`; update/scaling only → `ScalingOrUpdating`; message describes
   every concurrent activity.

Verified:

- `TestRBGActivityPrecedenceDefined` (new contract guard): **red at f622d642**
  ("no within-Activity precedence declaration found"), **green at da818779**.
  The guard derives the expected reason set from the family table's RBG
  activity column (Creating, ScalingOrUpdating, Recovering), so it checks
  exhaustiveness, not just presence; it also pins the reviewed order and the
  Test Plan concurrency bullet. Evidence: results/bite-check-c4457744212-prev-f622d642.txt,
  results/unit-head-da818779-c4457744212.txt.
- Full suite at da818779: 22/23 green; the one red is the intentional F11
  probe below (results/unit-head-da818779.txt).
- markdownlint-cli2 with .github/.markdownlint.json: 0 issues
  (results/markdownlint-head-da818779.txt).
- No consistency conflicts found: the precedence list covers exactly the
  family table's RBG activity reasons; the cross-family rule (external
  blockers > Activity > ChildReadiness) is untouched; the candidate-reasons
  table, the RBG example, and :296 ("Each condition has one reason ...
  ScalingOrUpdating") all remain consistent.

New finding this round:

| finding | severity | status | evidence |
| --- | --- | --- | --- |
| C4457744212-activity-family-tie-break | nit (Copilot advisory) | FIXED at da818779 | TestRBGActivityPrecedenceDefined fail@f622d642 / pass@da818779 |
| F11-ris-activity-tie-break-unstated | nit | OPEN | TestRISActivityTieBreakStated red at da818779 (and at f622d642 — gap pre-exists the delta): RIS Progressing reason selection is underdetermined when RI recovery overlaps an active RIS update/scaling workflow. README.md:349 resolves update-vs-scaling only; the new precedence at :184 is explicitly RBG-scoped; :358 "RIS Progressing follows RBG rules for progress" does not state whether the Activity precedence is inherited (and ScalingOrUpdating would need mapping to the concrete RIS workflow reason). One sentence — e.g. "RIS applies the RBG Activity precedence with the concrete workflow reason in place of ScalingOrUpdating" — closes it. |

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
