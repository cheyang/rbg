# Verification — PR #409 (restart-policy backoff improvements)

**PR:** https://github.com/sgl-project/rbg/pull/409
**Rounds:** R1 reviewed head `1e5b0e6f`; **R2 (current) reviewed head `67b13b6d`.**
**Layers:** L1 unit (pure logic) + L2 integration (isolated envtest with the PR's CRDs).
**Production code is untouched** — additive test files + this `docs/verification/` tree only.

## Round 2 summary (head `67b13b6d`)

The author pushed three commits directly addressing R1:
- `9ce22679` — **fixes F1 and F3** (exactly as suggested).
- `25f61156` — **documents F4** in `failure-handling.md`.
- `67b13b6d` — adds the author's own envtest + e2e regression tests.

`re-verify.sh` result against `67b13b6d`: **all 4 findings FIXED** (F1/F3 canaries flipped;
F2/F4 contracts green). The two canaries have been **inverted into contract tests** so they now
guard against regression.

| ID | Finding | Polarity (R2) | R1 | R2 verdict | Evidence |
|----|---------|---------------|----|-----------|----------|
| F1 | Slow-path backoff froze rolling updates / spec changes | contract (was canary) | Confirmed bug | **Fixed** — early `return 0` on Generation/Revision mismatch | `results/L1-unit-round2-fixed.txt` |
| F3 | `wasInstanceReady` bypass was permanent | contract (was canary) | Confirmed bug | **Fixed** — bounded via `hasRecentRestart()` | `results/L1-unit-round2-fixed.txt` |
| F2 | `ClearRestarting` on delete unblocks same-name instance | contract | Confirmed fix | **Still green** | `results/L1-unit-round2-fixed.txt` |
| F4 | `maxDelaySeconds=600` default + CEL rejects `base>600` w/ `max` omitted | contract | Confirmed behavior change | **Still green + now documented** | `results/L2-envtest-cel-prhead.txt` |

### F1 fix (author)
```go
if !shouldRecreateInstance(...) {
    if instance.Generation != instance.Status.ObservedGeneration ||
        instance.Status.CurrentRevision != instance.Status.UpdateRevision {
        return 0 // don't freeze a spec change / rolling update
    }
    ...
}
```
### F3 fix (author)
`hasRecentRestart(instance)` (restart within `max(maxDelay*2,10min)`) replaces the old
`LastRestartTime == nil`, so the not-Ready bypass is bounded, not permanent. `LastRestartTime`
is preserved as a historical record. Shared `stableThreshold()` helper extracted.

## New in Round 2 — F5 (non-blocking, test quality)

The author's **new** envtest suite `test/envtest/testcase/restart_policy` is **flaky under a
full-suite run**: `15 Passed | 2 Failed` of 17, but **both failing specs PASS when run in
isolation** (`results/L2-presweep-round2-*.txt`). So the production recreate logic is fine —
the tests race under contention:

- `RestartPolicy=None … should only replace the failed pod` (@505): fails with
  `409 Conflict — the object has been modified` on `Status().Update(pod)`. The test `Get`s a pod
  then updates its status without `retry.RetryOnConflict`; the controller mutates the pod
  concurrently. **Fix:** wrap the status update in `retry.RetryOnConflict` (or re-`Get` before
  update). Applies to the other `Status().Update(pod)` sites too.
- `Recreate … should recreate all component pods when worker fails in LeaderWorker pattern`
  (@349): 60s `Eventually` timeout under full-suite CPU contention (passes alone in ~30s for
  both specs). **Fix:** raise this spec's timeout and/or reduce cross-spec contention.

This is a CI-flakiness risk, not a correctness bug. Worth fixing before relying on the suite
as a merge gate.

## Harness-bites check (Round 1, still valid)

Applying the proposed F1+F3 fixes to `instance_scale.go` flipped both canaries to FAIL (control
+ contracts stayed green); production diff empty after revert. R2 confirms the *real* author fix
makes the inverted contracts pass.

## How to run

```bash
# L1 (fast):
go test ./pkg/reconciler/roleinstance/sync/ -run TestVerifyPR409 -v
# L2 (isolated envtest; does NOT touch any real cluster):
export KUBEBUILDER_ASSETS=$(ls -d ./bin/k8s/*/ | head -1)   # setup-envtest use 1.31.0 --bin-dir ./bin
go test ./test/envtest/testcase/restart_policy_cel/ -run TestVerifyPR409_F4 -v
# Re-verify everything against the current PR head (auto-resolved from manifest.pr):
bash docs/verification/pr409-restart-backoff-improvements/scripts/re-verify.sh
```

## Continuing after further changes (any machine)

All durable state is on this branch. `re-verify.sh` reads `.last-reviewed` (now `67b13b6d`) and
reports the `last-reviewed..head` delta. F1/F3 are now **contract** tests (green = still fixed).

**Kickoff prompt:**
> Resume the review pipeline for https://github.com/sgl-project/rbg/pull/409. Check out
> `verify/pr409-restart-backoff-improvements`, read the README, run `scripts/re-verify.sh`,
> report per finding. All of F1–F4 are contracts now; F5 (envtest flakiness) is tracked in the README.
