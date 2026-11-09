# Verification — PR #557 "fix(roleinstance): recreate all member pods on failure or deletion"

Reviewer: codex (stage ② of review-pipeline). Round: initial.
Reviewed head: `8f63c864e669cfc14920ab3d9c79b46d2762c942` (base `fd160cc964b0e1438bead4ad7324d0a8f0bf5eac`).
Production code is untouched on this branch; the harness is additive test files + this directory.

## P0 — premise of fixes #542: **Confirmed**

Claim (quoted from issue #542): once a member Pod is deleted/evicted without ever entering
`Failed`, "the same reconcile flips RoleInstanceReady to False, and shouldRecreateInstance()
gates on that current condition, so from then on the restart policy is permanently disabled".

Evidence (differential, unit layer):

| test | base `fd160cc` | head `8f63c86` |
| --- | --- | --- |
| `TestPR557PremiseTerminatingMemberTriggers` (terminating member, NotReady, no recent restart) | FAIL (bug reproduced) | PASS |
| `TestPR557PremiseRestartWhileNotReadyTriggers` (container restart, NotReady, no recent restart) | FAIL (bug reproduced) | PASS |

Integration corroboration on base: envtest spec "should not recreate during initial creation
(instance not yet Ready)" PASSES on base — i.e. the base suite asserted the gated behavior this
PR removes (results/envtest-base-guard-old-behavior.txt). On head that spec was rewritten as
cross-instance isolation and the new `durable_recovery_test.go` suite passes
(results/envtest-restart-policy-head.txt).

Component match: report names `pkg/reconciler/roleinstance/sync/instance_scale.go` and
`instance_status.go`; the PR patches exactly those. Symptom match: yes (NotReady-forever after
node failure). Residual scope NOT covered by the PR (documented by the author): a member that
disappears before being observed terminating is still only single-pod-replaced, and there is no
not-ready timeout (deferred to #380) — see finding F-D.

## Findings and harness

| ID | claim | polarity | layer | head result | base result |
| --- | --- | --- | --- | --- | --- |
| P0-guard | terminating/restarted member triggers recreation while NotReady | contract | unit | pass | fail (bug) |
| CORE-drain | drain resumes across reconciles/restarts, counts once, blocks replacement until old UIDs gone, pre-Ready retries work | contract | integration (envtest `Durable RoleInstance recovery`) | pass | n/a (feature absent) |
| F-A | `calculateDiffsWithExpectation` now performs an unconditional, fatal fresh API read for EVERY instance, including `restartPolicy=None` ones that can never carry a retiring list; a transient read error blocks all scaling (pre-PR: read only for the policy, non-fatal) | canary | unit | pass (behavior present) | fail (absent) |
| F-B | `deletePods` adds UID+resourceVersion preconditions on ALL delete paths; non-restart paths (scale-in, gang recreate, Failed cleanup) delete from informer snapshots, so a stale RV yields 409 Conflict + FailedDelete event + retry (pre-PR: no preconditions) | canary | unit | pass (behavior present) | fail (absent) |

Raw runs: results/unit-head.txt, results/unit-base.txt, results/unit-full-head.txt,
results/envtest-restart-policy-head.txt, results/envtest-all-head.txt,
results/envtest-base-guard-old-behavior.txt.

Harness-bites check: the base/head differential above IS the bites check for every claim —
each test passes on exactly one side of the change. No production file was modified at any point
(git diff base..branch shows only docs/verification + `verify_pr557_*_test.go`).

Reasoning-only findings (no harness): F-C (dead `restartTrackingChanged`), F-D (auto-close
scope of #542 vs deferred not-ready timeout), missing e2e for the new deletion trigger
(existing e2e cover container-restart recreation; envtest covers the new trigger).

## Re-run / resume after a fix

```bash
git fetch https://github.com/cheyang/rbg.git verify/roleinstance-retiring-pods-codex
git checkout verify/roleinstance-retiring-pods-codex
bash docs/verification/roleinstance-retiring-pods-codex/scripts/re-verify.sh   # auto-discovers current PR head
```

- contract tests green = fix still intact; canary flipped (FAIL) = F-A/F-B behavior addressed
  (then invert or drop the canary).
- `.last-reviewed` records the reviewed head; advance it after each round.
- L1 (unit) is deterministic. L2 (envtest) needs `KUBEBUILDER_ASSETS` or `setup-envtest` for
  k8s 1.31.0. L3 (live) was skipped per round instructions; see `liveNote` in the manifest.
