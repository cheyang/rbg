# stale-uid-children-codex — bug verification

Reproducible evidence for the findings raised while reviewing
https://github.com/sgl-project/rbg/pull/500 ("fix: reject child resources controlled by a
previous same-named parent"). Reviewer: Codex (debate Reviewer B). Harness is additive only —
**production code is untouched** on this branch.

Layers run against the code under review (PR head `d2915172`):

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit | `RoleBasedGroupSetReconciler.Reconcile`, `applyRBGSAReplicasOverride`, `ReconcileScalingAdapter`, volcano + k8s-scheduler-plugin `createOrUpdate`/`deletePodGroup` (fake client, in-package) | `go test -mod=vendor -count=1 ./internal/controller/workloads/ ./pkg/scheduler/volcano/ ./pkg/scheduler/k8s-scheduler-plugin/ -run 'TestVerify' -v` |
| 2. Integration | real API server (envtest) + real RBGSet/RBG/RBGSA controllers: recreate-with-same-name flows | `KUBEBUILDER_ASSETS=<envtest k8s assets> go test -mod=vendor -count=1 -v ./test/envtest/testcase/rbg/ -run TestRBGController -args -ginkgo.focus 'Verify PR500'` |
| 3. Live | skipped this round (debate policy: unit + integration only) | see `liveNote` in verify-manifest.json |

> Test polarity: every test here is a **contract** test (asserts the intended correct
> behavior). On buggy code they FAIL; on fixed code they PASS. No canaries.

## Problem premise (P0)

| | |
|---|---|
| Claimed symptom (quoted) | "A recreated RoleBasedGroupSet listed children purely by the set-name label, so it could delete a foreign child on scale-down, rewrite its spec, and count it as Ready in status. The RBG controller read a bound RoleBasedGroupScalingAdapter by name and let it override role replicas... Both gang-scheduling PodGroup implementations reused the PodGroup found by name and rewrote its spec while keeping the old controller reference; the Volcano path also never adopted orphans, leaking them past deletePodGroup." |
| Linked issues | none closing; PR body references #487 and #497 (merged predecessor fixes in the same bug class) |
| Reported component | RBGSet controller, RBG controller scaling-adapter paths, both PodGroup schedulers |
| Patched component | `internal/controller/workloads/{rolebasedgroupset,rolebasedgroup}_controller.go`, `pkg/scheduler/{k8s-scheduler-plugin,volcano}/scheduler.go` |
| Component match | Yes |
| **Verdict** | **Confirmed** (positive reproduction on base `56c49531`) |
| Evidence | L1 on base: `TestVerifyPremise*` all RED — foreign child deleted on scale-down; foreign bound adapter overrode replicas 1→5; foreign PodGroup `MinMember` rewritten 7→2 on both schedulers. L2 on base: 3/3 envtest specs FAIL. results/l1-*-base.txt, results/l2-envtest-base.txt |

## Summary of results (on PR head `d2915172`)

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| P0 | the claimed stale-UID bug class exists on base | 1+2 | **Confirmed** | all premise contracts RED on base, GREEN on head |
| P0-L1 | PR head fixes the controller paths | 1 | Confirmed fixed | results/l1-workloads-head.txt (3/3 PASS) |
| P0-L2 | PR head fixes the flows against a real API server | 2 | Confirmed fixed | results/l2-envtest-head.txt (3/3 PASS) |
| F1 | same-spec orphan PodGroup is never adopted → still leaks past `deletePodGroup` (both schedulers) | 1 | **Confirmed open** | results/l1-volcano-head.txt, results/l1-k8sscheduler-head.txt: `TestVerifyF1*` RED on head |

## Per-finding detail

### F1 — same-spec orphan PodGroup never adopted (volcano + k8s-scheduler-plugin)

Claim (review): the PR states both PodGroup implementations now "update and adopt orphans so
the existing `IsControlledBy`-gated delete can clean them up later". In both
`createOrUpdate` implementations the adoption (`OwnerReferences = append(...)`) lives **inside
the spec-changed branch**. An orphaned PodGroup whose spec already equals the desired spec —
the canonical `--cascade=orphan` delete + recreate-with-unchanged-spec case — never enters
that branch, so it is never adopted. `deletePodGroup` is gated on `metav1.IsControlledBy`, so
when gang scheduling is later disabled (or the RBG deleted), the orphan is skipped and leaks.

Note the sibling fix #487 in this same bug class explicitly pinned the opposite pattern:
"never skip the apply for a workload without a controller reference, so the reference is
re-attached even when the spec is already equal".

