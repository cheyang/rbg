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
