# pr474-rbgs-rolling-update — review verification harness

Reproducible evidence for the findings raised while reviewing
https://github.com/sgl-project/rbg/pull/474 ("feat: support RoleBasedGroupSet
rolling update", head `44241903`, base `0821cb5b` = origin/main).

Harness branch: `verify/pr474-rbgs-rolling-update-claude` (reviewer: cheyang's
fork). Production code is untouched — the diff is tests + docs only.

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit (fake client, in-package) | pacing, convergence, migration, resolver, webhook, static-path overwrite | `go test ./internal/controller/workloads/ ./api/workloads/v1alpha2/ -run 'TestVerifyPR474' -count=1 -v` |
| 2. Integration (envtest, real API server + full controller stack) | paced rollout end-to-end incl. foreground deletion, legacy migration, surge-backed zero unavailability | `KUBEBUILDER_ASSETS=$HOME/envtest-assets/k8s/1.37.0-linux-amd64 go test ./test/envtest/testcase/rbg/ -count=1 -ginkgo.focus 'PR #474' -timeout 20m` |
| 3. Live | **not run this round** — the debate pipeline mandates unit + integration only. A live cluster is reachable on this host (`kubectl get no` → 2 aliyun v1.36 nodes); the PR's own `make test-e2e` (kind) covers the same scenarios if L3 is ever wanted. | — |

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
`updateRevision` status) contradicts the body's "pure content comparison with
no revision history". Anyone reviewing from the body, or a user reading its
field table (`paused: bool`) and setting `paused: true`, is misled — the CRD
silently prunes the unknown field and the rollout proceeds.

## Harness-bites checks performed

- **POS1/POS2 contract tests vs base**: the paced-recreate assertion is
  unsatisfiable on base (the base path updates all 3 children in one pass —
  that is exactly the P0 reproduction), and green on the PR head. The fix and
  the test bite each other by construction.
- **F2**: symmetric-rejection fix applied → red (canary + author's pinned
  case); reverted → green, `git diff` empty. results/l1-harness-bites.log.
- **F3 canary**: fails loudly if `needsUpdate`/`updateExistingRBGs` stop
  overwriting the spec (i.e. if the behavior is "fixed"); polarity documented
  in the test comment.

## Proposed fixes (NOT applied to production here)

- **F1**: rewrite the PR description against the shipped API (drop `paused`,
  describe the ControllerRevision-based design, list the real tests), or
  implement the missing field. The description is the review contract for a
  feature this size.
- **F2**: either reject the percent spelling symmetrically (compute the
  resolved values before the both-zero check) or keep the fallback but surface
  it — an Event and a note in the MaxUnavailable CRD description saying a
  resolved 0 with no surge degrades to 1.
- **F3**: keep the full-spec propagation (it fixes templateRef roles), but
  correct the "legacy path unchanged" claim in the PR body and consider a
  release note for the one-time child rewrite.

## Continuing after the fix (possibly on another machine)

The harness lives on `verify/pr474-rbgs-rolling-update-claude` with production
code untouched, so it grafts onto whatever the fixed code is.

```bash
git clone https://github.com/cheyang/rbg.git && cd rbg
git fetch origin verify/pr474-rbgs-rolling-update-claude
git checkout verify/pr474-rbgs-rolling-update-claude
bash docs/verification/pr474-rbgs-rolling-update/scripts/re-verify.sh   # no arg = current PR head
```

`re-verify.sh` fetches the PR head from the manifest's `pr` URL, grafts the
harness onto it, runs layers 1–2, and prints per-test verdicts. Exit 0 iff the
runs succeed. Interpretation: **contract** tests (POS1, POS2) are fixed when
green; **canaries** (F2, F3, and the P0 premise test) are *fixed* only when
they flip to red — then invert them or promote the new behavior to a contract
test. `.last-reviewed` holds `44241903` (this round's reviewed head); advance
it after the next round.

Layer-2 prerequisites: Go 1.27, envtest binaries (etcd + kube-apiserver) at
`$KUBEBUILDER_ASSETS` (1.37.0 or 1.30.3 assets both work; the repo's Makefile
pins 1.31.0). No cluster needed; no cluster is touched.
