# Verification — PR #514 "test(e2e): use durable pod failure injection" — Reviewer A (Claude)

- **PR:** https://github.com/sgl-project/rbg/pull/514
- **Head reviewed:** `288a840a91b7e3a45c164b06fe006475c41937d3` (base `8a7bcfcb`)
- **Harness branch:** `verify/pr514-durable-failure-injection-claude` (this branch; production code untouched apart from additive test files)
- **Layers run this round:** unit + integration (envtest). Live e2e/cluster layer skipped per round policy.

## Premise (P0) — Confirmed

Issue #490 (OPEN) claims `SetPodFailed`/`SetPodEvicted` patch a live Pod's status
subresource, which kubelet — the owner of Pod status — rewrites, so the failure
trigger can evaporate before the controller observes it, producing intermittent
timeouts or vacuous passes in the backoff specs. Verified on the base branch:
`8a7bcfcb:test/utils/utils.go:146-166` patched `pod.Status.Phase` via
`rclient.Status().Update`, and issue #490 links three failing CI runs. The
component named by the issue (the three e2e files + test/utils) is exactly what
the PR touches. Not reproduced locally — that would need a live kubelet (out of
scope this round); the mechanism and CI evidence are consistent, so the premise
is Confirmed, not Refuted. Tests-only PR, so the premise carve-out applies
anyway; the PR is reviewed on its merits as test infrastructure.

## Observed vs expected

