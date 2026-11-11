# Verification — KEP-502 workload status model (PR #528) — Reviewer A (Claude)

- PR: https://github.com/sgl-project/rbg/pull/528
- Round 8 reviewed head: `9862499f722089a3e72f2326e65db5e872bfeee9` (round 7: `058073514731f8d2ebfeffe68a51b587102ab110`; round 6: `39e2ecdf0d8f6a3656923ab33b8391dd7ab88fd1`; round 5: `0b7285b7c2ad23f139f0edb8549a316129e9b4a2`; round 4: `da818779ef984be98467d4ad65d0818bb17aa0e6`; round 3: `f622d642ac10326f66554b590e0513734aa9249a`; round 2: `6335eac61b7ef71d7479a8b2686829d7e59ce3e1`; round 1: `f05e1dd83d2691e4157bf1bd3548fe405b2a49d5`; base `885d655bd1b4c26e6d14837709eeda675f3ae886`)
- Harness branch: `verify/502-workload-status-model-claude` (pushed to the reviewer's fork `cheyang/rbg`)
- This is a **docs-only PR** (adds `keps/502-workload-status-model/{README.md,kep.yaml}`); the code
  under review is a design document, so the harness checks the *document's* contract: its factual
  claims about the current API, and the internal consistency of its machine-readable reason taxonomy.
- Live layer (L3): skipped — debate setup runs unit + integration only, and a KEP has no runtime.

## Round 8 (delta `0580735..9862499`) — gang + creation-reason observations; 5 ledger items fixed, 1 new minor (F24), 10 open

The author pushed one docs commit (+9/−7, README only): `9862499f "docs: define gang and
creation reason observations"`. Design changes: (a) `WaitingForGang` is re-derived from **runtime
gang-scheduling state** (PodGroup phase, member scheduling status) instead of "gang state owned at
RBG scope (today the `GangConfigured` condition)", with a new paragraph scoping `GangConfigured`
to configuration compatibility only; (b) own CR-operation errors are classified **with the same
family rules** (`UnclassifiedBlocker` reserved for unclassifiable errors) instead of "directly as
`UnclassifiedBlocker`"; (c) the RBG `Creating` boundary moves from the collective "initial RBG
establishment" to a **per-child durable-evidence discriminator**, with a restart tie-break
(no evidence → `Creating` + "replacement unconfirmed" message) and a test-plan bullet; (d) the
events test bullet gains the "by itself" qualifier plus a confirmed-blocker clause.

Fixed this round (all guarded, all bite-checked — `results/bite-check-round8.txt`):

| Finding | Check | At 0580735 (prev) | At 9862499 (head) | Verdict |
| --- | --- | --- | --- | --- |
| F22 own-CR blocker reason contradiction (minor) | **C26** | fail | **pass** | **FIXED** — the categorical "directly as `UnclassifiedBlocker`" sentence is gone; taxonomy paragraph, provenance table, and RIS section now all prescribe "classifies with the same rules; `UnclassifiedBlocker` when the family cannot be determined" |
| F19 RIS `Creating` boundary existential vs RBG collective (minor) | **C13/C16 (re-anchored, dual-anchor)** | pass | **pass / pass** | **FIXED** — the RBG boundary is now the same *kind* of evidence-based discriminator as RIS (per-child durable evidence vs established revision/RIs), and the restart tie-break defines the previously underdetermined zero-replica case. First-run "STILL-BROKEN" was anchor staleness (Harness-update), not a regression: substance holds at both heads (F12 verification: base not-run, previous pass, head pass) |
| C4528984285 `GangConfigured` cannot distinguish waiting (Copilot, nit) | **C29 (new)** | fail | **pass** | **FIXED** — `WaitingForGang` derived from runtime gang facts; code claim verified: `setGangConfiguredCondition` sets True (Reason `GangConfigured`) on config success regardless of Pod scheduling, False (`IncompatibleGangConfig`) on error, removed when gang is off (`results/round8-code-claims.txt`) |
| C4528984415 restart establishment evidence (Copilot, nit) | **C30 (new)** | fail | **pass** | **FIXED (determinism half)** — no-evidence → `Creating`, message notes replacement unconfirmed, test bullet pins both arms; the evidence-base half is the new F24 |
| C4528984469 events-vs-blockers conflict (Copilot, nit) | **C31 (new)** | fail | **pass** | **FIXED** — "a *single* failure ... does not *by itself* change the aggregated reason" + "a confirmed current blocker changes the reason through the documented classification and propagation rules" |

New open item this round:

1. **F24 [minor, introduced by 9862499], README.md:217 area** — the new `Recovering` rule
   "requires durable evidence of prior establishment, such as persisted role status or revision
   history in the RBG status", but (a) `RoleBasedGroupStatus` has **no revision-history field**
   (observedGeneration, conditions, roleStatuses only — `rolebasedgroup_types.go:522-534`), and
   (b) "persisted role status" is **zeroed on child deletion**: `ConstructRoleStatus` returns a
   zero-counter `RoleStatus` on NotFound (`roleinstanceset_reconciler.go:510-513`) and
   `updateRBGStatus` overwrites the entry when counters change
   (`rolebasedgroup_controller.go:814-824`), so a 3/3 entry becomes 0/0 on the first reconcile
   after deletion — and 0/0 is exactly what a never-established child shows, so the doc's own
   canonical example cannot be distinguished. The KEP neither states a preservation rule for
   roleStatuses entries of missing children nor specifies the new status content, so the
   "with durable evidence → `Recovering`" arm of the new test bullet is unreachable with current
   fields. C32 (new): red at head, green under the proposed fix.

Carried open (all nit except F24): F13 (C10 part 4 — the Creating-branch concurrency bullet is
still absent), F16 (C21 — minReadySeconds migration row still stateless-only), F17 (PR body still
promises "persisted phase transitions for RBG, RIS, and RI" — re-confirmed stale at the live PR
body this round), F18-kep527 (C34, new guard — five KEP-527 references, no `keps/527-*` in tree,
upstream PR #546 still OPEN at 2026-11-12, no pending markers), F20 (C19 — RI summary prose still
unscoped), F21 (C25 — display-priority reference still dangling), F22-ris-readiness (C33, new
guard — the unscoped "Today's RIS code reads only `RoleInstanceReady`" is still present at :338;
stateless `IsRunningAndReady` also reads `spec.readinessGates`), F23 (C27 — the :430
"Readiness gates only `Creating → Running`" sentence still contradicts the phase/gate separation),
Q5 (C22), Q6 (C24).

Full re-verify at head (`results/reverify-head-9862499.txt`): 17 FIXED, 10 open (F13, F16, F20,
F21, F23, F24, F22-ris-readiness, F18-kep527, Q5, Q6 — plus unguarded F17 confirmed open by
direct PR-body read). F24 is the only minor; everything else is nit. No blockers, no majors.
Code-claim verification for the delta: `results/round8-code-claims.txt` (PodGroup + GangConfigured
claims accurate; durable-evidence claim inaccurate → F24; RIS reads-only claim unchanged-inaccurate).
markdownlint: 0 issues at head (C7 green). CI at head: 9 SUCCESS / 6 SKIPPED / 0 failure
(e2e groups legitimately skip for the docs-only diff), observed live 2026-11-12.

## Round 7 (delta `39e2ecd..0580735`) — second redesign (drop Accepted, deprecate RBG Ready, rename reasons); 4 flips were Harness-updates, all now fixed; 2 new open items

The author pushed ten docs commits (+120/−126, README only): `d0c9266e` (phase vs external gates),
`71823db3` (unclassified family), `5258477a` (deprecate RBG Ready), `65b92ea1` (drop Accepted),
`8ce83ba7` (rename RBG activity reason), `fc85dad1` (reason provenance), `4726de68` (RIS update
reason strategy-agnostic), `6a7c3b14`/`09b9edb6`/`05807351` (wording).

Design changes: RBG **drops the `Accepted` condition** (configuration incompatibility becomes a
`Progressing=True` configuration-family blocker ranked first); the RBG **`Ready` condition is
deprecated** (removed after a compatibility window; readiness = roleStatuses + RIS counters + RI
`RoleInstanceReady`); RBG update/scale reason renamed `ScalingOrUpdating` → **`RoleInstanceSetUpdated`**,
RIS `RollingUpdate` → **`RoleInstanceUpdated`** (strategy-agnostic, modeled on Deployment's
`ReplicaSetUpdated`); a new **unclassified blocker family** (`PodOperationUnclassified` at RI,
`UnclassifiedBlocker` at RIS/RBG) replaces the old retain-Activity-reason rule; a **reason-provenance
table** fixes the lowest observer for every RBG reason (dependencies/gang/config are RBG-owned;
RIS must newly read RI `Progressing`); and **RI phase completion is separated from external readiness
gates** (controller-observed workload readiness only).

The naive first re-run showed 9 red vs round 6's 6 — but four of those flips were stale anchors
(the redesign renamed their guarded text), not regressions. After re-anchoring, all four are
**genuinely fixed by the redesign**:

| Finding | Check | At 39e2ecd (old anchor) | At 0580735 (re-anchored) | Verdict |
| --- | --- | --- | --- | --- |
| F4 RoleInstanceReady reasons vs code (minor) | **C4 (re-anchored)** | pass (old anchor) | **pass** | **FIXED (Harness-update)** — new sentence "RoleInstanceReady reasons are Ready, PodsNotReady, InplaceUpdateNotReady, and ReadinessGatesNotReady when a configured gate condition is missing" matches `instance_status.go` `instanceReady.Reason` writes exactly |
| F11 RIS activity tie-break (nit) | **C12 (re-anchored)** | pass | **pass** | **FIXED (Harness-update)** — precedence statement present with the renamed `RoleInstanceUpdated`; "Recovery outranks active update and scaling" unchanged |
| F12 replacement-child mapping (minor) | **C13/C16 (re-anchored)** | pass | **pass / pass** | **FIXED (Harness-update)** — mapping present with renamed tokens; establishment discriminator wording unchanged |
| F15 preCheck status-write contradiction (minor) | **C15 (re-anchored)** | pass | **pass** | **FIXED by the Accepted drop** — configuration blockers are consistently `Progressing=True` in the common rules, the validation-ownership section, and the test plan; no surviving `Progressing=False` mapping |
| F14 attention-row dead disjunct (nit) | **C20 (re-anchored, inverted contract)** | fail | **pass** | **FIXED by the Accepted drop** — under the new model configuration-family blockers *do* occur with `Progressing=True`, so the attention row's disjunct is live; re-anchored as a liveness contract |

Round-6 Copilot advisories resolved by this redesign (pinned by new C28, green at head, red at
every earlier head):

- **C4515734270** (example made `RoleInstanceCustomReady` determine `RoleInstanceReady`): the RI
  example now gives the custom gate its own condition and reason (`ExternalCheckPending`) and
  `RoleInstanceReady` carries `PodsNotReady` — exactly the suggested fix; prose states "external
  gates are not folded into it", matching `getInstanceReadyCondition`.
- **C4515733997** (readiness-gate circular wait with KEP-527 `markPodNotReady`): phase completion
  is now controller-observed workload readiness only; the doc states the lifecycle-owned gate
  "remains until the RI enters `Running`, but it does not prevent the RI from entering `Running`".

New factual claims verified against k8s sources this round (`k8s.io/api@v0.34.2`,
`k8s.io/kubernetes@v1.34.2`): Deployment/StatefulSet define no `Ready` condition (StatefulSetConditionType
has no constants in the shipped API), and `ReplicaSetUpdated` is a genuine Deployment
`Progressing`-condition reason — the new analogy claims are accurate. "Today's RIS code reads only
`RoleInstanceReady`" re-verified against `pkg/reconciler/roleinstanceset/` (no reads of RI
`Progressing`).

