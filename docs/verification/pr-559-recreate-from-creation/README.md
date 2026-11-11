# pr-559-recreate-from-creation — bug verification

Reproducible evidence for the findings raised while reviewing
https://github.com/sgl-project/rbg/pull/559
(head `8d190d0ddebaab43bbcc2ae996489165806af45b`, base `fd160cc964b0e1438bead4ad7324d0a8f0bf5eac`).

Layers run against the **code under review** (PR head):

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit | `shouldRecreateInstance` / `shouldRecreateInstanceGuarded` / `isAlreadyRestarting` / `checkRestartBackoff` in `pkg/reconciler/roleinstance/sync` | `go test ./pkg/reconciler/roleinstance/sync/ -run 'TestVerify559_' -v` |
| 2. Integration | full controller stack (RBG → RoleInstanceSet → RoleInstance) against a real API server (envtest 1.31) | `KUBEBUILDER_ASSETS=$HOME/.local/share/kubebuilder-envtest/k8s/1.31.0-linux-amd64 go test ./test/envtest/testcase/restart_policy/ -run TestRestartPolicy -args -ginkgo.focus 'verify559'` |
| 3. Live | skipped this round (debate setup); see `liveNote` in verify-manifest.json | — |

> Test polarity: contract tests (assert intended behavior) FAIL on buggy code / PASS when
> fixed. Bug-canary tests (assert current behavior) PASS now / FLIP to red when fixed.

## Problem premise (P0)

