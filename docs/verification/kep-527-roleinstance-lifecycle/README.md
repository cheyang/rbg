# Verification harness — KEP-527 RoleInstance Lifecycle Hooks (PR #546)

Reviewer: Codex (Reviewer B). Round: incremental #3. Reviewed head: `fb6c2b3605fff9fab5ea0606529791f28c671d69`, base: `3612013e6f779401831f5947dc124cc4313faddb` (merge-base `09accf63`). Previous round heads: `6e8419ab` (round 1), `71dd5e87ca8704e2447caf574db72c06007d77ef` (round 2).

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