New open items this round:

1. **F22 [minor, revision-introduced by fc85dad1], README.md:167 area** — the taxonomy prescribes
   that each controller's own CR-operation errors are reported "directly as `UnclassifiedBlocker`"
   (categorical), while the new provenance table lists "RI (or RIS / RBG for their own CR
   operations)" as lowest observer for the *specific* families (PermissionDenied, AdmissionDenied,
   WaitingForQuota, WaitingForResources) — implying a classifiable own-operation error (e.g. a
   quota-blocked RIS create) reports the family reason; the RIS section hedges with "normally
   `UnclassifiedBlocker`". Three passages, two answers for the same scenario. C26: fail at head;
   scoping the categorical sentence flips it green (`results/bite-check-round7.txt`).
2. **F23 [nit, revision-introduced by d0c9266e], README.md:430** — the leftover sentence "Readiness
   gates only `Creating → Running` and completion decisions" contradicts the new rule ("external
   readiness gates do not gate phase transitions") under the doc's own vocabulary, where
   "readiness gates" = `spec.readinessGates` / externally owned gates; the phase table requires
   "controller-observed readiness requirements". C27: clean introduction differential (green at
   39e2ecd, red only at 0580735); rewording to "readyPolicy and the in-place update readiness
   gate" flips it green.

