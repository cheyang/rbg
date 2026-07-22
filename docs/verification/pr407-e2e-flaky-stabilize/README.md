# Verification — PR #407 `test(e2e): stabilize flaky v1alpha2 specs on slow CI clusters`

- **PR:** https://github.com/sgl-project/rbg/pull/407
- **Reviewed at (PR head):** `93545869584de11e146470623e40ac572c9f8581`
- **Base:** `c962917e28dbd5a8a865f063e21b7d020030327c`
- **Scope:** test-only — `test/e2e/testcase/v1alpha2/{coordinated_policy,ris,scaling_adapter}.go`. No production code changed. All CI green (incl. `e2e-test`, 28m).

## What this harness is

PR #407 fixes flaky e2e specs; it does **not** change production code, so there is no bug to
disprove. Instead this harness turns the two claims the review conclusion rests on into
**reproducible, contract-style proofs** that run against real production code. "All green"
here means "the PR's reasoning is confirmed."

Harness is additive only (one new test package + this dir). The production diff vs the PR head
is empty.

## Hypotheses → claims

| ID | Claim | Layer | Polarity |
|----|-------|-------|----------|
| **V1** | `coordinated_policy.go` changing `MaxSkew` 50%→33% is correct: the real `CoordinationScaler` advances **1→2→3** (one replica/batch) at 33% but **jumps 1→3** in a single batch at 50%. The single-batch jump is what let ready-skew reach 2 and made the `skew<=1` assertion flaky. `OrderReady` gating holds the next batch until the lagging role catches up. | unit | contract |
| **V2** | `scaling_adapter.go` `updateRbgSpecV2Retry` is retry-safe: re-`Get`'ing into the reused object resets `Spec.Roles`, so re-applying the `append` after a 409 Conflict yields 3 roles, not 4 (no double-append). | unit | contract |

## Observed vs expected

| ID | Test | Expected | Observed | Verdict |
|----|------|----------|----------|---------|
| V1 | `TestPR407_MaxSkewBoundsBatchStep` | 50% → target 3 (jump); 33% → target 2 then 3 | matches | ✅ Confirmed |
| V1 | `TestPR407_OrderReadyGatesNextBatch` | 33%, current=2 ready=1 → held at 2 | matches | ✅ Confirmed |
| V2 | `TestPR407_UpdateRetryReGetResetsSlice_NoDoubleAppend` | re-Get+mutate×2 → 3 roles; control (mutate×2, no re-Get) → 4 | matches | ✅ Confirmed |

Raw output: [`results/unit-scaler-and-retry.txt`](results/unit-scaler-and-retry.txt),
[`results/e2e-compile.txt`](results/e2e-compile.txt).

### The scaler math (V1), spelled out

`target = ceil((minProgress + maxSkew) · desired)`, `desired=3`, both roles start `current=1`
(`progress=1/3`):

- **50%:** `ceil((0.333 + 0.50)·3) = ceil(2.5) = 3` → **1→3 in one batch** → one role can hit
  ready=3 while its partner is still ready=1 → skew 2 → flaky failure.
- **33%:** `ceil((0.333 + 0.33)·3) = ceil(1.99) = 2`, next batch `ceil(0.997·3) = 3` →
  **1→2→3**. With `OrderReady` gating both roles to ready before advancing, ready-skew stays
  ≤1 by design.

## How to run

```bash
# unit proofs (fast, deterministic, no cluster)
go test ./test/verify/pr407/... -run TestPR407 -v

# e2e package still compiles at this ref
go vet ./test/e2e/testcase/v1alpha2/
```

## Non-blocking review notes (not defects)

1. `updateRbgSpecV2Retry` overlaps ~90% with the existing `updateRbgV2` (`rbg.go:296`), which
   also Get+mutate+Update inside `Eventually`. The real distinction — it preserves the
   caller's `rbg` and returns the fresh object — justifies a separate helper, but the `…Retry`
   suffix reads oddly since `updateRbgV2` retries too. A one-line doc or rename would help.
2. The `33%` ↔ `desiredReplicas=3` coupling is a magic constant (documented in-comment); it
   would silently stop stepping 1→2→3 if the replica count changed.

## Continuing after a change (resume / next round / another machine)

All durable state is on this branch (`verify/pr407-e2e-flaky-stabilize`): the harness, the
manifest, `.last-reviewed`, and this table. From a checkout of this branch:

```bash
# auto-discovers the current PR head from manifest.pr (machine-independent), grafts the
# harness onto it, runs the unit layer, and prints per-finding Fixed/Still-broken/Partial.
bash docs/verification/pr407-e2e-flaky-stabilize/scripts/re-verify.sh --layers unit
```

Both findings are **contract** polarity → they should stay **green** (`FIXED`) on every future
PR head as long as the scaler batching contract and the client re-Get semantics hold. A red
result would mean a real regression in production behavior the PR relied on — investigate, do
not just update the test. After reviewing a new delta, advance the marker:

```bash
echo <new-head-sha> > docs/verification/pr407-e2e-flaky-stabilize/.last-reviewed
git add docs/verification/pr407-e2e-flaky-stabilize/.last-reviewed
git commit -m "review(pr407): advance last-reviewed"
```

### Kickoff prompt for a fresh agent

> Resume the review-pipeline for https://github.com/sgl-project/rbg/pull/407. Check out branch
> `verify/pr407-e2e-flaky-stabilize`, read `docs/verification/pr407-e2e-flaky-stabilize/README.md`
> and `.last-reviewed`, run `scripts/re-verify.sh --layers unit`, then incrementally review the
> `.last-reviewed..head` delta and update the table.
