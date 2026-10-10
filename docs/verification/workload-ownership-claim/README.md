# workload-ownership-claim — bug verification

Reproducible evidence for the review of https://github.com/sgl-project/rbg/pull/487
("fix: do not claim a child workload the RBG does not control"), head `feb77ba7`, base `0821cb5b`.

Layers run against the code under review:

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit | `ConstructRoleStatus` / `CheckWorkloadReady` / `Reconciler` of all 4 workload reconcilers (fake client) | `go test ./pkg/reconciler/ -run 'TestZZVerify' -v` |
| 2. Integration | real API server via envtest (no GC -> the leftover window is stable), full RBG manager running | `KUBEBUILDER_ASSETS=<envtest assets> go test ./test/envtest/testcase/rbg/ -run TestRBGController -ginkgo.focus 'PR487 workload ownership claim' -v` |
| 3. Live | not run (no cluster provided for this round) | — |

> Polarity: P0/F-tests are CONTRACT tests (red on base, green on head). The ConfigMap test is a
> CANARY: it asserts current behavior on both refs and flips red only when the residual window is fixed.

## Problem premise (P0)

| | |
|---|---|
| Claimed symptom | "the RBG reported readiness it never earned" — a re-created same-named RBG reads `readyReplicas` from a workload left behind by its deleted predecessor (PR body) |
| Linked issue | none (`NONE.` in the PR body); premise taken from the PR body itself |
| Reported component | RBG workload reconcilers + `ConstructRoleStatus` (pkg/reconciler) |
| Patched component | same (pkg/reconciler/common.go + 4 reconcilers) |
| Component match | Yes |
| **Verdict** | **Confirmed** |
| Evidence | L1: `TestZZVerifyP0LeftoverReadinessNotInherited` red on base — status 1/1 + ready=true read from a leftover controlled by a stale UID (results/l1-unit-base.txt). L2: full-stack on base — the re-created RBG reaches `Ready=True` on the leftover's seeded status while the same reconcile error-loops with `FailedReconcileWorkload` (results/l2-envtest-base.txt). Nuance proven by the harness: on base the *write* path did not silently take over — the apiserver rejects the second controller ref with 422 — so the silent part of the bug is exactly the status/readiness inheritance the PR body names. |

## Summary of results

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| P0 | leftover readiness is inherited on base | 1+2 | Confirmed | base red / head green; results/l1-unit-base.txt, results/l2-envtest-base.txt |
| F-write | reconciler must not write to a stale-UID workload | 1+2 | Confirmed fix | head: `Reconciler` returns `ErrWorkloadNotClaimable`, object untouched; base: fake-client apply mutates owner refs (L1) / apiserver 422 (L2, `ZZ-EVIDENCE direct-reconcile error`) |
| F-terminating | terminating workloads report 0/0 and refuse reconcile | 1 | Confirmed behavior change | red on base (1/1, ready), green on head; results/l1-unit-*.txt |
| F-orphan-nolabel | orphan without group label is refused | 1 | Confirmed | red on base (adopted), green on head |
| F-orphan-equal | equal-spec orphan is still re-applied/adopted (skip-path fix) | 1 | Confirmed | red on base (skip leaves it unadopted), green on head |
| F-guard | own workload still managed normally | 1 | Green on both | no over-refusal |
| F-adopt-e2e | orphan with group label adopted in place by the full controller | 2 | Confirmed | green on head: controller ref re-attached, UID preserved; results/l2-envtest-head.txt |
| F-residual-cm | refined discovery ConfigMap apply still 422s against a stale-controlled CM | 2 | Confirmed (canary, both refs) | `ZZ-EVIDENCE configmap-apply error: ... Only one reference can have Controller set to true` in results/l2-envtest-head.txt and -base.txt |

## Per-finding detail

- **P0 (premise)** — On base, `ConstructRoleStatus`/`CheckWorkloadReady` read whatever object sits at
  `<rbg>-<role>` with no ownership check. Unit: leftover (stale UID, 1/1) yields role status 1/1 and
  ready=true on base; head returns 0/0/false. envtest: with the full manager running, a same-named RBG
  created behind a stale-controlled Deployment went `Ready=True` on base (`P0 violated` failure is the
  red contract), while its own reconcile looped on the 422. On head the RBG stays `Ready=False`, the role
  status reads 0, and the Deployment keeps its stale owner ref and spec.
- **F-terminating** — documented behavior change: a workload with a deletionTimestamp is now refused even
  when this RBG controls it. Windows are tiny for finalizer-less workloads; longer drains (finalizers) now
  show 0/0 + `FailedReconcileWorkload` events instead of the last live status.
- **F-residual-cm** — the leftover window is closed for the 4 workload kinds but not for the other
  RBG-owned singletons written with `controller=true` owner refs (refined discovery ConfigMap named after
  the RBG, gang PodGroup, scaling adapter). The CM case is proven: the apply still fails with the
  single-controller 422 until the GC collects the stale object. Self-healing, visible via events, no false
  readiness (a fresh RBG has no status to corrupt). Minor.
- **Harness-bites check** — the same two files were run unmodified against base (`0821cb5b`) and head
  (`feb77ba7`): every contract test flips red->green exactly where the PR changes behavior, and the guard
  test stays green on both, so the harness exercises the changed paths. No production file was modified;
  the harness is additive (`zz_verify_*` files).

## Continuing after the fix (possibly on another machine)

The harness lives on branch `verify/workload-ownership-claim-codex` (production code untouched).

```bash
git fetch https://github.com/cheyang/rbg.git verify/workload-ownership-claim-codex
git checkout <fixed-ref>
git checkout FETCH_HEAD -- docs/verification/workload-ownership-claim \
  pkg/reconciler/zz_verify_pr487_ownership_test.go \
  test/envtest/testcase/rbg/zz_verify_pr487_leftover_test.go
go test ./pkg/reconciler/ -run 'TestZZVerify' -v
KUBEBUILDER_ASSETS=$(setup-envtest use -p path) go test ./test/envtest/testcase/rbg/ \
  -run TestRBGController -ginkgo.focus 'PR487 workload ownership claim' -v
```

Expected on fixed code: all unit contract tests green; envtest specs 1-3 green; the ConfigMap
canary flips red only when the residual window is closed (then invert it into a contract test).
Or one line from a checkout of this branch: `bash docs/verification/workload-ownership-claim/scripts/re-verify.sh`

Kickoff prompt for a fresh agent: "Re-verify sgl-project/rbg PR #487: fetch branch
verify/workload-ownership-claim-codex from https://github.com/cheyang/rbg.git, read
docs/verification/workload-ownership-claim/README.md, run scripts/re-verify.sh, report per-finding
Fixed/Still-broken."
