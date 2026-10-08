# Verification — PR #474 "feat: support RoleBasedGroupSet rolling update"

Reviewer: Codex (debate pipeline, Reviewer B). Reviewed head: `44241903`
(`.last-reviewed`). Production code untouched — this branch adds only
`docs/verification/**` and `*_verify_test.go` files.

## Premise (P0)

> "when the template changes, every outdated child is updated within a single
> reconcile, with no ordering and no availability gating" — PR body.

**Verdict: Confirmed** (run against base `0821cb5b`, results/p0-base.txt): one
`Reconcile` moved all 3 children to the new template at once. The same test passes
on the PR head with `rolloutStrategy` unset, which also confirms the
backward-compatibility claim (static path unchanged).

## Findings

| id | claim | polarity | layer | status on 44241903 |
|----|-------|----------|-------|--------------------|
| F1 | v1alpha1↔v1alpha2 conversion drops `spec.rolloutStrategy` and the rollout status fields (`currentRevision`, `updateRevision`, counters); v1alpha1 is still served, so a full-object v1alpha1 write silently reverts a rolling set to the un-paced static path mid-rollout | contract | unit | **RED = confirmed bug** (`TestVerify_ConversionPreservesRolloutStrategy` fails) |
| F3 | webhook rejects integer `maxUnavailable: 0` + `maxSurge: 0` but accepts `"0%"`/`"0%"` (resolves to 0/0 for any replica count); the controller then silently clamps maxUnavailable to 1, rewriting explicit user intent | canary | unit | canary passes (pins current behavior); flip when fixed |

## Guard-rail tests (no finding — the PR's core claims, verified)

| id | claim | layer | status |
|----|-------|-------|--------|
| G1 | paced recreate: never deletes ready groups beyond `ready-(replicas-maxUnavailable)`; converges; `RolloutComplete`; steady state does NOT requeue | unit | green, converged in 12 steps |
| G2 | `maxUnavailable: 0` + `maxSurge: 1`: serving never drops below replicas; surge created then reclaimed | unit | green |
| G3 | `partition: 2`: ordinals 0,1 keep old template and identity; a deleted held-back group is rebuilt at the OLD revision | unit | green |
| G4 | template flip-flop A→B→A converges | unit | green |
| G5 | replicas-only template diff scales in place, no recreation | unit | green |
| G6 | scale-in during a rollout is budget-gated and converges | unit | green |
| G7 | same drive against a REAL API server (envtest, real CRDs/defaulting): converges, each child recreated exactly once (no defaulting-induced recreate loop), foreground delete + UID precondition work | integration | green (results/l2-envtest.txt) |

## Harness-bites check (Step 4)

Two production perturbations were applied temporarily and reverted
(results/harness-bites.txt):

1. Removing the budget gate in `deleteGroupSetOutdatedChildren` → G1/G2 fail
   ("deleted 3 ready groups with ready=3 replicas=3 maxUnavailable=1").
2. Removing the rebuild-at-current rule in `fillMissingGroupSetBaseOrdinals` →
   G3 fails ("must come back on the previous template").

Production diff is empty after the revert.

## How to run

```bash
# unit layer (fake client)
go test ./internal/controller/workloads/ ./api/workloads/v1alpha1/ ./api/workloads/v1alpha2/ -run 'TestVerify_' -count=1 -v

# integration layer (needs envtest assets)
KUBEBUILDER_ASSETS=$(ls -d $HOME/.local/share/kubebuilder-envtest/k8s/*-linux-amd64 | head -1) \
  go test ./internal/controller/workloads/ -run 'TestVerify_Envtest' -count=1 -v -timeout 8m
```

Note: on the reviewed head the unit run FAILS on
`TestVerify_ConversionPreservesRolloutStrategy` — that red IS the F1 reproduction.
Expected state after the author fixes the conversion: all green, and the two F3
canaries flip red (invert or promote them then).

## Continuing after the fix

```bash
git fetch https://github.com/cheyang/rbg.git verify/rbgs-rolling-update-codex
git checkout verify/rbgs-rolling-update-codex
bash docs/verification/rbgs-rolling-update/scripts/re-verify.sh   # resolves the current PR head itself
```

re-verify.sh grafts the harness onto the current PR head, runs unit+integration, and
prints per finding Fixed / Still-broken / Harness-update (F1 fixed iff its contract
test goes green; F3 fixed iff its canaries flip). Live e2e (L3) is covered by the
repo's own kind-based suite (`make test-e2e`), which is green in CI.

Kickoff prompt for a fresh agent: "Continue verifying sgl-project/rbg PR #474 from
branch verify/rbgs-rolling-update-codex on the cheyang fork. Read
docs/verification/rbgs-rolling-update/README.md and run scripts/re-verify.sh."
