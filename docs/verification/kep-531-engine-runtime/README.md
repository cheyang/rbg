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

## Round 3 (2026-11-17) — incremental review, head c87a2e1e

The PR moved `f3d4849a -> c87a2e1e`: the round-2 stack was rebased tree-identically
(merge-base with main is still `ea5e82ae` — the PR is NOT rebased onto current main),
plus 7 new commits addressing the round-2 debate ledger: RawExtension deep-copy
(3dd34d72), UPDATE-wedge exemption (8f9da58d), deterministic scope-error order
(7fe890a6), cross-reconcile warning dedupe (44e2e531), KEP text amendment
(4a8c6f9a), drills wired into PR CI (eb1d8740), and a merge-with-main compile fix
(c87a2e1e — the PR branch predates main's #567 `DefaultImage` var; the fix keeps
the merged tree compiling). Main advanced `3abe58a5 -> 1fc69f70` (#567 e2e image
registry picking, #552/#563/#568/#585); a local test-merge of c87a2e1e into
1fc69f70 auto-merges clean and builds+vets green (incl. test/e2e).

Re-verify at c87a2e1e (results/reverify/, integration.parsed copied to
results/round3/reverify-integration-c87a2e1e.parsed): F1 canary still passes
(F-COLLISION-EXTRAPATCHES unchanged — still a minor); AC-1/AC-2/AC-3 unit
contracts green; AC-4 integration green (immutability + rbg envtest suites vs
kube-apiserver 1.31); R2-F1/F2/F3 fix contracts green. CI at head: 23/23 checks
SUCCESS, including the new "Engine-runtime upgrade drills" job.

Round-3 verdicts for the round-2 findings the new commits address:

| ID | finding | verdict at c87a2e1e | evidence |
|----|---------|--------------------|----------|
| R3-F3 | F-UPDATE-WEDGE (minor, round 2) | **Fixed** — UPDATE exempts roles whose engine-runtime surface is unchanged (both webhooks); the resolver stays the authoritative render-time gate for the stored violation | `TestVerifyPR571R3_UpdateWedgeExemptionRBG`/`RBGS` green on head, red on f3d4849a; surface-read completeness verified by grep (ValidateEngineRuntimes reads only the captured fields) |
| R3-F4 | F-SCOPE-ERROR-ORDER (nit, round 2) | **Fixed** — sorted (profile, component) walk | `TestVerifyPR571R3_ScopeOwnershipErrorOrder` green on head (20 runs, sorted+stable), red on f3d4849a with visibly unsorted output |
| R3-F1 | F-EVENTS-REEMIT (nit, round 2) | **Fixed** for the three named reasons — dedupe keyed by UID+generation+message; generation bump re-arms; render failures keep firing | `TestVerifyPR571R3_SuccessfulRenderWarningDedupe` green on head, red on f3d4849a; controls (`UIDlessFixtureAlwaysEmits`, `RenderFailureNotDeduped`) green on both |
| R3-F5 | F-CONVERSION-RAW-COPY (nit, round 2) | **Fixed** — both conversion directions deep-copy the complete RawExtension | `TestVerifyPR571R3_ConversionDeepCopiesObjectBackedValue` green on head, red on f3d4849a (Object dropped pre-fix) |
| — | Q-KEP-TEXT (round 2) | **Fixed** — KEP Validation Model, scenario table, Graduation Criteria and alternatives now record the webhook placement + >100x cost pin | doc audit of 4a8c6f9a against the enforced semantics |
| — | Q-DRILLS-CI (round 2) | **Fixed for drills** — `make test-e2e-drills` now runs on every PR (e2e-test.yml e2e-drills job) and is green at head; `test-e2e-upgrade` deliberately stays in release-test paths (stated reason: from-end matrix pinned per release hop) | CI rollup at head; workflow read |

New this round (both nit, both canaries on this branch):

| ID | finding | evidence |
|----|---------|----------|
| R3-F2 | F-DEDUPE-ROLE-COLLAPSE — dedupe fingerprint is reason+message and the Unaimed/EnvCollision messages do not name the role, so identical warnings from two roles of one RBG collapse into one event per generation | `TestVerifyPR571R3_DedupeCollapsesIdenticalCrossRoleWarnings` (1 event at head; 2 at f3d4849a, so the collapse is delta-introduced) |
| R3-F6 | F-DEDUPE-COVERAGE — CapabilityFormHazard and WrapShadowed keep the re-fire-per-reconcile property the dedupe removed for the other three | `TestVerifyPR571R3_DedupeCoverageGap`; pod_reconciler.go:306 bypasses emitEngineRuntimeWarning; container_patch.go:917's reason is not in successfulRenderWarningReasons |

Harness-bites for the round-3 contract tests:
`results/round3/bites-previous-head-f3d4849a.log` (all finding cases red on the
round-2 head; controls green). Round-3 head run:
`results/round3/r3-tests-head-c87a2e1e.log` (all green).
