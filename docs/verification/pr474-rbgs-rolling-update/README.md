# pr474-rbgs-rolling-update — review verification harness

Reproducible evidence for the findings raised while reviewing
https://github.com/sgl-project/rbg/pull/474 ("feat: support RoleBasedGroupSet
rolling update").

- **Round 1** (head `44241903`, base `0821cb5b`): P0 premise + POS1/POS2 + F1–F3.
- **Round 2** (head `9e423e86`, base branch tip `79f7e585`, merge base
  `0821cb5b` unchanged): delta `44241903..9e423e86` = author commits
  `f6019ead` (envtest + v1alpha1 round-trip — the round-1 consensus-blocker
  fix) and `9e423e86` (InPlaceUpdate rollout strategy). The base move
  `0821cb5b..79f7e585` touches neither the RBGS controller nor the rolling
  code, so the round-1 premise and contracts stay valid against the new base.
- **Round 3** (head `98720938`, same base tip `79f7e585`): history was
  **force-pushed/rewritten** (the three commits were re-authored; `9e423e86`
  is no longer an ancestor), but the tree delta `9e423e86..98720938` is
  exactly one file — `examples/basic/rbgs/rbgs-rolling-update.yaml` (the
  C4242488204 example-comment fix). The PR **body was fully rewritten** this
  round: it now documents the shipped API (InPlaceUpdate default, no `paused`,
  ControllerRevision design, real test names, budget-paced scale-only under
  InPlaceUpdate). F1 (the round-1/2 must-fix) is closed by
  `scripts/doc-match-r3.sh` (results/l1-doc-match-r3.log, 16/16 PASS) plus
  the full harness re-run on `98720938`.

Harness branch: `verify/pr474-rbgs-rolling-update-claude` (reviewer: cheyang's
fork). Production code is untouched — the diff is tests + docs only.

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit (fake client, in-package) | pacing, convergence, migration, resolver, webhook, static-path overwrite, InPlaceUpdate pacing/gating, v1alpha1 conversion round-trip | `go test ./internal/controller/workloads/ ./api/workloads/v1alpha2/ ./api/workloads/v1alpha1/ -run 'TestVerifyPR474' -count=1 -v` |
| 2. Integration (envtest, real API server + full controller stack) | paced rollout end-to-end incl. foreground deletion, legacy migration, surge-backed zero unavailability, InPlaceUpdate UID preservation | `KUBEBUILDER_ASSETS=$HOME/envtest-assets/k8s/1.37.0-linux-amd64 go test ./test/envtest/testcase/rbg/ -count=1 -ginkgo.focus 'PR #474' -timeout 20m` |
| 3. Live | **not run this round** — the debate pipeline mandates unit + integration only. A live cluster is reachable on this host (`kubectl get no` → 2 aliyun nodes); the PR's own `make test-e2e` (kind) covers the same scenarios if L3 is ever wanted. | — |

> envtest has no garbage collector. A foreground delete of a child RBG would
> hang Terminating forever (its RoleInstanceSets carry blockOwnerDeletion owner
> refs), so the integration harness ships `simulateGC`, which does what
> kube-controller-manager does on a real cluster: delete the blocking
> dependents, then drop the finalizer. Without it the rollout stalls after the
> first delete — an environment artifact, not a PR defect.

> Test polarity: **contract** tests assert intended behavior (red on buggy
> code, green when fixed). **Canary** tests assert current behavior (green now,
> flip red when the behavior changes — then invert them).

## Problem premise (P0)

Run against the **base** branch (`0821cb5b`) without the patch, because the
question is whether the problem exists today.

| | |
|---|---|
| Claimed symptom | "when the template changes, every outdated child is updated within a single reconcile, with no ordering and no availability gating. For a set of N groups that means all N groups can restart at once" |
| Linked issue | none — the PR body states the problem itself (no `fixes #N`) |
| Reported component | RoleBasedGroupSet controller's template propagation |
| Patched component | same (rolebasedgroupset_controller.go + new rolling/revision files) |
| Component match | Yes |
| **Verdict** | **Confirmed** |
| Evidence | On base, one `Reconcile` with 3 outdated ready children updated **all 3 in a single pass** (`Updating existing RoleBasedGroups count: 3` → 3 successful updates). results/premise-base.log |

