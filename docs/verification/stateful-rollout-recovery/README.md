# stateful-rollout-recovery — bug verification

Reproducible evidence for the findings raised while reviewing
https://github.com/sgl-project/rbg/pull/484 (Reviewer A — Claude, first round).

Harness branch: `verify/stateful-rollout-recovery-claude` (pushed to the
reviewer fork `cheyang/rbg`), based on the code under review = PR head
`90dd90684755e647433b0d9c210d5546ec5c7d71`. Production code is untouched — the
diff against the PR head is exactly this harness (one test file + this docs
tree).

Layers run against the **code under review** (PR head `90dd9068`):

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit | statefulmode control-plane logic (`progressUpdate`, `computeTopology`, `updateStatefulInstanceSet`) with the package's existing fakes | `GOCACHE=/tmp/rbg-go-cache go test -mod=vendor -count=1 -run 'TestClaude' -v ./pkg/reconciler/roleinstanceset/statefulmode/` |
| 2. Integration | real kube-apiserver + etcd via envtest, real manager + controllers | `KUBEBUILDER_ASSETS=$HOME/.local/share/kubebuilder-envtest/k8s/1.36.2-linux-amd64 GOCACHE=/tmp/rbg-go-cache go test -mod=vendor -count=1 ./test/envtest/testcase/rbg/... ./test/envtest/testcase/restart_policy/...` |
| 3. Live | not run this round (see liveNote) | — |

> Test polarity: every test here is a **contract** test (asserts intended
> behavior; FAILS on buggy code, PASSES when fixed).

## Problem premise (P0)

Answered before the findings, and run against the **base** branch
(`9b22f4a8`, merge-base) without the patch.

| | |
|---|---|
| Claimed symptom | issue #464: "A Stateful RoleInstanceSet rollout may stop progressing when its Pod template is updated again while the Pods from the previous update are still NotReady. The rollout does not automatically resume unless another event triggers reconciliation." |
| Linked issue | #464, OPEN since 2026-09-12, no labels; PR says "Related to #464" (no auto-close keyword). #470 was the earlier partial fix; this PR is the follow-up. |
| Reported component | Stateful RoleInstanceSet rollout recovery (statefulmode controller) |
| Patched component | `pkg/reconciler/roleinstanceset/statefulmode` — same component |
| Component match | Yes |
| **Verdict** | **Confirmed** |
| Evidence | `TestClaudeP0UnhealthyNonTargetGetsRequeue` is RED on base (no timed requeue is scheduled when an unhealthy non-target consumes the availability budget — `wait = 0s`) and GREEN on the PR head. `results/unit-base.txt` vs `results/unit-head.txt`. |

The PR's three other claimed gaps were each reproduced on base the same way
(G1/G2/G3 below): the base controller does not retry the OrderedReady
readiness gate, does not resume an A→B→A same-name rollback, and does not
recycle an in-range stale-revision surge slot.

## Summary of results

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| P0 | Unhealthy non-targets consuming `maxUnavailable` get no requeue on base (rollout stalls) | 1 | **Confirmed** (red on base, green on head) | `results/unit-base.txt`, `results/unit-head.txt` |
| G1 | OrderedReady must retry and then resume past its first stably-unhealthy update target | 1 | **Fixed by PR** (red on base, green on head; later ordinals untouched) | same |
| G2 | Early A→B→A rollback with matching revision names must resume | 1 | **Fixed by PR** (red on base, green on head) | same |
| G3 | OrderedReady must recycle an in-range stale-revision surge instance | 1 | **Fixed by PR** (red on base, green on head) | same |
| F1 | Cancelled early rollback leaves the in-memory signal active → set stays "in rollout" → ordinary scale-down blocked / extra instance retained while a base instance is unhealthy | 1 | **Confirmed on head (regression vs base)** — reported by cheyang (P2), independently reproduced here with a second, non-paused trigger variant | `results/unit-head.txt` (red on head), `results/unit-base.txt` (green on base), `results/bite-check.txt` |
| F2 | The retry must not relax `maxUnavailable` (unhealthy non-targets still consume budget) | 1 | Holds on base and head (regression guard) | same |
| — | Whole-tree regression sweep + race on affected trees on head | 1 | all green except the intentional F1 red | `results/full-suite-head.txt` |
| — | envtest suites (real apiserver) on head | 2 | green (rbg 48.8s, restart_policy 202.5s) | `results/integration-head.txt` |

## Per-finding detail

### F1 — cancelled early-rollback signal blocks scale-down (major)

Mechanism: `trackEarlyRollbackReplacement` (stateful_instance_set_utils.go:514)
stores the signal into the process-local `earlyRollbackReplacementUIDs` map
whenever `hasStaleBaseInstance` is true — including when the rollout is
**paused** (no update work can happen) or when the budget blocks every target
(no work actually happened). The signal is only cleared when
`allBaseAtUpdateRevHealthy` becomes true. If the operator cancels the rollout
(reverts the template so revision names match again) while some base instance
is *unhealthy*, the signal never clears: `computeTopology` keeps
`inRollout = true` with no update targets left, the surge stickiness floor
retains instances beyond `spec.replicas`, and an ordinary scale-down never
happens for as long as the unhealthy instance stays unhealthy (a broken pod at
the desired revision is never a cleanup target, by design).