| | |
|---|---|
| Claimed symptom | "When a member Pod of a **Ready** `RoleInstance` goes away because its **node failed / got disconnected**, the RBG controller only creates a replacement Pod with the same name — it never recreates the `RoleInstance` ... from then on the restart policy is **permanently disabled** for that instance." (issue #542) |
| Linked issue | #542, OPEN, filed 2026-11-02, verified by the reporter against `origin/main@09accf63`; still valid |
| Reported component | `pkg/reconciler/roleinstance/sync/instance_scale.go` (`shouldRecreateInstance`, Ready gate) |
| Patched component | same file + API/admission for a new opt-in policy value |
| Component match | Yes |
| **Verdict** | **Confirmed** (real problem) |
| Evidence | `results/premise-base.out`: on base `fd160cc`, `TestVerify559Base_DeletedMemberIsNotATrigger` (deleted member pod is not a trigger), `TestVerify559Base_ReadyGateClosedAfterNotReady` (NotReady + no recent restart ⇒ policy inert), `TestVerify559Base_GateStaysClosed` (stale restart does not re-arm) all PASS — the reported mechanism exists on base exactly as described. |

Scope note: the PR fixes the symptom only for workloads that **opt in** to
`RecreateRoleInstanceOnPodRestartFromCreation`; the default legacy policy keeps the reported
behavior (issue proposals #1 `lastReadyTime` and #3 not-ready timeout are not implemented).
Whether opt-in-only closure of #542 is acceptable is a maintainer decision (see findings Q1).

## Summary of results

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| P0 | Premise of #542 exists on base | 1 (base) | Confirmed | `results/premise-base.out` (4/4 PASS on base) |
| F1 | FromCreation can recreate only ONCE before first Ready; a second pre-Ready failure is silently suppressed by `isAlreadyRestarting` (in-memory cache cleared only on Ready + persisted `Restarting=True` cleared only on Ready) | 1 + 2 | **Confirmed** | `results/unit-head.out`: `TestVerify559_FromCreation_SecondPreReadyFailure_{InMemoryGuard,PersistedCondition}` FAIL on head. `results/integration-head.out`: `verify559 TL3` FAILS (30s timeout) while TL1/TL2/TL2c pass |
| F1-CANARY | stuck Restarting ⇒ no backoff AND no recreation (silent inaction) | 1 | Confirmed (buggy behavior asserted) | `results/unit-head.out`: `TestVerify559_FromCreation_StuckRestartingSkipsBackoff` PASS on head; flipped to FAIL with the guard bypassed (bites check) |
| F3 | legacy-policy backoff slow path no longer counts Ignore-annotated Failed pods (unannounced legacy delta) | 1 | Confirmed (delta) | base: `TestVerify559Base_IgnorePodHoldsBackoffOpen` PASS (requeue>0); head: `TestVerify559_LegacyBackoff_IgnorePodNoLongerHoldsBackoff` PASS (requeue=0) |
| F4 | Succeeded pod being deleted triggers whole-instance recreation under FromCreation (KEP says Succeeded is a non-goal) | 1 | Confirmed canary | `TestVerify559_FromCreation_SucceededPodDeletion_Canary` PASS on head (= trigger fires) |
| CTRL | first pre-Ready failure / member deletion DO trigger whole-instance recreation under FromCreation; legacy policy unchanged | 1 + 2 | PASS | `TestVerify559_FromCreation_FirstPreReadyFailureTriggers`; TL1, TL2, TL2c in `results/integration-head.out` |

## Per-finding detail

### F1 — one-shot pre-Ready recreation (blocker candidate)

Mechanism:
1. Recreation fires → `setRestartingCondition` stores `restartingCache[ns/name]` (in-memory)
   and sets `RoleInstanceRestarting=True`; both are cleared ONLY when the instance becomes
   Ready (`instance_reconciler.go` clears the cache on Ready/delete; `instance_status.go`
   `setInstanceConditions` drops the condition only when `instanceReady`).
2. `shouldRecreateInstanceGuarded` suppresses recreation whenever `isAlreadyRestarting`
   (cache hit, or persisted condition via apiReader) is true.
3. Under FromCreation — whose purpose is pre-Ready recovery — an instance that fails again
   before EVER reaching Ready therefore gets no second recreation. `checkRestartBackoff`
   also short-circuits on `restarting` (`return 0`), so the failure is not even paced: the
   net effect is silent inaction, and `Restarting=True` can sit in status forever while
   nothing is restarting.

Observed vs expected:
- expected: second pre-Ready failure triggers another (backoff-paced) whole-instance recreation.
- observed: suppressed; only the failed pod is replaced individually (proved alive by the
  TL3 liveness assertion passing while the F1 assertion times out).

Commands: layer-1 and layer-2 commands from the table above.
Artifacts: `results/unit-head.out`, `results/integration-head.out`.

Harness-bites: with `isAlreadyRestarting` temporarily bypassed (`if true { return false }`,
reverted afterwards, production diff empty), F1 unit contracts PASS, F1-CANARY flips to FAIL,
and TL3 PASSES (`results/bites-integration-tl3-with-guard-bypass.out`). So the tests bite the
guard, not some unrelated gate.

### F3 — legacy backoff delta for Ignore-annotated pods (minor)

`checkRestartBackoff`'s slow path gained a `hasTriggerPolicyIgnore` skip. On base, a Failed
Ignore-annotated pod holds the backoff window open (delays its own replacement by up to
maxDelay); on head it does not. Benign direction, but the PR body claims the legacy policy is
unchanged.

### F4 — Succeeded-pod deletion triggers recreation (minor, canary)

The new deletion trigger (`p.DeletionTimestamp != nil`) does not exclude `Succeeded` pods,
while the KEP non-goals exclude normal completion. A completed member pod being cleaned up
would rebuild the whole group (killing in-flight siblings). Canary asserts current behavior.

### Harness note — Restarting condition is not a reliable observable

During bring-up, `Restarting=True` was observed to be persisted and then dropped again within
a second (`results/integration-flake-restarting-condition-dropped.out`): `updateStatus`
carries only NON-owned conditions forward from the live object, so a reconcile computed from
a stale informer cache overwrites the status without `Restarting`. Consequences: (a) tests
must assert pod-UID replacement, not the condition; (b) after a controller restart, the
`isAlreadyRestarting` API fallback can falsely report not-restarting (weakens the
anti-cascade guard — the in-process cache remains authoritative until Ready).

## Proposed fixes (NOT applied to production here)

- **F1**: scope the "already restarting" suppression to the deletion-in-progress window
  instead of until-Ready — e.g. clear the cache/condition once the recreation's replacement
  pods all exist (all component ordinals present as fresh pods), or expire it after a bounded
  window, so subsequent pre-Ready failures are paced by `checkRestartBackoff` instead of
  silently swallowed. Alternatively drop the in-memory fast path's "until Ready" lifetime for
  the FromCreation policy. The author should pick the design; the harness flips green under
  any fix that lets a second pre-Ready failure recreate.
- **F3**: mention the legacy backoff delta in the PR description (or split it out).
- **F4**: exclude terminal pods from the deletion trigger: `p.Status.Phase != v1.PodSucceeded`
  (and arguably `!= v1.PodFailed` is already covered by the Failed branch).

## Continuing after the fix (possibly on another machine)

The harness is on branch `verify/pr-559-recreate-from-creation-codex` (fork
`https://github.com/cheyang/rbg.git`; production code untouched), so it grafts onto whatever
the fixed code is.

1. One line: `bash docs/verification/pr-559-recreate-from-creation/scripts/re-verify.sh`
   (auto-discovers the current PR head from `manifest.pr`; grafts the harness; runs L1+L2;
   prints per-finding Fixed / Still-broken / Partial / Harness-update).
2. Manual equivalent:
   ```bash
   git fetch https://github.com/cheyang/rbg.git verify/pr-559-recreate-from-creation-codex
   git checkout <fixed-ref>
   git checkout FETCH_HEAD -- docs/verification/pr-559-recreate-from-creation \
     pkg/reconciler/roleinstance/sync/verify_pr559_fromcreation_test.go \
     test/envtest/testcase/restart_policy/fromcreation_verification_test.go
   ```
   then run the layer commands from the table above.
3. Prereqs: Go toolchain; envtest assets (`setup-envtest use 1.31.0`) for layer 2. No cluster
   needed; layer 3 was skipped.
4. Polarity: contract tests (F1, F1-E2E, F3, CTRL-*) should be GREEN on fixed code; canaries
   (F1-CANARY, F4) will be RED and must then be inverted.
5. `.last-reviewed` = `8d190d0ddebaab43bbcc2ae996489165806af45b`; advance it after review
   rounds so re-verify prints the right incremental diff.

### Kickoff prompt for a fresh agent
```text
Continue a verification task on branch verify/pr-559-recreate-from-creation-codex
(remote https://github.com/cheyang/rbg.git). Background: a review of
https://github.com/sgl-project/rbg/pull/559 produced findings F1 (one-shot pre-Ready
recreation), F3 (legacy backoff delta), F4 (Succeeded-pod deletion trigger); a harness
reproduced them. Read docs/verification/pr-559-recreate-from-creation/README.md
("Continuing after the fix") and follow it: graft the harness onto the fixed code, re-run
layers 1+2 (no live layer this round), mind the polarity table (invert canaries), and report
an observed-vs-expected table. Do not modify production code on the verify branch.
```
