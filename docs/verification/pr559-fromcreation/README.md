# pr559-fromcreation — bug verification

Reproducible evidence for the findings raised while reviewing
https://github.com/sgl-project/rbg/pull/559 (issue #542).

Layers run against the **code under review** (`8d190d0ddebaab43bbcc2ae996489165806af45b`, PR head):

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit | `shouldRecreateInstance` / `shouldRecreateInstanceGuarded` predicate matrix | `go test ./pkg/reconciler/roleinstance/sync/ -run 'TestP0\|TestFromCreation' -v` |
| 2. Integration | real API server (envtest) + full controller chain: RBG → RoleInstanceSet → RoleInstance → Pods | `KUBEBUILDER_ASSETS=$HOME/.local/share/kubebuilder-envtest/k8s/1.31.0-linux-amd64 go test ./test/envtest/testcase/restart_policy/... -timeout 25m -run TestRestartPolicy -v -args -ginkgo.focus='PR #559'` |
| 3. Live | real cluster | not run this round (unit + integration layers only, per round instructions) |

> Test polarity: contract tests (assert intended behavior) FAIL on buggy code / PASS when
> fixed. Bug-canary tests (assert current behavior) PASS now / FLIP to red when fixed.

Fixture realism notes (they matter — see the harness file header):
- Pods are **bound to a fake node** via the `binding` subresource before deletion, so a
  graceful Delete leaves the object Terminating with `deletionTimestamp` — what the
  controller actually sees during a node failure / eviction. Unbound envtest pods are
  removed instantly by the API server and would hide the trigger entirely.
- Crash injection requires `ContainerStatuses` to exist; the helper first puts pods in
  Running-but-NotReady (crash-looping-during-startup shape) before bumping RestartCount.
- `spec.nodeName` is NOT mutable via `Update` (rejected by the API server) — binding must
  go through the subresource.

## Problem premise (P0)

Answered before the findings, and run against the **base** branch (`fd160cc964b0e1438bead4ad7324d0a8f0bf5eac`) without the patch, because the
question is whether the problem exists today.

| | |
|---|---|
| Claimed symptom | "the same reconcile flips RoleInstanceReady to False, and shouldRecreateInstance() gates on that current condition, so from then on the restart policy is permanently disabled for that instance"; "A member lost this way is only replaced individually, the replacement cannot re-join an established process group, and the instance stays NotReady until the RoleInstance is deleted by hand." |
| Linked issue | #542, OPEN, created 2026-11-02 — still valid (verified on base) |
| Reported component | `pkg/reconciler/roleinstance/sync/instance_scale.go` (`shouldRecreateInstance` Ready gate) |
| Patched component | same file (new opt-in `RecreateRoleInstanceOnPodRestartFromCreation`), plus api v1alpha1/v1alpha2 types, admission, conversion, CRDs |
| Component match | Yes |
| **Verdict** | **Confirmed** |
| Evidence | `TestP0_*` canaries pass on base: legacy policy ignores an observed member deletion, and the restart gate stays closed once NotReady without a prior rebuild; `results/premise-p0-base.txt` |

## Summary of results

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| P0 | premise: #542 symptom exists on base (legacy policy) | 1 | Confirmed | `results/premise-p0-base.txt` |
| F1 | observed member deletion after Ready triggers whole-instance rebuild under the new policy (core #542 fix) | 2 | Confirmed (contract passes) | scenario A; `results/envtest-l2-fromcreation.txt` |
| F2 | pre-Ready container crash triggers whole-instance rebuild (both members replaced) | 2 | Confirmed (contract passes) | scenario B; same file |
| F3 | mid-recovery member loss is suppressed by the Restarting guard until Ready — residual wedge window under the new policy | 2 + 1 | Confirmed as current behavior (canary passes; design question, see finding) | scenario C + `TestFromCreation_RestartingGuardSuppressesMemberLossDuringRecovery_Canary` |
| F4 | force-deleted member misses the deletion trigger (missed-window hypothesis) | 2 | **Refuted** — even grace-0 delete is observed; hypothesis withdrawn, regression test kept | scenario D (flipped to contract) |
| F5 | scale-in removal is not member loss | 1 | Confirmed | `TestFromCreation_ScaleInRemovalNotMemberLoss_Contract` |
| F6 | Failed pod during in-place update still triggers | 1 | Confirmed | `TestFromCreation_FailedPodDuringInPlaceUpdate_Contract` |

Also verified (no finding): legacy policy behavior unchanged on head (P0 canaries still
pass; full 28-spec envtest suite passes); webhook/deprecation envtest suites pass;
backoff (`checkRestartBackoff`, `syncRestartTrackingFromAPI`) now also applies to the new
policy; RoleInstanceSet template carries the new policy end-to-end.

## Per-finding detail

### P0 — premise
Base `shouldRecreateInstance` with legacy policy: pod with `deletionTimestamp` set (Running)
→ no recreation; NotReady instance + unexpected container restart with no prior rebuild →
no recreation; the only bypass (`hasRecentRestart`) requires a rebuild that never happened.
Mechanism matches issue #542's root-cause analysis exactly.

### F1 — deletion trigger (envtest scenario A)
RBG with the new policy, 2-pod LeaderWorker instance, pods bound to a node, instance Ready.
Graceful-delete one member → `RestartCount=1`, `Restarting=True`, sibling torn down, both
members recreated after names free, instance Ready again, Restarting cleared. The legacy
policy does none of this (P0).

### F2 — pre-Ready recovery (envtest scenario B)
Same group; pods deliberately left NotReady (instance never Ready); one pod's container
RestartCount=1. Whole instance rebuilt (both members replaced, RestartCount=1), then
recovers to Ready. This is the "before first Ready" half of the fix, unimplementable with
the legacy policy.

### F3 — Restarting-guard suppression window (envtest scenario C + unit canary)
After rebuild #1, fresh pods Running-but-NotReady; a further member loss (container crash)
does NOT trigger rebuild #2 for the 15s Consistently window (guard: `isAlreadyRestarting`
via the persisted `Restarting` condition, which only clears on Ready — `instance_status.go`
"Clear Restarting condition when instance becomes Ready", no timeout). Once the instance
becomes Ready, the standing trigger fires rebuild #2 (RestartCount=2) — so the guard does
release via Ready. Residual risk: if the mid-recovery member loss itself prevents Ready,
the instance stays wedged (no rebuild, no timeout) — a narrower recurrence of #542 under
the new policy. Current behavior asserted by canary; if maintainers re-trigger mid-recovery
loss, the canary flips and must be inverted.

