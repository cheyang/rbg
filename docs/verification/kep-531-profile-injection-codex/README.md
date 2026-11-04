# Verification — PR #548 (KEP-531: Engine Runtime Profile Injection)

Reviewer: Codex (debate Reviewer B). Head reviewed: `12693d0264fc8c4d4b8f700c50db1c0440b709d7`
(base `3612013e6f779401831f5947dc124cc4313faddb`). The PR is **docs-only** (one KEP),
so the harness pins the KEP's *premises about main's renderer* — the compatibility
surface the follow-up implementation PRs must preserve — instead of exercising new code.

## P0 — premise verdict: CONFIRMED (with one contradiction)

The KEP's description of main's renderer was verified claim by claim against the base
code and by 16 additive tests (16/16 PASS on the PR head, whose code is identical to
base). Issue #531 (OPEN, 2026-10-30) names the same components the KEP designs against
(`pkg/discovery/sidecar_builder.go`, `pkg/reconciler/pod_reconciler.go`) — component
match OK, and the KEP is `Refs #531`, not an auto-close.

One sentence in the KEP contradicts the code: Compatibility #6 says v1alpha1's new
`roles` MaxItems=16 matches "the v1alpha2 cap" — **no MaxItems exists on `roles` or
`engineRuntimes` in either served version** (see F1). The premise is otherwise accurate,
including the subtle leader-only sidecar default under leaderWorkerPattern.

## Per-claim results (observed vs expected)

| claim | layer | test | expected (KEP) | observed | verdict |
|---|---|---|---|---|---|
| Legacy merge is env/args-only | unit | TestKEP531_LegacyOverrideMergesEnvArgsOnly | command/image/volumeMounts ignored | only Env+Args appended | confirmed |
| Legacy override no-ops for init containers | unit | TestKEP531_LegacyOverrideInitContainerIsNoop | silent no-op | no-op, no error | confirmed |
| Unknown override container skipped (V(1) log) | unit | TestKEP531_LegacyOverrideUnknownContainerSkipped | skipped, render succeeds | skipped | confirmed |
| Append-if-missing, role's container wins | unit | TestKEP531_AppendIfMissingRoleWins | role wins, no dup | role image kept, 1 container | confirmed |
| Duplicate refs: pod-level dedup, legacy appends per ref | unit | TestKEP531_DuplicateReferencesDedupPodLevelLegacyAppendsTwice | 1 container, env x2 | exactly that | confirmed |
| Cross-profile name collision: earlier ref wins silently | unit | TestKEP531_CrossProfileCollisionFirstReferenceWins | first wins | first image kept | confirmed |
| Env stage runs after sidecar, covers injected containers | unit | TestKEP531_InjectEnvCoversProfileInjectedContainer | RBG_* on sidecar | RBG_ROLE_NAME present | confirmed |
| mergeEnvVars last-wins + reorder | unit | TestKEP531_MergeEnvVarsLastWinsAndReorders | [base, batch, lwp, user] | exact order, last wins | confirmed |
| LWP worker skips sidecar stage (both reconcilers) | integration | TestKEP531_S1_WorkerTemplatesSkipSidecarInjection | leader-only default | confirmed in source | confirmed |
| injectContainers inert | integration | TestKEP531_S2_InjectContainersInert | no consumers | zero consumers | confirmed |
| No MaxItems caps on roles/engineRuntimes (either version) | integration | TestKEP531_S3_NoMaxItemsCapsExistOnMain | KEP says v1alpha2 cap exists | **none exists** | **KEP text refuted (F1)** |
| customComponents names unconstrained | integration | TestKEP531_S4_CustomComponentNamesUnconstrained | (KEP silent) | no Pattern on InstanceComponent.Name | confirmed (F3) |
| CERP cluster-scoped | integration | TestKEP531_S5_CERPClusterScoped | cluster scope | marker present | confirmed |
| No CERP watch; updateStrategy unconsumed | integration | TestKEP531_S6_ProfileEditsDoNotPropagate | KEP silent | sole consumer is sidecar_builder | confirmed (F2) |
| templateRef prohibited for LWS | integration | TestKEP531_S7_TemplateRefProhibitedForLWS | prohibited | validation present | confirmed |
| Duplicate mountPath rejected by apiserver | integration | TestKEP531_S8_ApiserverRejectsDuplicateMountPath | KEP says kubelet last-wins | apiserver "must be unique" | **KEP mechanism wrong (F4)** |

Harness-bites: see `results/harness-bites.txt` — four inverted assertions all failed as
expected; production code was never modified (`git diff` vs PR head: harness files only).

## How to run

```bash
go test ./pkg/discovery/  -run 'TestKEP531_' -v   # behavior claims (fake client)
go test ./pkg/reconciler/ -run 'TestKEP531_' -v   # source-structure claims
```

Or re-run everything against the current PR head (auto-fetched via `manifest.pr`):

```bash
bash docs/verification/kep-531-profile-injection-codex/scripts/re-verify.sh
```

## Continuing after KEP revisions / implementation PRs

- All tests are **contract** polarity: they PASS while main's renderer matches the KEP's
  compatibility premises. If an implementation PR intentionally changes renderer behavior
  (e.g. adds worker-side injection by default), the corresponding test flips red — that is
  the signal to update the KEP's compatibility section, not to blindly fix the test.
- The debate findings (F1..Fn) are **doc-text** defects; a KEP text fix is judged by
  re-reading `keps/531-engine-runtime-profile-injection/README.md` at the new head —
  re-verify green does not prove the text changed. `verify-manifest.json`'s
  `_docFindings` maps each F-number to its evidence test.
- Handoff prompt: "Check out branch verify/kep-531-profile-injection-codex of the
  reviewer's fork (https://github.com/cheyang/rbg.git), read
  docs/verification/kep-531-profile-injection-codex/README.md, run scripts/re-verify.sh."
