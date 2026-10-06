# pr489-restart-backoff-e2e — bug verification

Reproducible evidence for the review of https://github.com/sgl-project/rbg/pull/489
("test: make restart backoff e2e deterministic"). The PR is test-only: it changes one e2e
spec (`test/e2e/testcase/v1alpha2/restart_policy_stability.go`,
"It: RecreateRoleInstanceOnPodRestart with backoff delays second recreation") to stop
injecting failures via a Pod status patch and instead trigger a real nginx container restart.

Layers run against the code under review (PR heads `42f57f72` and `3ae23fd9` — the second
commit adds `--kubeconfig` forwarding to `restartNginxContainer` plus a unit test; merge-base
`7ed1860c`):

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit/compile | the e2e package builds and vets clean at PR head | `go build ./test/... && go vet ./test/e2e/...` |
| 3. Live | the focused spec on a real cluster (controller + kubelet) | `scripts/00-prereq-check.sh` → `scripts/20-run-new-test.sh` (PR head) / `scripts/30-run-old-test.sh` (base) / `scripts/35-observe-old-test.sh` (base, instrumented) |

> Test polarity: F1 is a contract test (PASS = fix works). F2 is a canary of the old
> mechanism (expected RED on base where the flake bites; it is environment-dependent —
> see below). The PR is test-only, so "fixed code" == base controller + new test.

## Problem premise (P0)

Run against the **base** branch without the patch.

| | |
|---|---|
| Claimed symptom | "The test injected failure by directly setting a live Pod phase to `Failed`. During the 90-second backoff, kubelet could overwrite it with `Succeeded`, so the delayed reconcile no longer saw a restart trigger and the test timed out intermittently." (quoted from the PR body) |
| Linked issue | none (PR says NONE); cites CI job `runs/37258247385/job/111599840074` on PR #488 |
| Reported component | e2e failure injection (`test/utils.SetPodFailed`) + controller `checkRestartBackoff`/`shouldRecreateInstance` trigger window |
| Patched component | same e2e spec only; controller untouched |
| Component match | Yes |
| **Verdict** | **Confirmed** |
| Evidence | (a) CI job cited by the PR: kubelet event `Killing pod/e2e-backoff-test-role-1-0-0` with no delete, final dump shows the pod `Phase=Succeeded, Reason=Completed, ExitCode=0, RestartCount=0`, no second `ReCreateInstance` event, 150s timeout at "pods should be recreated with new UIDs"; same spec also failed on main on 2026-09-21/22 (runs 35559402297, 35751708068, same file:line 611). (b) `results/p0-demo.log`: on the local k8s 1.36 cluster a patched `Failed` phase reverts to `Running` within 6–9s with `restartCount=0`. (c) `results/manual-replay.md`: full controller replay shows the outcome is environment-dependent (on this cluster the phase happened to persist and the old flow passed; on kind-1.31 it flipped to Succeeded and the trigger evaporated). |

## Summary of results

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| P0 | Patched `Failed` pod phase is not durable; whether it survives the 90s backoff is environment-dependent → old spec is inherently flaky | 3 | **Confirmed** | CI failure dumps (kind-1.31: →Succeeded, stuck forever); `results/p0-demo.log` (aliyun-1.36 bare pod: →Running in ≤9s) |
| F1 | PR-head spec passes deterministically on a real cluster: recreation happens only after the 90s backoff, UIDs change, instance stable | 3 | **Confirmed** | `results/runA-new-test.log` (head 42f57f72, PASS 118.7s) and `results/runA2-new-test-head-3ae23fd9.log` (head 3ae23fd9 incl. kubeconfig-forwarding commit, PASS 120.1s); PR #489's own CI e2e-test green, 31m14s |
| F2 | Base-version spec fails on the CI-like environment (kind-1.31) | 3 | **Not-reproduced on this cluster** (expected: environment-dependent) | `results/runB-old-test.log`, `results/runB2-old-test.log` — old spec PASSED twice here (117s/118s); on this kubelet the `Failed` phase persisted through the backoff window, see `results/manual-replay.md`. The base failure is positively documented by the CI artifacts listed in P0, so the canary is considered red on the CI environment rather than green overall. |
| — | Controller backoff semantics themselves (sanity, unchanged by the PR) | 3 | Confirmed correct | `results/manual-replay.md`: second recreation held until exactly `lastRestartTime+90s` (07:16:16 → 07:17:46), crashed pod preserved during the window, `restartCount` 1→2, no restart loop afterwards |

## Per-finding detail

### P0 — mechanism

`SetPodFailed` does `client.Status().Update(pod, phase=Failed)`. Pod status is owned by the
kubelet, so the injected state does not survive:

- kind / k8s 1.31 (CI): kubelet sees a terminal phase on a pod with running containers and
  kills them; nginx exits 0 on SIGTERM; kubelet rewrites the phase to `Succeeded`.
