# Verification harness — KEP-527 RoleInstance Lifecycle Hooks (PR #546)

Reviewer: Codex (Reviewer B). Round: incremental #6. Reviewed head: `c3ea6547cba7af45fe5826e162b4c5f4d112ca4b`, base: `3612013e6f779401831f5947dc124cc4313faddb` (merge-base `09accf63`). Previous round heads: `6e8419ab` (r1), `71dd5e87` (r2), `fb6c2b36` (r3), `43dab57b` (r4), `6c15cc92` (r5).

## What kind of PR this is

Documentation-only: the PR adds `keps/527-roleinstance-lifecycle/{README.md, README.zh-CN.md, kep.yaml}` (1,794 lines). No production code changes, so there is no Go/envtest layer to run. The harness is the docs-PR analogue of a unit test:

- **Claims checks (group 1)** — the KEP's factual statements about the *existing* system (RIS lifecycle model, `RoleInstanceCustomReady` + readiness-gate behavior, RI→Pod ownership, absence of an RI finalizer/phase field, admission webhook infra, API group) are asserted against the codebase with grep-level checks. A fail here after a rebase means the codebase moved → Harness-update / re-review the KEP's premises.
- **Finding checks (group 2)** — one check per review finding; `fail` on the reviewed head reproduces the finding, `pass` means the KEP text now addresses it. For design-level findings (F1–F3) the check is a **text proxy**: it proves the KEP *addresses the topic*, not that the added design is correct — that still needs human review.
- **Hygiene checks (group 3)** — markdownlint (the PR's own verification claim), EN/zh-CN section parity, whitespace.

All checks use **contract polarity**: pass = intended state.

## Premise (P0): confirmed

The claimed problem (issue #527: container hooks cannot express instance-level coordination; existing RIS lifecycle is limited to preDelete/inPlaceUpdate with executor-registration markers) is real and matches the codebase. All ten codebase-truth checks pass on the head. Scope divergence vs the issue (4 hooks instead of the requested 5; `preCreate`/`postDelete` dropped, `preRecover` added) is disclosed in the KEP's Drawbacks, and the PR only says `Refs #527` (no auto-close misfire).

## Observed vs expected (head 6e8419ab)

| Finding | Check | Expected (fixed state) | Observed on head | Verdict |
| --- | --- | --- | --- | --- |
| F1 preDelete vs GC cascade | CheckDeletionCascadeSpecified | pass | fail | reproduced (open) |
| F2 phase persistence undefined | CheckPhaseStorageSpecified | pass | fail | reproduced (open) |
| F3 old/new lifecycle coexistence | CheckOldNewCoexistenceRule | pass | fail | reproduced (open) |
| F4 kep.yaml defects (3 checks) | CheckKepYaml{NoSegmentComment,AuthorsSet}, CheckSeeAlsoDirsExist | pass | fail ×3 | reproduced (open) |
| F5 preDelete gate lifetime | CheckPreDeleteGateLifetime | pass | fail | reproduced (open) |
| Codebase-truth claims (10) | group 1 | pass | pass ×10 | KEP accurate |
| Hygiene (3) | group 3 | pass | pass ×3 | clean |

Raw output: `results/check-kep527-head-6e8419ab.tsv`, `results/reverify/unit.tsv`.

Harness-bites proof: the finding checks were re-run with simulated fix text applied (segment comment removed, authors filled, see-also pointed at an existing KEP, cascade-prevention / phase-storage / mutual-exclusion / preDelete-gate sentences added); all 7 flipped fail → pass, then the production files were reverted byte-identical (`git status` clean for `keps/`).


## Observed vs expected (head 71dd5e87 — round 2, incremental)

| Finding | Check | Expected (fixed state) | Observed on head | Verdict |
| --- | --- | --- | --- | --- |
| F1 preDelete vs GC cascade (F-PREDELETE-LIVENESS + F-CREATING-REPLACE-FINALIZER) | CheckDeletionCascadeSpecified | pass | pass | FIXED — design switched to reserved RI finalizer + controller-ordered Pod deletion; GC invariant stated; foreground cascade admission-rejected; per-Pod finalizers dropped (creating-phase release moot) |
| F2 phase persistence (F-PHASE-STORAGE) | CheckPhaseStorageSpecified | pass | pass | FIXED — pinned to KEP-502 `RoleInstance.status.phase` (five values; verified PR #528 defines exactly those); pre-implementable blocker #1 updated |
| F3 old/new coexistence (F-OLDNEW-ISOLATION) | CheckOldNewCoexistenceRule | pass | pass | FIXED — admission-enforced mutual exclusion stated; "sole definition point" scoped to the new protocol |
| F4 kep.yaml defects (F-KEPYAML-NOISE + F-SEEALSO-502) | CheckKepYaml{NoSegmentComment,AuthorsSet}, CheckSeeAlsoDirsExist | pass ×3 | pass ×2, fail ×1 | PARTIAL — Segment comment removed, authors=diw-zw, disable-supported: false; see-also `/keps/502-workload-status-model` still dangles in-repo (mitigated: README now says "KEP-502, currently proposed in PR #528", and PR #528 is OPEN adding exactly that directory) |
| F5 preDelete gate lifetime (F-PREDELETE-GATE-LIFETIME) | CheckPreDeleteGateLifetime | pass | pass | FIXED — "remains until the RI object is deleted; not restored after the delete marker is released" |
| F6 scope rationale (F-SCOPE-PRECREATE) | CheckPreCreateRationaleStated | pass | fail | STILL OPEN — zero preCreate rationale in Motivation/Non-Goals; Drawbacks admits the gap without justification |
| F7 zh/en RIS-scope parity (F-ZH-RIS-RI-PARITY, new nit) | CheckEnZhCoexistenceScopeParity | pass | fail | reproduced — zh-CN :546 says "同一 RI" where en :747 says "same RIS" |
| F8 in-flight admission strength (F-INFLIGHT-ADMISSION-MUST, new nit) | CheckInflightAdmissionMust | pass | fail | reproduced — README.md:362 "Admission should reject" vs "must" in sibling rules and the test plan |
| Codebase-truth claims (10) | group 1 | pass | pass ×10 | KEP accurate on head |
| Hygiene (3) | group 3 | pass | pass ×3 | clean |

Raw output: `results/reverify/unit-71dd5e87.tsv` (round 2), `results/check-kep527-head-6e8419ab.tsv` (round 1).

Round-2 harness-bites: the three new checks (F6–F8) were re-run on head `71dd5e87` with simulated fix text (preCreate rationale bullet in Non-Goals; zh "同一 RIS"; "Admission must reject"); all three flipped fail → pass, then the KEP files were reverted byte-identical (`git status` clean for `keps/`).

Round-2 questions with no runnable layer (reasoning only, no check): Q-propagate-legacy-routing (how the legacy RIS reconciler, which activates on `Spec.Lifecycle != nil` — pkg/reconciler/roleinstanceset/statelessmode/sync/scale.go:169,264, update.go:146-221 — is kept from acting on RBG-propagated new-protocol config in the same field), Q-finalizer-timing (reserved RI finalizer must be established before any deletionTimestamp since the API server forbids adding finalizers afterwards; creation-time addition is only implied by the sequence diagram).


## Observed vs expected (head fb6c2b36 — round 3, incremental)

Delta `71dd5e87..fb6c2b36` (docs-only, +441/-318): the revision **replaces the mutual-exclusion design with wholesale removal of the legacy RIS lifecycle protocol** (`docs: replace legacy RIS lifecycle protocol`), pins normative wire names/constants (`role-instance-hook-state` label, `role-instance-pre-delete` finalizer, `RoleInstanceLifecycleHooks` feature gate, KEP-502 PascalCase phase values), moves the RIS-side config to `spec.roleInstanceTemplate.lifecycle` + `RoleInstance.spec.lifecycle`, and answers every open round-2 question.

| Finding | Check | previous 71dd5e87 | head fb6c2b36 | Verdict |
| --- | --- | --- | --- | --- |
| F1/F2/F5 (deletion invariant, phase storage, gate lifetime) | CheckDeletionCascadeSpecified / CheckPhaseStorageSpecified / CheckPreDeleteGateLifetime | pass | pass | still FIXED (regression guard) |
| F3 old/new coexistence (F-OLDNEW-ISOLATION) | CheckOldNewCoexistenceRule | pass | pass | still FIXED — Harness-update: contract broadened to accept removal language; the redesign eliminates coexistence rather than ruling it |
| F4 kep.yaml defects | CheckKepYaml{NoSegmentComment,AuthorsSet} / CheckSeeAlsoDirsExist | pass/pass/fail | pass/pass/fail | PARTIAL — see-also `/keps/502-workload-status-model` still dangles (PR #528 still OPEN) |
| F6 scope rationale (F-SCOPE-PRECREATE) | CheckPreCreateRationaleStated | fail | pass | FIXED — Non-Goals now states the preCreate/postDelete rationale (EN+zh) |
| F7 zh/en scope parity (F-ZH-RIS-RI-PARITY) | CheckEnZhCoexistenceScopeParity | fail | pass | FIXED — section rewritten |
| F8 in-flight admission must (F-INFLIGHT-ADMISSION-MUST) | CheckInflightAdmissionMust | fail | pass | FIXED — "Admission must reject" |
| F9 propagated-vs-legacy (F-PROPAGATED-VS-LEGACY) | CheckLegacyRemovalStated | fail | pass | FIXED by redesign — removal + admission rejection + distinct fields (`roleInstanceTemplate.lifecycle`, `RoleInstance.spec.lifecycle`) dissolve the same-field ambiguity and the legacy-routing question |
| F10 finalizer bootstrap race (F-FINALIZER-BOOTSTRAP-RACE) | CheckFinalizerEstablishmentOrdering | fail | pass | FIXED — finalizer added before effective config exposure; deletionTimestamp enablement rejected/deferred; new RIs created with the finalizer |
| F11 namespace teardown (F-NS-TEARDOWN-BYPASS) | CheckNamespaceTeardownScoped | fail | pass | FIXED — scoped outside the live-engine guarantee, break-glass pointer (EN+zh) |
| F12 RBGSet foreground boundary (F-RBGSET-FOREGROUND-BOUNDARY) | CheckRBGSetForegroundBoundary | fail | pass | FIXED — RBGSet added to the admission boundary (EN+zh) |
| F13 postReady-omitted path (F-NOPOSTREADY-CLEANUP-CLARIFICATION) | CheckPostReadyOmittedPath | fail | pass | FIXED — explicit completion path + alt/else diagrams + test-plan entry |
| F14 disable-supported (F-DISABLE-SUPPORTED-CONTRADICTION) | CheckDisableSupportedClarified | fail | pass | FIXED — `disable-supported: true`, gate named, PRR clarification sentence |
| F15 wire constants pinned (new AC) | CheckWireConstantsPinned | fail | pass | PASS — hook-state key, finalizer, feature gate consistent across README table and kep.yaml |
| F16 kep.yaml gate shape (NEW nit F-KEPYAML-GATE-SHAPE) | CheckKepYamlFeatureGateShape | pass | fail | OPEN — `feature-gates: [RoleInstanceLifecycleHooks]` bare string vs repo template's object form (`- name:` / `components:`); introduced this round |
| F17 interrupt conditionality (NEW nit F-INTERRUPT-WHEN-DEAD-AMBIGUITY) | CheckInterruptConditionalityClear | fail | fail | OPEN — "Update may interrupt Recovery when the previous engine is dead" reads as a conditional interrupt; sentence present since 71dd5e87, unflagged in round 2 |
| Codebase-truth claims (10) | group 1 | pass ×10 | pass ×10 | KEP accurate on head |
| Hygiene (3) | group 3 | pass ×3 | pass ×3 | clean (markdownlint both configs, section parity 29/29, whitespace) |

Raw output: `results/reverify/unit-fb6c2b36.tsv` (head), `results/reverify/unit-71dd5e87-round3-bites.tsv` (updated harness re-run on the previous head — the fail→pass flips are the round-3 bites evidence).

Round-3 reasoning-only finding (no runnable layer, docs design gap): F-LEGACY-REMOVAL-TRANSITION — the removal redesign leaves three transition points unstated: (a) whether legacy-field rejection is conditioned on the `RoleInstanceLifecycleHooks` gate (gate-off behavior for existing users after controller upgrade), (b) what enforces "migration required before the feature is enabled" when the gate flips on with unmigrated objects, (c) drain/cleanup of legacy runtime state (legacy hook finalizers from `finalizersHandler`, old `role-instance-lifecycle-state` labels) at migration — a controller that "neither writes nor interprets them" never removes a leftover legacy finalizer, wedging RI deletion. Pre-implementable blocker #1 names "the migration procedure" but none of these specifics.


## Observed vs expected (head 43dab57b — round 4, incremental)

Delta `fb6c2b36..43dab57b` (docs-only, +219/-153): the revision **reverts the round-3 removal pivot** — the legacy RIS lifecycle protocol is retained and coexists with the new protocol under admission-enforced mutual exclusion per RIS; the new protocol moves to an **independent hook type** (`labelsHandler` + `markNotReady`; `postReady` has `labelsHandler` only), so the legacy `markPodNotReady` wire name stays with the legacy protocol; in-flight config changes switch from admission-rejected to **declarative latest-wins / absence semantics** (with test-plan coverage); kep.yaml feature-gates adopts the template object form; the interrupt sentence and the zh untranslated-English nits are fixed; the snapshot-ownership sentence returns ("must not be independently edited").

| Finding | Check | base 09accf63 | previous fb6c2b36 | head 43dab57b | Verdict |
| --- | --- | --- | --- | --- | --- |
| F1/F2/F5/F6/F7, F10-F15 (carried fixes) | carried pins | fail | pass | pass | still FIXED (regression guard) |
| F3/F9 old/new boundary (F-OLDNEW-ISOLATION, F-PROPAGATED-VS-LEGACY) | CheckOldNewCoexistenceRule + CheckLegacyCoexistenceSpecified | fail | pass / fail | pass | still FIXED — Harness-update: round-3 removal pin replaced by the round-4 coexistence pin (mutual exclusion + legacy retained + distinct fields/keys, EN+zh); previous-head red is the mechanism change, not a reopened finding |
| F8 in-flight config (F-INFLIGHT-CONFIG-ADMISSION) | CheckInflightSemanticsDefined | fail | fail | pass | still FIXED — Harness-update: admission-rejection pin replaced by the declarative-semantics pin the revision substituted; concern (undefined in-flight behavior) stays addressed |
| F4 kep.yaml defects | CheckKepYaml{NoSegmentComment,AuthorsSet} / CheckSeeAlsoDirsExist | fail×3 | pass/pass/fail | pass/pass/fail | PARTIAL — see-also `/keps/502-workload-status-model` still dangles; PR #528 still OPEN (re-fetched 2026-11-10, head 39e2ecdf) |
| F16 kep.yaml gate shape (F-KEPYAML-GATE-SHAPE) | CheckKepYamlFeatureGateShape | fail | fail | pass | FIXED — object form `- name: RoleInstanceLifecycleHooks` |
| F17 interrupt conditionality (F-INTERRUPT-WHEN-DEAD-AMBIGUITY) | CheckInterruptConditionalityClear | fail | fail | pass | FIXED — "Update may interrupt Recovery; the previous engine may then be dead." |
| F18 snapshot ownership (F-PROPAGATED-SNAPSHOT-EDIT) | CheckSnapshotOwnershipStated | fail | fail | pass | FIXED — "must not be independently edited or combined with the legacy protocol" (EN+zh) |
| F19 zh English terms (F-ZH-ENGLISH-TERMS) | CheckZhTermsTranslated | fail | fail | pass | FIXED — 'source of truth'/'desired snapshot' translated |
| F20 naming split (F-MARKNOTREADY-NAME, round-4 form) | CheckMarkNotReadyNaming | fail | fail | pass | still FIXED — new protocol `markNotReady`, legacy `markPodNotReady` confined to legacy-context sentences (EN: 2/2, zh: 2/2); matches code (`MarkNotReady` Go field / `markPodNotReady` wire at roleinstanceset_types.go:96-104) |
| Codebase-truth claims (10) | group 1 | pass ×10 | pass ×10 | pass ×10 | KEP accurate on head (incl. "legacy protocol executed by the RIS controller" — pkg/reconciler/roleinstanceset/statelessmode/sync/{scale,update}.go) |
| Hygiene (3) | group 3 | pass ×3 | pass ×3 | pass ×3 | clean (markdownlint 0 issues, section parity, whitespace) |

Raw output: `results/reverify/unit-43dab57b.tsv` (head), `unit-fb6c2b36-round4.tsv` (previous head, updated harness), `unit-09accf63-round4.tsv` (base control).

Round-4 reasoning-only closures (no runnable layer on this PR's tree):

- **F-LEGACY-REMOVAL-TRANSITION — dissolved by the pivot-back.** All four sub-questions from round 3 are moot: gate-off behavior is the status quo (legacy controller unchanged), no migrate-before-enable enforcement is needed (the legacy field keeps working), legacy runtime state drains via the still-running legacy controller, and the divergence from issue #527's "Remain compatible with existing lifecycle and readiness gate mechanisms" disappears (Goal: "Preserve the legacy RIS lifecycle protocol"; Non-Goal: mutual exclusion; migration deferred to a **post-Alpha** path named in pre-implementable blocker #1).
- **F-KEP502-CREATING-PHASE-CONFLICT — FIXED on the dependency side.** PR #528 head 39e2ecdf (fetched 2026-11-10) now prescribes "`Creating` | update/recovery target identified before initial creation completes | **Remain `Creating`; fold the new target into Create convergence without invoking hooks**" and adds an explicit cross-reference ("consistent with the KEP-527 hook rules"; "This matches KEP-527"). KEP-527's own Creating row is unchanged. Not pinnable in this harness: keps/502 is absent from this PR's tree until #528 merges.

Round-4 design note (reasoning only, surfaced as question F-CONFIG-REMOVAL-SKIPS-HOOK): the new declarative semantics make removing a hook configuration mid-operation a supported skip path — including `preDelete` during deletion ("removes the reserved finalizer so deletion may continue. This is the direct result of the user removing the hook"). Round 3 explicitly forbade this ("must not be interpreted as marker release … or permission to skip the hook"). The reversal is stated plainly and is test-plan-covered, so it is a legitimate design choice; but the Risks/Troubleshooting sections never acknowledge that config removal now doubles as a hook-bypass (the break-glass section still covers only marker removal). One sentence would close it.

## Observed vs expected (head 6c15cc92 — round 5, incremental)

Delta `43dab57b..6c15cc92` (docs-only, +276/-219, EN+zh only; kep.yaml untouched): a wording/consistency revision — configuration authority reframed as **object ownership** (RBG-managed: `roles[].lifecycle`; standalone: `roleInstanceTemplate.lifecycle`), with an explicit overwrite rule for direct edits on RBG-owned RIS snapshots; the "user-configured" leg of the mutual-exclusion admission check is now **defined** (any non-empty legacy `spec.lifecycle` the RBG controller does not own counts as user configuration); absence semantics strengthened (hook removal **follows the released-hook transition**, e.g. `PreUpdate`→`Updating`); the stale round-3 integration-test bullet is rewritten to match; the marker-key admission rule is **strengthened** (reject reserved `rbg.workloads.x-k8s.io/` prefix, not just the exact hook-state key); preDelete executor must be **independently deployed**; enablement on a deleting RI clarified (hook never exposed, deletion continues without it); gate-off behavior stated (fields rejected while the gate is disabled); field-ownership paragraph added; normative "must" softened to declarative in several places.

| Finding | Check | base 09accf63 | previous 43dab57b | head 6c15cc92 | Verdict |
| --- | --- | --- | --- | --- | --- |
| F1-F3, F5-F8, F10, F12-F17, F19, F20 (carried fixes) | carried pins | fail | pass | pass | still FIXED (regression guard) |
| F9 old/new boundary (F-OLDNEW-ISOLATION / F-PROPAGATED-VS-LEGACY) | CheckLegacyCoexistenceSpecified | fail | fail* | pass | still FIXED — Harness-update: zh Summary reworded ("…不会同时激活二者"); zh Non-Goal form unchanged and now pinned alongside. *previous-head red is the round-5 wording change, not a reopened finding |
| F11 namespace teardown (F-NS-TEARDOWN-BYPASS) | CheckNamespaceTeardownScoped | fail | pass | pass | still FIXED — Harness-update: EN sentence re-wrapped; pin now wrap-tolerant |
| F18 snapshot ownership (F-PROPAGATED-SNAPSHOT-EDIT) | CheckSnapshotOwnershipStated | fail | fail* | pass | still FIXED — Harness-update: clause reworded + strengthened ("is not independently editable" + "overwritten on the next RBG reconcile"; the not-combined-with-legacy half is now carried by F30) |
| F21 gate-off behavior (F-COEXIST-GATE-EDGE, r4 question) | CheckGateOffBehaviorStated | fail | fail | pass | CORE FIXED — "The gate defaults to false. While it is disabled, the new protocol fields are rejected and the legacy RIS lifecycle protocol retains its existing behavior" (EN+zh). Dual-config enablement is answered transitively (fields rejected while off; both-non-empty rejected while on). Residual still open: legacy `inPlaceUpdate` deprecation timeline ("post-Alpha migration path" only) |
| F22 absence test-plan (F-TESTPLAN-ABSENCE-CONTRADICTION, r4 nit) | CheckAbsenceTestplanConsistent | fail | fail | pass | FIXED — integration bullet rewritten to the absence semantics (EN+zh); stale round-3 bullet pinned absent |
| F23 stale-label disposition (F-STALE-LABEL-DISPOSITION, r4 nit) | CheckStaleLabelDispositionSingle | fail | fail | pass | FIXED — single "removes" strength for hook removal (:454/:701/:993 + zh); markerA→markerB reconfiguration deliberately keeps the old marker — exactly the distinction the finding suggested |
| F24 prefix-rule test coverage (NEW nit F-TESTPLAN-PREFIX-COVERAGE) | CheckTestplanPrefixCoverage | vacuous pass | vacuous pass | **fail** | OPEN — design (:437-441 EN, :331-333 zh) now rejects reserved-prefix handler keys, but the unit-test bullet (EN :938-940, zh :696-698) still covers only the exact-key collision. Bites-verified: simulated fix flips fail→pass |
| F25 removal-risk acknowledgment (F-CONFIG-REMOVAL-SKIPS-HOOK, r4 question) | CheckRemovalRiskAcknowledged | fail | fail | **fail** | STILL OPEN, strengthened — the skip path is now explicit ("follows the same transition as a released hook; for example, PreUpdate advances to Updating", :453-456), Risks/Troubleshooting still carry no acknowledgment, and the adjacent "forcibly removing markers is not a routine recovery procedure" caution was deleted this round (EN+zh). Bites-verified: simulated Risks row flips fail→pass |
| F26-F30 round-5 content guards | CheckPreDeleteExecutorIndependent / CheckDeletingRINeverExposesHook / CheckReservedPrefixRejected / CheckFieldOwnershipExplicit / CheckUserConfiguredClarified | fail ×5 | fail ×5 | pass ×5 | new normative content pinned (regression guards for future revisions) |
| F4 see-also residue (F-SEEALSO-502) | CheckSeeAlsoDirsExist | fail | fail | **fail** | STILL OPEN (5th round) — kep.yaml untouched; `/keps/502-workload-status-model` absent from head tree and origin/main; PR #528 still OPEN (re-fetched 2026-11-11, NEW head `05807351`) and still adds exactly that directory |
| Codebase-truth claims (10) | group 1 | pass ×10 | pass ×10 | pass ×10 | KEP accurate on head |
| Hygiene (3) | group 3 | pass ×3 | pass ×3 | pass ×3 | clean (markdownlint repo+default configs 0 issues, section parity 42/42, `git diff --check` clean) |

Raw output: `results/reverify/unit.tsv` (head, persisted by re-verify.sh), `results/reverify/unit-6c15cc92-round5.tsv` (head), `unit-43dab57b-round5.tsv` (previous head, updated harness), `unit-09accf63-round5.tsv` (base control).

Round-5 reasoning-only closures (no runnable layer on this PR's tree):

- **F-KEP502-RECOVERING-GATE-CIRCULARITY (r4 question) — FIXED on the dependency side.** PR #528's new head `05807351` (fetched 2026-11-11) rewrote the exit rules: "To leave `Recovering`, … and readyPolicy must be satisfied" (no longer "and configured readiness gates"), the `Updating` exit likewise switched to "controller-observed readiness conditions", and a new paragraph states "`RoleInstanceCustomReady` and other externally owned readiness gates do not gate phase transitions … This … removes the circular wait with KEP-527: the lifecycle-owned `RoleInstanceCustomReady` contribution established by `markPodNotReady` remains until the RI enters `Running`, but it does not prevent the RI from entering `Running`." — exactly the carve-out the finding asked for. Residuals: #528 is still OPEN (the fix could drift); the amendment says `markPodNotReady` where KEP-527's new-protocol field is `markNotReady` (cosmetic, on #528's side; the mechanism reference via `RoleInstanceCustomReady` is name-agnostic). Pre-implementable blocker #1 still couples the two KEPs.
- **F-KEP502-CREATING-PHASE-CONFLICT stays FIXED** — #528's Creating fold row is unchanged at `05807351`.

Round-5 review notes (reasoning only): the multi-label AND-absence sentence deleted from "Shared Markers" is redundant with :323-326/:422 (still stated twice); the deleted Version-Skew sentence survives in the PRR ("A version that does not understand the current protocol does not take over an unfinished operation"); the trigger-table `postReady` cell now reads "Already `Running`" (EN) while zh keeps the extra state-cleanup clause — EN prose step 5 covers it, no information loss at document level.

## How to run

```bash
# against the checked-out ref (must contain keps/527-roleinstance-lifecycle):
bash docs/verification/kep-527-roleinstance-lifecycle/scripts/check-kep527.sh

# against the current PR head, auto-fetched (from a checkout of THIS branch):
bash docs/verification/kep-527-roleinstance-lifecycle/scripts/re-verify.sh
```

## Continuing after a revision

1. Fetch + check out this branch, run `re-verify.sh` (no args) — it fetches the
   current PR head, grafts the harness, prints per-finding Fixed/Still-broken.
2. Review the delta printed as `last-reviewed..head`, then advance the marker:
   `echo <head-sha> > docs/verification/kep-527-roleinstance-lifecycle/.last-reviewed`
   and commit + push this branch.
3. A group-1 fail after a rebase is a Harness-update: the codebase moved; adjust
   the check and re-assess the KEP claim it backs.

Kickoff prompt for a fresh agent: "Check out verify/kep-527-roleinstance-lifecycle-codex from the cheyang/rbg fork, run docs/verification/kep-527-roleinstance-lifecycle/scripts/re-verify.sh, review the printed delta of PR sgl-project/rbg#546, then update the README table and .last-reviewed."

## Proposed fixes (summary)

- F1: specify how member Pods survive until `preDelete` completes (orphan Pods / remove ownerReferences at RI deletion start, controller-ordered Pod deletion), and correct the risk-table mitigation ("add finalizer" retains the API object only — kubelet still terminates containers once deletionTimestamp is set).
- F2: state where the top-level phase is persisted (new RI label or status field); restart recovery reconstructs from it.
- F3: state the coexistence rule between `RoleInstanceSetSpec.lifecycle` (existing) and the proposed Role-level lifecycle (mutual exclusion / precedence / deprecation plan) — Open Question 1 made concrete.
- F4: remove the "Segment" comment from kep.yaml, fill authors/reviewers, fix or drop the dangling `/keps/502-workload-status-model` see-also.
- F5: define the lifetime of the `preDelete.markNotReady` gate.

## Observed vs expected (head c3ea6547 — round 6, incremental)

Delta `6c15cc92..c3ea6547` (docs-only, +322/-256, EN+zh only; kep.yaml untouched): a **design revision**, not a wording pass —

- `labelsHandler` renamed to `signalLabels` across API text, examples, wire table, and test plan (EN+zh).
- **`markNotReady` and the entire Ready Gating section removed**: hooks never participate in RI Ready computation; new Non-Goal ("Providing a readiness-gating field or changing RI Ready"); all four sequence diagrams drop the NotReady-gate steps; the `RoleInstanceCustomReady` wire-table row is gone.
- **New "Signal Label Propagation to Pods"**: the RI controller mirrors `signalLabels` keys onto member Pods (unidirectional, metadata-only, no revision/recreate, batched per RI reconcile); declaring a signal key in Pod template labels is admission-rejected.
- **New marker-key record**: annotation `rbg.workloads.x-k8s.io/role-instance-hook-marker-keys` (Go const `RoleInstanceHookMarkerKeysAnnotation`), written atomically with the marker, a key removed only after the RI label AND its Pod mirrors are confirmed absent; rollback precondition now includes the record.
- **New admission rule**: marker keys used by the owning RIS `spec.selector` (`matchLabels` or any `matchExpressions` entry, incl. `DoesNotExist`-only) are rejected — rationale: a marker write can unmatch the selector and the stateful RIS instance-reference logic then releases the RI's owner reference (resolves advisory F-C4528290874).
- **Foreground-delete webhook resolution is now fail-closed** on cache miss (resolves advisory F-C4528291011).
- Advisory F-C4528290945 dissolved by the gate removal (no gate => no stateful-readiness divergence).
- Stale-label cleanup now routes through the record and also cleans Pod mirrors (F23 sub-pin re-worded, substance unchanged).

| Finding | Check | base 3612013e | previous 6c15cc92 | head c3ea6547 | Verdict |
| --- | --- | --- | --- | --- | --- |
| F1-F3, F6-F19, F21-F22, F26-F30 (carried fixes) | carried pins | fail | pass | pass | still FIXED (regression guard) |
| F5 gate lifetime (F-PREDELETE-GATE-LIFETIME) | CheckReadyGatingRemoved | fail | pass* | pass | still FIXED — RESOLVED BY DESIGN REMOVAL: markNotReady / Ready Gating deleted wholesale; no gate whose lifetime needs stating. *previous=pass under the round-5 contract; Harness-update: CheckPreDeleteGateLifetime superseded (kept, fails by design) |
| F20 field naming (F-MARKNOTREADY-NAME) | CheckSignalLabelsOnlyHookField | fail | pass* | pass | still FIXED — RESOLVED BY DESIGN REMOVAL: markNotReady gone; contract now pins `signalLabels` as the only hook field (EN+zh) and markPodNotReady confined to legacy contexts. *previous=pass under the round-5 contract; Harness-update: CheckMarkNotReadyNaming superseded |
| F23 stale-label disposition (F-STALE-LABEL-DISPOSITION) | CheckStaleLabelDispositionSingle | fail | fail* | pass | still FIXED — Harness-update: disable-flow sub-pin re-worded to the marker-key-record form ("removes stale runtime labels and their Pod mirrors identified through the marker-key record" + zh); *previous-head red is the re-pin, not a reopened finding (round-5 wording passed its own pin) |
| F24 prefix-rule test coverage (F-TESTPLAN-PREFIX-COVERAGE) | CheckTestplanPrefixCoverage | vacuous pass | vacuous pass | **fail** | STILL OPEN (2nd round) — unit-test bullet still covers only the exact-key collision, not the reserved prefix (EN unit-test bullets, zh mirror) |
| F25 removal-risk acknowledgment (F-CONFIG-REMOVAL-SKIPS-HOOK) | CheckRemovalRiskAcknowledged | fail | fail | **fail** | STILL OPEN (3rd round) — Risks/Troubleshooting still carry no row for config-removal-as-skip (mid-deletion preDelete removal force-releases the barrier); the round-6 text keeps the released-hook transition |
| F31 Pod propagation guard | CheckPodPropagationSpecified | fail | fail | pass | new normative content pinned (EN+zh) |
| F32 marker-key record (F-C4528291076) | CheckMarkerKeyRecordPinned | fail | fail | pass | advisory FIXED — durable record specified and pinned (EN+zh) |
| F33 selector admission (F-C4528290874) | CheckSelectorAdmissionStated | fail | fail | pass | advisory FIXED — selector-collision rejection incl. DoesNotExist + owner-reference-release rationale (EN+zh) |
| F34 fail-closed webhook (F-C4528291011) | CheckFailClosedCacheMiss | fail | fail | pass | advisory FIXED — "Resolution is fail-closed … A cache miss must not establish that deletion is safe" (EN+zh) |
| F35 zh rollout-risk parity (NEW minor F-ZH-EN-ROLLOUT-RISK-PARITY) | CheckZhEnRolloutRiskParity | vacuous pass | pass | **fail** | OPEN — EN dropped "circular waits involving Ready" from the rollout-risk list with the gate removal; zh still lists "Ready 循环等待" (:865). Bites-verified: deleting the zh clause flips fail→pass |
| F36 record ownership (NEW minor F-MARKERKEY-RECORD-OWNERSHIP) | CheckMarkerKeyRecordOwnership | vacuous pass | vacuous pass | **fail** | OPEN — the field-ownership enumeration (EN :849-853, zh :647-649) lists phase/hook-state/snapshot/finalizer/Pod mirrors but NOT the marker-key record annotation, and no rule bars users/metadata-sync/executors from editing it; rollback verification + stale-label attribution depend on its integrity. Bites-verified |
| F37 scalability mirror cost (NEW nit F-SCALABILITY-POD-MIRROR) | CheckScalabilityPodMirrorCost | vacuous pass | vacuous pass | **fail** | OPEN — Scalability (:1221 EN, :888 zh) omits the Pod-mirror fan-out (1 RI write + N Pod writes per signal transition, re-synced every RI reconcile), the dominant new write cost for the multi-Pod RIs the KEP targets. Bites-verified |
| F38 rollback-precondition shorthand (NEW nit F-ROLLBACK-PRECONDITION-SHORTHAND) | CheckRollbackPreconditionComplete | vacuous pass | vacuous pass | **fail** | OPEN — "The rollback precondition is an absent hook-state label together with an empty or absent marker-key record" (:474-476 EN, :357 zh) reads complete but omits the reserved-finalizer condition that Upgrade/Downgrade (:1134-1141) carries. Bites-verified |
| F39 webhook defer wording (NEW nit F-WEBHOOK-DEFER-WORDING) | CheckWebhookDeferralNotClaimed | vacuous pass | vacuous pass | **fail** | OPEN — "rejects or defers the request" (:732-735 EN; "应拒绝或暂缓" zh :568-570 + test-plan :774): admission webhooks cannot defer, denial is the only fail-closed mechanism. Bites-verified |
| F4 see-also residue (F-SEEALSO-502) | CheckSeeAlsoDirsExist | fail | fail | **fail** | STILL OPEN (6th round) — kep.yaml untouched; `/keps/502-workload-status-model` absent from head tree and origin/main; PR #528 still OPEN (re-fetched 2026-11-12, NEW head `9862499f`, retains the KEP-527 circular-wait carve-out and the keps/502 directory) |
| Codebase-truth claims (10) | group 1 | pass ×10 | pass ×10 | pass ×10 | KEP accurate on head |
| Hygiene (3) | group 3 | pass ×3 | pass ×3 | pass ×3 | clean (markdownlint repo config 0 issues on both files, section parity 29/29, `git diff --check` clean) |

Raw output: `results/reverify/unit.tsv` (head, persisted by re-verify.sh), `results/reverify/unit-c3ea6547-round6.tsv` (head), `unit-6c15cc92-round6.tsv` (previous head, updated harness), `unit-3612013e-round6.tsv` (base control).

## Observed vs expected (head d038c23b — round 7, incremental)

Delta `c3ea6547..d038c23b` (docs-only, single commit "docs: address round-6
review findings in KEP-527", +127/-60, EN+zh only; kep.yaml untouched): the
author addressed essentially every open round-6 finding —

- **zh rollout-risk parity restored** ("Ready 循环等待" dropped) — F35 fixed.
- **Field ownership now covers the marker-key record** ("the controller owns
  ... the marker-key record ..."; "Users, metadata synchronization, and
  executors must not write the marker-key record", EN+zh) — F36 fixed.
- **Rollback precondition completed** with the reserved-finalizer condition
  (design text + test plan, EN+zh) — F38 fixed.
- **Foreground-delete redesigned**: the fail-closed cache-miss rule was
  REPLACED by unconditional rejection — while the gate is enabled, admission
  rejects every `Foreground` propagation at RBGSet/RBG/RIS/RI entries, with NO
  informer-cache dependence ("neither a cache miss nor a successful lookup can
  prove that no reachable RI has preDelete in effect"); the "rejects or
  defers" wording is gone; test-plan bullet updated (EN+zh). Disclosed
  mechanism replacement, strictly stronger — F34 pin superseded
  (CheckForegroundRejectUnconditional), F39 fixed, advisory F-C4541237496
  (informer cache-hit staleness) resolved by the redesign.
- **Mirrors-are-not-authenticated rule** added to propagation (EN+zh) — the
  Pod-mirror spoof question (F-POD-MIRROR-SPOOF-WINDOW) answered.
- **Selector reverse rule**: admission rejects an owning RIS selector that
  uses a controller-reserved key (EN+zh) — F-SELECTOR-RESERVED-KEY-REVERSE
  answered.
- **Cross-generation key uniqueness**: a signal key may not be reassigned to
  another hook while any governed RI still records it (EN+zh) — advisory
  F-C4541237385 (postReady->preDelete key reassignment) resolved.
- **KEP-502 drift disclosed**: "The phase model is defined by KEP-502, now
  merged" — externally verified: PR #528 MERGED 2026-11-12 (merge commit
  c2896cc0); merged keps/502-workload-status-model still references
  markPodNotReady as a KEP-527 gate (origin/main README.md:413, :485). The
  KEP requires a KEP-502 follow-up amendment removing the stale references
  before implementation (pre-implementable item #1, EN+zh) —
  F-KEP502-GATE-REFERENCE-DRIFT fixed on this PR's side.
- **Gate edges stated** (EN+zh): gate-on applies mutual exclusion to
  subsequent writes (dual-configured objects must be corrected before their
  next update); gate-off leaves hook-state/markers/record for a compatible
  controller; legacy inPlaceUpdate supported until a deprecation release is
  announced — F-COEXIST-GATE-EDGE fixed.
- **Removal-skip risk acknowledged**: risk-table row + Risks bullet
  ("Removing an active hook configuration mid-operation skips its remaining
  barrier. The preDelete release is one-way ...", EN+zh) —
  F-CONFIG-REMOVAL-SKIPS-HOOK fixed.
- **Prefix-rule test coverage** landed in the unit-test bullet (EN+zh) —
  F24 fixed (Harness-update: pin regex now spans the dotted qualified name).
- **Scalability** now accounts for the mirror fan-out (EN+zh) — F37 fixed.
- F-SEEALSO-502: kep.yaml untouched, but PR #528 merged first — the
  see-also target keps/502-workload-status-model now EXISTS on origin/main.
  The pin still fails on the PR head tree (it predates that merge); the
  reference resolves on main post-merge. Externally resolved.

| Finding | Check | base 3612013e | previous c3ea6547 | head d038c23b | Verdict |
| --- | --- | --- | --- | --- | --- |
| F1-F3, F6-F19, F21-F23, F26-F30 (carried fixes) | carried pins | — | pass | pass | still FIXED (regression guard) |
| F5, F20 (round-6 forms) | CheckReadyGatingRemoved / CheckSignalLabelsOnlyHookField | fail | pass | pass | still FIXED; F20 Harness-update (markPodNotReady now also allowed in KEP-502-drift disclosure sentences) |
| F12 foreground boundary | CheckRBGSetForegroundBoundary | fail | pass (old pin) | pass | still FIXED; Harness-update to the new unconditional wording (substance strengthened) |
| F24 prefix coverage | CheckTestplanPrefixCoverage | vacuous pass | fail | pass | FIXED |
| F25 removal-risk ack | CheckRemovalRiskAcknowledged | fail | fail | pass | FIXED |
| F34 fail-closed -> unconditional | CheckForegroundRejectUnconditional | fail | fail | pass | FIXED BY REDESIGN (CheckFailClosedCacheMiss superseded, fails by design) |
| F35-F39 round-6 opens | round-6 pins | vacuous pass | fail | pass | ALL FIXED |
| F40 gate edges (F-COEXIST-GATE-EDGE) | CheckGateEdgesStated | fail | fail | pass | FIXED |
| F41 cross-generation uniqueness (F-C4541237385) | CheckCrossGenerationKeyUniqueness | fail | fail | pass | FIXED (advisory confirmed-then-fixed) |
| F42 mirrors not authenticated (F-POD-MIRROR-SPOOF-WINDOW) | CheckMirrorNotAuthenticated | fail | fail | pass | FIXED |
| F43 selector reverse rule (F-SELECTOR-RESERVED-KEY-REVERSE) | CheckSelectorReservedKeyReverse | fail | fail | pass | FIXED |
| F44 KEP-502 drift disclosure (F-KEP502-GATE-REFERENCE-DRIFT) | CheckKep502DriftDisclosed | fail | fail | pass | FIXED (external facts gh-verified) |
| F45 affected-ancestor wording (NEW nit) | CheckAffectedAncestorRetired | vacuous pass | vacuous pass | **fail** | OPEN — integration bullet (EN :1083, zh :804) still says "at every affected ancestor" — a reachability term whose definition the redesign removed; align with the unconditional rule |
| F46 foreground scope broadened (NEW question) | CheckForegroundScopeAcknowledged | vacuous pass | vacuous pass | **fail** | OPEN — the unconditional rejection also covers RBGSet/RBG/RIS/RI objects with NO hooks configured while the gate is enabled; the KEP never acknowledges the broadened blast radius (clients retry with background) |
| F4 see-also residue (F-SEEALSO-502) | CheckSeeAlsoDirsExist | fail | fail | fail (PR tree) | FIXED EXTERNALLY — target dir now on origin/main (PR #528 merged); pin red only because the PR tree predates that merge |
| Codebase-truth claims (10) | group 1 | pass x10 | pass x10 | pass x10 | KEP accurate on head |
| Hygiene (3) | group 3 | pass x3 | pass x3 | pass x3 | clean (markdownlint 0 issues, section parity, whitespace) |

Bites verification (round 7): mutating/deleting the pinned sentences at head
flips CheckForegroundRejectUnconditional, CheckGateEdgesStated,
CheckCrossGenerationKeyUniqueness, CheckMirrorNotAuthenticated,
CheckSelectorReservedKeyReverse, CheckKep502DriftDisclosed, and
CheckRBGSetForegroundBoundary to FAIL; simulating the fixes flips
CheckAffectedAncestorRetired and CheckForegroundScopeAcknowledged to PASS.
Tree restored byte-identical (git checkout -f d038c23b -- keps/).

Raw output: `results/reverify/unit.tsv` (head, persisted by re-verify.sh),
`results/reverify/unit-d038c23b-round7.tsv` (head), `unit-c3ea6547-round7.tsv`
(previous head, updated harness), `unit-3612013e-round7.tsv` (base control),
`unit-bites-round7.tsv` (mutation run).