Carried open (all nit except F19): F13 (C10 part 4 — the Creating-branch concurrency bullet is
still absent from the test plan), F16 (C21 — the minReadySeconds migration row still scopes the
lastTransitionTime dependency to stateless only; stateful `isInstanceAvailable` also reads it),
F17 (PR body still promises "persisted phase transitions for RBG, RIS, and RI" and "Clarify
Accepted / Progressing / Ready semantics" — now doubly stale after the Accepted drop and the Ready
deprecation), F18 (KEP-527 references still dangling — no `keps/527-*` at head; verified by
`git ls-tree`), F19 (RIS `Creating` boundary still existential — :338 unchanged), F20 (C19 — RI
summary prose still unscoped), F21 (C25 — display-priority reference still dangling), Q5 (C22 —
decision table still kind-unscoped for the `Converged` row), Q6 (C24 — Progressing during
deletion still unspecified).

Full re-verify at head (`results/reverify-head-05807351.txt`): 12 FIXED, 8 open (F13, F16, F20,
F21, F22, F23, Q5, Q6 — plus unguarded ledger items F17/F18/F19 confirmed open by direct
head-blob reads). F22 is the only new minor; F23 is a nit. No blockers, no majors.

## Round 6 (delta `0b7285b..39e2ecd`) — both round-5 minor findings fixed; 7 nits open, 2 new

The author pushed three docs commits (+21/−19, README only): `1f7fc3e1 "docs: fix progress write
and creation reason rules"`, `667f7c71 "docs: fold Creating target changes into create
convergence"`, `39e2ecdf "docs: tighten normative status wording"`. Together they land every
[SHOULD] item of the round-5 review except the small nits.

Round-5 open items:

