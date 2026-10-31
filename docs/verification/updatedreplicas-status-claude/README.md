# Verification — PR #533 "fix: update updatedReplicas status" (Reviewer A: Claude)

- PR: https://github.com/sgl-project/rbg/pull/533
- Head: `4ab4a5ee63f7836ae93b6e0684bdb20e433086f1`
- Base (merge-base with the PR's base): `7894133d5b59d44f2a19dcf6b75c531996285bcf`
- Harness branch: `verify/updatedreplicas-status-claude` (this tree; production code untouched, all additions are tests + docs)

## Premise (P0) — CONFIRMED

Claimed symptom (PR body, quoted):
> "RBG status synchronization only compared Replicas and ReadyReplicas, so changes to
> UpdatedReplicas alone were ignored, leaving stale rollout progress even after repeated
> reconciliations."

Mechanism on **base**: `updateRBGStatus` copies an observed role status into
`rbg.Status.RoleStatuses` only when `Replicas` or `ReadyReplicas` differ, and the
write decision afterwards is `reflect.DeepEqual(oldStatus, rbg.Status)`. When only
`updatedReplicas` moves, the in-memory copy is never updated, the equality holds, no
SSA patch is sent — the stale value is reproduced by every later reconcile. Confirmed
by differential evidence at two layers (see table).

Component match: the report names RBG status synchronization; the patch touches exactly
`updateRBGStatus` in `internal/controller/workloads/rolebasedgroup_controller.go`.
The prepare step surfaced #15 as a "closing" reference, but #15 is a **pull request**
(merged Sep 2025, "add status check when diff workload") and the PR body says
`fixes #XXXX` (a placeholder), so nothing auto-closes on merge; no component-mismatch
blocker applies.

| layer | base 7894133 | head 4ab4a5e | evidence |
| --- | --- | --- | --- |
| L1 unit (`TestUpdateRBGStatusUpdatedReplicas`, the PR's own test, grafted) | FAIL (3/4 cases: stale `UpdatedReplicas`, 0 patches) | PASS (4/4) | `results/unit-base.json`, `results/unit-head.json` |
| L2 integration, real envtest API server (`TestUpdateRBGStatusSSAPersistence`) | FAIL: persisted `updatedReplicas` stays `1` while observed moves 1→2→0 with replicas/ready stable | PASS | `results/integration-base.json`, `results/integration-head.json` |
| envtest rbg suite (regression sweep) | ok | ok | `results/envtest-suite-summary.txt` |
| unit sweep (workloads + reconciler + utils) | — | ok | `results/unit-sweep-head.txt` |

## Findings ledger

| id | claim | polarity | layer | base | head | verdict |
| --- | --- | --- | --- | --- | --- | --- |
| P0 | PR's premise (stale updatedReplicas) | contract | integration | fail | pass | CONFIRMED, fixed by the PR |
| F1 | PR's unit test captures the contract (patch emitted, payload correct, explicit zero, idempotent) | contract | unit | fail | pass | PASSES on head — the PR's own regression guard |
| F2 | Real-API-server persistence: updatedReplicas-only change is written; explicit zero clears; no-op reconcile writes nothing (resourceVersion stable) | contract | integration | fail | pass | PASSES on head |

No code defect was found in the PR. The two-line guard removal in
`updateRBGStatus` is correct because:

- `RoleStatus` has exactly the four fields the SSA apply config serializes
  (`name/replicas/readyReplicas/updatedReplicas`), so the unconditional copy cannot
  desynchronize the in-memory status from what is persisted (no hot-patch loop).
- Redundant writes are still suppressed by the pre-existing `reflect.DeepEqual` skip.
- The anti-flicker guard in `ConstructWorkloadRoleStatus` (observedGeneration lag) and
  the NotFound path are unaffected.
- `RBGPredicate` ignores RBG status-only updates, so the new status writes do not
  self-retrigger the controller. The scaling adapter does get (cheap, no-op) reconciles
  on `updatedReplicas` moves — acceptable write amplification during rollouts.

## Harness-bites check

`TestUpdateRBGStatusSSAPersistence` was run on the base with the production code
**unpatched** (red: steps 2 and 3 fail) and on the head (green). The PR's own unit test
likewise fails 3/4 cases on base. Both tests demonstrably exercise the changed path.
Production diff of this branch vs `4ab4a5e` is tests + docs only.

## How to re-run

```bash
# unit layer
go test ./internal/controller/workloads/ -run 'TestUpdateRBGStatusUpdatedReplicas' -count=1

# integration layer (needs envtest assets; auto-detected from
# ~/.local/share/kubebuilder-envtest/k8s/*-linux-amd64 or $KUBEBUILDER_ASSETS)
KUBEBUILDER_ASSETS=$(ls -d $HOME/.local/share/kubebuilder-envtest/k8s/*-linux-amd64 | sort -V | tail -1) \
  go test ./internal/controller/workloads/ -run 'TestUpdateRBGStatusSSAPersistence' -count=1

# whole harness against the current PR head (fetches it, grafts this tree, runs both layers)
bash docs/verification/updatedreplicas-status-claude/scripts/re-verify.sh
```

Polarity: all tests above are **contract** tests — they assert the intended behavior.
No bug-canaries in this round.

## Continuing after the fix / next round

- `scripts/re-verify.sh` (no args) resolves the current PR head from `manifest.pr`,
  grafts `harnessPaths` onto it and prints per-finding FIXED / STILL-BROKEN / PARTIAL.
  Exit 0 iff every finding is fixed.
- `.last-reviewed` = `4ab4a5ee63f7836ae93b6e0684bdb20e433086f1`; advance it after
  reviewing the next delta.
- L3 (live) was intentionally skipped this round; if it runs later, the observable
  signal is: during a rollout, `kubectl get rbg <name> -o yaml` shows
  `status.roleStatuses[].updatedReplicas` advancing while `readyReplicas` stays at spec,
  and dropping to 0 when a new revision starts.

Kickoff prompt for a fresh agent resuming from this branch alone:

> You are resuming a review of https://github.com/sgl-project/rbg/pull/533 from branch
> verify/updatedreplicas-status-claude. Read docs/verification/updatedreplicas-status-claude/README.md
> and verify-manifest.json, run scripts/re-verify.sh to check the current PR head against
> the harness, then review the delta since .last-reviewed.
