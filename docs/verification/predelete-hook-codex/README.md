# predelete-hook-codex — bug verification for PR #558

Reproducible evidence for the findings raised while reviewing
https://github.com/sgl-project/rbg/pull/558 (`fix(stateful): honor pre-delete lifecycle hooks`).

Layers run against the **code under review** (head `4d82f84`, base `fd160cc`):

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit | lifecycle patch semantics (fake client), PR's own new controller test | `go test -mod=vendor -count=1 ./pkg/inplace/instance/lifecycle/ ./pkg/reconciler/roleinstanceset/statefulmode/ -run 'TestVerify|TestStatefulRollingUpdateAndScaleDownWaitForPreDeleteHook'` |
| 2. Integration | real apiserver via envtest (1.31.0): foreground delete pinning, MarkNotReady conflict, the repo's own stateful suite | `KUBEBUILDER_ASSETS=$HOME/.local/share/kubebuilder-envtest/k8s/1.31.0-linux-amd64 go test -mod=vendor -count=1 -timeout 12m ./pkg/inplace/instance/lifecycle/ ./pkg/reconciler/roleinstanceset/statefulmode/ ./test/envtest/testcase/stateful/ -run 'TestVerify|TestStatefulRoleInstanceSetController'` |
| 3. Live | skipped per debate setup | — |

> Test polarity: contract tests (assert intended behavior) FAIL on buggy code / PASS when
> fixed. Bug-canary tests (assert current behavior) PASS now / FLIP to red when fixed.

## Problem premise (P0)

| | |
|---|---|
| Claimed symptom | "Stateful RoleInstanceSet rolling updates and scale-down can delete a RoleInstance without waiting for its existing `spec.lifecycle.preDelete` hook. A consumer therefore cannot finish draining the instance before its Pods are removed." (PR body) |
| Linked issue | none (`NONE` in PR body) |
| Reported component | stateful RoleInstanceSet controller (`pkg/reconciler/roleinstanceset/statefulmode`) |
| Patched component | same, plus `pkg/inplace/instance/lifecycle` patch mechanics |
| Component match | Yes |
| **Verdict** | **Confirmed** |
| Evidence | The PR's own new `lifecycle_test.go`, grafted unchanged onto base `fd160cc`, FAILS in both subtests: `deleted = [hook-rolling-update-0], want []` and `deleted = [hook-scale-down-0], want []` — base deletes the hooked instance on the first reconcile, never entering PreparingDelete. (`results/lifecycle_p0_test.go.on-base`; command: copy the file into base and run `go test -mod=vendor ./pkg/reconciler/roleinstanceset/statefulmode -run TestStatefulRollingUpdateAndScaleDownWaitForPreDeleteHook -count=1 -v`.) The same test passes on the PR head. |

