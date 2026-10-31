# Verification — PR #474 "feat: support RoleBasedGroupSet rolling update"

Reviewer: Codex (debate pipeline, Reviewer B). Reviewed head: `44241903`
(`.last-reviewed`). Production code untouched — this branch adds only
`docs/verification/**` and `*_verify_test.go` files.

## Premise (P0)

> "when the template changes, every outdated child is updated within a single
> reconcile, with no ordering and no availability gating" — PR body.

**Verdict: Confirmed** (run against base `0821cb5b`, results/p0-base.txt): one
`Reconcile` moved all 3 children to the new template at once. The same test passes
on the PR head with `rolloutStrategy` unset, which also confirms the
backward-compatibility claim (static path unchanged).

## Findings

| id | claim | polarity | layer | status on 44241903 |
|----|-------|----------|-------|--------------------|
| F1 | v1alpha1↔v1alpha2 conversion drops `spec.rolloutStrategy` and the rollout status fields (`currentRevision`, `updateRevision`, counters); v1alpha1 is still served, so a full-object v1alpha1 write silently reverts a rolling set to the un-paced static path mid-rollout | contract | unit | **RED = confirmed bug** (`TestVerify_ConversionPreservesRolloutStrategy` fails) |
| F3 | webhook rejects integer `maxUnavailable: 0` + `maxSurge: 0` but accepts `"0%"`/`"0%"` (resolves to 0/0 for any replica count); the controller then silently clamps maxUnavailable to 1, rewriting explicit user intent | canary | unit | canary passes (pins current behavior); flip when fixed |

## Guard-rail tests (no finding — the PR's core claims, verified)

| id | claim | layer | status |
|----|-------|-------|--------|
| G1 | paced recreate: never deletes ready groups beyond `ready-(replicas-maxUnavailable)`; converges; `RolloutComplete`; steady state does NOT requeue | unit | green, converged in 12 steps |
| G2 | `maxUnavailable: 0` + `maxSurge: 1`: serving never drops below replicas; surge created then reclaimed | unit | green |
| G3 | `partition: 2`: ordinals 0,1 keep old template and identity; a deleted held-back group is rebuilt at the OLD revision | unit | green |
| G4 | template flip-flop A→B→A converges | unit | green |
| G5 | replicas-only template diff scales in place, no recreation | unit | green |
| G6 | scale-in during a rollout is budget-gated and converges | unit | green |
| G7 | same drive against a REAL API server (envtest, real CRDs/defaulting): converges, each child recreated exactly once (no defaulting-induced recreate loop), foreground delete + UID precondition work | integration | green (results/l2-envtest.txt) |

## Harness-bites check (Step 4)

Two production perturbations were applied temporarily and reverted
(results/harness-bites.txt):

1. Removing the budget gate in `deleteGroupSetOutdatedChildren` → G1/G2 fail
   ("deleted 3 ready groups with ready=3 replicas=3 maxUnavailable=1").
2. Removing the rebuild-at-current rule in `fillMissingGroupSetBaseOrdinals` →
   G3 fails ("must come back on the previous template").

Production diff is empty after the revert.

## How to run

```bash
# unit layer (fake client)
go test ./internal/controller/workloads/ ./api/workloads/v1alpha1/ ./api/workloads/v1alpha2/ -run 'TestVerify_' -count=1 -v

# integration layer (needs envtest assets)
KUBEBUILDER_ASSETS=$(ls -d $HOME/.local/share/kubebuilder-envtest/k8s/*-linux-amd64 | head -1) \
  go test ./internal/controller/workloads/ -run 'TestVerify_Envtest' -count=1 -v -timeout 8m
```

Note: on the reviewed head the unit run FAILS on
`TestVerify_ConversionPreservesRolloutStrategy` — that red IS the F1 reproduction.
Expected state after the author fixes the conversion: all green, and the two F3
canaries flip red (invert or promote them then).

## Continuing after the fix

