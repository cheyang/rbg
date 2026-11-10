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
| F12-rbg-replacement-creation-reason-mapping | minor | OPEN | TestRBGReplacementCreationMapsToRecovery red at f622d642 and da818779: a replacement RIS (new UID) reports Creating; under the :169 aggregation model the new precedence (Creating > Recovering) then reports Creating for a group :213 classifies as entering recovery, and the :495 Test Plan bullet would codify it. Independently confirms Copilot C4471100373. Fix: state the replacement-child -> parent-reason mapping, or scope RBG Creating to the group's initial creation. |
| F11-ris-activity-tie-break-unstated | nit | OPEN | TestRISActivityTieBreakStated red at da818779 (and at f622d642 — gap pre-exists the delta): RIS Progressing reason selection is underdetermined when RI recovery overlaps an active RIS update/scaling workflow. README.md:349 resolves update-vs-scaling only; the new precedence at :184 is explicitly RBG-scoped; :358 "RIS Progressing follows RBG rules for progress" does not state whether the Activity precedence is inherited (and ScalingOrUpdating would need mapping to the concrete RIS workflow reason). One sentence — e.g. "RIS applies the RBG Activity precedence with the concrete workflow reason in place of ScalingOrUpdating" — closes it. |

## Round 5 — revision 0b7285b7 (incremental, 2026-11-07)