## Summary of results

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| P0 | base deletes hooked instances without waiting | 1 | Confirmed | PR's own test fails on base, passes on head |
| F1 | foreground `DeleteInstance` breaks the repo's own envtest stateful suite (no GC in envtest → instance pinned terminating forever → replacement never created) | 2 | Confirmed | head: 3/4 specs FAIL (`results/envtest-stateful-head.log`); base: 4/4 PASS (`results/envtest-stateful-base.log`); mechanism: `TestVerifyForegroundDeletePinsInstanceWithoutGC` PASS on head (`results/envtest-verify-head.log`) |
| F2 | first PreparingDelete transition with `MarkNotReady=true` always fails with a resourceVersion conflict (readiness status write bumps RV before the optimistic-lock patch is built from the stale cached object); self-heals on retry | 1+2 | Confirmed (transient) | fake client + real apiserver both conflict on the first call (`results/unit-lifecycle-verify-head.log`, `results/envtest-verify-head.log`); retry bound green on both base and head |
| PROP | optimistic-lock patch conflicts after a concurrent consumer hook release (PR's claimed safety property) | 1 | Holds | `TestVerifyOptimisticPatchConflictsAfterConsumerUpdate` PASS on head, FAIL on base |

## Per-finding detail

### F1 — foreground deletion pins instances where no garbage collector runs (blocker)

`stateful_instance_control.go` `DeleteInstance` now issues
`Delete(..., PropagationPolicy(Foreground), Preconditions{UID, ResourceVersion})` for **every**
stateful RoleInstance deletion, hooked or not. Foreground deletion makes the apiserver keep the
object with a `foregroundDeletion` finalizer until the garbage collector removes all dependents.
envtest (used by `test/envtest/testcase/stateful`, gated in CI by `.github/workflows/envtest.yml`
on any `pkg/` change) runs **no kube-controller-manager**, so nothing ever removes that finalizer.

Observed (head): all three rollout-recovery specs time out
("waiting for new nginx:2.0 instance ...", 25s each), because the old instance object never
disappears and the stable name stays reserved (`AlreadyExists` on recreate — shown by the
micro-test). Observed (base): suite passes in 42s.

The production semantic change (serialize pod teardown before name reuse) is deliberate per the
PR body, but as submitted the PR turns the repo's own integration suite red; either the deletion
policy or the test environment has to change.

### F2 — MarkNotReady + optimistic lock: guaranteed first-attempt conflict (minor)

`UpdateRoleInstanceLifecycle` (now optimistic-lock merge patch) runs
`executeRoleInstanceNotReadyPolicy` **before** patching, and the readiness write bumps the
instance's resourceVersion, so the patch computed from the stale cached object always conflicts
on the first call whenever `markNotReady=true` and the instance carries the
`RoleInstanceCustomReady` readiness gate. Confirmed on the fake client and on a real apiserver
(envtest): first call → 409 Conflict, state label never written. A retry with a fresh read
succeeds (`...HealsWithinTwoAttempts` is green on both base and head), so the blast radius is one
failed reconcile per hooked deletion — but the same regression now also hits the pre-existing
**stateless** PreDelete / PreparingUpdate / Normal transitions with MarkNotReady, which never
conflicted before this PR.

### Supporting property — optimistic retry works as advertised

`TestVerifyOptimisticPatchConflictsAfterConsumerUpdate` proves the PR's design claim: after a
concurrent consumer hook release, a lifecycle patch built from a stale cache conflicts instead of
clobbering the release (passes on head; on base the raw merge patch silently overwrote — the
property is new).

## Proposed fixes (NOT applied to production here)

- **F1**: either drop unconditional foreground (e.g. use it only when a preDelete hook exists, or
  rely on background + ownerRef GC), or teach the envtest harness to strip `foregroundDeletion`
  finalizers / run a GC. The suite must be green again before merge.
- **F2**: reorder `UpdateRoleInstanceLifecycle` so the optimistic-lock patch is issued first and
  the readiness flip second, or build the patch from a freshly re-read object after the readiness
  write (or drop the RV precondition when a preceding write already serialized the transition).

## Continuing after the fix (possibly on another machine)

The harness is on branch `verify/predelete-hook-codex` (production code untouched), so it grafts
onto whatever the fixed code is.

1. `git fetch https://github.com/cheyang/rbg.git verify/predelete-hook-codex`
2. Prereqs: Layer 1 = Go toolchain only; Layer 2 = envtest binaries
   (`KUBEBUILDER_ASSETS=<path with etcd+kube-apiserver>`; 1.31.0 matches CI).
3. Re-run the two layer commands above, or `bash docs/verification/predelete-hook-codex/scripts/re-verify.sh`
   from a checkout of this branch (auto-resolves the current PR head from `manifest.pr`).
4. Polarity: F1 suite specs + F2 contract tests should go GREEN when fixed; the F1 canary
   (`TestVerifyForegroundDeletePinsInstanceWithoutGC`) flips RED if deletion stops pinning —
   invert or drop it depending on the chosen fix.
5. Harness-bites: F1 canary and F2 contracts behave as recorded above on head `4d82f84` and base
   `fd160cc`.

### Kickoff prompt for a fresh agent
```text
Continue a verification task on branch verify/predelete-hook-codex (remote
https://github.com/cheyang/rbg.git). Background: a review of
https://github.com/sgl-project/rbg/pull/558 produced findings F1 (foreground deletion pins
RoleInstance objects where no GC runs; repo envtest suite fails) and F2 (MarkNotReady=true
PreparingDelete transition always conflicts once). Read
docs/verification/predelete-hook-codex/README.md ("Continuing after the fix") and follow it:
graft the harness onto the fixed code, re-run all layers, mind the polarity table, and report an
observed-vs-expected table. Do not touch production code; do not run cluster-wide destructive
actions.
```
