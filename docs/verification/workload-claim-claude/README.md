# Verification — PR #487 "do not claim a child workload the RBG does not control"

Reviewer: Claude (Reviewer A, debate round 2026-10-11). Branch: `verify/workload-claim-claude`,
based on the PR head `feb77ba7` (+ the harness files below; production code is untouched —
`git diff feb77ba7` is empty apart from these additive test/doc files).

- PR: https://github.com/sgl-project/rbg/pull/487
- Base: `0821cb5b` (merge base with main)
- Head: `feb77ba7` (two commits: the claimable check, then orphan adoption by group label)

## Premise verdict: **Confirmed**

The PR claims: *A RBG created inside the background-deletion window finds its predecessor's
leftover workload by name and reports readiness it never earned — `ConstructRoleStatus` reads the
leftover's `readyReplicas`, so the RBG goes `Ready=True` on a workload it does not control.*

Reproduced on **base 0821cb5b** at both layers:

- Unit (`results/base/unit-base.txt`): all three `TestVerifyClaim_P0_Unit_*` contract tests FAIL —
  `ConstructRoleStatus` returns `readyReplicas=1` from a workload controlled by a foreign UID
  ("Should be zero, but was 1") and `CheckWorkloadReady` returns `true`.
- Integration (`results/envtest-base.txt` + `results/envtest-base-controller-evidence.txt`): with a
  real API server and the real RBG controller, the persisted RBG flips to `Ready=True /
  AllRolesReady` with `readyReplicas=1` read from the leftover; meanwhile the workload apply fails
  with `metadata.ownerReferences: Only one reference can have Controller set to true`, i.e. the
  base controller can never take the leftover over and the false Ready persists for the whole GC
  window. (The same base logs show the controller's own status patch with
  `"reason":"AllRolesReady","status":"True"`.)

The PR head fixes exactly this: the same tests PASS on `feb77ba7` (`results/head/unit-head.txt`,
`results/envtest-head.txt`).

Component match: the PR touches the four workload reconcilers whose status/readiness the premise
names — no mismatch.

## Observed vs expected

| ID | Claim | Layer | Base (0821cb5b) | Head (feb77ba7) | Verdict |
|----|-------|-------|-----------------|-----------------|---------|
| P0 | Role status must not be read from a foreign-controlled leftover (premise) | unit | **FAIL** ×3 | PASS ×3 | Confirmed; fix works |
| P0 | RBG must not go Ready off a foreign-UID leftover (end to end) | envtest | **FAIL** | PASS | Confirmed; fix works |
| F1 | Removing a role must still clean up its workload while another role's workload is stuck terminating | envtest | PASS | **FAIL** (timeout 20 s) | **Regression introduced by the PR** |
| F1-control | Same cleanup with nothing stuck (attribution anchor) | envtest | PASS | PASS | attributes F1 to the stuck-terminating workload |
| F2 | Canary: a claimable orphan (group label, not yet adopted) still feeds `readyReplicas` into the role status | unit | n/a (check does not exist on base) | PASS (canary documents residual) | residual of the original bug, see finding |
| F3 | Canary: workload-name truncation (63 chars) + orphan branch ignores the role label | unit | n/a | PASS (mechanism shown) | hardening gap, see finding |

## Finding F1 — the regression (the reason this round requests changes)

`checkWorkloadClaimable` (`pkg/reconciler/common.go:139`) rejects a terminating workload **even
when it is controlled by the RBG being reconciled**. Every workload reconciler then returns that
error from `Reconciler`, `reconcileRoles` aborts on the first role error
(`internal/controller/workloads/rolebasedgroup_controller.go:551`), `Reconcile` returns at Step 8,
and **Step 9 `cleanup()` never runs**. While any role's workload is terminating:

- roles in later dependency tiers are not applied at all;
- workloads of roles **removed from the spec** are never deleted — they keep running (and keep
  holding their GPUs/accelerators) for as long as the termination is stuck.

The project's own bar is explicit about this: the incompatible-gang-config path in
`rolebasedgroup_controller.go` deliberately keeps running cleanup ("leaving them running would
leak their accelerators"). The envtest spec `F1: removing a role still cleans up its workload
while another role's workload is stuck terminating` fails on the head (20 s timeout, roleb's
RoleInstanceSet still present) and passes on base — a real API server, the real controller, only
the PR between the two runs.

Suggested fix direction: don't return an error for a workload this RBG controls (terminating or
not) — skip the apply and let the deletion settle, or scope the error to the genuinely
unclaimable cases (foreign UID, orphan without the label) so one wedged workload cannot stall the
group's cleanup.

## Findings F2 / F3 — residuals, documented by canaries

- **F2**: the orphan-adoption branch (commit 2) makes a label-carrying orphan claimable, and
  `ConstructRoleStatus` happily reads its `readyReplicas` **before** adoption has happened. In the
  normal flow adoption follows within one reconcile; but if the adoption apply keeps failing, the
  RBG reports Ready off a workload it still does not control — a narrow residual of the exact bug
  this PR fixes. Canary: `TestVerifyClaim_F2_Unit_OrphanReadinessInheritedBeforeAdoption`.
- **F3**: `rbg.GetWorkloadName` truncates to 63 chars, so two roles of one RBG sharing a 52-char
  prefix map to the *same* workload name, and the orphan branch of `checkWorkloadClaimable`
  inspects only `constants.GroupNameLabelKey` — role A's orphan is claimable at role B's slot.
  The name collision itself is pre-existing; the new code inherits it. Canary:
  `TestVerifyClaim_F3_Unit_WorkloadNameCollision`.

## How to run

Unit layer (fast, no cluster):

```bash
go test ./pkg/reconciler/ -run 'TestVerifyClaim' -count=1 -v
```

Integration layer (envtest; needs kube-apiserver/etcd binaries, e.g.
`KUBEBUILDER_ASSETS=/tmp/rbg-envtest/k8s/1.31.0-linux-amd64` or
`bin/setup-envtest use 1.31.0 -p path`):

```bash
KUBEBUILDER_ASSETS=… go test ./test/envtest/testcase/rbg/ -count=1 -timeout 15m \
  -args -ginkgo.focus 'Workload claim verification'
```

Differential premise check (P0 must FAIL on base): in a worktree of `0821cb5b`, copy
`pkg/reconciler/workload_claim_verify_p0_test.go` and
`test/envtest/testcase/rbg/workload_claim_verify_test.go` (both files are written against API
surface that exists on base and head) and run the same commands.

## Re-verify after the fix

```bash
bash docs/verification/workload-claim-claude/scripts/re-verify.sh <fixed-ref>
```

Polarity: P0 and F1 are contract tests (green = fixed). F2 and F3 are canaries — when they flip
to red, the behavior they document has changed; invert or promote them then. `.last-reviewed`
holds `feb77ba7`; advance it after each round.

## Live layer

Skipped by instruction for this round: a cluster *was* reachable (2 nodes, v1.36.2), but the
debate setup restricts stage ② to unit + integration. The PR's own kind-cluster e2e already
covers the orphan-adoption path (that spec passed in the PR's CI run). What a live layer could
still add: GC-timing behavior of the leftover window on a real garbage collector.
