# pr560-restart-budget — bug verification

Reproducible evidence for the findings raised while reviewing
https://github.com/sgl-project/rbg/pull/560 (head `ef35b481`, base `fd160cc`).

Layers run against the **code under review** (`ef35b48109068fb57b4ad7648e530b45ebee9c74`):

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit | budget/status merge logic (`updateStatus`, `reconcileRestartBudget`, `Scale`) | `go test ./pkg/reconciler/roleinstance/ ./pkg/reconciler/roleinstance/sync/ -run 'TestR560\|TestStaleStatusPreservesLiveRestartBudget' -v` |
| 2. Integration | real API server + real controller manager (envtest), PR's restart-budget lifecycle + deprecation suites | `KUBEBUILDER_ASSETS=/home/ecs-user/.local/share/kubebuilder-envtest/k8s/1.36.2-linux-amd64 go test -count=1 ./test/envtest/testcase/restart_policy/... ./test/envtest/testcase/deprecationv1a1/...` |
| 3. Live | **skipped per review instructions** (unit + integration only) | — |

> Test polarity: contract tests (assert intended behavior) FAIL on buggy code / PASS when
> fixed. Bug-canary tests (assert current behavior) PASS now / FLIP to red when fixed.

## Problem premise (P0)

Answered before the findings, and run against the **base** branch without the patch, because the
question is whether the problem exists today.

| | |
|---|---|
| Claimed symptom | PR body: "Adds an optional `maxGroupRestarts` budget that bounds how many times RBG may automatically rebuild a RoleInstance for one Pod-template revision." / "The accounting is durable per Pod-template revision and independent of the backoff counter, which may reset after a quiet period." |
| Linked issue | none (no closing reference; problem stated in the PR body) |
| Reported component | RoleInstance restart-policy recreation path (`pkg/reconciler/roleinstance/sync`) |
| Patched component | same — `restart_budget.go`, `instance_scale.go`, `instance_status.go` |
| Component match | Yes |
| **Verdict** | **Confirmed** |
| Evidence | At base `fd160cc`: `calculateRestartDelay` caps the delay at `maxDelaySeconds` (default 600s) but restarts are never refused, and `updateRestartTracking` resets `RestartCount` to 0 after `stableThreshold` — recovery is unbounded and its counter decays. `results/premise-base.txt` (test source: `results/premise-base-test.go`). |

## Summary of results

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| F1 | A stale status write can roll back the LIVE externally-written condition (`RoleInstanceCustomReady`); the PR's own `TestStaleStatusPreservesLiveRestartBudget` fails at head, contradicting the PR claim "stale writes cannot roll back live budget/conditions" | 1 | **Confirmed** | contract test `TestR560F1StaleWriteMustNotClobberLiveExternalCondition` FAILS at head; `results/unit-f1-f3.txt` |
| F2 | Budget lifecycle read amplification: one `Scale` call on a budgeted instance issues ≥4 uncached (APIReader) GETs + 1 LIST of the same data; `Update` re-runs `reconcileRestartBudget` again | 1 | **Confirmed** (efficiency, not correctness) | canary `TestR560F2UncachedReadAmplificationPerScale`: 4 GETs + 1 LIST; `results/unit-f2-f4.txt` |
| F3 | `updateStatus` silently drops the ENTIRE status write when the live generation advanced — including restart tracking and the `Restarting` condition that non-budgeted recreate instances rely on to prevent duplicate group recreations | 1 | **Confirmed** (drop proven; worst-case impact is code-path) | canary `TestR560F3StatusWriteSilentlyDroppedWhenGenerationAdvances` PASSES at head (documents the drop); `results/unit-f1-f3.txt` |
| F4 | Informer-lag generation mismatch on a budgeted instance surfaces as a blocking error from `Scale` (errored reconcile + transient FailedScale condition noise) instead of a benign requeue | 1 | **Confirmed** | canary `TestR560F4BudgetGenerationMismatchSurfacesAsBlockingError`; `results/unit-f2-f4.txt` |
| AC-budget | PR's claimed exhaustion lifecycle (exact counting, retention, explicit recovery, revision reset, deletion cleanup, CEL rejection) | 2 | **Passes** | envtest suites green at head (217s fresh run); `results/integration-envtest-restart.txt`, `results/integration-envtest-deprecation.txt` |

