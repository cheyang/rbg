# Verification — PR #491 (KEP-455: rbgset rolling update) — Reviewer B (Codex)

PR under review: https://github.com/sgl-project/rbg/pull/491
Head reviewed: `f711893c835648a49e2f73ff0be1b73d9c7a13ef` (round 3; round-2 head `b03a7e67cf681599fb1c69537be2a2078ec35cf0`; round-1 head `719851a65959ebc91157ce90651aaa911c26f30c`)
Base: `79f7e5858fdc30ffaeadef4aea12c3809dcb348d` (merge-base `0821cb5b`)

The PR is **docs-only**: it adds `keps/455-rbgs-rolling-update/{README.md,kep.yaml}`.
There is no runtime behavior to reproduce, so this harness verifies the two
things that decide whether the KEP is mergeable:

1. **The KEP's factual claims about the current codebase** (its premises) —
   contract tests `TestVerify_C*`, expected to PASS on the PR head. If a future
   code change flips one of them, the KEP text has gone stale.
2. **The review findings F1/F2** — contract tests `TestVerify_F*` that FAIL on
   the PR head (the failure *is* the reproduction) and must PASS once the KEP
   is amended.

## Premise (P0)

Claim (KEP "Child Template Propagation"): children currently receive only
`spec.roles`; a set template defining `roleTemplates` produces children that
cannot resolve a `templateRef`. **Confirmed** against the base/head code:
`newRBGForSet` and `updateExistingRBGs` in
`internal/controller/workloads/rolebasedgroupset_controller.go` handle only
`Spec.Roles`; `RoleBasedGroupSpec` (v1alpha2) holds exactly `roles` +
`roleTemplates` (C1, C2 pass; see `results/unit.out`).

