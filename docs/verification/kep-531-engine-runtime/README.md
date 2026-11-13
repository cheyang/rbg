# Verification — PR #571 (KEP-531 engine runtime profile injection) — Reviewer: codex

Branch: `verify/kep-531-engine-runtime-codex`, based on PR head `1810f5c6ed28a48addcc3e348699c3f4ae170a6c`
(merge-base `ea5e82ae59169e0e954fb1c7068f9480fb4b7375`). Harness is additive: test files + this
directory only; production code untouched.

## Premise (P0) — Confirmed

KEP-531 (`keps/531-engine-runtime-profile-injection/`, merged via #548) specifies component
targeting (`engineRuntimes[].component`), per-container consent (`targetContainers`) and
profile-side `containerPatches`; the base has none of them (base `EngineRuntime` carries only
`profileName`/`injectContainers`/`containers`, and `sidecar_builder.go` has no targeting).
Component match: the PR touches exactly the components the KEP names. The claimed CEL
cost-budget deviation (presence rule in the webhook instead of a RoleSpec-scoped CEL rule) is
plausible and documented in code; envtest proves the webhook carries the rule.

## Observed vs expected

| ID | layer | polarity | expected | observed | verdict |
|----|-------|----------|----------|----------|---------|
| AC-1 | unit  | contract | scenario-1 render byte-identical to main oracle | identical across 10-case matrix; negative control bites | Confirmed (guarantee holds) |
| AC-2 | unit  | contract | grammar enforced per KEP (roots, entrypoint rule, RBG_ prefix, mounts, env default-only) | all enforced | Confirmed |
| AC-3 | unit  | contract | ValidateEngineRuntimes enforces D6/D7/vocabulary/deprecated-surface/dup-env | all 25 cases pass | Confirmed |
| AC-4 | integration (envtest, kube-apiserver 1.31) | contract | CERP freeze across both versions, tracking status, targeting renders | PR's immutability + rbg suites pass re-run | Confirmed |
| F1 | unit  | canary | KEP D8 gray zone: losing-collision reference's extraPatches | land on the surviving other-profile container | Confirmed behavior; minor finding |

## How to re-run

Unit layer (no cluster needed):

```bash
go test ./pkg/discovery/ ./api/workloads/v1alpha2/ -run TestVerifyPR571 -v
```

Integration layer (needs envtest assets, e.g. from setup-envtest):

```bash
KUBEBUILDER_ASSETS=$(setup-envtest use 1.31.0 -p path) \
  go test -count=1 ./test/envtest/testcase/immutability/... ./test/envtest/testcase/rbg/...
```

Live layer: intentionally skipped (debate setup); the PR's 20/20 real-cluster drill claim is
author-reported.

## Continuing after a fix

`scripts/re-verify.sh` fetches the current PR head, grafts this harness onto a temp worktree,
and re-runs the unit layer. F1 is a **canary**: if the collision+patch interplay is changed to
a render error, `TestVerifyPR571_CollisionSkipThenRolePatch` (F1) flips red — invert its assertion
(or promote the render error to a contract test) when that lands. All other tests are contract
tests: green = behavior intact.

## Raw outputs

- `results/unit-verify.log` — the harness's own tests (13 PASS)
- `results/unit-full-suite.log` — full repo unit suite at head (exit 0)
- `results/envtest-immutability.log`, `results/envtest-rbg.log` — PR's envtest suites re-run

## Round 2 (2026-11-14) — incremental review, head f3d4849a

The PR moved `1810f5c6 -> f3d4849a` (rebase-identical first three commits, then 8 fix commits
addressing the round-1 debate) and main advanced `ea5e82ae -> 3abe58a5` (#575, manager probes —
no contract overlap). Round-2 verdicts:

| ID | finding | verdict at f3d4849a | evidence |
|----|---------|--------------------|----------|
| R2-F1 | F-LWS-DROP (blocker, round 1) | **Fixed** — vocabulary collapses to {leader, worker} with leader default for the grandfathered LWS × non-LWP shape | `TestVerifyPR571R2_LWSVocabularyCollapse` + `TestVerifyPR571R2_LWSConstructDeliversLeader` green on head, red on 1810f5c6; Claude's original `TestReview_LWSNonLWPPatternFragmentRouting` re-run green (`results/round2/claude-contract-tests-head-f3d4849a.out`) |
| R2-F2 | F-ENV-OWNERSHIP (major, round 1) | **Fixed** — `envCreationTracker` records only genuinely created names | `TestVerifyPR571R2_EnvOwnershipAcrossStackedProfiles` green on head, finding case red on 1810f5c6; Claude's `TestReview_EnvOwnershipAfterDefault` re-run green |
| R2-F3 | C4557947790 (advisory, round 1) | **Fixed** — consent-only `targetContainers` validated against role + own-profile declarations | `TestVerifyPR571R2_ConsentOnlyTargetsValidated` green on head, unknown-target case red on 1810f5c6 |
| F1 | F-COLLISION-EXTRAPATCHES (minor) | **Still present** — canary unchanged | re-verify at f3d4849a: canary still passes |
| AC-1/AC-2/AC-3 | compat / grammar / validation contracts | **Green** on f3d4849a | `results/reverify/` (re-verify.sh at f3d4849a) |
| AC-4 | envtest integration | **Green** on f3d4849a, 8/8 packages vs kube-apiserver 1.31 | full `./test/envtest/testcase/...` re-run (immutability 95s, rbg 322s) |

Also verified this round: the CEL cost-budget evidence is now an in-repo reproducible test
(`TestGeneratedCRDsAdmitNoRoleSpecCELPresenceRule`, offline apiserver CRD validation — the
shipped CRDs validate clean, injecting the KEP's RoleSpec CEL rule is rejected >100x over
budget); the scope-ownership check is linear + capped at 20 errors with admission-equivalent
semantics; the CERP webhook no longer intercepts CREATE (no-op there anyway); all 11 commits
carry DCO sign-off; the `[WIP]` marker is dropped; the shipped example is self-consistent
(the init container installs the wrapper it prepends).

Harness-bites for the new contract tests: `results/round2/bites-previous-head-1810f5c6.out`
(every finding case red on the round-1 head, controls green).