Delta: two docs-only commits (`e09cb647` "docs: scope creation and define RIS
activity precedence", `0b7285b7` "docs: clarify status contract and RI phase
transitions"; +155/-137 in `keps/502-workload-status-model/README.md`). This is
a **design pivot**, not a polish revision:

1. RBG and RIS no longer add `status.phase`; workflow is reported through
   `Progressing.reason` plus existing counters (:251, :283, :330-344). RIS also
   drops its proposed new `Ready` condition. Only RI keeps an optional phase
   and gains an explicit allowed-edge table (:420-436).
2. The RBG `Ready=False(reason=Terminating)` trigger (F4's subject) is removed:
   "A deletionTimestamp does not by itself rewrite Ready" (:299), and Ready is
   never Unknown after the first observation (:140, :291) — matching the
   current controller (no `ConditionUnknown` path in
   internal/controller/workloads/rolebasedgroup_controller.go).
3. `Accepted` is redefined as a compatibility gate with explicit
   True/False/Unknown semantics (:221-234); the section is renamed
   "Validation Ownership and Accepted Semantics".
4. New sections/rules: Consuming the Status decision table (:205-217),
   deterministic blocker classification with 403 disambiguation and the
   unclassifiable-error rule (:158-160), parent observedGeneration dispatch
   rule (:141), condition-only-writes rule (:142), RIS activity precedence
   (:347-349, the F11 fix), RIS stateless/stateful readiness-mode difference
   (:361), RI transition-timestamps migration row (:564).

Harness handling (all re-anchors additive; production diff still empty):

- `TestYAMLExamplesParse` (G1): polarity flipped per kind — RBG/RIS examples
  must now carry NO `status.phase`, RI must carry one. Red at all pre-pivot
  heads by construction (contract change, recorded on AC-doc-tooling).
- `TestReadyTerminatingReasonRowSplit` (O1): re-anchored — the Terminating
  reason is gone from Ready rows; the durable contract is split rows and no
  Terminating under Ready=True. Green at 6335eac..head.
- `TestRecoveryPhaseSplitRISvsRI` (Q1): accepts either the pre-pivot ("RIS
  does not define a Recovering phase") or post-pivot ("RIS does not add
  status.phase" + recovery via Progressing) resolution. Green at 6335eac..head.
- `TestRISOverlapPrecedenceStated` (Q3): accepts the old display-priority
  formulation or the new "an active update outranks scaling" + RBG-rules
  inheritance. Green at 6335eac..head.
- `TestTransitionInputVocabularyConsistent` (F9): site regex broadened to the
  post-pivot aggregation phrasing with proximity anchoring (excludes the
  meta-discussion at :283); uniformity semantics unchanged. Matrix: PASS
  f05e1dd / FAIL 6335eac / PASS f622d642 / PASS da818779 / PASS 0b7285b7.
- `TestRISActivityTieBreakStated` (F11): strengthened with the Test Plan
  coverage check. **FIXED at 0b7285b7** — :347 states the RIS precedence
  (Creating > Recovering > RollingUpdate > ScalingUp/Down, "Recovery outranks
  active update and scaling") and :511 adds the recovery-overlap bullet.
- New premise guards (green at head and at base 885d655): stateless/stateful
  RI-readiness difference (`IsRunningAndReady` checks RoleInstanceReady +
  every spec.readinessGates + components; stateful `isHealthy` checks only
  RoleInstanceReady), RI condition timestamp rebuild
  (`instance_status.go:248,311` stamp `metav1.Now()`, `setInstanceConditions`
  replaces the list), minReadySeconds reads RoleInstanceReady.lastTransitionTime
  in BOTH modes, lifecycle label `rbg.workloads.x-k8s.io/role-instance-lifecycle-state`
  + its five values exist, RBG Ready has no Unknown path.

Findings at head:

| finding | severity | status | evidence |
| --- | --- | --- | --- |
| F11-ris-activity-tie-break-unstated | nit | FIXED at 0b7285b7 | guard fail@da818779 / pass@0b7285b7 (:347 + :511) |
| F12-rbg-replacement-creation-reason-mapping | minor | OPEN (narrowed, not closed) | `TestRBGReplacementCreationMapsToRecovery` red at 0b7285b7. The :199 definitions scope `Recovering` to "an established child is being repaired or replaced", but the family-table row (:169) maps Initial creation -> `Creating` unconditionally, parents are told to aggregate child reason families mechanically (:133, :176), a replacement RIS does report Creating (:347), and Creating outranks Recovering (:191-197) — the collision is unresolved. Fix simulation (one mapping sentence) flips the probe green (results/bite-check-round5.txt). |
| F13-test-plan-creating-branch-dropped | minor (revision regression) | OPEN | `TestRBGActivityPrecedenceCoverage` pass@da818779 / fail@0b7285b7: the delta dropped the "one role `Creating` while another updates/recovers" bullet while the precedence still ranks Creating first; the Creating branch now has no concurrent-activity coverage. Fix simulation (restore one bullet) flips green. |
| F14-consuming-table-config-compat-dead-branch | nit | OPEN | `TestConsumingTableAttentionRowConsistency` red at 0b7285b7: the "need attention" row (:213) lists configuration-compatibility families under `Progressing=True`, but every config-incompatibility reason maps to `Progressing=False` (candidate table + :234). Dead disjunct in the table consumers are told to codify. Fix simulation flips green. |
| F15-minready-timestamp-scope-stateful-too | nit | OPEN | `TestReadyTimestampMigrationScope` red at 0b7285b7: the migration row (:564) says "stateless RIS uses these timestamps for minReadySeconds", but stateful `isInstanceAvailable` (stateful_instance_set_helper.go:240-256) reads the same `RoleInstanceReady.lastTransitionTime` (feeds `status.availableReplicas`). Scope the behavior change to both modes. Fix simulation flips green. |

All other ledger findings stay FIXED at head; the suite is
28/32 green with the four intentional reds above (F12, F13, F14, F15)
— results/unit-head-0b7285b7.txt; re-verify.sh summary in
results/reverify-head-0b7285b7.txt; markdownlint replay 0 issues
(results/markdownlint-head-0b7285b7.txt); polarity matrix across
f05e1dd/6335eac/f622d642/da818779/0b7285b7 in results/polarity-round5.txt.

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

## Round 6 — revision 39e2ecdf (incremental, 2026-11-10)

Delta 0b7285b7..39e2ecdf (3 commits, docs-only, +21/-19): "docs: fix progress
write and creation reason rules", "docs: fold Creating target changes into
create convergence", "docs: tighten normative status wording".

Fixed this round (double evidence: guard differential + head-blob read):

| finding | severity | status @39e2ecdf | evidence |
| --- | --- | --- | --- |
| F12-rbg-replacement-creation-reason-mapping (ledger F12, incl. Copilot C4495569498 scale-up extension) | minor | FIXED | `TestRBGReplacementCreationMapsToRecovery` (extended with the scale-up clause) red@0b7285b7 / green@39e2ecdf: :199 scopes RBG `Creating` to initial establishment and classifies an established group's missing child under the owning workflow (`Recovering` for replacement, `ScalingOrUpdating` for an added role); :348 does the same for RIS (`ScalingUp` / `RollingUpdate` / `Recovering`); the Test Plan bullet pins "scale-up 2→3 with the third RI not yet created reports `ScalingUp`". |
| F15-precheck-progressing-contradiction (ledger F15; harness id added this round) | minor | FIXED | New guard `TestPreCheckProgressingWriteDerived` red@0b7285b7 (both the :140 rule and the :510 test-plan bullet) / green@39e2ecdf: the dispatch write now derives the `Progressing` value from the validation/pause/progress rules and names "a confirmed compatibility failure writes `Progressing=False` with the matching incompatibility reason" — consistent with the Accepted=False mapping. |

Harness-update (not a regression): `TestRIReadinessReasonsMatchCode` (F6)
re-anchored — the delta renamed "Candidate reasons for aggregated RI
readiness" to "Reasons for ..." with the enumeration unchanged; the guard now
matches the stable suffix and its polarity is re-proven (red@f05e1dd catches
the original `InPlaceUpdateNotReady` casing bug, green@head).

New findings introduced by this delta:

| finding | severity | status | evidence |
| --- | --- | --- | --- |
| F18-kep527-reference-dangling | nit | OPEN | `TestReferencedKEPsResolvable` green@0b7285b7 / red@39e2ecdf: three normative references to "KEP-527" (:248, :412, :438) but no `keps/527-*` directory in this tree (KEP-527 is PR #546, OPEN) — if this KEP merges first the pointer dangles; the round-5 code-verified concrete hook-label sentence it replaced is gone. The claim itself matches KEP-527's current draft text (verified against PR #546 head: creating folds target changes into Create convergence without hooks). Bite check: marking the reference pending flips green (results/polarity-round6.txt). |
| F19-ris-establishment-boundary-existential | minor | OPEN | `TestRISEstablishmentBoundaryCollective` green@0b7285b7 / red@39e2ecdf: the RIS Creating boundary (:348 "before the RIS has an established revision or established RIs for its target") reads existentially while the RBG analog (:199 "before the target's required roles have been established") is collective — read as written, an initial 0→3 bootstrap stops being `Creating` once the FIRST RI establishes and the remaining initial RIs fall under "a required RI that does not yet exist" → `ScalingUp`, the misclassification the Test Plan (:493) tells implementers to avoid. Bite check: collective rewording flips green. |

Still open (unchanged by the delta; guards red at head as designed):
F13-test-plan-creating-branch-dropped (nit; no concurrent-activity `Creating`
scenario in the Test Plan while the precedence ranks Creating first),
F14-consuming-table-config-compat-dead-branch (nit; the "need attention" row
(:213) is unchanged — config-compatibility families still listed under
`Progressing=True` where they can never occur),
F15-minready-timestamp-scope-stateful-too (= ledger F16; nit; migration row
:566 unchanged, still stateless-only while stateful `isInstanceAvailable`
reads the same timestamp).

Ledger items without harness guards, checked by head-blob read: Q5's
condition-observedGeneration half is ADDRESSED (:212/:216 add the
condition-level check; Copilot C4495569437) but the row remains
kind-unscoped and RI has no `Converged` reason (:461) — RI half open; Q6
(Progressing during deletion) untouched; ledger F17 (stale PR body: still
promises "persisted phase transitions for RBG, RIS, and RI") untouched.

Suite at head: 30 pass / 5 intentional open-finding reds (F13, F14,
F16-minready, F18, F19) — results/unit-head-39e2ecdf.txt; polarity matrix in
results/polarity-round6.txt; markdownlint replay 0 issues. CI at head:
9 SUCCESS / 6 SKIPPED (e2e groups legitimately skip on the docs-only diff).

## Round 7 — revision 05807351 (incremental, 2026-11-11)

Delta 39e2ecdf..05807351 (10 commits, docs-only, +120/-126): a redesign round —
drops the `Accepted` condition (configuration incompatibility is now a
`Progressing=True` blocker family), deprecates the RBG `Ready` condition
(compatibility window, then removal), renames the activity reasons
(`ScalingOrUpdating` -> `RoleInstanceSetUpdated`, RIS `RollingUpdate` ->
`RoleInstanceUpdated`), adds the Unclassified blocker family
(`PodOperationUnclassified` / `UnclassifiedBlocker`) with a confirmed-blocker
protection rule, separates RI phase completion from externally owned readiness
gates, and adds a reason-provenance table.

Fixed / resolved by the redesign (guard differential + head-blob read):

| finding | severity | status @05807351 | evidence |
| --- | --- | --- | --- |
| F14-consuming-table-config-compat-dead-branch | nit | FIXED (by redesign) | The "need attention" row's config-compatibility disjunct is live: config blockers now report `Progressing=True` with the family reason (:140, :238-246, candidate row :306). Guard re-anchored to the generalized contract (every family the row names must have a reason that can occur with the row's status): red@0b7285b7 / red@39e2ecdf / green@head (results/polarity-round7.txt). |
| C4515733997-readiness-gate-circular-wait (Copilot advisory) | nit | ADDRESSED | Phase completion now uses controller-observed workload readiness only; external gates "do not gate phase transitions" (:247-256, :402-422) — the circular wait with the KEP-527 lifecycle gate is removed explicitly. New pin `TestPhaseCompletionIndependentOfExternalGates` red@39e2ecdf / green@head. |
| C4515734101-activity-fallback-vs-blocker-clearing (Copilot advisory) | nit | ADDRESSED | "When a blocker family is already confirmed for the current target, an unclassifiable error does not replace it" (:177) + Unclassified family + test-plan bullet (:501). New pin `TestConfirmedBlockerSurvivesUnclassifiedError` red@39e2ecdf / green@head. |
| C4515734270-customready-aggregation-example (Copilot advisory) | nit | ADDRESSED | RI example's `RoleInstanceReady=False` is now `PodsNotReady` (missing Pod), and "RoleInstanceCustomReady and other externally owned readiness gates remain separate conditions and do not change RoleInstanceReady" (:453). New pin `TestCustomReadyDoesNotDetermineInstanceReady` red@39e2ecdf / green@head. |

Harness-updates (re-anchored for the redesign; polarity re-proven, none are
regressions): `TestRIReadinessReasonsMatchCode` (F6; sentence lead-in renamed,
enumeration unchanged and still matches the code), `TestRBGActivityPrecedenceDefined`
(C4457744212; order check derived from the family table, rename-proof),
`TestConsumingTableAttentionRowConsistency` (F14; new contract above),
`TestPreCheckProgressingWriteDerived` (F15; the dispatch rule must now agree
with the doc's own config mapping — green at both 39e2ecdf (False form) and head
(True form), red@0b7285b7), `TestProgressingFalseCoversConfigRejection` (F3;
was a vacuous pass after the redesign removed the `Accepted=False` row — now
asserts every config-family reason has a defined `Progressing` mapping;
red@f05e1dd re-proven), `TestReadyTerminatingReasonRowSplit` (O1; the Ready
rows are gone with the deprecation — successor contract pins the documented
deprecation + compatibility window, combined-row tripwire retained).
`TestRBGActivityPrecedenceCoverage` (F13) re-anchored to derive branches from
the declared fence; red@head on `[Creating]` only, as designed.

New finding introduced by this delta:

| finding | severity | status | evidence |
| --- | --- | --- | --- |
| F22-ris-readiness-code-claim-overbroad | nit | OPEN | `TestRISBlockerPropagationClaimAccurate` green@39e2ecdf (sentence absent) / red@head: :336 claims "Today's RIS code reads only `RoleInstanceReady`", but stateless `IsRunningAndReady` (pkg/reconciler/roleinstanceset/statelessmode/utils/utils.go) also reads every `spec.readinessGates` condition — the doc's own mode-difference sentence (:343) says so. The design point (RIS reads no RI `Progressing`, so blocker propagation is new) is correct; only the absolute wording is wrong. Fix: "reads no RI `Progressing` condition". |

Still open (unchanged by the delta; guards red at head as designed):
F13-concurrent-activity-coverage-regression (nit; no `Creating` concurrent-activity
scenario in the Test Plan), F16-minready-timestamp-scope-stateful-too (nit; the
migration row :559 still says stateless-only while stateful `isInstanceAvailable`
reads the same timestamp), F18-kep527-reference-dangling (nit; KEP-527 references
at :248/:403/:422/:430 still resolve to no in-tree KEP; PR #546 still OPEN),
F19-ris-establishment-boundary-existential (minor; :338 unchanged),
F20-ri-prose-vs-edge-table (nit; :410 "Identifying an update target enters
Updating" still unscoped vs the fold edge — new guard
`TestRISummaryProseScopedToFoldRule` green@0b7285b7 / red@39e2ecdf / red@head),
F21-display-priority-dangling-ref (nit; :257 still the only "display priority"
occurrence — new guard red@0b7285b7 / red@head), F17-pr-body-stale-after-pivot
(nit; PR body re-fetched 2026-11-11 still promises "persisted phase transitions
for RBG, RIS, and RI" and now also omits the Accepted-drop / Ready-deprecation /
reason renames), Q5-converged-undefined-at-ri-layer (nit; kind-scope half — new
guard `TestDecisionTableStatesKindScope` red@0b7285b7 / red@head; the serving-row
half improved: the row now enumerates per-kind facts), Q6-progressing-during-
deletion-unspecified (nit; new guard `TestProgressingDuringDeletionSpecified`
red@0b7285b7 / red@head).

New code-factual claims in the delta verified against main-branch code (head ==
base for code, docs-only PR): RI readiness aggregation order (in-place gate
before Pod readiness) and reason set match `instance_status.go`
getInstanceReadyCondition; "external gates are not folded into
`RoleInstanceReady`" matches the code; `roleStatuses` and the RBG-scoped
`GangConfigured` condition exist; RIS code reads no RI `Progressing` today;
`markPodNotReady` exists (instance_status.go:487). The one inaccurate claim is
F22. Deployment's `ReplicaSetUpdated` Progressing reason and the absence of a
Deployment/StatefulSet `Ready` condition are upstream facts relied on by the
rename rationale.

Suite at head: 34 pass / 9 intentional open-finding reds (F13, F16, F18, F19,
F20, F21, F22, Q5, Q6) — results/unit-head-05807351.txt; polarity matrix in
results/polarity-round7.txt; markdownlint replay 0 issues
(results/markdownlint-head-05807351.txt).