The premise is valid: a whole-set simultaneous restart is reachable today on
the static path, and the rolling strategy is opt-in. The review question
becomes whether the new path is correct — covered by POS1/POS2 below.

## Summary of results

### Round 3 (head `98720938`, delta `9e423e86..98720938` — body rewrite + example fix)

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| F1 (round-1/2 must-fix) | PR body documents an API the code does not implement | doc | **Fixed** — body rewritten against the shipped API: `type` enum + `InPlaceUpdate` default, "no `paused` field or condition", ControllerRevision design, budget-paced scale-only under InPlaceUpdate, all 17 unit + 4 admission test names and all 10 e2e It descriptions now exist in the tree | results/l1-doc-match-r3.log (16/16 PASS, incl. the example manifest) |
| C4242488204 | Example manifest says InPlaceUpdate is "reserved; the CRD enum rejects it for now" | doc | **Fixed** — comments now describe Recreate vs InPlaceUpdate accurately and mark InPlaceUpdate as the default | examples/basic/rbgs/rbgs-rolling-update.yaml (the only file in the tree delta) |
| POS1/POS2/POS3/POS4 + F2/F8 contracts | Full regression suite on the new head | 1+2 | **Green** — 17/17 unit-layer tests, envtest `PR #474` suite ok (27.7s) | results/reverify-r3.log |
| F3, F7, F5, F6, Q1, C4242488155, C4242488120, C4242488140, C4242488181 | Open minor/nit findings from rounds 1–2 | 1 | **Still open** — tree is identical to `9e423e86` outside the example, so every code path they cite is unchanged (F3 canary still green) | results/reverify-r3.log; `git diff 9e423e86 98720938` = 1 file |

Residuals noted this round: the body's special note "`isServing` centralizes
readiness" names a symbol that does not exist (the function is
`groupSetChildReady` — the sentence names it correctly one clause later;
cosmetic), and the body still does not mention the v1alpha1
conversion-stash residual (F8b) — both fold into F1's fix as nits.

The force-pushed history re-authored the three PR commits (same content),
which is why the round-2 commit ids (`f6019ead`, `9e423e86`) no longer
resolve as ancestors of the head; the acceptance evidence references the
*tree*, which is what the contracts run against.

### Round 2 (head `9e423e86`, delta `44241903..9e423e86`)

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| F8 (round-1 consensus blocker) | A full-object write through the still-served v1alpha1 API silently dropped `spec.rolloutStrategy` + rollout status (mid-rollout revert to the un-paced static path) | 1 | **Fixed at `f6019ead`** — annotation stash round-trips both strategy types and all six status fields; garbage stash fails conversion; stash never leaks into hub storage | results/reverify-selftest-r2.log (TestVerifyPR474_V1alpha1RoundTrip_*) |
| F2 | Both-zero budget rule was spelling-sensitive | 1 | **Fixed at `9e423e86`** — round-1 canary flipped RED (percent spelling now rejected); promoted to contract | same log (TestVerifyPR474_Webhook_ZeroBudgetRejectedSymmetrically) |
| F4 (round-1 missingTests) | No envtest integration test drove the rolling loop | 2 | **Fixed** — author added `test/envtest/testcase/rbgs_rolling/` (full-stack, kubelet simulation) + webhook envtest; both suites ran green here | results/author-envtest-r2.log |
| POS3 | InPlaceUpdate: paced in-place spec updates (≤1 ready child/pass under maxUnavailable=1), never deletes an in-scope child, holds back while the previous child is still rolling, converges, UIDs stable, RolloutComplete | 1 | Confirmed (green) | results/reverify-selftest-r2.log (TestVerifyPR474_InPlaceUpdate_*) |
| POS4 | InPlaceUpdate on a real API server: template propagates, every child keeps its UID, rollout completes | 2 | Confirmed (green) | I4 in results/integration-verify-r2.log |
| POS1/POS2 | Recreate-path regression suite (pacing, convergence, migration, surge floor) | 1+2 | Still green — the InPlaceUpdate work did not regress the Recreate path | same logs |
| F1 | PR body still describes a different API than shipped — and the divergence grew | n/a | Still open (see findings doc) | results/l1-doc-mismatch-r2.log |
| F3 | Static path still overwrites the child's whole spec | 1 | Still open (canary green) | results/reverify-selftest-r2.log |

