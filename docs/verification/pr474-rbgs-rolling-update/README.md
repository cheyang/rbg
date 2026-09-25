# PR #474 verification — RoleBasedGroupSet rolling update

PR: https://github.com/sgl-project/rbg/pull/474 (`feat: support RoleBasedGroupSet rolling update`, head `5bfa08b7`)
Branch: `verify/pr474-rbgs-rolling-update` (based on the PR head). **Production code untouched** — the branch adds only `docs/verification/**` and `verify_pr474_*_test.go` files.

Round 1 of the review pipeline (find → verify → publish). Round-1 review by diw-zw
(on `197548de`) had raised two blockers; the author rewrote the change as `5bfa08b7`.
This round re-checks those fixes and verifies new findings, each at the cheapest layer
that exercises it, plus a live layer on a dedicated ACK cluster.

## Premise (P0) — Confirmed

> "Today that propagation is un-paced: when the template changes, every outdated child is
> updated within a single reconcile, with no ordering and no availability gating. For a set
> of N groups that means all N groups can restart at once — availability might drop to zero
> for the whole set." (PR body)

- **Unit, against base `fc201cdf` (no patch):** the pre-existing
  `TestRoleBasedGroupSetReconciler_Reconcile_OptimizedOrder` passes — one `Reconcile`
  updates every outdated child in place (`results/unit-base-p0.txt`).
- **Live, base binary on the real cluster:** after the template patch, BOTH children
  carried the new template within 1s (`t+1s`), same UIDs, and `readyGroups` dropped to
  **0 for t+2s..t+4s** — both groups' pods rolled simultaneously
  (`results/base-timeline.log`).
- **Live, PR-head binary:** the same template change rolled one group at a time:
  `Recreating outdated rollout-demo-1` at 20:47:01.5, `rollout-demo-0` at 20:47:05.9 —
  the second delete only after the first replacement was Ready; `readyGroups` never fell
  below 1 (`results/head-timeline.log`).

Premise **Confirmed**: the problem exists on base, the patch's component matches, and the
head behavior visibly fixes it.

## Observed-vs-expected

| ID | Claim | Layer | Polarity | Verdict | Evidence |
|----|-------|-------|----------|---------|----------|
| P0 | un-paced propagation on base | unit + live | contract | **Confirmed** | above |
| F1 | v1alpha1 round trip silently drops `spec.rolloutStrategy` (and the new status counters) | unit + envtest + live | contract | **Confirmed (3 layers)** | unit: `TestVerifyPR474_RolloutStrategySurvivesV1alpha1RoundTrip` RED on head; envtest: ginkgo F1 spec RED through the real API server with the PR-head conversion webhook; live: `F1_LIVE_CONFIRMED` on the ACK cluster (create v1alpha2 with strategy → `kubectl replace` via v1alpha1 with an unrelated label → stored `.spec.rolloutStrategy` gone), `results/f1-conversion.log`. Reproduced on two clusters. |
| F2 | the scalingAdapter-in-groupTemplate guard is create-only; an update newly enabling it is accepted | unit + envtest | contract | **Confirmed** | `TestVerifyPR474_UpdateEnablingScalingAdapterRejected` RED; envtest F2 spec RED through the real admission chain. Compat (sets already carrying the adapter stay updatable) GREEN and pinned by `TestVerifyPR474_UpdateOnSetAlreadyCarryingAdapterAllowed`. |
| F3 | round-1 blocker re-check: partition>0 + maxSurge>0 must converge and release surge | unit + live | contract | **Fixed on head** | author's `TestRollingUpdate_PartitionReleasesSurgeAfterInScopeRollout`, `TestRollingUpdate_StandingCanaryKeepsSurge` PASS; live e2e `surge-backed partition rollout releases surge when the batch completes` (results/e2e-rbgset-head.log) |
| F4 | round-1 blocker re-check: replicas-only diff with legacy strategy spelling must scale in place, not recreate | unit | contract | **Fixed on head** | `TestVerifyPR474_LegacySpellingReplicasOnlyScalesInPlace` PASS (zero deletes, replicas applied); author's `TestRollingUpdate_ReplicasOnlyChangeIsScaledInPlace` PASS |
| F5 | webhook accepts negative maxSurge/maxUnavailable/partition; maxUnavailable=-1 with surge wedges the rollout forever (budget = -1+readySurge = 0 → never deletes) | unit + envtest | contract + canary | **Confirmed** | `TestVerifyPR474_NegativeRolloutBudgetsRejected` RED ×3; canary `TestVerifyPR474_NegativeMaxUnavailableStallsRollout` PASS(=bug present): no delete issued despite a ready surge; envtest F5 spec RED |
| F6 | partition>0: a held-back group that stops serving pins surge forever while status reports `RolloutComplete` | unit | canary | **Confirmed** | `TestVerifyPR474_HeldBackBrokenGroupPinsSurgeWhileStatusComplete` PASS(=behavior present): surge not reclaimed, `Rolling=False/RolloutComplete` |
| F7 | while `paused`, a replicas-only in-place scale also propagates template labels/annotations, contradicting "paused freezes template propagation" | unit | canary | **Confirmed** | `TestVerifyPR474_PausedScaleOnlyPropagatesTemplateMetadata` PASS(=behavior present) |
| F8 | the PR adds ~6 min of e2e to a suite already flaking at the 30m `-timeout`; both `e2e-test` and `e2e-test-manifest` failed on the head sha by timeout (main fails the same way) | CI logs | n/a | **Noted (non-blocking; main is also red)** | workflow runs 35570675415 (PR head) vs 35559402297 (main) |

