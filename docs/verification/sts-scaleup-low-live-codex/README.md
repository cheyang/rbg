# Verification: PR #553 — fix(sts): scale up before rolling when live replicas are low

Reviewer B (Codex) harness for https://github.com/sgl-project/rbg/pull/553
(fixes https://github.com/sgl-project/rbg/issues/547).

- Base (merge-base): `98d3c4be8c4c018ddbeb516b66a4eba4981c225c` (#540, also broken on `main`)
- Head under review: `b28c5478739b2b3de28f254de1ee4b902527e045`
- Production code: **untouched** by this branch (harness files only).

## P0 — premise verdict: CONFIRMED

Issue #547 claims: `rollingUpdatePartition` indexes `states` (sized by the live
sts replica count) past its end when the live count is far below the desired
count; controller-runtime recovers the panic and the workload never converges.

Reproduced on the base commit with the issue's own repro
(`results/premise-base.txt` — the test passes *because* the panic fires) and,
more importantly, end-to-end through the real controller against a real API
server (`results/integration-summary.txt`): after scaling the sts 4 → 1, the
base controller panics on every reconcile with

```
panic: runtime error: index out of range [1] with length 1 [recovered]
sigs.k8s.io/rbgs/pkg/reconciler.rollingUpdatePartition(...) sts_reconciler.go:421
sigs.k8s.io/rbgs/pkg/reconciler.(*StatefulSetReconciler).rollingUpdateParameters(...) sts_reconciler.go:324
sigs.k8s.io/rbgs/pkg/reconciler.(*StatefulSetReconciler).reconcileStatefulSet(...) sts_reconciler.go:147
sigs.k8s.io/rbgs/internal/controller/workloads.(*RoleBasedGroupReconciler).reconcileSingleRole(...)
```

and the StatefulSet stays at `spec.replicas: 1` forever. Note the blast radius:
the panic unwinds through `reconcileRoles`, so the entire RBG reconcile aborts —
later roles of the same group are skipped too, not just the shrunken role.

Component match: the report names `StatefulSetReconciler.rollingUpdateParameters`
case 6 → `rollingUpdatePartition`; the PR touches exactly that file and function.
Symptom match: exact. The issue is OPEN and was filed against `main@3612013e`.

## Claims and polarity

| ID | claim | layer | polarity | base (98d3c4be) | head (b28c5478) |
|----|-----|------|----------|-----------------|------------------|
| P0 | premise: panic + non-convergence on base | — | contract | reproduced (panic) | — |
| F-fix | `rollingUpdatePartition` clamps to `len(states)`; `rollingUpdateParameters` returns replicas=roleReplicas, partition=min(partition, live) when live < desired | unit | contract | panics (index out of range [1] with length 1) | PASS |
| F-fix-e2e | real controller restores sts to replicas=4 after external scale-down | integration (envtest) | contract | FAIL (60s timeout, sts stuck at 1, panic loop) | PASS (~8s) |

All harness tests are contract polarity: they FAIL on the buggy code and PASS
on the fix. "All green" on the verify branch therefore means fixed.

## Harness layout

- `pkg/reconciler/verify_pr553_sts_scaleup_test.go` — unit layer:
  - `TestVerifyPR553RollingUpdatePartitionLiveFarBelowDesired` — pure-function
    contract: live=1/rollingStep=-2 and live=0/rollingStep=-3 must not panic.
  - `TestVerifyPR553RollingUpdateParametersScalesBackUp` — fake-client caller
    chain through `rollingUpdateParameters` (role 4, maxUnavailable 1,
    maxSurge 1, role-size annotation "4", stsUpdated=false): live=1/partition=2
    → (replicas=4, partition=1); live=0 → (4, 0); live=3/partition=0 → (4, 0).
- `test/envtest/testcase/stsscaleup/` — integration layer (envtest, real API
  server + real RBG controller). Separate suite because the shared
  `testutil.SetupTestEnv` registers the RBG controller with
  `enableDeprecatedWorkloadTypes=false` (no StatefulSet watch), while
  production defaults it to true; this suite wires the production default so
  the external scale-down actually enqueues a reconcile. The test also
  emulates the kube sts controller's ControllerRevision (envtest runs no
  built-in controllers; `getReplicaStates` resolves revisions through the sts
  selector labels, which the RBG-created revisions do not carry — in a real
  cluster the kube sts controller provides them).

## How to run

```bash
# unit layer
go test -mod=vendor ./pkg/reconciler -run 'TestVerifyPR553' -count=1 -v -timeout=180s

# integration layer (needs envtest assets; repo default k8s 1.31.0)
export KUBEBUILDER_ASSETS="$(./bin/setup-envtest use 1.31.0 --bin-dir ./bin -p path)"
go test -mod=vendor ./test/envtest/testcase/stsscaleup/ -count=1 -v -timeout=6m

# full loop against the current PR head (auto-fetches it via manifest.pr)
bash docs/verification/sts-scaleup-low-live-codex/scripts/re-verify.sh
```