- aliyun k8s 1.36 (this cluster), unmanaged pod: phase reverts to `Running` within ~6–9s,
  container untouched (`results/p0-demo.log`).
- aliyun k8s 1.36, RBG-managed pod: phase happened to persist ≥44s until the controller
  deleted the pod at backoff expiry (`results/manual-replay.md`) — which is why the old
  spec passes here and fails on CI.

In all variants the controller's delayed post-backoff reconcile can no longer rely on
`phase=Failed` still being there; `shouldRecreateInstance` only triggers on `Failed` or
`containerRestartCount>0` and explicitly ignores `Succeeded` (KEP non-goal), and the
inactive-pod replacement path only deletes `Failed` pods (`instance_scale.go`), so a
`Succeeded`/`Running` flip leaves the instance stuck.

### F1 — the fix

The PR replaces injection with `kubectl exec <pod> -c nginx -- nginx -s quit` (a real,
kubelet-owned container restart; pod template restartPolicy defaults to Always) and waits
for `containerStatuses[0].restartCount` to increase before asserting the backoff window.
`restartCount` is written by the kubelet and cannot evaporate, so the post-backoff reconcile
still sees the trigger. Observed: PASS, 118.7s, single run; the whole spec sequence
(restart → recreation → recovery → 2nd restart → 15s no-recreation window → post-backoff
recreation → 20s stability) completed with the expected timing.

### F2 — polarity note

The base spec was run twice on this cluster expecting the 150s timeout; it passed both times
because this kubelet does not rewrite the phase of controller-owned pods fast enough to beat
the controller. This does NOT refute P0 (CI artifacts are positive evidence on kind-1.31,
twice on main + once on PR #488). It does mean the old flake is invisible on some
environments, which is exactly why it slipped through repeatedly.

## Live run notes

- Cluster: 3-node aliyun k8s v1.36.2 (KUBECONFIG default), pre-existing `rbg-system`
  controller `v0.9.0-eadb6c20` (merge-base `7ed1860c` descendant delta touches only
  unrelated warmup/API-deprecation code — restart/backoff logic identical to PR head,
  and the PR is test-only anyway).
- CRDs already contained `leaderWorkerPattern.restartPolicyConfig.{type,baseDelaySeconds,maxDelaySeconds}`; nothing was installed or modified cluster-wide.
- Spec runs used `--ginkgo.focus='RecreateRoleInstanceOnPodRestart with backoff delays second recreation'`.
- All test namespaces were deleted after the runs (`test-ns-*` by the suite itself,
  `rbg-p0-demo`, `rbg-verify-manual`, `rbg-repro2` manually).

## Review notes (not bugs in this PR)

- `runRestartBackoffSpecChangeTest` (same file, "rolling update proceeds during backoff") and
  `runRestartPolicyRecreateTest` still inject via `SetPodFailed`. Same latent race; for the
  spec-change test a vanished `Failed` phase makes the "no recreation during backoff"
  assertion vacuous rather than failing. Worth a follow-up, not a blocker for this PR.
- nit: the restartCount wait reads `containerStatuses[0]`; fine for the single-container
  template, but a lookup by container name would be sturdier.

## Continuing after the fix (possibly on another machine)

The harness is on branch `verify/pr489-restart-backoff-e2e` (production code untouched —
the branch adds only this `docs/verification/` tree).

1. Check out the branch; everything is under `docs/verification/pr489-restart-backoff-e2e/`.
2. Prereqs: Go toolchain; Layer 3 needs `KUBECONFIG` to a cluster with rbgs CRDs + controller
   (`scripts/00-prereq-check.sh`).
3. Re-run: `scripts/re-verify.sh` (resolves the current PR head from `manifest.pr`, runs the
   compile layer, prints the review delta from `.last-reviewed`), then the live scripts
   `20-run-new-test.sh` (expect PASS) and — only on a kind-1.31-like environment —
   `30-run-old-test.sh` (expect the 150s timeout on base).
4. Polarity: F1 contract = green on the PR; F2 canary = red on base *where the kubelet
   rewrites patched pod phases*; do not "fix" F2 by inverting it — it documents the
   pre-PR failure mode.

### Kickoff prompt for a fresh agent
```text
Continue a verification task on branch verify/pr489-restart-backoff-e2e (fork remote).
Background: review of https://github.com/sgl-project/rbg/pull/489 (test-only deflake of the
restart-backoff e2e spec). Harness and evidence live under
docs/verification/pr489-restart-backoff-e2e/ — read its README.md first. Re-run
scripts/re-verify.sh to get the current PR head and the review delta since .last-reviewed,
then run the live layer with a working KUBECONFIG (scripts/20-run-new-test.sh; optionally
30-run-old-test.sh on a kind-like cluster). Report an observed-vs-expected table. Clean up
scoped test namespaces; no cluster-wide destructive actions.
```
