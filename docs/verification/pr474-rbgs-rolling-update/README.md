# Verification: PR #474 — RoleBasedGroupSet rolling update

- **PR**: https://github.com/sgl-project/rbg/pull/474
- **Reviewed head**: `5bfa08b7038d7b827c5fd7db21f4f50e78b8d09a` (also in `.last-reviewed`)
- **Reviewer**: Reviewer B (Codex), debate pipeline round 1
- **Branch**: `verify/pr474-rbgs-rolling-update-codex` (production code untouched; the diff
  against the PR head is harness-only)

## Premise (P0) — CONFIRMED

> "Today that propagation is un-paced: when the template changes, every outdated child is
> updated within a single reconcile, with no ordering and no availability gating."

Runs against the **base** branch (merge-base `fc201cdf`): a single `Reconcile` after a
`groupTemplate` change rewrites all 3 outdated children at once
(`TestVerify_P0_BaseUpdatesEveryOutdatedChildInOnePass`, `results/premise-base.txt`).
Re-run with:

```bash
bash docs/verification/pr474-rbgs-rolling-update/scripts/verify-premise.sh <repo-root> origin/main
```

The component matches: the report and the patch both concern the v1alpha2
RoleBasedGroupSet controller. The premise is real; the findings below are about the fix.

## Findings and results (observed vs expected)

| ID | Severity | Claim | Layer | Polarity | Observed on PR head | Expected |
|----|----------|-------|-------|----------|---------------------|----------|
| F1 | major | Negative rollout budgets pass the validating webhook; `maxUnavailable: -1` with `maxSurge >= 1` wedges the rollout forever (`budget = -1 + readySurge`, never deletes a serving group, no error, `Rolling=True` forever) | unit + integration | contract | `TestVerify_F1_NegativeRolloutBudgetsRejected` FAIL (all 4 negative cases admitted); envtest create with `maxUnavailable: -1` accepted by the API server; mechanism demo shows zero deletions | rejection at admission |
| F2 | major | `spec.rolloutStrategy` is v1alpha2-only and not preserved by the v1alpha1<->v1alpha2 conversion webhook, so a full-object v1alpha1 write silently drops it and the set reverts to un-paced static updates | unit | contract | `TestVerify_F2_RolloutStrategySurvivesV1alpha1RoundTrip` FAIL (`RolloutStrategy == nil` after round trip) | preserved (e.g. annotation stash, like `preserveV1alpha1Fields`) |
| F3 | major | `validateNoRoleScalingAdapter` runs on create only; an update can newly enable `scalingAdapter` in the template, reaching the non-converging set-controller/adapter fight the rule exists to prevent | unit + integration | contract | `TestVerify_F3_ScalingAdapterNewlyEnabledOnUpdateRejected` FAIL; envtest update enabling the adapter accepted by the API server | reject newly-enabled adapters, grandfather pre-existing ones |
| F4 | major | With `partition > 0`, a held-back group that stops serving keeps surge groups alive forever (`rolloutComplete` requires every base group serving) while the status reports `Rolling=False/RolloutComplete` (computed over in-scope ordinals) — status and surge handling contradict each other | unit | contract (consistency) | `TestVerify_F4_...` FAIL: `Rolling` reason `RolloutComplete` while surge group `s-3` is retained | status and surge handling agree |
| F5 | minor | While `paused`, the scale-only path (`updateExistingRBGs` → `syncRBGMetadata`) still propagates template labels/annotations, although the pure metadata path is frozen by pause and the field documents "Paused freezes the rollout" | unit | contract | `TestVerify_F5_...` FAIL: child gains template label while paused | metadata frozen on both paths while paused |

Regression pins (pass before and after a correct fix): `TestVerify_F1_ValidBudgetsStillAdmitted`,
`TestVerify_F3_ScalingAdapterGrandfatheredOnUpdate`, plus the whole pre-existing
`TestRollingUpdate_*` / `TestStaticUpdate_*` suite.

Sanity anchor (passes today, must keep passing): integration spec **I3** — on a real API
server with the real RBGS controller, a template change under `maxUnavailable=1` puts
exactly one serving group into terminating and never a second one over an 8s window.

## How to run

Unit layer (no cluster needed):

```bash
go test ./internal/controller/workloads/ ./api/workloads/v1alpha1/ ./api/workloads/v1alpha2/ \
  -run 'TestVerify_' -count=1 -v
```

Integration layer (envtest; needs kubebuilder assets):

```bash
KUBEBUILDER_ASSETS="$(ls -d ~/.local/share/kubebuilder-envtest/k8s/* | sort -V | tail -1)" \
  go test ./test/envtest/testcase/rbgsrolloutreview/ -count=1 -timeout 10m
```

Raw outputs: `results/unit-controller.txt`, `results/unit-api.txt`,
`results/integration-envtest.txt`, `results/premise-base.txt`.

## Harness-bites proof

The proposed fixes were applied temporarily, the harness re-run (all contract tests green,
only the PR's own `TestRoleBasedGroupSetValidator_ScalingAdapterCreateOnly` flips — it pins
the disputed F3 behavior and must be rewritten with the fix), then reverted. Details:
`results/harness-bites.txt`.

## Continuing after the fix

On any machine:

```bash
git fetch https://github.com/cheyang/rbg.git verify/pr474-rbgs-rolling-update-codex
git checkout verify/pr474-rbgs-rolling-update-codex
bash docs/verification/pr474-rbgs-rolling-update/scripts/re-verify.sh
```

`re-verify.sh` resolves the current PR head from `manifest.pr`, grafts this harness onto it,
runs the unit + integration layers, and prints per-finding Fixed / Still-broken / Partial.
All findings are `contract` polarity: green = fixed. Kickoff prompt for a fresh agent:
"Continue the review verification for https://github.com/sgl-project/rbg/pull/474 from branch
verify/pr474-rbgs-rolling-update-codex; run docs/verification/pr474-rbgs-rolling-update/scripts/re-verify.sh,
review the delta in .last-reviewed..head, then advance .last-reviewed."

## Proposed fixes (direction, not shipped)

- **F1**: reject negative resolved values in `validateGroupSetRollout` (CRD cannot express a
  minimum on IntOrString, so the webhook is the only gate).
- **F2**: stash `rolloutStrategy` in a conversion annotation on `ConvertFrom`, restore on
  `ConvertTo` — the repo already does exactly this for v1alpha1-only fields.
- **F3**: in `ValidateUpdate`, reject adapters that are newly enabled relative to the old
  object; keep pre-existing adapters updatable.
- **F4**: either make `Rolling=RolloutComplete` require every base group serving (honest
  status), or scope the serving requirement to `>= partition` on both the status and the
  surge-release side. The harness accepts either consistent fix.
- **F5**: gate the metadata sync inside `updateExistingRBGs` on `!paused` when called from
  the scale-only path (or document the exception in the API field docs).