Regression sweep on the PR head: full repo unit tests (35 packages ok, 0
failures), all envtest suites (`rbg`, `rbgs_rolling`, `webhook`,
`restart_policy`), `go build ./...` clean — results/full-unit-r2.log,
results/author-envtest-r2.log, results/full-envtest-others-r2.log.

### Round 1 (head `44241903`) — carried forward

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| P0 | Base updates every outdated child in one reconcile (un-paced) | 1 (base) | Confirmed | all 3 children rewritten in one pass; results/premise-base.log |
| POS1 | Paced rollout: ≤1 deletion/reconcile under maxUnavailable=1; A→B→A converges; migration with unchanged template does not recreate | 1 | Confirmed (green) | results/unit-verify.log |
| POS2 | Same, end-to-end on a real API server with foreground deletion; surge-backed zero-unavailability keeps ready (base+surge) ≥ replicas | 2 | Confirmed (green) | 3/3 specs pass; results/integration-verify.log |
| F2 | `maxUnavailable: "0%"` + `maxSurge: 0` bypasses the both-zero webhook rule and silently resolves to maxUnavailable=1 (integer spelling is rejected) | 1 | Confirmed as *intended* by the author's own test; flagged as an API-consistency smell | canary green; results/l1-harness-bites.log |
| F3 | Static path (rolloutStrategy unset) now overwrites the child's whole spec — out-of-band RoleTemplates are wiped — despite the PR body calling that path "unchanged legacy behavior" | 1 | Confirmed (canary) | results/unit-verify.log |
| F1 | PR body describes a materially different implementation than shipped (no `paused` field, "no revision history", `isServing`/`readySurge` budget, ~14 unit tests and a webhook e2e that do not exist) | n/a | Verified by inspection (grep) — see findings doc | results/l1-doc-mismatch.log |

## Per-finding detail

### P0 — premise (confirmed, base)

`TestVerifyPR474_Premise_StaticPathUpdatesAllChildrenAtOnce` seeds 3 ready
children at template v1, sets the set's template to v2 (no rolloutStrategy),
calls `Reconcile` once on **base** and asserts every child is rewritten.
Observed: 3/3 updated, zero ordering, zero gating. Expected for the claim:
same. The PR's premise holds.

### POS1 — the feature works (contract, PR head)

- `TestVerifyPR474_Rollout_PacedRecreate`: first reconcile deletes exactly the
  highest ordinal and nothing else; the freed ordinal is refilled at the update
  revision on the next reconcile.
- `TestVerifyPR474_Rollout_ConvergesUnderBudget`: driving the loop to
  convergence never removes more than 1 child per reconcile (maxUnavailable=1),
  reaches all-v2, and an A→B→A flip converges back to A with the budget intact.
- `TestVerifyPR474_Rollout_MigrationNoRecreate`: enabling rolloutStrategy over
  legacy (revision-label-less) children whose content already matches deletes
  nothing and keeps UIDs.

### POS2 — integration (contract, PR head, envtest)

- **I1** `recreates one group at a time under maxUnavailable=1 and completes`:
  paced rollout against the real API server, foreground deletes completed by
  the simulated GC, ends with `Rolling=False/RolloutComplete`,
  `UpdatedReplicas=3`, `CurrentRevision==UpdateRevision`.