Observed vs expected (head):
- `TestVerifyF1SameSpecOrphanPodGroupAdopted` (volcano, k8s): expected `IsControlledBy(pg, rbg)`
  after reconcile; observed owner refs stay empty → FAIL.
- `TestVerifyF1SameSpecOrphanLeaksPastDeletePodGroup` (volcano, k8s): expected the PodGroup to
  be deleted once gang scheduling is disabled; observed it remains → FAIL.

Harness-bites: with the proposed one-line fix per scheduler (below) both tests flip GREEN and
the full `./pkg/scheduler/...` suite still passes
(results/l1-scheduler-full-with-proposed-fix.txt). The fix was then reverted; production diff
on this branch is empty.

Proposed fix (NOT applied to production here): include ownership in the update trigger, e.g.
volcano `scheduler.go` — add `!metav1.IsControlledBy(podGroup, rbg) ||` to the
`if podGroup.Spec.MinMember != ...` condition (same for the k8s-scheduler-plugin condition).

### Traced interaction (not a finding): adapter replacement waits for GC of the leftover workload

`reconcileRole` runs the workload reconciler before `ReconcileScalingAdapter`. A leftover
foreign-UID RoleInstanceSet makes the workload step return `ErrWorkloadNotClaimable` (#487),
so the stale adapter is not replaced until GC removes the workload. Harmless in production
(same GC dependency as every foreign-child path here; `applyRBGSAReplicasOverride` already
ignores the stale adapter in the meantime); the envtest spec simulates GC explicitly.

### Pre-existing gap surfaced while tracing (not introduced by this PR)

`getCurrentRevision`/`ListRevisions` filters ControllerRevisions by owner UID, so a leftover
revision from the predecessor is invisible to the recreated RBG's list but collides on
`Create` (`AlreadyExists`, deterministic name `<rbg>-<hash>-1`). The recreate flow error-loops
until GC collects the leftover revision. Same bug class, fourth path, out of this PR's scope;
self-heals with GC.

## Continuing after the fix (possibly on another machine)

The harness is on branch `verify/stale-uid-children-codex` (production code untouched).

1. `git fetch <fork> verify/stale-uid-children-codex && git checkout verify/stale-uid-children-codex`
2. `bash docs/verification/stale-uid-children-codex/scripts/re-verify.sh` — with no ref it
   fetches the current PR head (manifest `pr`), grafts the harness, runs L1+L2, and prints
   per-finding Fixed / Still-broken / Harness-update.
3. Polarity: all contract — F1 is Fixed only when `TestVerifyF1*` go GREEN; the P0 tests must
   STAY green (they are the regression suite for the premise).
4. Premise re-check against base: create a worktree at `56c49531`, copy the four
   `zz_verify_stale_uid*` files in, run the L1 command above and the L2 command with
   `KUBEBUILDER_ASSETS` set; all premise contracts must be RED there.

### Kickoff prompt for a fresh agent
```text
Continue verifying sgl-project/rbg PR #500. Fetch branch verify/stale-uid-children-codex from
the reviewer's fork, read docs/verification/stale-uid-children-codex/README.md and
verify-manifest.json, then run scripts/re-verify.sh with no args. Contract polarity
throughout: F1 fixed iff TestVerifyF1* go green; P0-L1/P0-L2 must stay green.
```

---

## Round 2 — head `4f32bb9f` (delta `d2915172..4f32bb9f`)

The new head is a single commit addressing round-1 findings:

- **F1 fix taken exactly as proposed**: `!metav1.IsControlledBy(podGroup, rbg) ||` added to the
  update trigger in both `pkg/scheduler/volcano/scheduler.go` and
  `pkg/scheduler/k8s-scheduler-plugin/scheduler.go`, with new same-spec orphan rows
  (`TestCreateOrUpdateSameSpecOrphanPodGroupAdopted` /
  `TestCreateOrUpdate_SameSpecOrphanPodGroupAdopted`) in both scheduler suites — also closing
  the F5 test-row gap.
- **F8 fix taken**: the scaling-adapter replace path now deletes with the observed UID as a
  precondition (`Preconditions.UID`), covered by
  `TestReconcileScalingAdapter_RecreateUsesUIDPrecondition`.

Re-verify results (`scripts/re-verify.sh 4f32bb9f`, grafts this harness onto the head):

| ID | Round-1 state | Round-2 verdict | Evidence |
|----|---------------|-----------------|----------|
| P0-L1 | fixed | **still fixed** (regression suite green) | results/reverify/unit.parsed |
| P0-L2 | fixed | **still fixed** (3/3 envtest specs green) | results/reverify/integration.parsed |
| F1 | open | **FIXED** — `TestVerifyF1SameSpecOrphanPodGroupAdopted` + `TestVerifyF1SameSpecOrphanLeaksPastDeletePodGroup` now GREEN in both schedulers | results/reverify/unit.parsed |

Fix attribution (red-on-previous): the PR's own three new tests were grafted onto the previous
head `d2915172` and all FAIL there (`delete must include a UID precondition`; `same-spec
orphan PodGroup should still be adopted` x2), and PASS on `4f32bb9f` —
results/r2-new-tests-on-prev-d2915172.txt vs results/r2-new-tests-on-head-4f32bb9f.txt.

Regression sweep on head `4f32bb9f`: `go test -mod=vendor -count=1
./internal/controller/workloads/... ./pkg/scheduler/...` all green —
results/r2-full-suites-head-4f32bb9f.txt.

Delta review: no new findings. Still open from round 1 (untouched by this delta): F2 (silent
foreign-name stall in the Set), F3 (delete+create AlreadyExists transient — the UID
precondition does not change the same-reconcile create-after-delete), F4 (adopt-then-delete
churn nit), F6 (leftover ControllerRevision collision, pre-existing), F7 (adapter name
ambiguity, unconfirmed Copilot advisory).

---

## Round 3 — head `b1d47c14` (delta `4f32bb9f..b1d47c14`)

One commit, +4/−6 in two files — exactly the F8/AC6 fix round 2 asked for:

- `rolebasedgroup_controller.go`: the adapter replace-delete now sets the **top-level**
  `client.DeleteOptions.Preconditions` (was: `DeleteOptions.Raw.Preconditions`, which
  controller-runtime's `AsDeleteOptions()` overwrites with the nil top-level field —
  vendored `options.go:354` — so the round-2 guard never reached the wire).
- `scalingadapter_uid_test.go`: the test now asserts the **post-conversion** shape
  (`delOpts.AsDeleteOptions().Preconditions`) instead of the pre-conversion `Raw` struct.

Verification this round:

| Evidence | Result |
|----------|--------|
| `scripts/re-verify.sh b1d47c14` (grafts this harness onto the head) | P0-L1, P0-L2, F1, F8-L1, F8-L2 all **FIXED/green** — results/reverify/ |
| New harness unit test `TestVerifyF8AdapterReplaceDeletePreconditionReachesWire` (asserts the wire body via the same `ApplyOptions`+`AsDeleteOptions()` conversion `typedClient.Delete` performs) | **RED on 4f32bb9f, GREEN on b1d47c14** — harness bites with correct polarity |
| Control `TestVerifyF8WireConversionControlRawOnlyFormDropped` (pins the vendored conversion semantics) | green on both |
| Runtime probe against vendored controller-runtime v0.22.1: round-3 form puts the UID in the wire body; round-2 Raw-only form still drops it | results/r3-wire-conversion-probe.txt |
| PR's updated `TestReconcileScalingAdapter_RecreateUsesUIDPrecondition` grafted onto `4f32bb9f` | **FAILS** ("delete must include a UID precondition") — red-on-previous, the updated test genuinely bites: results/r3-updated-pr-test-on-prev-4f32bb9f.txt vs r3-updated-pr-test-on-head-b1d47c14.txt |
| New envtest spec `F8: the API server enforces the UID delete precondition...` (real API server: wrong-UID delete → 409 Conflict + object survives; stale-UID delete after same-name replacement → 409 + replacement survives; correct-UID delete → succeeds) | **PASS** (1/1) — the enforcement link the fake client cannot provide |
| `go test -mod=vendor ./internal/controller/workloads/... ./pkg/scheduler/...` + `go vet` on b1d47c14 | all green — results/r3-full-suites-head-b1d47c14.txt |
| GitHub CI on b1d47c14 | 22/22 SUCCESS |

Consequence chain now closed end to end: controller sends the observed UID →
`AsDeleteOptions()` propagates it into the DELETE body → the API server rejects a
UID-mismatched delete with 409, the reconcile returns the error (Conflict is not
IsNotFound) and retries. A stale-cache reconcile can no longer wipe a live adapter.
F8/AC6: **fixed**.

Still open (unchanged by this delta): F2 (log-only foreign-name stall), F3 (transient
AlreadyExists on delete+recreate in one reconcile), F4 (adopt-then-delete churn nit),
F6 (pre-existing ControllerRevision collision), F7 (unconfirmed adapter-name ambiguity),
F9 (the sibling disabled-scaling delete at rolebasedgroup_controller.go:1031 is still
unconditional — the one-line symmetry follow-up).