```bash
git fetch https://github.com/cheyang/rbg.git verify/rbgs-rolling-update-codex
git checkout verify/rbgs-rolling-update-codex
bash docs/verification/rbgs-rolling-update/scripts/re-verify.sh   # resolves the current PR head itself
```

re-verify.sh grafts the harness onto the current PR head, runs unit+integration, and
prints per finding Fixed / Still-broken / Harness-update (F1 fixed iff its contract
test goes green; F3 fixed iff its canaries flip). Live e2e (L3) is covered by the
repo's own kind-based suite (`make test-e2e`), which is green in CI.

Kickoff prompt for a fresh agent: "Continue verifying sgl-project/rbg PR #474 from
branch verify/rbgs-rolling-update-codex on the cheyang fork. Read
docs/verification/rbgs-rolling-update/README.md and run scripts/re-verify.sh."

## Round 2 — head `9e423e86` (delta `44241903..9e423e86`: v1alpha1 round-trip fix, envtest suite, InPlaceUpdate strategy)

Reviewed head advanced to `9e423e86` (`.last-reviewed`). All round-1 guard rails
(G1-G7) still pass unmodified on the new head (results/l1-unit-round2.txt,
results/l2-envtest-round2.txt).

### Round-1 findings re-verified

| id | status on 9e423e86 | evidence |
|----|--------------------|----------|
| F1 (blocker: conversion dropped rolloutStrategy) | **FIXED** — annotation stash in `rolebasedgroupset_conversion.go` | contract test now green, extended to InPlaceUpdate + full status block (`TestVerify_ConversionPreservesInPlaceStrategy`) |
| F3 (minor: "0%"/"0%" admission gap) | **FIXED** — declared 0/0 rejected in every spelling for Recreate; InPlace rules enforced; clamp is now only the documented fallback for positive percents resolving to 0 | flipped canary promoted to contract tests (`TestVerify_WebhookRejectsPercentZeroZero`, `TestVerify_WebhookInPlaceUpdateRules`, `TestVerify_ControllerClampsPercentZeroZero_Contract`) |
| F8 (minor: no envtest drive) | **FIXED** — author added `test/envtest/testcase/rbgs_rolling` running real controllers | CI envtest job green on 9e423e86 |
| F4 / F5 / F6 / F7 | unchanged (F6 shrinks: InPlaceUpdate, now the default, syncs metadata-only changes in place; Recreate still recreates) | code inspection |

### Round-2 additions

| id | claim | polarity | layer | status |
|----|-------|----------|-------|--------|
| G8 | InPlaceUpdate pacing: never recreates a base child (UID/name stable), at most one child in flight under maxUnavailable=1, a stalled in-flight child holds the whole budget (no further child touched, Rolling stays True), steady state quiet | contract | unit | green |
| G8b | `groupSetChildReady` barrier semantics: Ready + RollingUpdateInProgress=True at current generation is NOT serving; stale-observation barrier is NOT serving; absent barrier (old RBG controller) is serving | contract | unit | green |
| G9 | InPlaceUpdate against a real API server: UIDs stable, stalled child holds the budget, converges under real CRD defaulting, exactly one generation bump per child for the template change and no churn afterwards | contract | integration (envtest) | green (results/l2-envtest-round2.txt) |
| F9 | replicas-only template diff under InPlaceUpdate is budget-paced (one child per readiness window) instead of immediate like in Recreate mode; the PR body claims scaling is never paced | canary | unit | canary passes = behavior pinned; intent questioned |

Harness-bites round 2 (results/harness-bites-round2.txt): disabling the budget gate
turns G8's stall test red; dropping the stale-observation guard turns G8b red.
Production diff empty after reverts.

### Note on the fake client

The controller-runtime fake client never manages `metadata.generation`, which would
hide the generation-lag barrier the InPlace budget relies on. The round-2 unit drive
(`rolebasedgroupset_inplace_verify_test.go`) emulates the API server: it bumps a
child's generation itself when the controller rewrites the child spec, and plays the
child RBG controller by flipping `status.observedGeneration` after a cooldown. The
envtest layer (G9) needs no emulation and is the authoritative check.