Test: `TestClaudeF1CancelledRollbackBlocksScaleDown` — two variants:
- `paused-cancel` — cheyang's exact scenario (pause → template B → reconcile →
  restore A → unpause → scale 2→1). On head: instance `test-set-1` retained,
  `deleted: []`. On base: deleted (scale-down proceeds).
- `immediate-revert` — my own variant showing the paused path is not required:
  unpaused A→B reconcile (budget-blocked, nothing updated) → revert to A →
  scale 2→1. Same blocked scale-down on head.

Harness-bites check (`results/bite-check.txt`): with a minimal candidate fix
(gate the signal `Store` on `!Paused`), the `paused-cancel` variant flips to
green — the test genuinely exercises the signal path — while
`immediate-revert` stays red, i.e. a real fix must scope the signal to actual
replacement work (e.g. remember which ordinals were being replaced, or
invalidate when no update target remains), not merely skip paused reconciles.
The candidate fix was then reverted; production code is untouched.

Proposed fix sketch (NOT applied): record the signal only when replacement
work is actually issued (an update target was acted on), key it by the
ordinals being replaced, and clear it when those ordinals are healthy at
updateRev, when the update range moves past them, or when the rollout is
cancelled/superseded. Also see the author's own note: persisting the signal
across restarts is tracked in #482.

### G1/G2/G3/G4 — the PR's four claimed fixes (all verified)

- G1 (`TestClaudeG1OrderedReadyResumesStablyUnhealthyTarget`): before the
  stable-unhealthy window expires, a requeue in (0, 4s] is pushed and nothing
  is deleted; after expiry, exactly the blocking ordinal is replaced. On base:
  no requeue and no deletion at all.
- G2 (`TestClaudeG2EarlyRollbackSameNameResumes`): after A→B→A with equal
  revision names, both stale instances are replaced on head; on base neither
  is (Phase C never runs because `inRollout` was `currentRev != updateRev`
  only).
- G3 (`TestClaudeG3OrderedReadyRecyclesStaleSurge`): the unhealthy stale-rev
  surge slot is recycled on head (and only it — base ordinals untouched); on
  base it is never recycled because OrderedReady exits before Phase C.

### P0 — premise (see table above)

### F2 — budget not relaxed (`TestClaudeF2UnhealthyNonTargetStillConsumesBudget`)

Green on both refs: an expired unhealthy non-target schedules no further
retries and is never free-deleted. This guards the PR's own claim that "the
retry does not relax maxUnavailable".

## Findings verified by reasoning only (no harness claim)

- **F3 (minor)** — `earlyRollbackReplacementUIDs` has no
  `pruneInstanceHealth`-style informer pruning; entries for deleted sets are
  reclaimed only by the NotFound branch in `Reconcile` (controller.go:190) or
  a later owner-UID mismatch. `pruneInstanceHealth`'s own doc comment explains
  why relying on a NotFound reconcile is not enough (lister lag can make the
  deletion reconcile see the object, not NotFound, and no further event
  follows). Agrees with Copilot's and cheyang's review comments. Impact is a
  bounded memory leak (one string per affected set), not behavior.
- **F4 (minor, acknowledged)** — the signal is process-local: a controller
  restart during the replacement bridge (stale base gone, replacement not yet
  ready) loses the signal and condemns the surge. Acknowledged by the author
  in the PR body and tracked in #482.

## Live run notes

Not run this round (unit + integration only, per the review setup).

## Continuing after the fix (possibly on another machine)

The harness is on branch `verify/stateful-rollout-recovery-claude` of the
reviewer fork (`https://github.com/cheyang/rbg.git`), so it grafts onto
whatever the fixed code is.

1. Get it onto the fixed code:
   ```bash
   git fetch https://github.com/cheyang/rbg.git verify/stateful-rollout-recovery-claude
   git checkout <fixed-branch>
   git checkout FETCH_HEAD -- docs/verification/stateful-rollout-recovery \
       pkg/reconciler/roleinstanceset/statefulmode/verify_stateful_rollout_recovery_claude_test.go
   ```
2. Prereqs: Layer 1 = Go toolchain + vendored deps only; Layer 2 =
   envtest assets (`setup-envtest use 1.31.0` or any local
   `~/.local/share/kubebuilder-envtest/k8s/<ver>`); Layer 3 = a real cluster.
3. Re-run:
   ```bash
   bash scripts/re-verify.sh            # resolves the current PR head from manifest.pr
   bash scripts/re-verify.sh <fixed-ref>
   ```
4. Read results via the polarity table: everything here is a **contract**
   test, so all should be GREEN on fixed code. `F1` is red on the current PR
   head — that is the reproduced bug, and it is the only expected red.
5. Harness-bites: run Layer 1 once against the pre-fix head (or base for
   P0/G1..G3) to confirm the reds — already captured in `results/`.

### Kickoff prompt for a fresh agent
```text
Continue a verification task on branch verify/stateful-rollout-recovery-claude
(https://github.com/cheyang/rbg.git). Background: a review of PR
https://github.com/sgl-project/rbg/pull/484 produced findings F1..F4; the
harness in docs/verification/stateful-rollout-recovery reproduced them. The PR
is now fixed at <ref>. Read docs/verification/stateful-rollout-recovery/README.md
("Continuing after the fix") and follow it: graft the harness onto the fixed
code, re-run all layers (bash scripts/re-verify.sh <ref>), mind the polarity
table (all tests are contract tests), run the harness-bites check, and report
an observed-vs-expected table. Clean up scoped test resources; do not run
cluster-wide destructive actions.
```