### F4 — missed deletion window (REFUTED)
Hypothesis: a pod force-deleted (`--force --grace-period=0`) or GC-removed vanishes without
an observable `deletionTimestamp`, so the trigger would be missed. Envtest refuted it: the
API server still exposes the pod with `deletionTimestamp` (MODIFIED watch event) before
removal, and the event-driven reconcile catches it — the whole-instance rebuild fires
(scenario D, now a contract test). The pure-predicate gap (a pod simply absent from the
list triggers nothing) remains true at unit level (`TestFromCreation_MissedDeletionWindow_Canary`)
but only matters if the controller is down/lagging exactly during the deletion window.

## Live run notes

Not run this round (unit + integration layers only). If run later: real node failure →
expect `ReCreateInstance` within the eviction window and recovery to Ready without manual
RoleInstance deletion; also observe mid-recovery double-failure behavior (F3).

## Proposed fixes (NOT applied to production here)

- **F3** (if maintainers agree it is a defect): bound the Restarting condition (e.g. clear
  it after `max(2*maxDelay, stableThreshold)` even without Ready, or allow one re-trigger
  per backoff window), so a member lost during recovery cannot wedge the instance until
  manual deletion. Also consider the not-ready timeout requested by #542's expected
  behavior #3, which would cover the "no failure signal at all" case too.

## Continuing after the fix (possibly on another machine)

The harness is on branch `verify/pr559-fromcreation-claude` (production code untouched),
so it grafts onto whatever the fixed code is.

1. Get it onto the fixed code:
   ```bash
   git fetch https://github.com/cheyang/rbg.git verify/pr559-fromcreation-claude
   git checkout <fixed-branch>
   git checkout FETCH_HEAD -- docs/verification/pr559-fromcreation \
     pkg/reconciler/roleinstance/sync/pr559_premise_test.go \
     pkg/reconciler/roleinstance/sync/pr559_unit_test.go \
     test/envtest/testcase/restart_policy/pr559_fromcreation_verify_test.go
   ```
2. Prereqs: Layer 1 = Go toolchain only; Layer 2 = envtest assets
   (`~/.local/share/kubebuilder-envtest/k8s/1.31.0-linux-amd64` or
   `setup-envtest use 1.31.0`); Layer 3 = a real cluster with `KUBECONFIG`.
3. Re-run the two commands in the table at the top.
4. Read results via the polarity table: contract tests (A, B, D scenarios; F1/F2/F5/F6
   unit tests) should be GREEN; bug-canaries (P0 legacy-policy tests, F3 suppression
   canary, F4 absence-canary) assert current behavior — if the fix changes it, they flip
   RED and must be inverted.
5. Harness-bites: `results/harness-bites-check.txt` shows the check (base
   `instance_scale.go` swapped in → scenarios fail). Repeat after any predicate refactor.
6. Or simply: `bash docs/verification/pr559-fromcreation/scripts/re-verify.sh` (no
   argument — it resolves the current PR head from the manifest's `pr` URL, grafts the
   harness, runs unit + integration layers, and prints per-finding
   Fixed / Still-broken / Partial / Harness-update with polarity applied).

### Kickoff prompt for a fresh agent
```text
Continue a verification task on branch verify/pr559-fromcreation-claude of
https://github.com/cheyang/rbg.git. Background: a review of
https://github.com/sgl-project/rbg/pull/559 produced findings P0, F1–F6 (see
docs/verification/pr559-fromcreation/README.md); a harness reproduced them.
Read that README ("Continuing after the fix") and follow it: graft the harness onto
the fixed code, re-run all layers, mind the polarity table (invert bug-canaries),
run the harness-bites check, and report an observed-vs-expected table. Do not run
cluster-wide destructive actions; production code must stay untouched by the harness.
```