## Round 3 — head `98720938` (delta `9e423e86..98720938`: example manifest fix + PR body rewrite)

The code delta is exactly one file: `examples/basic/rbgs/rbgs-rolling-update.yaml`
— the stale "InPlaceUpdate is reserved; the CRD enum rejects it" comments are
replaced with accurate descriptions (closes Copilot finding C4242488204). The PR
body was rewritten and is now accurate on every point checked: strategy field table
(type default InPlaceUpdate, maxUnavailable/maxSurge semantics), no paused
field/condition, ControllerRevision-based design, per-strategy scale-only behavior,
all 22 unit + 10 e2e test names exist in the tree, webhook rules match
`validateGroupSetRolloutStrategy`, and the controller-behavior narrative matches
`rolebasedgroupset_rolling.go`.

Re-verify (results/reverify/ round 3): all contract tests green on the new head;
F9 canary still passes (budget-paced replicas-only under InPlaceUpdate — now
correctly documented in the body).

### Round-3 addition

| id | claim | polarity | layer | status |
|----|-------|----------|-------|--------|
| F10 | body claims held-back ordinals are "excluded from ... currentReplicas"; `updateRollingGroupSetStatus` counts them (no partition filter on the CurrentReplicas branch; author's own `TestSyncRollingGroupSet_StrategiesRecoverMissingOrdinals` expects CurrentReplicas=2 with partition=3) | canary | unit | passes = body clause inaccurate (one-line body fix) |

## Round 4 — head 8c024b28 (delta 98720938..8c024b28)

The round-4 delta is one commit, `8c024b28 fix(rbgs): exclude conversion
annotations from RBGSet revision and sync`: it moves the v1alpha1 conversion
annotation keys into `systemManagedAnnotationKeys` (rolebasedgroupset_revision.go).
That map feeds three consumers: (a) revision hashing/drift via
`normalizedGroupSetTemplate`, (b) metadata sync via `syncRBGMetadata` /
`needsTemplateAnnotationUpdate`, and (c) child creation, because
`newRBGForSetRevision` builds children from the normalized `revision.template`.

New contracts (`internal/controller/workloads/rolebasedgroupset_conversion_annotation_verify_test.go`,
all contract polarity, differential across both heads):

| id | test | claim | 98720938 | 8c024b28 |
|----|------|-------|----------|----------|
| R4FIX | TestVerify_ConversionAnnotation_RevisionIgnoresCoordination | revision hash ignores the coordination annotation (no churn) | RED (churn real) | **GREEN** |
| R4FIX | TestVerify_ConversionAnnotation_SyncPreservesChildCoordination | syncRBGMetadata preserves the child-side coordination annotation (no wipe) | RED (wipe real) | **GREEN** |
| R4M1 | TestVerify_ConversionAnnotation_RollingCreatePropagatesToChildren | rolling-path create propagates the coordination annotation to children (both strategies) | GREEN | **RED = regression** |
| R4M1 | TestVerify_ConversionAnnotation_CoordinationValueChangeReachesChildren | a coordination-only value change reaches settled children | GREEN | **RED = regression** |
| R4M1 | TestVerify_ConversionAnnotation_StaticPathStillPropagates | static path unaffected | GREEN | GREEN |

R4M1 mechanism: with the annotation stripped from `revision.template`,
`newRBGForSetRevision` creates children WITHOUT it, and since it is also stripped
from drift comparison the children classify as up-to-date forever — no sync ever
repairs them. The RBG reconciler's `EnsureV1alpha1CoordinatedPolicy`
(rolebasedgroup_controller.go:229) reads that annotation to migrate v1alpha1
CoordinationRequirements into a CoordinatedPolicy, so rolling-path children of a
v1alpha1-originated set silently lose their coordination policy.

Harness-bites (results/harness-bites-round4.txt): on 8c024b28, deleting the
coordination key from `systemManagedAnnotationKeys` flips R4FIX RED and R4M1 GREEN —
the tests key on exactly the delta's mechanism. Production diff empty afterwards.

Re-verify this round (results/reverify/): all carried contracts FIXED on 8c024b28;
canaries F9 (budget-paced replicas-only under InPlaceUpdate) and F10 (body
currentReplicas clause) STILL-PRESENT, i.e. the pinned behaviors are unchanged.

## Round 5 — head 4a9043c3 (delta 8c024b28..4a9043c3)

Three fix commits, all verified on this harness:

- `6dd7de5f` carry v1alpha1 conversion intent through the rolling path —
  **closes R4M1/F11 and F13**: `newRBGForSetRevision` materializes live
  system-managed keys onto created children, an unbudgeted convergence loop in
  `syncRollingGroupSet` repairs them on settled/partition-held children, and the
  PodGroupPolicy stash is snapshotted onto its owning ControllerRevision
  (removal gated on the previously owned snapshot). Also closes F15 (shared
  constants everywhere) and the RBGSet half of C4242488155 (`copyAnnotations`
  before any mutation in ConvertTo/ConvertFrom).
- `51bf17bb` request/ack handshake — **answers C4273377135**: budgeted child
  writes carry `rbg.workloads.x-k8s.io/rollout-request`; the child controller
  acks via `status.observedRolloutRequest` only after every role workload has
  rolled out (`AddGroupAnnotationsToRoleHashes` folds the pod-shaping group
  annotations into role hashes so metadata-only changes still roll workloads).
- `4a9043c3` Recreate replicas-only updates are now budget-paced (in place) —
  the F9 Recreate canary **flipped as intended**; both scale-only tests are now
  contracts. NOTE: the PR body's "scaling stays immediate" / "Recreate …
  applied in place immediately, bypassing the budget" clauses are now stale
  (tracked under F10), and the new annotation/status field are undocumented
  there.

Harness updates (additive): the simulated child controllers now echo the
rollout-request acknowledgement (unit + envtest sims); without that the new
budget gate wedges every paced rollout — a harness-model gap, not a product
defect: the author's own envtest runs the real RBG reconciler and acks
naturally (rbgs_rolling suite green, 212s).

New contracts (rolebasedgroupset_round5_verify_test.go, all green on 4a9043c3):

| id | test | claim |
|----|------|-------|
| R5ACK | TestVerify_RolloutAck_MetadataOnlyTemplateChangePaced | metadata-only template change is paced one child per ack window; with the ack frozen the budget holds forever (no further writes); unfreeze converges with UIDs preserved |
| R5PGP | TestVerify_ConversionAnnotation_PodGroupPolicyRollingLifecycle | policy stash: create propagation, in-place value convergence (unversioned), gated removal, no resurrection, steady-state quiet |
| R5PGP | TestVerify_ConversionAnnotation_PodGroupPolicyHeldOrdinalKeepsSnapshot | a partition-held ordinal keeps the policy its pinned revision owns while the in-scope ordinal rolls to the new revision+policy |

Bites (results/harness-bites-round5.txt): neutering `requestGroupSetChildRollout`
turns R5ACK RED; neutering the revision snapshot/template re-attach turns the
R5PGP lifecycle RED; production tree restored byte-identical after both.

Re-verify (results/reverify/): every carried contract FIXED on 4a9043c3 —
F1, F3, G1-G9 (unit+integration), F9 (promoted), R4FIX, R4M1, R5ACK, R5PGP.
F10 canary STILL-PRESENT (doc drift unchanged).

## Round 6 — head af9dd84b (rebased; 4 new commits 13cbcab4..af9dd84b)

Range-diff shows the 7 round-5 commits byte-identical through the rebase (one e2e
label change); the review delta is the four new commits plus the new upstream base.

Harness update (disclosed, not a product signal): 13cbcab4 replaced the rolling
path's 1s poll with event-driven progress (`changed || adopted`). The sim settle
loops keyed off requeue requests and exited mid-rollout (G5/F9/G8/R5ACK initial
failures, results/r6-reverify-initial-harness-failures.log); settle now stops at a
true world fixpoint (children + set status + sim timers). All re-run contracts pass
at head after the fix; the frozen-ack budget-hold phase is untouched.

New contracts (all GREEN at head af9dd84b unless noted; differentials in
results/r6-differential-4a9043c.log and results/r6-differential-13cbcab4.log):

| id | test | claim | previous head |
|----|------|-------|---------------|
| R6SURGE | TestVerify_R6_SurgeSizedFromLiveAvailability_Contract | #519: failed held-back group spends maxUnavailable yet surge is still created from live availability; in-scope base group waits for surge readiness; floor never breached; surge reclaimed at PartitionComplete | RED on 4a9043c (stall) |
| R6ADOPT | TestVerify_R6_RollingPathAdoptsOrphans_Contract | rolling path adopts label-matching orphans, UID preserved, higher ordinals still filled | RED on 4a9043c (create collision error) |
| R6ADOPT | TestVerify_R6_RollingPathRefusesForeignChild_Contract | foreign-UID child refused with error, never adopted | PASS both heads (pre-existing guard) |
| R6ADOPT | TestVerify_R6_TerminatingOrphanDoesNotBlockAdoption_Contract | terminating orphan skipped, live sibling adopted | RED on 4a9043c |
| R6REVADOPT | TestVerify_R6_OrphanRevisionHistoryReclaimed_Contract | orphan predecessor revisions adopted; protected (child-referenced) survive; unused pruned to 5, oldest first | RED on 4a9043c (no adoption, no pruning) |
| R6REVADOPT | TestVerify_R6_RevisionAdoptionRefusedWhenParentReplaced_Contract | stale-parent UID mismatch refuses adoption loudly | RED on 4a9043c |
| R6FINALIZE | TestVerify_R6_RequestlessLabelsOnlyCompletesInSameReconcile | requestless labels-only role change completes in the applying reconcile; no ack stamped, no requeue | RED on 13cbcab4 (wedge: barrier True + no requeue); RED on 4a9043c strict same-reconcile assert (2s poll cleared it next pass) |
| R6PARTITION | TestVerify_R6_PartitionAboveReplicasAdmitted_Contract | #521: partition>replicas admitted on create and metadata-only update; negative and >100% still rejected | RED on 4a9043c ("must be between 0 and replicas") |
| R6REVDATA | TestVerify_R6_ControllerRevisionCanonicalDataOrphanPatch (envtest) | canonical revision data accepts the GC strategic-merge orphan patch byte-identically; the legacy struct-order form is REJECTED ("data: field is immutable") — premise confirmed on a real apiserver | n/a (behavioral premise probe) |
| R6EMPTYANN | TestVerify_R6_EmptyRoleAnnotationsMapNoWedge (envtest) | C4358788576 CONFIRMED: roles[].annotations:{} (stored via raw patch, omitted from children via omitempty) makes rolesEqual DeepEqual mismatch → child perpetually outdated, rewritten every pass (rollout-request token 3→4 in one quiescent pass), rollout never completes | RED at head = bug present |

Author suites at head (with harness grafted): full unit sweep EXIT=0
(results/r6-full-unit-head.log), -race ./internal/controller/workloads EXIT=0
(results/r6-race-head.log), envtest rbgs_rolling 236.7s EXIT=0
(results/r6-author-envtest-head.log). CI: 22/22 SUCCESS on af9dd84b.

Re-verify (results/r6-reverify-final.log): every carried contract FIXED at head
(F1, F3, G1-G9, F9, R4FIX, R4M1, R5ACK, R5PGP + all R6 contracts except
R6EMPTYANN). F10 canary STILL-PRESENT (behavior pin; the body no longer makes the
contradicted claim — the round-5 body findings F1/F10 are resolved by the rewrite).