| Finding | Check | At 0b7285b (prev) | At 39e2ecd (head) | Verdict |
| --- | --- | --- | --- | --- |
| F12 replacement-child mapping (minor) | **C13 + C16 (re-anchored)** | fail / fail | **pass / pass** | **FIXED** — `Creating` scoped to initial establishment at BOTH layers ("only during initial RBG establishment" / "only during initial set establishment, before the RIS has an established revision or established RIs for its target"); post-establishment missing children classified under the requiring workflow (Recovering for replacement, ScalingOrUpdating/ScalingUp for scale-up, RollingUpdate for update-created); the :492 test bullet pins 2→3 → `ScalingUp`, retiring Copilot C4495569498's scale-up over-capture; the 'established' discriminator residual is closed by the persisted-facts wording (RIS: established revision / established RIs) |
| F15 preCheck status-write contradiction (minor) | C15 | fail | **pass** | **FIXED** — :140/:510 now derive the Progressing companion from "the validation, pause, and progress rules" with the three explicit cases (incomplete work → True; confirmed compatibility failure → False with the matching reason; full pause → False(Paused)); consistent with :234 |
| F13 concurrency-bullet regression (nit) | C10 part (4) | fail | fail | **OPEN** — :507 still covers only Recovering-over-update and ScalingOrUpdating; no Creating branch |
| F14 attention-row dead disjunct (nit) | **C20 (new)** | fail | fail | **OPEN** — with F15 fixed, config-compat reasons unambiguously never occur with Progressing=True, so the disjunct is dead |
| F16 minReady migration row (nit) | **C21 (new)** | fail | fail | **OPEN** — stateless-only scoping; code premise re-verified first-hand this round (stateful `isInstanceAvailable` reads `RoleInstanceReady.lastTransitionTime`, stateful_instance_set_helper.go:240-256 → :219) |
| Q5 decision-table scope (nit) | **C23 (new, fixed half) + C22 (new, open half)** | — / fail | **pass / fail** | **NARROWED** — the completion row now requires the `Progressing` condition's own `observedGeneration` (rollback-safe; Copilot C4495569437's mechanism); the kind-scope gap remains |
| Q6 Progressing during deletion (nit) | **C24 (new)** | fail | fail | **OPEN** — still no statement |
| C4495569567 Updating-over-blocker (Copilot advisory) | **C18 (new)** | fail | **pass** | **FIXED** — the `Creating → Updating` edge is removed entirely (fold into Create convergence); :438 adds the verbatim fix ("`Updating` is the Activity reason only when no current blocker or incomplete observation takes precedence") |