| ID | Claim | Layer | Result | Evidence |
| --- | --- | --- | --- | --- |
| P0 | Base's status-patch failure injection is non-durable | code-path | **Confirmed** (mechanism + CI evidence) | base `test/utils/utils.go:146-166`; issue #490 CI runs |
| F1 | Both backoff e2e specs inject the 2nd failure as a container restart (pod stays Running), so `checkRestartBackoff`'s slow path has no `PodFailed` pod and the generation/revision bypass (`pkg/reconciler/roleinstance/sync/instance_scale.go:413-416`) can be removed without either spec failing | unit (coverage guard) + code-path | **Confirmed (still open at head)** | `TestBackoffSpecsRetainTerminalFailureCoverage` FAILS; mutation: removing the bypass makes the envtest freeze spec fail (`results/mutation-bypass-removed.log`) while the same mutation is invisible on the new e2e injection path |
| A4 | `ExecInPod` forwards the suite kubeconfig, preserves args verbatim, reports pod/container + command output | unit | **Pass** | `TestPodFailureInjection`; bites: dropping kubeconfig forwarding fails it (`results/mutation-kubeconfig-dropped.log`) |
| A5 | The terminal-failure fixture is healthy until the marker, then exits exactly 1 (failPod's watch requires ExitCode==1), and exits 0 on TERM | unit | **Pass** | `TestFailingPodTemplateFixture`; bites: `exit 1` → `exit 0` fails it (`results/mutation-fixture-exit0.log`) |
| G1 | Controller contracts the migrated specs rely on: restartCount triggers Recreate, Ignore annotation suppresses it, Failed-pod-only replacement under None, backoff delay, update-not-frozen-during-backoff | integration (envtest) | **Pass** (24/24 specs, 201s) | `results/envtest-head.log`; mutation: bypass removed → freeze spec FAILS, restored → passes |

## How to run

```bash
# unit layer (fixture contract, helper contract, F1 coverage guard)
go test -mod=mod ./test/utils ./test/e2e/testcase/v1alpha2 \
  -run 'TestPodFailureInjection|TestFailingPodTemplateFixture|TestBackoffSpecsRetainTerminalFailureCoverage' -v

# integration layer (envtest; needs envtest binaries, e.g. setup-envtest use 1.31.0)
KUBEBUILDER_ASSETS=$HOME/.local/share/kubebuilder-envtest/k8s/1.31.0-linux-amd64 \
  go test -mod=mod ./test/envtest/testcase/restart_policy/... -count=1

# one-shot re-verify against the current PR head (or a fixed ref)
bash docs/verification/pr514-durable-failure-injection-claude/scripts/re-verify.sh
```

## Harness-bites record (production code mutated then reverted; diff empty after)

| Mutation | Expected | Observed |
| --- | --- | --- |
| `pod_failure.go`: drop `--kubeconfig` forwarding | `TestPodFailureInjection` FAILS | FAILS (`results/mutation-kubeconfig-dropped.log`) |
| `pod_failure.go`: drop `-c <container> --` args | `TestPodFailureInjection` FAILS | FAILS (run in session; same shape as above) |
| `inactive_pod.go`: fixture `exit 1` → `exit 0` | `TestFailingPodTemplateFixture` FAILS | FAILS (`results/mutation-fixture-exit0.log`) |
| `instance_scale.go`: remove generation/revision bypass (413-416) | envtest freeze spec FAILS | FAILS in 46.8s (`results/mutation-bypass-removed.log`); restored → PASSES (`results/mutation-bypass-restored.log`) |

## F1 detail (the one change-requesting finding)

`runRestartBackoffDelayTest` and `runRestartBackoffSpecChangeTest` at head inject
the second failure via `utils.RestartNginxContainer`/`waitForNginxContainerRestart`
— the pod stays `Running` and is never `PodFailed`. In `checkRestartBackoff`
(`pkg/reconciler/roleinstance/sync/instance_scale.go`):

1. after the spec change, `shouldRecreateInstance` returns `false` purely on the
   `Generation != ObservedGeneration` / `CurrentRevision != UpdateRevision` check;
2. the slow path then scans for `PodFailed` pods (`instance_scale.go:417-426`).
   A restarted-but-Running pod is not Failed, so `hasFailedPods=false` →
   backoff returns 0 → the rolling update proceeds anyway.

So if the 413-416 bypass is ever removed (the exact "backoff freezes rolling
updates" regression this spec was written to guard), **these specs still pass**.
The maintainer requested a terminal-failure variant in review (unaddressed at
head). Mitigation that keeps the repo covered meanwhile: the envtest spec
"should not freeze rolling update when revision mismatch occurs during backoff"
(`test/envtest/testcase/restart_policy/restart_policy_test.go:1348`) injects real
`PodFailed` pods and catches the mutation (proven above) — but it simulates the
revision mismatch by patching `Status.UpdateRevision` directly, so no single
layer covers the real end-to-end propagation plus the bypass.

Suggested fix (uses this PR's own fixture): a variant of the spec-change
backoff test with `buildFailingPodTemplate` + `failPod` as the second injection,
asserting (a) the Failed Pod object survives with its UID through the backoff
window (note `IsPodActive` filters it, so active-set assertions must account
for that) and (b) the rolling update still creates the updated-env pod before
`backoffDeadline`. `TestBackoffSpecsRetainTerminalFailureCoverage` in this
harness turns green once that lands.

## Continuing after the fix

From any checkout of this branch:

```bash
bash docs/verification/pr514-durable-failure-injection-claude/scripts/re-verify.sh [fixed-ref]
```

- No ref → resolves the current PR head from `manifest.pr`.
- Prints per finding **Fixed / Still-broken / Partial / Harness-update**, applying
  polarity (all entries here are contract tests; the F1 coverage guard is red
  now and must go green when the terminal-failure variant is added).
- Exit 0 iff everything is fixed. Live layer: see `liveNote` in the manifest.
- `.last-reviewed` holds `288a840a…`; after reviewing a new delta, advance it
  (`echo <new-head> > .last-reviewed && git commit`).

Kickoff prompt for a fresh agent: "You are resuming a PR #514 review. Read
`docs/verification/pr514-durable-failure-injection-claude/README.md`, run
`scripts/re-verify.sh`, report the per-finding verdicts, review the
`last-reviewed..head` delta, update this README's table, advance `.last-reviewed`,
commit and push to `verify/pr514-durable-failure-injection-claude`."