The full L1 unit sweep on the head (all of `api/...` + `internal/controller/workloads`):
only the three contract tests fail; every PR test and every canary passes
(`results/unit-head.txt`).

## Round 2 — second reviewer's three P1s, independently verified

A follow-up review (by a different reviewer, unpublished) raised three P1 findings against
the same head. Each was re-derived from the code and then proven with a contract probe in
`internal/controller/workloads/verify_pr474_rollout_test.go` (`results/unit-head-round2.txt`):

| ID | Claim | Verdict | Evidence |
|----|-------|---------|----------|
| F9 (their P1-1) | surge reclaimed earlier in the same reconcile still counts in the delete budget (`readySurge` reads the pre-reclaim snapshot) | **Confirmed**: 3 serving base + 1 ready surge, `maxUnavailable: 1`, `maxSurge` 1→0 → surge reclaimed AND 2 serving base deleted, leaving 1 serving instead of ≥2 | `TestVerifyPR474_ReclaimedSurgeDoesNotWidenBudget` RED on head ("2 is not ≤ 1"); green with `children.surge = keptSurge` |
| F10 (their P1-2) | a retained surge group never follows subsequent template changes | **Confirmed**: `maxUnavailable: 0, maxSurge: 1`, surge stuck unready on superseded template B, template corrected to C → budget stays 0, no base can roll, repeated reconciles never recover; the same omission stalls a standing canary on the old template | `TestVerifyPR474_SurgeGroupFollowsTemplateChanges` RED on head (stale surge never deleted) |
| F11 (their P1-3) | a budget-blocked serving group `break`s the loop, skipping budget-free repair of broken lower-ordinal groups | **Confirmed**: s-2 serving on bad template B (budget-blocked), s-1 stuck unready on B → s-1 never replaced → the rollback wedges permanently. The author's `NotServingOutdatedBypassesBudgetOnly` only covers the broken group at the highest ordinal | `TestVerifyPR474_BudgetBlockedServingGroupDoesNotStopBrokenRepair` RED on head; green with `break`→`continue`, and the author's full test package still passes under that change |

Harness-bites for round 2: minimal fixes applied per finding, all three probes flipped green,
then reverted; production diff empty again.

Their other adjustments, evaluated: the F6 fix direction is better than my original wording
(fix the status/`Rolling` reporting, do NOT scope `rolloutComplete`'s serving requirement —
the surge is load-bearing protection when a held-back group breaks); the F5 fix should
validate the raw IntOrString (a negative percentage can round to 0 and escape a
resolved-value check) and can additionally live in the CRD as CEL
(`x-kubernetes-validations`), not only in the webhook; the CI-timeout note is moot after
main merged #485 (30m→45m).

## Harness-bites check (done this round)

The three contract tests were proven to detect their fixes: with a minimal
annotation-preservation patch in `rolebasedgroupset_conversion.go` (stash
rolloutStrategy+status counters on ConvertFrom, restore on ConvertTo), F1 went green;
with a newly-enabled-only check in the RBGS validator's update path, F2 went green while
the compat test stayed green; with negative-value rejection in `validateGroupSetRollout`,
F5 went green. All fix patches were then reverted; the production diff of this branch is
empty (`git diff origin/pr/474 -- ':!docs/verification' ':!*verify_pr474*'` shows nothing).