## Per-finding detail

### F1 (blocker) — stale status write clobbers live external conditions
- Mechanism: `updateStatus` (`pkg/reconciler/roleinstance/instance_status.go`) classifies
  `RoleInstanceCustomReady` / `RoleInstanceInPlaceUpdateReady` as controller-owned for the merge
  (`controllerOwnedConditionTypes`), so `newStatus`'s copy wins over the freshly-read live value.
  The design comment says `setInstanceConditions` copies the live external condition into
  `newStatus` — but it copies from the **informer-cached** instance, so a stale informer can
  persist a stale external condition and roll back a concurrent external write (readiness-gate /
  in-place-update controllers).
- Repro: `go test ./pkg/reconciler/roleinstance/ -run 'TestR560F1'` → FAIL at head
  (`expected: "LiveExternal", actual: "StaleExternal"`).
- The PR's own `TestStaleStatusPreservesLiveRestartBudget` (added in this PR) fails identically —
  the PR ships a red test; `go test ./api/... ./internal/... ./pkg/...` is otherwise green
  (`results/unit-full-sweep.txt`). CI at round start is failing.
- Harness-bites: applying a minimal fix (snapshot live `CustomReady`/`InPlaceUpdateReady` before
  overwriting `clone.Status`, re-apply after) turns both my contract test and the PR's test green
  and the whole package passes; fix was then reverted (production diff empty).

