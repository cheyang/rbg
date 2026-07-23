# Verification — PR #409 (restart-policy backoff improvements)

**PR:** https://github.com/sgl-project/rbg/pull/409
**Code under review (head):** `1e5b0e6f` ("guard against negative baseDelaySeconds")
**Layers:** L1 unit (pure logic) + L2 integration (envtest, isolated API server with the PR's CRDs).
**Production code is untouched** — this branch adds only test files + this `docs/verification/` tree.

## Hypotheses → falsifiable claims

| ID | Claim | Polarity |
|----|-------|----------|
| F1 | When a spec change / rolling update is in progress (`Generation != ObservedGeneration`), a `Recreate`-policy instance with a recent restart + a failed pod has its update **frozen**: `checkRestartBackoff` returns >0, so `Scale()` requeues and skips scaling. Documented in-tree only as TODO "Issue #4". | **canary** (passes now, flips when fixed) |
| F3 | `shouldRecreateInstance`'s not-Ready bypass is **permanent**: `LastRestartTime` is never cleared, so an instance that restarted once bypasses `wasInstanceReady()` forever (even an hour-old restart). TODO "Issue #2". | **canary** |
| F2 | The PR's fix works: `ClearRestarting` on the delete paths removes the in-memory cache entry, so a new same-name instance is not blocked by `isAlreadyRestarting`. | **contract** (green now, stays green) |
| F4 | With `maxDelaySeconds` now defaulting to 600 + the new CEL rule, a config of `baseDelaySeconds > 600` with `maxDelaySeconds` **omitted** is **rejected at admission** (default 600 < base). Previously accepted (and silently capped at runtime). | **contract** (documents behavior change) |

## Observed vs expected (on PR head `1e5b0e6f`)

| ID | Layer | Test | Observed | Verdict |
|----|-------|------|----------|---------|
| F1 | L1 | `TestVerifyPR409_F1_SlowPathFreezesSpecChange_CANARY` | `checkRestartBackoff` returns >0 during a spec change → update frozen | **Confirmed** |
| F1 | L1 | `..._F1_Control_LegitimateBackoffStillApplies` | genuine crash (gen matched) still backs off | control green |
| F3 | L1 | `TestVerifyPR409_F3_PermanentLastRestartTimeBypass_CANARY` | hour-old `LastRestartTime` still bypasses not-Ready guard | **Confirmed** |
| F2 | L1 | `TestVerifyPR409_F2_ClearRestartingUnblocksSameName_CONTRACT` | after `ClearRestarting`, fresh same-name instance not blocked | **Confirmed (fix works)** |
| F4 | L2 | `TestVerifyPR409_F4_CEL_And_Defaulting` | `base=700, max omitted` → `Invalid value ... maxDelaySeconds must be greater than or equal to baseDelaySeconds`; `base/max omitted` → accepted, defaulted to 30/600 | **Confirmed (behavior change)** |

Raw output: `results/L1-unit-prhead.txt`, `results/L2-envtest-cel-prhead.txt`.

## Harness-bites check (Step 4)

A temporary patch applying **both** proposed fixes was applied to `instance_scale.go`, tests re-run, then reverted (production diff empty again):
- F1 fix (return 0 in the slow path when recreate was skipped due to Generation/Revision mismatch) → **F1 canary flipped to FAIL**, F1 control stayed green.
- F3 fix (bound the not-Ready bypass to the stability window) → **F3 canary flipped to FAIL**.
- F2 contract and F4 unaffected (stayed green).

This proves the canaries exercise the real code paths and are not red/green for the wrong reason.

## Proposed fixes (for the author)

- **F1 (Issue #4):** in `checkRestartBackoff`, when `shouldRecreateInstance` returned false, only enter the slow-path backoff if the reason was `wasInstanceReady`, not `Generation != ObservedGeneration` or `CurrentRevision != UpdateRevision`. Otherwise return 0 so spec changes / rolling updates are not frozen.
- **F3 (Issue #2):** clear `LastRestartTime` after a sustained Ready period (`max(maxDelay*2, 10min)`) so `wasInstanceReady()` re-gates the bypass. (Equivalent: bound the bypass by `time.Since(LastRestartTime)`.)
- **F4:** not a bug — it is a validation tightening. Worth one line in the PR/docs: `baseDelaySeconds > 600` with `maxDelaySeconds` omitted is now rejected (defaulting fills 600). Callers relying on the old silent-cap behavior must set `maxDelaySeconds` explicitly.

## How to run

```bash
# from a checkout of THIS branch (verify/pr409-restart-backoff-improvements)
# L1 (fast, no deps):
go test ./pkg/reconciler/roleinstance/sync/ -run TestVerifyPR409 -v

# L2 (envtest — isolated API server; does NOT touch any real cluster):
export KUBEBUILDER_ASSETS=$(ls -d ./bin/k8s/*/ | head -1)   # setup-envtest use 1.31.0 --bin-dir ./bin
go test ./test/envtest/testcase/restart_policy_cel/ -run TestVerifyPR409_F4 -v
```

## Continuing after the fix (any machine)

All durable state is on this branch: the harness, `verify-manifest.json`, `.last-reviewed`, this table.

```bash
git fetch origin && git checkout verify/pr409-restart-backoff-improvements
# re-run the harness against the current PR head (auto-discovered from manifest.pr):
bash docs/verification/pr409-restart-backoff-improvements/scripts/re-verify.sh
```

`re-verify.sh` applies polarity: a **canary** is reported *Fixed only when it flips to FAIL*; a **contract** is *Fixed when green*. After a canary flips, invert its assertion (or promote to a contract test) so it guards against regressions. Advance `.last-reviewed` to the new head and commit.

**Kickoff prompt for a fresh agent:**
> Resume the review pipeline for https://github.com/sgl-project/rbg/pull/409. Check out branch `verify/pr409-restart-backoff-improvements`, read `docs/verification/pr409-restart-backoff-improvements/README.md`, run `scripts/re-verify.sh`, and report Fixed/Still-broken per finding honoring polarity (F1, F3 are canaries; F2, F4 are contracts).
