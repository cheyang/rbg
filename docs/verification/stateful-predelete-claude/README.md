# Verification — Stateful PreDelete hooks (PR #535) — Reviewer A (Claude)

Branch: `verify/stateful-predelete-claude` (based on PR head `6d650165`)
PR: https://github.com/sgl-project/rbg/pull/535
Base: `4a0765f` (merge-base)

**The production code on this branch is byte-identical to the PR head.**
The only additions are the harness test file and this documentation
(`git diff 6d650165 -- pkg/ api/ internal/ cmd/` is empty).

## P0 — premise verdict: **CONFIRMED**

Claim (issue #534 Task 1): "On the community Stateful deletion path, the
configured PreDelete hook is bypassed and deletion proceeds."

Evidence: the PR's own `predelete_test.go` grafted onto the **base** commit
fails all four hook tests — `deleteInstance` deleted the hooked instance
immediately with no `PreparingDelete` state and no delete deferral
(`results/premise-base-differential.txt`). The same tests pass on the PR head.
Component match holds: the issue names the Stateful deletion path
(`processCondemned` / `DeleteStatefulInstance`) and the PR patches exactly
`deleteInstance`, which both scale-down and recreate-update call.

## Findings — observed vs expected

| ID | claim (one sentence) | polarity / layer | head result | verdict |
|----|----------------------|------------------|-------------|---------|
| F1 | A blocked scale-down that is regretted (replicas scaled back up before the hook releases) leaves the instance labeled `PreparingDelete` forever; stateless mode has `managePreparingDelete` for this, stateful has nothing. | contract / unit | `TestVerifyRegretRevertsPreparingDeleteState` FAILS | **Confirmed (reproduced)** |
| F1-impact-unavailable | Same wedge with `markPodNotReady: true` + readiness gate: the regretted instance stays `Ready=False` and `status.ReadyReplicas` is permanently undercounted. | canary / unit | PASSES on head (documents the bug) | Confirmed |
| F1-impact-orderedready | In OrderedReady the stuck (NotReady) ordinal also freezes creation of higher ordinals after further scale-up. | canary / unit | PASSES on head (documents the bug) | Confirmed |
| F2 | `processCondemned` now exits the OrderedReady loop while a condemned instance is merely *terminating* (base continued and deleted the lower ordinals too) — undisclosed behavior change, though it matches upstream k8s StatefulSet semantics. | canary / unit | PASSES on head; FAILS on base (`results/base-harness-differential.txt`) | Confirmed change |
| F3 | `markPodNotReady: true` is a silent no-op unless the template declares the `RoleInstanceCustomReady` readiness gate — deferred Task 2 of #534. | canary / unit | PASSES on head (documents the gap) | Confirmed gap |
| P0 | PreDelete hook blocks stateful deletion and resumes after release. | contract / integration (envtest) | stateful envtest suite PASS | Confirmed fixed by PR |

The PR's own claims that were re-verified green on head:
`pkg/reconciler/roleinstanceset/statefulmode` full package (only the
intentionally-red F1 contract test fails), `-race` on the same packages, and
the `test/envtest/testcase/stateful` ginkgo suite (both Parallel and
OrderedReady entries of the new PreDelete DescribeTable).

## Harness-bites check

`results/harness-bites-check.txt`: applying a minimal reviewer fix (a
stateless-parity `PreparingDelete → Normal` revert for in-range instances,
gated on `!IsLifecycleMarkRoleInstanceNotReady`) turns the F1 contract test
green while the PR's own tests stay green; reverting the fix turns it red
again, and the production diff is empty afterwards.

## How to run

```bash
# unit layer (fast; F1 contract test is expected RED until fixed)
go test ./pkg/reconciler/roleinstanceset/statefulmode/ -run 'TestVerify|TestStatefulPreDelete' -v

# integration layer (envtest; needs controller-runtime envtest binaries)
go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest
KUBEBUILDER_ASSETS=$(setup-envtest use 1.34.x --bin-dir bin -p path) \
  go test ./test/envtest/testcase/stateful/ -run TestStatefulRoleInstanceSetController -timeout 12m

# one-shot re-verify against the current PR head (or a fixed ref)
bash docs/verification/stateful-predelete-claude/scripts/re-verify.sh
```

## Polarity table (continuing after the fix)

| test | now | after F1 is fixed |
|------|-----|-------------------|
| `TestVerifyRegretRevertsPreparingDeleteState` (contract) | RED | **green** |
| `TestVerifyRegretLeavesInstanceUnavailableCanary` (canary) | green | flips RED if the fix also restores readiness for `MarkNotReady=true` (needs Task 2); stays green if the fix keeps the stateless-compatible "no regret with MarkNotReady" rule — then invert only the state-label part |
| `TestVerifyRegretOrderedReadyFreezesScaleUpCanary` (canary) | green | flips RED once the stuck ordinal stops blocking OrderedReady scale-up |
| `TestVerifyOrderedReadyTerminatingCondemnedBlocksLowerCanary` (canary) | green | flips RED if the author reverts to the base "keep deleting lower condemned ordinals" semantics |
| `TestVerifyMarkNotReadyWithoutGateIsNoOpCanary` (canary) | green | flips RED when Task 2 (readiness-gate initialization) lands |

## Handoff

To resume on another machine: clone the PR repo, fetch this branch
(`verify/stateful-predelete-claude` from the reviewer fork `cheyang/rbg`),
then run `bash docs/verification/stateful-predelete-claude/scripts/re-verify.sh`
— it fetches the current PR head, grafts this harness onto it, and prints
per-finding Fixed / Still-broken / Partial / Harness-update. The
`.last-reviewed` marker holds `6d650165` for the incremental review delta.

Kickoff prompt: "You are resuming the review of sgl-project/rbg PR #535 from
branch verify/stateful-predelete-claude. Read
docs/verification/stateful-predelete-claude/README.md, run
scripts/re-verify.sh, honor the polarity table above, and report per-finding
outcomes with evidence."