Docs-only carve-out applies: no runtime symptom is claimed, so nothing is
reproduced against a cluster. The linked tracking issue #455 (open feature
request for "RBGS-level RollingUpdate implementation") matches the KEP's
subject. (`closesIssues: #15` reported by automation is the PR-template's
example text inside an HTML comment; #15 is long closed — no auto-close risk.)

## Observed vs expected (PR head 719851a)

| Test | Polarity | Expected now | Observed now | Meaning |
|------|----------|--------------|--------------|---------|
| C1 children receive only roles | contract | pass | PASS | KEP propagation premise true |
| C2 RoleBasedGroupSpec has 2 fields | contract | pass | PASS | "blast radius bounded" claim true |
| C3 RollingUpdateInProgress never published | contract | pass | PASS | **F1's code fact: no producer exists** |
| C4 conversion webhook in CRD manifests | contract | pass | PASS | KEP conversion claim true |
| C5 scalingAdapter rejection message | contract | pass | PASS | KEP conflict description matches code |
| C6 v1alpha2 label prefix | contract | pass | PASS | proposed label names fit convention |
| C7 kep.yaml milestone == Makefile VERSION | contract | pass | PASS | v0.9.0 consistent |
| C8 no set rolloutStrategy yet | contract | pass | PASS | feature is a proposal, not shipped |
| F1 KEP states RollingUpdateInProgress is new work | contract | **fail** | **FAIL** | finding reproduced |
| F2 KEP states checkpoint persistence | contract | **fail** | **FAIL** | finding reproduced |

## Findings

- **F1 [major]** — The KEP's InPlaceUpdate availability gating depends on the
  RBG condition `RollingUpdateInProgress` ("RBGSet requires that barrier to be
  false before spending another budget slot"), but no controller sets that
  condition today (C3: it exists only as an API constant, declared at
  `api/workloads/v1alpha2/rolebasedgroup_types.go:580`). The KEP never states
  that publishing it is new, in-scope RBG-controller work, nor when it is
  set/cleared. An implementation written literally from this KEP would find the
  barrier permanently absent (trivially "false") and could start the next
  ordinal before the downstream workload finished — the exact failure the KEP's
  risk table says the barrier prevents.
- **F2 [minor]** — The KEP requires a restart-safe `groupset-observed-replicas`
  checkpoint ("Controller restarts must not lose that reference or the
  observed-replicas checkpoint") but never says where it is persisted; it is
  absent from the Status table. If it is an annotation on the set, it interacts
  with the v1alpha1 conversion stash and GitOps diffs; if status, it must be
  specified there.

## Round 2 (head b03a7e6, delta 719851a..b03a7e6)

The round-2 revision (+114/-5 in the KEP README) addresses round-1 findings:
new "Child Rollout Barrier" design section (F1), "Scale subresource
interaction" paragraph (F2), enumerated controller-owned-key list covering
hash+drift+sync plus syncRBGMetadata carry-over (F3/F4), an Appendix pinning
checkpoint storage (F5), informational-only numeric revision (F6),
percentage-partition timing (F7), the InPlaceUpdate explicit-zero admission
verdict (Q1), and the operator-downgrade stance (Q2); plus test-plan additions
(M1 partial).

Harness changes this round:
- `TestVerify_F1_KEPStatesRollingUpdateInProgressIsNewWork` contract updated
  (harness-update): the round-1 regex expected an explicit "this is new work"
  acknowledgment; the round-2 head instead carries the full design section,
  which is the round-1 ask's first option. The contract now asserts the design
  section (producer/ordering/consumer) exists. Verified: FAIL on 719851a, PASS
  on b03a7e6.
- New guard tests `TestVerify_R2*` for every fixed finding — each FAILS on
  719851a and PASSES on b03a7e6 (both runs captured under results/reverify/),
  so they double as regression guards for future rounds.
- New finding N1 contract test + two discrete-event simulation exhibits.
- re-verify.sh: results are now persisted after the tree restore (previously
  `git checkout -f` clobbered fresh results back to the committed ones).

### Observed vs expected (head b03a7e6 / old head 719851a)

| Test | Polarity | b03a7e6 | 719851a | Meaning |
|------|----------|---------|---------|---------|
| C1-C8 premise/code-fact contracts | contract | PASS | PASS | KEP premises still true; code unchanged outside keps/ |
| F1 barrier designed (updated contract) | contract | PASS | FAIL | round-1 F1 FIXED by the design section |
| R2F2 scale-subresource interaction | contract | PASS | FAIL | round-1 F2 FIXED |
| R2F3F4 enumerated keys + sync carry-over | contract | PASS | FAIL | round-1 F3/F4 FIXED |
| F2(old id)/R2F5 checkpoint storage named | contract | PASS | FAIL | consensus F5 FIXED |
| R2F6 numeric revision informational-only | contract | PASS | FAIL | round-1 F6 FIXED |
| R2F7 partition resolution timing | contract | PASS | FAIL | round-1 F7 FIXED |
| R2Q1 explicit-zero admission verdict | contract | PASS | FAIL | round-1 Q1 ANSWERED |
| R2Q2 downgrade stance | contract | PASS | FAIL | round-1 Q2 ANSWERED |
| R2M1 v1alpha1 round-trip test-plan entry | contract | **FAIL** | FAIL | M1 residual reproduced (round-trip entry still missing; two Reviewer C wording catches also unchanged) |
| R2N1 absent-exception generation-gated | contract | **FAIL** | FAIL | **N1 reproduced** (new this round) |
| N1Exhibit concurrent rolls (simulation) | exhibit | PASS | PASS | specified rule: 2 children mid-roll with maxUnavailable=1; gated rule: 1 |
| N1Exhibit silent stale completion (simulation) | exhibit | PASS | PASS | specified rule: child controller down -> set declares complete with 0/4 rolled; gated rule: fail-safe stall after first bump |

### N1 — the absent-condition exception is not generation-gated (major, new)

The round-2 consumer rule (`groupSetChildReady`, KEP "Child Rollout Barrier"):
ready when the barrier condition is **absent**, or `ObservedGeneration >= gen`
and `False`. The absent-exception exists for legacy children that predate the
feature. But it is unscoped: it also fires in the window between the set
bumping a legacy child's spec (generation N -> N+1, revision label written in
the same update) and the child RBG controller's first reconcile of N+1 (which
per the KEP's own producer invariant stamps `RollingUpdateInProgress=True`).
In that window the set observes matches-update + stale Ready=True + barrier
absent and spends the next budget slot on a child that has not started rolling
— the stale-capacity failure the barrier exists to prevent, on exactly the
legacy-bootstrap population the exception serves. With the child controller
down, the set marches through every ordinal and declares RolloutComplete with
zero children rolled (silent stale completion; exhibit B).

Fix (one clause): treat absent as ready only when the child's
`status.observedGeneration >= child.generation` (the field exists —
api/workloads/v1alpha2/rolebasedgroup_types.go:524 — and is stamped every
reconcile by updateRBGStatus). Legacy converged children have
observedGen == gen and still bootstrap; just-bumped children have
observedGen == gen-1 and are held until their controller reacts.

Simulation assumptions (kep_round2_test.go): set re-reconciles every tick (its
own child writes re-trigger it via Owns(&RoleBasedGroup{})) and acts before
child controllers within a tick (its reconcile is the cheap side of the race);
child controller latency 1 tick, roll duration 3 ticks, 4 children,
maxUnavailable=1.

## Round-2 harness-bites check (still-open contracts)

Grafted onto head b03a7e6, then simulated the proposed fixes in the KEP README:
(1) consumer rule amended to "...condition is absent only when the child's status
observedGeneration has caught up to its current generation, or when..." and
(2) a "Conversion round-trip" v1alpha1 stash/restore entry added to the unit
test plan. Result: `TestVerify_R2N1_AbsentBarrierExceptionIsGenerationGated` and
`TestVerify_R2M1_V1alpha1RoundTripCoveredByTestPlan` both flipped FAIL -> PASS;
the KEP was then reverted (`git checkout -- keps/`, diff empty). Both contracts
bite. Note: the N1 regex rejects a period between "absent" and the
observedGeneration mention, so a fix sentence containing the literal Go path
`status.observedGeneration` may read as Harness-update — reword the regex, not
the finding.

## How to run

```bash
# unit layer (all claims + findings)
go test ./test/verification/pr491kep/ -run 'TestVerify_' -count=1 -v

# full re-verification against a revised PR head (from a checkout of this branch)
bash docs/verification/pr491-rbgs-rolling-update-kep/scripts/re-verify.sh
```

Integration (envtest) and live (L3) layers are intentionally omitted: the diff
contains no controller code. The unit layer's claims *are* the integration with
the repository (they read the actual source/manifests under review).

## Harness-bites check (done on this branch)

Temporarily appended to the KEP README: "`RollingUpdateInProgress` is not
currently published by any controller; this KEP adds it as new RBG controller
behavior. The `groupset-observed-replicas` checkpoint is persisted in the set
status." → F1/F2 tests went **green**; after reverting, red again. Production
and KEP diffs are empty (`git status` clean). The harness bites.

## Continuing after a KEP revision

1. Re-run `bash docs/verification/pr491-rbgs-rolling-update-kep/scripts/re-verify.sh`
   (no args — it fetches the current PR head from `manifest.pr`).
2. F1/F2 fixed ⇔ their tests flip to PASS. If the author rewords the statements
   completely differently, the tests may report Harness-update — loosen the
   regex in `test/verification/pr491kep/kep_claims_test.go`, then re-run.
3. C1–C8 must stay green; a flip means the code drifted from the KEP's claims
   (e.g. the implementation PR #474 landed first) and the KEP needs a re-read.
4. Advance `.last-reviewed` to the new head after the round.

Kickoff prompt for a fresh machine: "Check out branch
`verify/pr491-rbgs-rolling-update-kep-codex` of https://github.com/cheyang/rbg.git,
then run `bash docs/verification/pr491-rbgs-rolling-update-kep/scripts/re-verify.sh`."


## Round 3 (head f711893c, delta b03a7e6..f711893c)

The round-3 revision (+193/-81 in the KEP README, docs-only) resolves the
round-2 major N1 by redesign: the absent-condition exception is replaced by a
persistent **request/acknowledgement** mechanism — the set advances a per-child
`rbg.workloads.x-k8s.io/rollout-request` counter in the same
resource-version-protected `Update` as spec/metadata/revision label; the RBG
controller writes `status.observedRolloutRequest` only after policy migration,
PodGroup reconciliation, role workload application, and orphan cleanup succeed
plus a direct post-application recheck of every role workload; the consumer
requires request == acknowledgement whenever the annotation exists, in addition
to the generation/Ready/condition gates; a missing condition never bypasses a
pending request; the legacy fallback is restricted to never-requested children;
the counter gives A->B->A a fresh request; both values persist on the child
object across controller restarts. The system-key list grew 4 -> 6 (+
`rollout-request`, + `v1alpha1-pod-group-policy`) with restated scope; the
replicas-only scenario is redefined (both strategies pace under the
availability budget, Recreate preserves the UID); the test plan gained
request/ack entries and a manual kind-validation list.

Harness changes this round:
- N1 contract replaced additively (harness-update): the round-2
  `TestVerify_R2N1_AbsentBarrierExceptionIsGenerationGated` was remedy-shaped
  (asserted the generation gate specifically, as disclosed in round 2). The
  round-3 `TestVerify_R3N1_*` contracts assert the remedy-neutral acceptance
  criterion (a spec-bumped child must not count as ready before its controller
  has acknowledged the new request) plus the round-2 ask's risk-row/test-entry
  alignment. Verified: FAIL on b03a7e6, PASS on f711893c.
- New discrete-event exhibits `TestVerify_R3N1Exhibit_*`: budget held at
  maxUnavailable=1 under controller latency; fail-safe stall (no completion)
  with the child controller down; the decisive metadata-only case (generation
  unchanged, Ready/condition stale-valid: control without the request gate
  admits 2 concurrent rolls, with the gate concurrency=1); A->B->A revert never
  reuses an acknowledgement (ready-window 0 ticks).
- F1 and F3/F4 guards updated additively (section restructured; key list
  extended with restated scope). Round-2 guards kept as historical records.
- New finding contracts: R3M1 residual (stash/restore test entry + both wording
  catches), R3F9 (request-writer scope vs drift-repair writes), R3F10
  (replace-write strip fallback), R3Q3 (unbudgeted compatibility-annotation
  convergence). Bites-checked: one-clause simulated fixes flip each green;
  reverted clean.

### Observed vs expected (head f711893c / previous head b03a7e6)

| Test | Polarity | f711893c | b03a7e6 | Meaning |
|------|----------|----------|---------|---------|
| C1-C8 premise/code-fact contracts | contract | PASS | PASS | KEP premises still true; code unchanged outside keps/ |
| R3F1 barrier section (restructured) | contract | PASS | FAIL | F1 still fixed |
| R2F2 scale-subresource interaction | contract | PASS | PASS | F2 still fixed |
| R3F3F4 6 system keys, hash+drift+sync scope | contract | PASS | FAIL | F3/F4 still fixed |
| R2F5 checkpoint storage named | contract | PASS | PASS | F5 still fixed |
| R2F6 numeric revision informational-only | contract | PASS | PASS | F6 still fixed |
| R2F7 partition resolution timing | contract | PASS | PASS | F7 still fixed |
| R2Q1 explicit-zero admission verdict | contract | PASS | PASS | Q1 still answered |
| R2Q2 downgrade stance | contract | PASS | PASS | Q2 still answered |
| R3N1 request/ack gate closes bootstrap window | contract | PASS | FAIL | **N1 FIXED** (remedy-neutral) |
| R3N1 risk row + bootstrap test entry aligned | contract | PASS | FAIL | round-2 ask's second half done |
| R3N1Exhibit budget under latency | exhibit | PASS | PASS | concurrency=1 with maxUnavailable=1, completes |
| R3N1Exhibit child controller down | exhibit | PASS | PASS | fail-safe stall after first bump, no completion |
| R3N1Exhibit metadata-only window | exhibit | PASS | PASS | request alone holds; control without it: concurrency=2 |
| R3N1Exhibit A->B->A fresh request | exhibit | PASS | PASS | ready-window 0 ticks; completes |
| R3M1 conversion stash entry + wording catches | contract | **FAIL** | FAIL | M1 residual reproduced (3 sub-checks) |
| R3F9 request-writer scope covers repair writes | contract | **FAIL** | n/a | F9 reproduced (new) |
| R3F10 request-strip fallback stated | contract | **FAIL** | n/a | F10 reproduced (new) |
| R3Q3 unbudgeted convergence justified | contract | **FAIL** | n/a | Q3 reproduced (new) |

Code layers at f711893c (code byte-identical to merge-base outside keps/):
unit `internal/controller/workloads` PASS; envtest `test/envtest/testcase/{rbg,restart_policy,webhook}` PASS (see results/reverify/).

## Round 4 (head 5f14daaf, delta f711893c..5f14daaf)

The round-4 revision ("kep: correct the description related to Round 3",
+71/-22, KEP-only) makes six substantive changes, all verified this round:

1. **v1alpha1 round-trip guarantee narrowed** (risk row, scenario table,
   appendix): the conversion webhook no longer claims losslessness;
   `groupTemplate.labels`/`annotations` cannot round-trip through v1alpha1 and
   such a write changes revision identity and rolls every child; rolling sets
   must be written through v1alpha2. Closes the C4359588718 advisory extension
   of M1 on the design-text side (the stash/restore *test-plan entry* remains
   owed — M1 stays open). Guard: `TestVerify_R4V1alpha1_NotLosslessStatedConsistently`
   (red at f711893c, green at 5f14daaf).
2. **partition may exceed replicas**: admission does not bound it, the
   validating rule does not match `rolebasedgroupsets/scale`, the controller
   resolves `min(partition, replicas)` (StatefulSet parity — confirmed:
   StatefulSet validation bounds partition only at >= 0). Guard:
   `TestVerify_R4Partition_ClampAndAdmissionStated`. New finding **F11**: the
   scenario row says "Rationale in the Appendix" but the Appendix carries no
   such entry — dangling cross-reference, reproduced RED at head
   (`TestVerify_R4F11_AppendixRationaleForPartitionClamp`), bites-checked with
   a simulated appendix entry (flips green, reverted clean).
3. **canonical payload requirement** for ControllerRevision data (sorted keys,
   standard HTML escaping; byte-wise immutability vs strategic-merge
   re-serialization; GC orphan connection). Text contract
   `TestVerify_R4CR_CanonicalPayloadRequirement` green at head. The apiserver
   mechanism is **empirically confirmed** at the integration layer:
   `TestVerify_R4CR_ControllerRevisionDataImmutableUnderPatch` (envtest,
   kube-apiserver 1.34.1) — SMP and JSON-merge metadata patches on a
   struct-ordered (unsorted) `data` are REJECTED as Invalid "field is
   immutable"; a canonically sorted payload survives both patch flavors
   byte-identical. Source-verified at k8s.io/kubernetes v1.34.2:
   `pkg/apis/apps/validation/validation.go:366` (Data immutable),
   `apimachinery/pkg/api/equality/semantic.go` (no RawExtension equality →
   byte-wise), `pkg/controller/garbagecollector/patch.go:92` (GC orphans with
   SMP first, JMP fallback only on UnsupportedMediaType → an Invalid rejection
   is terminal for the orphan attempt, blocking the owner's orphan finalizer).
4. **surge sized from the live availability budget** (serving-only cover;
   headroom = ready base count above the floor; existing surge subtracted).
   Formula self-consistency re-derived against both worked examples
   (4/5/0 → four surge; maxUnavailable=1 → three; below-partition failure →
   headroom 0, surge = serving in-scope). Guard `TestVerify_R4Surge_LiveBudgetSizingStated`.
   New question **Q4**: the new row's "The failed group never becomes an
   update candidate" is unscoped and collides with the availability
   paragraph's budget-free repair of already-unready outdated groups
   (`TestVerify_R4Q4_FailedGroupCandidacyClarified` RED at head; simulated
   scoping clause flips green, reverted clean).
5. **orphan adoption scenario row** claiming parity with the legacy path.
   Code facts verified against the PR BASE tree a2e797c6 (`RBG_BASE_ROOT`;
   the PR head's non-KEP code is the stale merge-base 0821cb5b):
   children listed by `GroupSetNameLabelKey`; `isOwnedOrAdoptable` = no
   controllerRef + name label, else UID match; adoption precedes scaleUp;
   collision path detects foreign UID. CONFIRMED —
   `TestVerify_R4Orphan_LegacyAdoptionFactsMatchKEP`.
6. **requeue scope pinned** (unacknowledged request = the barrier's only
   periodic requeue; neither controller polls) + W1 wording fix ("admission
   check" → "availability gate"). Guards `TestVerify_R4Requeue_ScopeStated`,
   `TestVerify_R4W1_AvailabilityGateWording` — W1 FIXED this round.

### Observed vs expected (head 5f14daaf / previous head f711893c)

| Test | Polarity | 5f14daaf | f711893c | Meaning |
|------|----------|----------|----------|---------|
| C1-C8 premise/code-fact contracts | contract | PASS | PASS | KEP premises still true; PR head code unchanged outside keps/ (byte-identical to merge-base 0821cb5b) |
| R3F1 barrier section | contract | PASS | PASS | F1 still fixed (section unchanged this round) |
| R2F2 / R3F3F4 / R2F5 / R2F6 / R2F7 / R2Q1 / R2Q2 | contract | PASS | PASS | F2-F7, Q1, Q2 still fixed |
| R3N1 contracts + exhibits (6) | contract | PASS | PASS | N1 still fixed (request/ack paragraphs refined, mechanism intact) |
| R4W1 availability-gate wording | contract | PASS | FAIL | **W1 FIXED this round** |
| R4Partition clamp + admission stated | contract | PASS | FAIL | round-4 partition rule present |
| R4F11 appendix rationale for clamp | contract | **FAIL** | SKIP | **F11 reproduced** (dangling "Rationale in the Appendix") |
| R4CR canonical payload text | contract | PASS | FAIL | round-4 requirement present |
| R4CR apiserver hazard (envtest 1.34.1) | contract | PASS | n/a | KEP mechanism claim CONFIRMED empirically |
| R4Orphan legacy parity (base a2e797c6) | contract | PASS | FAIL | KEP row matches current main |
| R4Surge live-budget sizing | contract | PASS | FAIL | round-4 surge rule present, self-consistent |
| R4Q4 failed-group candidacy clarified | contract | **FAIL** | SKIP | **Q4 reproduced** (unscoped "never becomes an update candidate") |
| R4V1alpha1 not-lossless narrowed | contract | PASS | FAIL | C4359588718 design-text half closed |
| R4Requeue scope stated | contract | PASS | FAIL | round-4 requeue pin present |
| R3M1 stash/restore + wording catches | contract | **FAIL** | FAIL | M1 residual still open (design narrowing done; test entry + wording catches unchanged at :532/:571) |
| R3F9 repair-write request scope | contract | **FAIL** | FAIL | F9 still open (writer widened to "every budgeted child write" + metadata-only, but repair/one-shot-rewrite still unnamed) |
| R3F10 request strip fallback | contract | **FAIL** | FAIL | F10 still open (consumer section unchanged) |
| R3Q3 unbudgeted convergence justified | contract | **FAIL** | FAIL | Q3 still open (sync semantics unchanged) |

Repo code layers re-run at the grafted head (docs-only delta, same code as
round 3): `go test ./internal/controller/workloads` ok 3.6s; envtest rbg ok
47.9s (k8s 1.31.0 assets). CI at 5f14daaf: envtest/build/unit-test/lint/
markdown-check/copyright-check/e2e-matrix-coverage/check-changes SUCCESS; e2e
variants SKIPPED (docs-only path) — observed rollup, attribution unknown.

Harness notes: F11/Q4 SKIP (not FAIL) at f711893c because their anchors are
round-4 text — they are new findings first seen at 5f14daaf. The R4Orphan
code-fact contract evaluates against `RBG_BASE_ROOT` (a base checkout) rather
than the grafted head, because the PR head's controller code predates the
base movement that introduced the orphan-adoption path the KEP now references.
