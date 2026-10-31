# stateful-predelete-hooks — bug verification

Reproducible evidence for the review of https://github.com/sgl-project/rbg/pull/535
("fix: honor PreDelete hooks for Stateful instance deletion", addresses Task 1 of issue #534).

Code under review: head `6d650165d519be11798086fff95daaf57ac70dca`, base `4a0765f78b0930114a4b73a64433ee8f27c34372`.
Production code is untouched on this branch; the harness is additive only.

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit | `deleteInstance` / `processCondemned` hook behavior with a fake client | `go test ./pkg/reconciler/roleinstanceset/statefulmode/ -run 'TestVerify\|TestStatefulPreDelete' -count=1 -v` |
| 2. Integration | real API server + real RBG/RIS/RI controllers (envtest) | `KUBEBUILDER_ASSETS=$(setup-envtest use 1.31.0 -p path) go test ./test/envtest/testcase/stateful/ -run TestStatefulRoleInstanceSetController -ginkgo.focus 'PreDelete\|F1 regret' -count=1 -timeout 15m` |
| 3. Live | skipped this round (no KUBECONFIG layer) | — |

> Polarity: **contract** tests FAIL on buggy code / PASS when fixed. **Bug-canary** tests PASS
> while the bug/gap exists / FLIP to red once fixed (then invert the assertion).

## Problem premise (P0)

| | |
|---|---|
| Claimed symptom | "a matching PreDelete label or finalizer does not block Stateful deletion. The instance does not pass through the expected PreparingDelete waiting stage." (issue #534, still OPEN, filed 2026-10-31 against main@4a0765f) |
| Claimed mechanism | "DeleteStatefulInstance() calls DeleteInstance() without checking the configured PreDelete hook" |
| Reported component | `pkg/reconciler/roleinstanceset/statefulmode` (Stateful RoleInstanceSet deletion) |
| Patched component | same package — `stateful_instance_set_control.go` (`deleteInstance`, `processCondemned`) |
| Component match | Yes |
| **Verdict** | **Confirmed** |
| Evidence | `results/unit-base.txt`: on base, `TestVerifyP0HookedCondemnedInstanceDeletedOutright` PASSES — a hooked condemned instance is deleted outright (no PreparingDelete, delete call recorded). On head the same canary FLIPS to fail (`results/unit-head.txt`), i.e. the PR closes the bypass. |

## Summary of results

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| P0 | Base deletes hooked Stateful instances, bypassing PreDelete | 1 | Confirmed (canary passed on base, flips on head) | results/unit-base.txt vs results/unit-head.txt |
| AC1 | Scale-down honors matching label/finalizer hooks; repeated reconcile does not bypass; OrderedReady processes one blocked target at a time, Parallel prepares all | 1 | Fix verified | results/unit-head.txt, results/unit-head-pr-tests.txt |
| AC2 | Recreate rollout honors the hook; a blocked target is not counted as an old-replica removal (`status.CurrentReplicas` unchanged) | 1 | Fix verified | results/unit-head-pr-tests.txt (`TestStatefulPreDeleteRolloutKeepsCurrentReplicas`) |
| AC-env | Real controller: blocked target enters PreparingDelete, survives repeated reconcile, deletion resumes after a metadata-only hook release (Parallel + OrderedReady) | 2 | Fix verified | results/envtest-head-pr-tests.txt (PR's own specs, 2/2 pass) |
| F1 | Regret gap: an instance that entered PreparingDelete via a blocked scale-down keeps that lifecycle state forever when the scale-down is canceled — no stateful path resets it to Normal (stateless has `managePreparingDelete`) | 1+2 | **Confirmed — gap present at head** (canaries pass; flip when fixed) | results/unit-head.txt (`TestVerifyF1...` PASS), results/envtest-head.txt (both F1 specs PASS) |

## Per-finding detail

### F1 — no rescue path out of PreparingDelete on the Stateful path

Mechanism: the PR's `deleteInstance` sets `role-instance-lifecycle-state=PreparingDelete`
whenever a condemned/rollout target matches the hook. The only way out is deletion. If the
operator cancels the scale-down (replicas back up) or reverts a rollout while the hook is
still held, the instance is in range again but nothing in the stateful reconciler resets the
state — `git grep` shows only the stateless syncer reads the label, and stateless explicitly
handles this via `managePreparingDelete` (scale.go). Unit: block via `processCondemned`, then
run the instance through the in-range path (`processReplica`, both policies) → label still
`PreparingDelete`. envtest: real controllers, scale 2→1 (blocked) then 1→2 → Consistently(5s)
the instance (same UID, never deleted) keeps `PreparingDelete`, in Parallel and OrderedReady.

Harness-bites: a minimal candidate fix (reset PreparingDelete→Normal for in-range created
instances at the top of `processReplica`) flips the unit canary to red —
`results/unit-head-f1-bites-fix-probe.txt`. The probe was reverted; production diff is empty.

Expected once fixed: canary flips; invert the assertions to require
`RoleInstanceLifecycleStateNormal` (contract).

## Proposed fixes (NOT applied to production here)

- **F1**: add a stateful equivalent of stateless `managePreparingDelete`: when an in-range,
  non-condemned instance carries `PreparingDelete` and no longer needs deletion, return it to
  `Normal` (guarding the MarkNotReady regret caveat the stateless code documents). The rescue
  belongs on the replica path (e.g. `processReplica` or Phase B of `updateStatefulInstanceSet`)
  so both scale-down regret and rollout revert are covered.

## Continuing after the fix (possibly on another machine)

The harness is on branch `verify/stateful-predelete-hooks-codex` (reviewer fork), production
code untouched.

```bash
git fetch https://github.com/cheyang/rbg.git verify/stateful-predelete-hooks-codex
git checkout <fixed-ref>
git checkout FETCH_HEAD -- docs/verification/stateful-predelete-hooks \
  pkg/reconciler/roleinstanceset/statefulmode/predelete_verify_test.go \
  test/envtest/testcase/stateful/predelete_regret_verify_test.go
bash docs/verification/stateful-predelete-hooks/scripts/re-verify.sh <fixed-ref>
```

or simply `bash docs/verification/stateful-predelete-hooks/scripts/re-verify.sh` from a checkout
of this branch — the current PR head is auto-discovered from the manifest's `pr` URL.

Polarity reminder: F1 canaries are green today; a real fix turns them red (= FIXED(flipped)),
then invert their assertions. Contract tests (AC1/AC2/AC5) must stay green. P0 runs against
BASE only and is not part of re-verify's findings.

Kickoff prompt for a fresh agent: "Continue verification of sgl-project/rbg#535 from branch
verify/stateful-predelete-hooks-codex on https://github.com/cheyang/rbg.git — run
docs/verification/stateful-predelete-hooks/scripts/re-verify.sh and apply the polarity table in
docs/verification/stateful-predelete-hooks/README.md."