New content verified this round: the three KEP-527 cross-references introduced by the delta are
**accurate against PR #546's current head** (hook-state labels + preDelete finalizer; the
Create-fold "no hooks" rule; preUpdate/preRecover established-instance applicability) — see
`results/kep527-reference-check.txt`. The normative tightening ("should"→"must", "Candidate
Reasons"→"Reasons") is consistent: `Paused` and `ObservationIncomplete` are both present in the
now-definitive reason table (C2 sweep green). kep.yaml is untouched by the delta; TOC matches all
headings; markdownlint reports 0 issues (`results/markdownlint-head-39e2ecdf.txt`).

Harness updates (additive, disclosed): **C13/C16 re-anchored** (the fix removed their round-5
anchor phrases — Harness-update, not proof; polarity re-proven against every historical head,
`results/polarity-round6.txt`: fail at all six prior heads, pass at 39e2ecdf), **C11 claim 3
re-anchored** to the KEP-527 hook-protocol delegation (code constants still verified), and eight
new checks **C18–C25**. Every new/re-anchored guard is bite-proven by mutation
(`results/bite-check-round6.txt`): each open finding's one-sentence fix flips its guard green
(fixA–fixG) and each fixed guard flips red when its fix is reverted (mut1–mut6).

New open items this round (both nit):

1. **F18 [nit, revision-introduced by 667f7c71], README.md:414** — the RI section's summary prose
   still says "Identifying an update target enters Updating" / "Confirming independent recovery of
   required Pods outside an update enters Recovering" without phase scope, while the delta changed
   the normative edge table to fold update/recovery targets identified during initial creation
   into Create convergence. C19: pass@0b7285b / fail@39e2ecd (differential); scoping both
   sentences flips it green.
2. **F19 [nit, late discovery; dangling since the round-5 pivot], README.md:247** — "select the RI
   phase using the display priority defined in the RoleInstance section": the RoleInstance section
   defines no display priority. C25 fail@head; defining it (or rewording the reference) flips green.

Also noted (non-gating question Q7): KEP-502 now normatively depends on KEP-527, which is an OPEN
PR (#546); the claims are accurate today, but the two proposals must stay in sync.

Full re-verify at head (`results/reverify-head-39e2ecdf.txt`): 11 findings FIXED (F2, F3, F4, F9,
F10, F11, F12, F15, C4457744212, C4495569567, and all regression guards), 7 open (F13, F14, F16,
Q5-kind-scope, Q6, F18, F19) — **every open item is nit-severity with a one-sentence fix whose
repro lives on this branch**. F17 (PR body) is not guardable from the repo; it stays open by
direct PR-body comparison.

## Round 5 (delta `da81877..0b7285b`) — redesign; both round-4 open items fixed, three new open items (nit/nit/minor)

The author pushed two docs commits:
`e09cb64 "docs: scope creation and define RIS activity precedence"` (+8/−3 — exactly the F11/F12
fixes round 4 asked for) and then `0b7285b "docs: clarify status contract and RI phase
transitions"` (+154/−141 — a **fundamental redesign**): RBG and RIS **no longer add
`status.phase`** (workflow is reported through `Progressing.reason` + existing counters; RI keeps
its optional phase as the only lifecycle anchor), RIS drops its proposed `Ready` condition, the
`Terminating` reasons leave the RBG reason tables (a deletionTimestamp no longer rewrites Ready —
the round-2 F4 behavior change is withdrawn, matching current controller behavior), and new
content lands: a "Consuming the Status" decision table, blocker-classification rules, an
observedGeneration-dispatch rule, an RI phase-edge table, and four new factual claims about
current code.

Round-4 open items (both fixed, differentially):

| Finding | Check | At da81877 (prev) | At 0b7285b (head) | Verdict |
| --- | --- | --- | --- | --- |
| F11 RIS activity tie-break (nit) | **C12 (new)** | fail | **pass** | **FIXED** — RIS precedence stated (Creating, Recovering, RollingUpdate, ScalingUp/ScalingDown; recovery outranks update/scaling) + recovery-overlap test bullet |
| F12 replacement-child → parent reason (minor) | **C13 (new)** | fail | **pass** | **FIXED definitionally** — "Creating … when a required child does not yet exist"; "Recovering … when an established child is being repaired or replaced", at both RBG and RIS layers; the :495 bullet that prescribed the misfire is gone. Residual derivation ambiguity tracked as F14 |

New content verified against the repo (all accurate): the RI controller does rebuild
`RoleInstanceReady`/`RoleInstanceAllPodsReady` with `time.Now()` and replaces the condition list
(`instance_status.go` `setInstanceConditions`); stateless RIS counts an RI ready via
`RoleInstanceReady` + every `spec.readinessGates` (`statelessmode/utils/utils.go`) while stateful
`isHealthy` checks only `RoleInstanceReady`; the lifecycle-state label
`rbg.workloads.x-k8s.io/role-instance-lifecycle-state` exists with exactly the five documented
values; CoordinatedPolicy does lower partitions stepwise (`stepPartition` in
`rolebasedgroup_controller.go`). Guarded by **C11** (red at prev, green at head).

The redesign itself is internally consistent: TOC complete and matching all headings; no leftover
statement claims RBG/RIS add a phase (**C14**, red at prev, green at head); the retained-field
inventory was reworded but carries the same 12 fields (C1 re-anchored, green); markdownlint 0
issues; C2/C3/C4/C5/C6/C8/C9 all green.

Three new open items (all non-gating):

1. **F15 [minor], README.md:140 / :510 — normative contradiction in the new
   observedGeneration-dispatch rule.** Both the common rule and the unit-test bullet say the
   parent writes `status.observedGeneration=N` *together with `Progressing=True`* "including
   preCheck failures", while the Accepted semantics (:233) require `Progressing=False` with the
   matching incompatibility reason when `Accepted=False`. Nothing reconciles the two.
   Repro: **C15** (fail at head; vacuous pass at prev since the clause is new; fixA flips it green).
2. **F13 [nit, revision regression], README.md:500 — the redesign dropped the unit-test bullet
   covering the concurrent heterogeneous Activity combinations** (the Creating-over-updating/
   recovering clause of the round-4 C4457744212 fix). The precedence design content is intact
   (guarded by **C17**, split from C10); the full round-4 contract C10 goes
   pass@da81877 → fail@0b7285b; fixC (restoring the bullet) flips it green.
3. **F14 [nit], README.md:199/:347 — the "established" discriminator for Creating vs Recovering
   is not operationalized.** For an absent child being replaced, both clauses apply; the KEP
   never states how the controller derives "established" from persisted facts, and the restart
   rule says `Progressing.reason` is recomputed "from current facts" (e09cb64's explicit
   lifecycle-stage rule and its controller-restart test bullet were both removed by 0b7285b).
   Repro: **C16** (fail at head; fixB flips it green).

Bite matrix for the round-5 checks: `results/bite-check-round5.txt` (head / prev / fixA / fixB /
fixC). Re-verify at head: `results/reverify-head-0b7285b.txt` — 8 findings FIXED, F13/F14/F15
STILL-BROKEN (open, non-gating), exit ≠ 0 as expected while the three open items stand.

CI at `0b7285b`: 9 SUCCESS / 6 SKIPPED (docs-only diff; e2e groups skip), PR OPEN and MERGEABLE —
same standing caveat: `keps/**` is outside the docs-check workflow's lint scope, so CI greenness
is weak evidence; the substantive gates are the harness replays.

Two new **questions** (nit, non-gating): Q5 — the "Consuming the Status" decision table answers
"Is the change done?" with `Progressing=False, reason=Converged`, but `Converged` is defined only
in the RBG and RIS Progressing=False rows; the RI reason list has no `Converged` (RI completion
is phase=Running), and the table does not state its kind scope. Q6 — after the removal of the
`Terminating` reasons, RBG/RIS `Progressing` semantics during deletion are unspecified (the
current controller early-returns, i.e. Progressing goes stale; confirm that is the intended
contract).

## Round 4 (delta `f622d64..da81877`) — re-verify verdict: all findings fixed

The author pushed one docs commit, `da81877 "docs: define RBG activity reason precedence"`
(+11 lines, README only), addressing exactly the one open round-3 item — the Copilot-only
advisory C4457744212 (within-Activity tie across heterogeneous role facts), which round 3
recommended resolving by "one sentence of within-Activity precedence … plus contract-test
coverage for the concurrent creation/recovery/update combinations". The delta does both:

- README.md:184-192 — new fenced precedence `Creating > Recovering > ScalingOrUpdating` inside
  the Activity family, with the one-dominant-reason + all-activities-in-message rule and a
  rationale sentence per ranking.
- README.md:495 — new unit-test-plan bullet covering the concurrent heterogeneous combinations
  (Recovering-over-update, Creating-over-update/recovery, only-update/scaling).

New check **C10-rbg-activity-precedence** (contract) guards the new text: it asserts (a) the
fenced list is exactly the family-table RBG activity reasons with no duplicates, (b) the
highest-precedence-reason and message rules are stated, (c) the rationale's pairwise orderings
("Creating outranks recovery", "Recovering outranks ordinary target changes") agree with the list
order, and (d) the test plan covers the concurrent combinations. Bite check
(`results/bite-check-round4.txt`): **fail at f05e1dd, 6335eac, and f622d64** (statement absent —
confirming the round-3 attribution that the gap existed since round 1 and was a late discovery,
not a revision regression), **pass at da81877**.

| Finding | Check | At f622d64 (prev) | At da81877 (head) | Verdict |
| --- | --- | --- | --- | --- |
| C4457744212 within-Activity tie (Copilot round-3 advisory, nit) | **C10 (new)** | fail | **pass** | **FIXED** — deterministic precedence + rationale + test-plan coverage |
| F9 RBG transition-input vocabulary (round-3 fix) | C8 | pass | pass | stays fixed |
| F10 RIS activity reasons (round-3 fix) | C9 | pass | pass | stays fixed |
| F2/F3/F4 (round-1) | C2/C3/C4 | pass | pass | stay fixed |
| P0 motivation / retained fields / YAML+lint | C5/C1/C6/C7 | pass | pass | still accurate |

`re-verify.sh` at `da81877` (after C10 was added and the manifest updated): all six manifest
findings FIXED, exit 0 (`results/reverify-round4/`).

CI at `da81877`: all checks SUCCESS or SKIPPED (docs-only diff; e2e groups skip), PR OPEN and
MERGEABLE — same standing caveat: `keps/**` is outside the docs-check workflow's lint scope, so
CI greenness is weak evidence; the substantive gates are the harness replays.

One new **question** (nit, non-gating, filed this round): the RIS-layer analog of the fixed
advisory. The new precedence is explicitly RBG-scoped ("Within the Activity family, RBG uses
this deterministic precedence"); at the RIS layer, when RI-recovery facts coexist with an active
RIS workflow, the `Progressing` reason selection between `Recovering` and the workflow reason
(`RollingUpdate` / `ScalingUp` / `ScalingDown`) is still unstated — :349's rule covers
blocker-over-update and update-over-scaling, and :368's "RIS Progressing follows RBG rules for
progress, pauses, and insufficient observations" does not list activity precedence. The new RBG
rule also creates a visible cross-layer asymmetry worth one clarifying sentence: at RBG,
`Recovering` outranks `ScalingOrUpdating` (repair work outranks ordinary target changes), while
a literal reading of :349 at RIS keeps `RollingUpdate` for "an active update without a
higher-priority blocker" even when recovery facts are present. See Q4 in the round-4 findings.

## Round 3 (delta `6335eac..f622d64`) — re-verify verdict: all findings fixed

The author pushed one docs commit, `f622d64 "docs: align transition inputs and RIS reasons"`
(3 changed lines), addressing exactly the two open round-2 items:

| Finding | Check | At 6335eac | At f622d64 | Verdict |
| --- | --- | --- | --- | --- |
| F9 RBG transition-input vocabulary (ledger F9, revision regression) | C8 | fail | **pass** | **FIXED** — Goals (:70) and RBG intro (:211) now say "lower-level phases, conditions, and recovery facts", aligning with :463/:502 |
| F10 RIS activity reasons unenumerated (ledger F10, pre-existing nit) | C9 | fail | **pass** | **FIXED** — :358 now definitively enumerates `Creating`, `ScalingUp`, `ScalingDown`, `RollingUpdate`, `Recovering`, exactly the family table's RIS column |
| F2/F3/F4 (round 1) | C2/C3/C4 | pass | pass | stay fixed (no regression in the delta) |
| P0 motivation / retained fields / YAML+lint | C5/C1/C6/C7 | pass | pass | still accurate |

Harness note (recorded as such, not as a defect): the first re-verify run at `f622d64` reported
F10 `STILL-BROKEN` only because C9 anchored on the literal phrase "Activity reasons may include",
which the fix reworded to the definitive "Activity reasons are". C9 was re-anchored to accept both
phrasings (`scripts/check_kep502.py` commit "round-3 harness"), then re-proven to bite:
**fail at 6335eac** (missing `Creating`, `Recovering`), **pass at f622d64**
(`results/bite-check-round3.txt`). The fix is differential-confirmed, not assumed.

CI at `f622d64`: all checks SUCCESS or SKIPPED (docs-only diff; e2e groups skip), PR OPEN and
MERGEABLE — same standing caveat as rounds 1-2: `keps/**` is outside the docs-check workflow's
lint scope, so CI greenness is weak evidence; the substantive gates are the harness replays.

Raw output: `results/reverify/` (re-verify.sh at f622d642), `results/bite-check-round3.txt`.

Open items after round 3: **none** on this harness. Every tracked finding
(F2/F3/F4 from round 1, F9/F10 from round 2) is fixed at `f622d64`; the refuted
round-1 hypothesis (F1 phantom `rolloutReplacementState`) stays refuted (C1 green).

## Round 2 (delta `f05e1dd..6335eac`) — re-verify verdict

The author pushed 4 docs commits addressing the round-1 consensus findings. Re-run of
`re-verify.sh` at the new head:

| Finding | Check | At f05e1dd | At 6335eac | Verdict |
| --- | --- | --- | --- | --- |
| F2 `Preparing` undefined (ledger F1-preparing-undefined) | C2 | fail | **pass** | **FIXED** — token dropped from the Progressing=True row |
| F3 `RolesNotReady`/`RoleNotReady` unacknowledged (ledger F5) | C3 | fail | **pass** | **FIXED** — dedicated distinction paragraph at README.md:184 |
| F4 `InPlaceUpdateNotReady` casing (ledger F6) | C4 | fail | **pass** | **FIXED** — now matches the code spelling |
| P0 motivation claims | C5 | pass | pass | premise still Confirmed |
| retained-field inventory | C1 | pass | pass | still accurate (incl. `rolloutReplacementState`) |
| YAML/kep.yaml + markdownlint | C6/C7 | pass | pass | tooling claims still reproduce |
| **F9 RBG transition-input vocabulary inconsistent** (new, revision regression) | C8 | **pass** | **fail** | **STILL-BROKEN** — see below |
| **F10 RIS activity reasons unenumerated** (new, pre-existing nit) | C9 | fail | fail | **STILL-BROKEN** — see below |

Round-1 findings owned by Reviewer B (Codex) were verified fixed by direct reading of the delta
(priority rule now lists `Activity` and states child-readiness subsumption at :169/:182, making the
RBG example derivable; `TemplateInvalid`/`DependencyCycle` added to the Progressing=False row plus
prose mapping at :194; the terminating `Ready` trigger relabeled and covered in risks/upgrade/unit
tests at :237/:282/:529/:464; TOC entries added at :15-16; the event-budget risk row at :119;
Q1 answered at :345; Q2 answered by the new Validation Ownership section :186-194; Q3 accepted
trade-off stated at :341; Copilot C4431017493 precedence fix at :339; O1 row split at :295-296).

### New finding F9 (revision regression, minor)

The revision rewired RBG recovery derivation to **conditions** but left two statements on the old
**phases** vocabulary:

- README.md:463 — "RBG derives recovery facts from lower-level conditions, not a RIS `Recovering` phase"
- README.md:502 — "RBG transitions use lower-level conditions and recovery facts" (changed from "phases" in this delta)
- README.md:70 — Goals: "Define how **lower-level phases** and recovery facts trigger RBG phase transitions" (unchanged)
- README.md:211 — RBG intro: "transitions based on the current target, **lower-level phases**, and recovery facts" (unchanged)

C8 is a pure consistency contract: it **passes at f05e1dd** (all statements said "phases") and
**fails at 6335eac** — a genuine revision regression, not a pre-existing issue. An implementer
reading the RBG intro would look for child *phase* values as transition inputs, the very thing
:463 disclaims (RIS deliberately has no `Recovering` phase). Fix: align :70/:211 to the conditions
vocabulary, or use "phases and conditions" — both wordings verified to flip C8 green
(`results/bite-check-round2.txt`).

### New finding F10 (pre-existing nit)

README.md:358 — "Activity reasons may include `ScalingUp`, `ScalingDown`, and `RollingUpdate`" omits
`Creating` and `Recovering`, both defined as RIS reasons in the activity/child-readiness family
table (:162, :165). The text is identical at f05e1dd and 6335eac, so this is a late discovery on
unchanged code, not a regression; its materiality rose because the revision made RIS recovery
reporting load-bearing (:345). An implementer writing RIS's reason validator needs the complete
set in one place. Fix: enumerate all five, or state the family table is the complete source.

Raw output: `results/unit-l2-at-head.txt` (full layer at head + differential at previous head),
`results/bite-check-round2.txt`, `results/reverify/` (re-verify.sh run at 6335eac).

## Premise (P0) — Confirmed

The KEP's motivation claims were all verified against the base/HEAD code (`C5`, green at both
reviewed heads):

| Claim in KEP motivation | Verified at head |
| --- | --- |
| RBG / RIS / RI lack `status.phase` | no `json:"phase"` in any of the three v1alpha2 status types |
| RBG declares `Progressing` / `RollingUpdateInProgress` but controllers never write them | `RoleBasedGroupProgressing`/`RollingUpdateInProgress` (and the bare string literals) have zero writers in `internal/` + `pkg/`; only Ready and GangConfigured are written |
| RBG Ready ignores revision convergence | `rolebasedgroup_controller.go:783-793` computes Ready purely from `role.Replicas == rs.Replicas == rs.ReadyReplicas` |
| RIS / RI `FailedScale` / `FailedUpdate` record operation failures | declared in both v1alpha2 enums; written by `statelessmode/reconciler.go:237,251` and `instance_reconciler.go:191,235` |

Round-2 note: the new "Validation Ownership and Failure Semantics" section claims admission
(webhook) rejects dependency cycles. Verified against code: role dependencies are intra-object
(`RoleSpec.Dependencies []string`, `rolebasedgroup_types.go:272`) and `ValidateRoleDependencies`
(acyclicity check) is already called from admission on create and update
(`rolebasedgroup_admission.go:60,88`) — the KEP's classification matches the current code.

Premise verdict: **Confirmed** — the limitations the KEP describes are real on the current code.

## Round 1 observed vs expected (head `f05e1dd`)

| Finding | Check | Layer | Polarity | At f05e1dd | Meaning |
| --- | --- | --- | --- | --- | --- |
| F1 phantom `rolloutReplacementState` (reviewer hypothesis) | C1 | unit | contract | **pass** | **Refuted** — the field exists (`roleinstanceset_types.go:201`, present at base too). All 12 retained RIS fields + 3 preserved RI fields exist. |
| F2 `Preparing` candidate reason never defined | C2 | unit | contract | **fail** | Confirmed — defect present in the KEP (fixed in round 2) |
| F3 `RolesNotReady` vs `RoleNotReady` near-collision unacknowledged | C3 | unit | contract | **fail** | Confirmed — defect present in the KEP (fixed in round 2) |
| F4 `InPlaceUpdateNotReady` casing vs code `InplaceUpdateNotReady` | C4 | unit | contract | **fail** | Confirmed — defect present in the KEP (fixed in round 2) |
| P0 motivation claims | C5 | unit | contract | pass | premise Confirmed |
| YAML examples / kep.yaml parse | C6 | unit | contract | pass | PR body claim reproduced |
| markdownlint-cli2 with repo config | C7 | integration | contract | pass | PR body claim reproduced |

Raw output: `results/unit-l1-at-head.txt`, `results/bite-check-with-proposed-fixes.txt`,
`results/markdownlint-integration.txt`.

## Harness-bites check

The proposed fixes were applied to a **scratch copy** (`/tmp/kep502-bite/README.md`):
drop `Preparing` from the Progressing=True row, add one sentence acknowledging that
`RolesNotReady` (Progressing waiting reason) is distinct from the retained `RoleNotReady`
(Ready=False reason), and spell the RI aggregated-readiness reason as `InplaceUpdateNotReady`
to match the code. Result: C2/C3/C4 flip to **pass** (see `results/bite-check-with-proposed-fixes.txt`),
and `git status keps/` stays empty — production files untouched.

## How to run

```bash
# from a checkout of this branch (or after re-verify.sh grafts the harness):
python3 -I docs/verification/502-workload-status-model-claude/scripts/check_kep502.py
bash    docs/verification/502-workload-status-model-claude/scripts/lint.sh .

# re-verify against the current PR head (fetches it, grafts the harness, runs both layers):
bash    docs/verification/502-workload-status-model-claude/scripts/re-verify.sh
```

Requires: git, jq, python3 (+PyYAML), and npx with a cached markdownlint-cli2 for the
integration layer (emits `not-run` → `HARNESS-UPDATE` if unavailable).

## Continuing after the fix

- All checks are **contract** tests: on fixed KEP text they go green. There are no canaries.
- After the author amends the KEP, `re-verify.sh` (no argument) fetches the current PR head and
  reports F2/F3/F4 (round 1, fixed at 6335eac), F9 (round 2, fixed at f622d64), and F10
  (round 2, fixed at f622d64 after the C9 re-anchor) per check.
- C1 guards the retained-field inventory (both the "RIS retains ..." and the
  "Preserve restartCount ..." sentences) against future edits naming nonexistent fields.
- C5 guards the motivation's factual claims; if a future controller starts writing RBG
  `Progressing` (e.g. a partial implementation lands before the KEP is agreed), C5 flips red and
  the premise needs re-review.
- C8 guards the RBG-transition-input vocabulary (phases vs conditions) — the round-2 regression.
  C9 guards the RIS activity-reason enumeration against the family table.
- `.last-reviewed` holds the last reviewed PR head; advance it after each reviewed round.

### Kickoff prompt (fresh agent, from this branch alone)

> You are resuming a review of https://github.com/sgl-project/rbg/pull/528 (KEP-502, docs-only).
> Read `docs/verification/502-workload-status-model-claude/verify-manifest.json` and this README.
> Run `bash docs/verification/502-workload-status-model-claude/scripts/re-verify.sh` (no args) to
> test the current PR head; report the F2/F3/F4/F9/F10 verdicts and review the `last-reviewed..head`
> delta it prints. Round-1 findings (F2/F3/F4) were fixed at 6335eac; F9/F10 were fixed at
> f622d64 (C9 was re-anchored in round 3 — if a future fix rewords the RIS activity-reason
> sentence again, expect a Harness-update, adjust the C9 anchor, and re-prove it bites).
> All checks are contract-polarity (green = fixed). Do not touch production files.