## Continuing after the fix

The tests are all contract polarity: they stay green while the fix holds and
go red if the behavior regresses — no inversion needed. To re-check a future
PR head from any machine, check out this branch and run `re-verify.sh` with no
arguments (it fetches the current PR head via the `pr` URL in
`verify-manifest.json`); `.last-reviewed` records the reviewed head for the
incremental-diff printout.

## Side observation (pre-existing, NOT introduced by this PR, not gating)

`rollingUpdateParameters` dereferences
`*sts.Spec.UpdateStrategy.RollingUpdate.Partition` without a nil check. An
adopted/claimable StatefulSet with `updateStrategy.type: OnDelete` (RollingUpdate
nil after API defaulting) whose template/labels match panics the reconcile.
Verified by scratch test on base and head — identical behavior, unchanged by
this PR. Tracked in the review ledger as a pre-existing risk; the harness does
not gate on it.

## Round 2 (head c768d5cd, 2026-11-08) — design change: surge restored, partition kept

The PR was rebased (`98d3c4be` -> frozen baseSha `73e05db4`; `pkg/reconciler`
content identical between the old and new base) and gained commit `c768d5cd`
"restore surge when scaling back an in-flight rollout", which addresses the
round-1 F7 finding (Copilot C4483274801). The drift-recovery branch
(`stsReplicas < roleReplicas`) changed contract:

| | round 1 (b28c5478) | round 2 (c768d5cd) |
|---|---|---|
| partition | `min(partition, live)` (clamped) | kept unchanged |
| replicas | always `roleReplicas` | `wantReplicas(roleUnreadyReplicas)` when partition > 0 (surge restored), else `roleReplicas` |

Harness response (Harness-update, per policy — not silently treated as a fix):
the L2 expectations were rewritten to the round-2 contract and a new 4-step
convergence walk was added. Polarity was re-proven: the updated harness is RED
on the previous head `b28c5478` (step-1 returns the stall-triggering `(1,4)`;
see `results/unit-prevhead-round2.txt`) and on the round-2 base `73e05db4`
(panic, `results/unit-base-round2.txt`), GREEN on `c768d5cd`
(`results/unit-head-round2.txt`).

| ID | claim | layer | base (73e05db4) | prev head (b28c5478) | head (c768d5cd) |
|----|-------|-------|-----------------|----------------------|-----------------|
| F-fix | no panic; branch returns round-2 contract values | unit | panic | FAIL (old design) | PASS |
| F7-stall | recovery walk converges: (live1,p2)->(2,5)->(1,5)->(0,4)->(0,4) | unit | panic | FAIL at step 1: got (1,4) | PASS |
| F-fix-e2e | envtest external scale-down recovers to replicas=4 | integration | (round 1: panic loop) | not re-run | PASS (~7s, `results/integration-head-round2.txt`) |

Convergence walk detail (maxUnavailable "10%" -> 0, maxSurge 1, desired 4,
mid-rollout partition 2, external shrink to live 1):
1. recovery branch: live 1, partition 2 -> returns (partition 2, replicas 5) — surge restored.
2. sts (5,2), pods 0-1 old+ready, 2-4 new+ready -> case 6: (1,5) — partition advances.
3. sts (5,1), pod 0 old+ready, 1-4 new+ready -> case 6: (0,4) — partition 0, surge reclaimed.
4. sts (4,0), all new+ready -> case 4: (0,4) — at rest. The round-1 fixed point is unreachable.

Scratch critique tests (not in the manifest; working artifacts from the debate)
were updated for the round-2 behavior and all pass on head: F3 floor case now
uses the idle path (branch returns (0,4), floor raises to 2, log prints 0), and
the F7 decision-point assertion expects (2,5).

Full `go test ./pkg/reconciler` on c768d5cd: ok. `go build ./...`: clean.

### Round-2 harness fix (envtest flake, harness-only)

The round-1 emulated StatefulSet ControllerRevision carried no owner reference.
The RBG controller's `getCurrentRevision` lists revisions by the group-name
label (present in the STS selector labels) and adopts entries with no ownerRef;
the emulated revision then tied at `Revision: 1` with the RBG revision and, when
the cache list order placed it last (`GetHighestRevision` keeps the last max),
`SetMatchesRevision(rbg, proposed, {})` returned true (an empty patch applies
cleanly) and `GetRolesRevisionHash({})` failed every poisoned reconcile with
"spec not found or wrong type", blocking scale-back until timeout. The real
kube StatefulSet controller sets a controller reference to the STS, which
excludes its revisions from that listing (`ref.UID != rbg.UID`). The harness now
sets the same ownerRef; 3 consecutive green runs in the previously failing
environment. Production code untouched; this was a harness fidelity bug, not a
PR regression (revision-selection code is identical on both bases and untouched
by the PR).