## Layers & how to run

### L1 — unit (deterministic)

```bash
git fetch https://github.com/sgl-project/rbg.git pull/474/head
git checkout <branch-or-head>
git checkout <verify-branch> -- api/workloads/v1alpha1/verify_pr474_conversion_test.go \
    api/workloads/v1alpha2/verify_pr474_admission_test.go \
    internal/controller/workloads/verify_pr474_rollout_test.go
GOFLAGS=-mod=vendor go test ./api/... ./internal/controller/workloads/ -run 'TestVerifyPR474' -count=1 -v
```

Expected on the un-fixed code: the three `...Rejected`/round-trip contract tests FAIL,
the four canary/compat tests PASS. After a fix: contract tests go green; the canaries
(F5-stall, F6, F7) flip to red and should be inverted or retired.

### L2 — envtest (real API server, PR-head admission + conversion chain)

```bash
KUBEBUILDER_ASSETS=$(setup-envtest use 1.31.0 -p path) \
  go test ./test/envtest/testcase/webhook/ -count=1 -ginkgo.focus 'PR474 verification'
```

The additive spec file installs its own RoleBasedGroupSet ValidatingWebhookConfiguration
and a conversion stanza on the CRD (both pointed at the suite's local webhook server, the
PR head code), removing them via DeferCleanup. Expected today: F1/F2/F5 specs RED,
controls GREEN. (Ran on the sandbox: `results/l2-envtest-focused.log`.)

### L3 — live (dedicated ACK cluster, driven from the remote sandbox)

The cluster is reached from the sandbox host (`root@43.99.38.217`, kubeconfig
`~/.kube/config`); the binaries under test run out-of-cluster on the sandbox.

```bash
bash scripts/live-prepare.sh      # PR CRDs (server-side) + chart stack if absent + ns
bash scripts/live-conversion.sh   # F1 round trip through the in-cluster conversion webhook
bash scripts/live-rollout.sh base # P0: un-paced propagation, readyGroups dip to 0
bash scripts/live-rollout.sh head # head: paced recreate, one group at a time
bash scripts/live-e2e.sh          # PR's own v1alpha2 "rbgset controller" e2e cases
bash scripts/live-restore.sh      # restore webhook policies + controller replicas
```

Notes on the live rig:
- `live-prepare.sh` uses server-side apply (client-side apply overflows the 256KiB
  last-applied annotation on these CRDs) and adds the CRD conversion stanza when the
  cluster lacks one (CA bundle taken from the manager-synced admission webhook config).
- During the controller-behavior windows the in-cluster controller is scaled to 0 and
  the rbgs admission webhooks are relaxed to `Ignore`; `live-restore.sh` puts everything
  back (the deployment's replica count is backed up first).
- `live-e2e.sh` additionally drops the RBG/RBGS CRD conversion to `None` for the run:
  the e2e framework's `AfterEach` issues v1alpha1 `DeleteAllOf` calls, which a dead
  conversion webhook would 500. The stanza is restored verbatim afterwards.
- An earlier run of this harness on the previous cluster (since rebuilt) showed
  interference from leftover state; all results reported above are from the fresh
  cluster, where the head rollout was textbook-paced and the base run dipped to zero.

## Continuing after the fix (possibly on another machine)

```bash
bash scripts/re-verify.sh        # fetches the current PR head via the manifest's pr URL,
                                 # grafts this harness onto it, runs L1+L2, prints
                                 # per-finding Fixed / Still-broken / Harness-update
```

- F1/F2/F5 contract tests must go green on the fix.
- Canaries F5-stall/F6/F7 must FLIP to fail on the fix; then invert them into contract
  tests (or delete the F5-stall canary — its setup becomes unreachable once the webhook
  rejects negatives).
- L3 re-run needs the sandbox host above (or adapt scripts to another cluster); the
  observable signals are in each script's header.

## Cluster used

Dedicated ACK cluster via the remote sandbox (`root@43.99.38.217`), 3 nodes
(`cn-hongkong.*`, v1.36-aliyun), exclusive use for this review. The cluster was rebuilt
mid-investigation; every reported result is from the rebuilt cluster, except the first
F1 live confirmation, which was additionally reproduced there. The PR CRDs remain
applied; the chart stack (`rbg-system/rbgs-controller-manager`, released image
`rolebasedgroup/rbgs-controller:v0.9.0-eadb6c20`) was restored to 2 replicas; the
`pr474-verify` namespace was deleted.