- **I2** `enabling rolloutStrategy over matching legacy children does not
  recreate them`: Consistently(5s) zero terminating children, UIDs stable,
  `status.currentRevision` initialized from the legacy children.
- **I3** `maxUnavailable=0 with surge keeps ready base groups at
  spec.replicas`: the ready count over **all** serving children (base + surge —
  the same population the controller's budget counts) never dropped below
  spec.replicas while the rollout proceeded.

### F2 — zero-budget spelling asymmetry (canary; intended per author)

`validateGroupSetRolloutStrategy` only rejects the both-zero budget when both
values are **integers** (`!unavailablePercent && !surgePercent`). `maxUnavailable:
"0%"` + `maxSurge: 0` (or `"0%"`) passes, and `resolveGroupSetRollout` silently
rewrites `maxUnavailable` to 1 — the user's "never take a group down" intent
becomes "one group down at a time" with no event or condition.

Downgraded from a bug after discovery: the author's own table test
`TestValidateGroupSetRolloutStrategy/"zero percentages resolve to zero at
runtime and fall back to maxUnavailable=1"` pins the acceptance+fallback as
intended. Recorded as a minor API-consistency finding (the two spellings of the
same resolved budget take opposite paths; the fallback is invisible to the
user) rather than a defect.

Bites check (results/l1-harness-bites.log): with a symmetric-rejection fix
applied temporarily, the package goes red (my canary flips **and** the author's
pinned case fails), proving both tests exercise the real code path; reverting
restores green and an empty production diff.

### F3 — static path is not byte-for-byte unchanged (canary)

`updateExistingRBGs` now executes `latestRBG.Spec = *rbgset
.Spec.GroupTemplate.Spec.DeepCopy()` and `needsUpdate` compares RoleTemplates,
so the unset-strategy path (a) triggers a one-time child spec rewrite for
children whose RoleTemplates drift, and (b) wipes RoleTemplates a user set on
children out-of-band. For legacy sets that never used roleTemplates this is a
no-op (no drift → no update); the change is arguably the *fix* for roles with
`templateRef` (pre-PR the set never propagated roleTemplates to children), but
it contradicts the PR body's "Legacy behavior, unchanged" claim. Canary
`TestVerifyPR474_StaticPathWipesChildRoleTemplates` pins the overwrite.

### F1 — PR body vs shipped code (doc accuracy)

The PR body describes the pre-rewrite implementation. Verified by inspection:
`GroupSetRolloutStrategy` has no `paused` field (results/l1-doc-mismatch.log),
there are no `TestRollingUpdate_*` unit tests (the shipped ones are
`TestGroupSetMatchesRevision_*` / `TestWarmUpGroupSetSurge_*` /
`TestSyncRollingGroupSet_*`), no webhook e2e was added
(`test/e2e/testcase/v1alpha2/webhook_validation.go` is untouched), and the
revision-based design (`ControllerRevision` history, `currentRevision`/
`updateRevision` status) contradicted the body's "pure content comparison with
no revision history". Anyone reviewing from the body, or a user reading its
field table (`paused: bool`) and setting `paused: true`, was misled — the CRD
silently prunes the unknown field and the rollout proceeds.

## Round 2 per-finding detail

### F8 — v1alpha1 conversion round-trip (round-1 consensus blocker) — FIXED

The round-1 blocker: `ConvertTo`/`ConvertFrom` never carried `spec.rolloutStrategy`
or the v1alpha2-only rollout status fields, v1alpha1 remained `served: true`,
and the controller dispatches on `Spec.RolloutStrategy != nil` — so a
full-object write through v1alpha1 mid-rollout silently reverted the set to
the un-paced static path. `f6019ead` fixes it with the annotation-stash
pattern (`conversion.workloads.x-k8s.io/v1alpha2-rollout-strategy` /
`-rollout-status`), and the deployed CRD wires the conversion webhook
(`deploy/kubectl/manifests.yaml`, `config/crd/patches/webhook_in_rolebasedgroupsets.yaml`),
so the conversion functions actually run on real clusters.

My independent tests (not the author's):
- `TestVerifyPR474_V1alpha1RoundTrip_PreservesRolloutStrategy` — the exact
  blocker scenario (hub → v1alpha1 read view → client edit → full-object
  write-back → hub), for **both** strategy types: strategy + all six status
  fields survive, the stash never leaks into the stored hub object.
- `..._ClearsStaleStash` — removing the strategy through v2 does not
  resurrect it from a stale view; rollout status keeps round-tripping
  independently (faithful, not staleness).
- `..._GarbageStashFailsClosed` — a corrupted stash fails conversion loudly.
- `..._DroppedStashLosesStrategy` (canary) — residual limitation: a v1alpha1
  client that writes a *fresh* object (no annotation echo) still drops the
  strategy. kubectl edit/apply echo annotations, so the common paths are
  safe; programmatic rebuilds are not. Inherent to the stash design.

Bites check: removing the stash write in `ConvertFrom` turns the
round-trip contract test RED (results/reverify-selftest-r2.log run after
restore shows green).

### POS3 / POS4 — the new InPlaceUpdate strategy works (contract)

`9e423e86` adds `InPlaceUpdate` (now also the CRD/defaulting default): the
set rewrites the child RBG spec **in place** instead of deleting it, and
child readiness is gated by the child's new `RollingUpdateInProgress`
condition (`rolebasedgroup_rollout_status.go` computes per-role workload
rollout completion against the revision hash).

- Unit (`TestVerifyPR474_InPlaceUpdate_PacesSpecUpdates`): one ready child
  updated per pass (highest ordinal first), none deleted; while the updated
  child is still rolling the availability floor holds (no second child
  touched); convergence with stable UIDs and `Rolling=False/RolloutComplete`.
- Unit (`..._ResolverLimits` / `..._RejectsUnknownType`): empty type →
  in-place path; maxSurge ignored; both-zero fallback; unknown type errors.
- Envtest (**I4**, real API server + full controller stack): template
  propagates to all children with every UID preserved and no child ever
  terminating; ends `Rolling=False/RolloutComplete`,
  `CurrentRevision==UpdateRevision`.
- Bites checks: removing the in-place budget guard, or removing the child
  rolling-condition gate in `groupSetChildReady`, each turn the pacing test
  RED; restored → green.

### F2 — spelling-sensitive zero rule — FIXED

The round-1 canary
(`TestVerifyPR474_Webhook_ZeroBudgetIsSpellingSensitive`) **flipped RED** on
this head — the exact fix signal for a canary. `validateGroupSetRolloutStrategy`
now rejects `maxUnavailable`/`maxSurge` both-zero in *all* spellings for
Recreate, and InPlaceUpdate rejects a declared-zero `maxUnavailable` and any
positive `maxSurge`. Promoted to contract tests
(`TestVerifyPR474_Webhook_ZeroBudgetRejectedSymmetrically`,
`TestVerifyPR474_Webhook_InPlaceUpdateRules`); the resolver-level fallback
remains as defense-in-depth for persisted objects and percentages that round
down to zero, documented by the `..._PercentZeroBecomesOne` canary.

### F1 — PR body vs shipped API — STILL OPEN, divergence grew

**Round 3: FIXED.** The body was rewritten against the shipped API — every
round-1/2 divergence is closed (see results/l1-doc-match-r3.log, 16/16
PASS, re-checked test-by-test against the head tree). The section below is
the round-2 record of what was wrong before the fix.

The PR body (rounds 1–2) described the pre-rewrite implementation. Verified by inspection:



## Harness-bites checks performed

Round 2 (each temporary break turned the matching test RED; production diff
empty after restore — results/reverify-selftest-r2.log shows the final green run):

- **F2 fix**: removing the Recreate both-zero rejection →
  `TestVerifyPR474_Webhook_ZeroBudgetRejectedSymmetrically` RED.
- **POS3 pacing**: removing the in-place budget guard in
  `rolloutGroupSetChildren` → `TestVerifyPR474_InPlaceUpdate_PacesSpecUpdates` RED.
- **POS3 child gate**: removing the `RollingUpdateInProgress` gate in
  `groupSetChildReady` → same test RED.
- **F8 fix**: removing the strategy stash write in `ConvertFrom` →
  `TestVerifyPR474_V1alpha1RoundTrip_PreservesRolloutStrategy` RED.

Round 1 (results/l1-harness-bites.log):

- **POS1/POS2 contract tests vs base**: the paced-recreate assertion is
  unsatisfiable on base (the base path updates all 3 children in one pass —
  that is exactly the P0 reproduction), and green on the PR head. The fix and
  the test bite each other by construction.
- **F2**: symmetric-rejection fix applied → red (canary + author's pinned
  case); reverted → green, `git diff` empty.
- **F3 canary**: fails loudly if `needsUpdate`/`updateExistingRBGs` stop
  overwriting the spec (i.e. if the behavior is "fixed"); polarity documented
  in the test comment.

## Proposed fixes (NOT applied to production here)

- **F1**: rewrite the PR description against the shipped API — in particular
  the `type` default (now `InPlaceUpdate`; the body says `Recreate`), the
  absent `paused` field/condition, the ControllerRevision-based design, the
  real test list, and the budget-gated replicas-only behavior in InPlaceUpdate
  mode. The description is the review contract for a feature this size.
- **F3**: keep the full-spec propagation (it fixes templateRef roles), but
  correct the "legacy path unchanged" claim in the PR body and consider a
  release note for the one-time child rewrite.
- **F7 (round-2 note)**: consider gating the per-role uncached
  rollout-completion reads on the child's `RollingUpdateInProgress`
  condition instead of running them on every RBG reconcile, and watch the
  apiserver QPS of large sets during rollouts — the repo's own
  stress-testing guide is the benchmark.

## Continuing after the fix (possibly on another machine)

The harness lives on `verify/pr474-rbgs-rolling-update-claude` with production
code untouched, so it grafts onto whatever the fixed code is.

```bash
git clone https://github.com/cheyang/rbg.git && cd rbg
git fetch origin verify/pr474-rbgs-rolling-update-claude
git checkout verify/pr474-rbgs-rolling-update-claude
bash docs/verification/pr474-rbgs-rolling-update/scripts/re-verify.sh   # no arg = current PR head
```

`re-verify.sh` fetches the PR head from the manifest's `pr` URL, grafts
**only the manifest's `harnessPaths`** onto it (round-2 fix: the round-1
script extracted the whole tree, which would have reverted production code
once the head moved past the branch parent), prints the
`.last-reviewed..head` delta, runs layers 1–2, and prints per-test verdicts.
Exit 0 iff the runs succeed. Interpretation: **contract** tests (POS1–POS4,
F2, F8) are fixed when green; **canaries** (F2b resolver fallback, F3, F8b
stash-less write, and the P0 premise test) are *fixed* only when they flip
to red — then invert them or promote the new behavior to a contract test.
`.last-reviewed` holds `98720938` (this round's reviewed head); advance it
after the next round. Round 3 also added `scripts/doc-match-r3.sh` (usage:
`bash scripts/doc-match-r3.sh <pr-body-file> <git-rev>`) — it checks every
API-surface claim in the current PR body against a given rev and is the
machine-checkable form of F1.

Layer-2 prerequisites: Go 1.27, envtest binaries (etcd + kube-apiserver) at
`$KUBEBUILDER_ASSETS` (1.37.0 or 1.30.3 assets both work; the repo's Makefile
pins 1.31.0). No cluster needed; no cluster is touched.

Copy-paste kickoff for a fresh agent: clone this branch, read
`verify-manifest.json`, run `scripts/re-verify.sh` with no argument, and
report per-finding Fixed / Still-broken / Partial / Harness-update using the
polarity table above.