### F2 (minor) — uncached read amplification per reconcile
- `Scale` → `reconcileRestartBudget` (GET #1); `calculateDiffsWithExpectation` →
  `syncRestartTrackingFromAPI` (GET #2); `acceptBudgetRestart` → `readBudgetInstance` (GET #3) +
  `listBudgetPods` (LIST) + per-pod GETs in `terminateBudgetPods`/`deletePods`; then `Update` runs
  `reconcileRestartBudget` again (another GET) for instances whose Scale pass was not blocked.
- Counted with a wrapping APIReader: 4 GETs + 1 LIST in a single `Scale` call
  (`TestR560F2UncachedReadAmplificationPerScale`, log line in `results/unit-f2-f4.txt`).
- Efficiency only; no correctness impact observed.

### F3 (major) — wholesale silent status-write drop on generation advance
- `updateStatus` gained a guard: `clone.Generation != instance.Generation → return nil` — a silent
  success that drops the whole write. Intended to stop stale writes rolling back budget state, but
  it applies to **every** RoleInstance, budgeted or not, and every status field.
- What can be lost in the race (spec edit concurrent with an accepted restart): the
  `Restarting` condition and the `RestartCount`/`LastRestartTime` increment of a just-accepted
  group restart. In-memory `restartingCache` still guards within the controller's lifetime; after
  a controller restart the persisted condition is the only guard, and a lost write can permit a
  duplicate full-group recreation and resets the exponential backoff baseline.
- Repro (canary, documents current behavior): `TestR560F3StatusWriteSilentlyDroppedWhenGenerationAdvances`
  — at head the write is dropped (`RestartCount` stays 3, no `Restarting` condition persisted);
  at base `fd160cc` the same write lands (guard added by this PR).
- Fix direction (design decision for the author): narrow the drop — e.g. only skip the write when
  it would regress budget-owned fields, or re-run the status computation against the new
  generation, instead of discarding restart tracking.

### F4 (minor) — informer-lag generation mismatch becomes a blocking error
- `readBudgetInstance` errors on `fresh.Generation != instance.Generation`. Any pod-event-driven
  reconcile that starts before the informer catches up with a just-applied spec edit (or any
  concurrent spec edit landing mid-reconcile) makes the whole `Scale` call return an error:
  errored reconcile + rate-limited requeue, and a `FailedScale=True` condition is computed
  (which the same F3 guard then drops — noise on both sides). Self-healing, transient.
- Repro (canary): `TestR560F4BudgetGenerationMismatchSurfacesAsBlockingError`.

## Live run notes
Skipped (unit + integration layers only, per review instructions). The untested live-only claim:
"Kubelet releases the resources; the Pod API objects are retained for inspection" — envtest has
no kubelet, so pod resource release after finalizer-based retention is unverified.

## Proposed fixes (NOT applied to production here)
- **F1**: in `updateStatus`, snapshot the live `RoleInstanceCustomReady` /
  `RoleInstanceInPlaceUpdateReady` conditions before overwriting `clone.Status` and re-apply them
  after (live external writers win over the computed copy). Verified via the bites-check above.
- **F3**: narrow the new generation guard so restart-tracking and condition writes are not
  discarded wholesale (or recompute status at the new generation).
- **F4**: treat a generation/identity mismatch as a benign requeue (log + return requeue, not an
  error).
- **F2**: share one fresh read between `reconcileRestartBudget`, `syncRestartTrackingFromAPI` and
  `acceptBudgetRestart`; drop the duplicate `reconcileRestartBudget` call in `Update` when
  `Scale` already ran it.

## Continuing after the fix (possibly on another machine)

The harness is on branch `verify/pr560-restart-budget-claude` (production code untouched), so it
grafts onto whatever the fixed code is.

1. Get it onto the fixed code:
   ```bash
   git fetch https://github.com/cheyang/rbg.git verify/pr560-restart-budget-claude
   git checkout <fixed-branch>
   git checkout FETCH_HEAD -- docs/verification/pr560-restart-budget-claude \
       pkg/reconciler/roleinstance/review560_harness_test.go \
       pkg/reconciler/roleinstance/sync/review560_harness_test.go
   ```
2. Prereqs: Layer 1 = Go toolchain only; Layer 2 = envtest binaries
   (`KUBEBUILDER_ASSETS=/home/ecs-user/.local/share/kubebuilder-envtest/k8s/1.36.2-linux-amd64`
   or `setup-envtest use 1.36.2 -p path`).
3. Re-run the two commands in the layer table (use `-count=1` for envtest).
4. Read results via the polarity table:
   - `TestR560F1StaleWriteMustNotClobberLiveExternalCondition` (contract) should be **GREEN**
     when F1 is fixed; it is red at head.
   - `TestStaleStatusPreservesLiveRestartBudget` (the PR's own test, same contract) likewise.
   - `TestR560F2/F3/F4...` are **canaries** — they are green at head and must FLIP to red
     (then be inverted) when the corresponding behavior changes.
   - envtest suites must stay green.
5. Harness-bites: F1's bites-check was performed at head (minimal fix → green, then reverted).

### Kickoff prompt for a fresh agent
```text
Continue a verification task on branch verify/pr560-restart-budget-claude
(https://github.com/cheyang/rbg.git). Background: a review of
https://github.com/sgl-project/rbg/pull/560 produced findings F1-F4; a harness reproduced them.
The PR is now fixed at <ref>. Read docs/verification/pr560-restart-budget-claude/README.md
("Continuing after the fix") and follow it: graft the harness onto the fixed code, re-run all
layers (envtest with KUBEBUILDER_ASSETS=... -count=1), mind the polarity table (invert
bug-canaries F2/F3/F4 when they flip), and report an observed-vs-expected table. Or simply run
`bash docs/verification/pr560-restart-budget-claude/scripts/re-verify.sh` from a checkout of the
harness branch. Clean up scoped test resources; do not run cluster-wide destructive actions.
```
