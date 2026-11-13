# KEP-531 Engine Runtime Profile Injection — verification harness (Reviewer A, Claude)

- PR under review: https://github.com/sgl-project/rbg/pull/571
- Head reviewed (round 2): `f3d4849a3d3dc25232c8e1b70e61042f2f077bb9` (base branch tip `3abe58a5`; the head still sits on merge-base `ea5e82ae` — GitHub reports MERGEABLE)
- Head reviewed (round 1): `1810f5c6ed28a48addcc3e348699c3f4ae170a6c`
- Harness branch: `verify/kep531-engine-runtime-claude` (reviewer fork `cheyang/rbg`)
- Production code is untouched: the diff of this branch against the PR head is
  only the harness test files and this `docs/verification/` tree.

## Round 2 (incremental, at f3d4849a)

The PR was rebased (the three original commits rewritten tree-identically, now
DCO-signed) and nine fix commits landed on top. `re-verify.sh` verdicts:

| finding | polarity | round 1 | round 2 | evidence |
|---|---|---|---|---|
| **F-ENV-OWNERSHIP** profile-side env ownership hole | contract | FAIL | **FIXED** — `container_patch.go` now records into `envCreationTracker` only when `!hasEnvName` (52794c9e); my contract test green; the author also grafted `TestEngineRuntimeEnvOwnershipAfterDefault` + a creation control into the PR | `results/reverify/unit.out` |
| **F-LWS-DROP** LWS × non-LWP silent fragment drop | contract | FAIL | **FIXED** — `positionVocabulary` collapses LWS-typed non-LWP roles to `{leader, worker}` with leader the default (dbc8f208), exactly the harness-bites fix from round 1; my contract test green; the author grafted `TestReview_LWSNonLWPPatternFragmentRouting` into `pkg/reconciler/lws_reconciler_test.go` (renamed my harness twin `…FragmentRoutingVerify` to avoid the symbol collision) | `results/reverify/unit.out` |
| **F-COLLISION-EXTRAPATCHES** losing reference's extraPatches land on the winning profile's container | canary | present | **STILL-PRESENT** (non-blocking, consensus minor / KEP D8 gray zone; the author did not act on it — advisory) — `resolvePatchTarget` still resolves a profile-declared name by name against the rendered pod | `results/reverify/unit.out` |
| AC5 validation contracts (Codex 25-case suite) | contract | pass | **pass** — including the linear D6 rewrite (f7ea2b43) and the 20-error cap (52794c9e) | `results/reverify/unit.out` |
| AC6 patch grammar (Codex suite) | contract | pass | **pass** | `results/reverify/unit.out` |
| AC1 scenario-1 byte-for-byte compat (Codex oracle) | contract | pass | **pass** | `results/reverify/unit.out` |

Full-suite re-run at `f3d4849a` (round 2, independent of the PR's CI):

- `go build ./...`, `go vet ./...`: clean.
- All unit packages green (`results/unit-full-green-round2.out`; the raw
  `go test ./...` sweep also picks up `test/e2e` and envtest packages, whose
  failures there are environmental — missing default envtest assets and no
  cluster — while the dedicated envtest run below is green).
- Full envtest suite against a real kube-apiserver 1.31: **8/8 packages ok**
  (`results/envtest-full-suite-green-round2.out`, ~13 min), including the
  updated CERP immutability suite with the CREATE-free webhook registration.
- The CEL cost-budget deviation is now an executable invariant:
  `TestGeneratedCRDsAdmitNoRoleSpecCELPresenceRule` passes
  (`results/cel-cost-pin-round2.out`) — the generated RBG/RBGS CRDs stamped
  with the KEP's exact presence rule are rejected by apiextensions' own
  validator for exceeding the budget by >100x, with the unmodified CRDs as
  the clean control.

CI at this head is green with full attribution (build, unit-test, envtest,
lint, 12 e2e jobs) but does **not** run the drill suite (`RBGS_DRILLS=1` is
referenced by no workflow) — AC10's drills-in-CI ask remains open.

Base-branch note: `3abe58a5` (controller-manager readiness/liveness probes)
overlaps the PR's `deploy/kubectl/manifests.yaml` / chart edits; GitHub
reports the PR MERGEABLE, and the pull_request CI runs against the merged
tree, so the combined tree is exercised by the green CI matrix.


## Premise (P0) — verdict: CONFIRMED

The PR implements merged KEP-531. The claimed problem is the legacy
engine-runtime grammar's inventory (issue #531):

1. "Referencing a profile injects wherever the pattern happens to; a role
   cannot say 'this runtime belongs to the worker component.'"
2. "No way to touch role-declared containers. A profile author cannot add an
   env var or a volumeMount to the role's own engine container."
3. "Role customization of profile containers is env/args-only."

Confirmed on **base** (`ea5e82ae`) by code inspection of
`pkg/discovery/sidecar_builder.go::injectRuntime` — `profileName` is the only
targeting knob, the legacy override loop merges `env`/`args` only and silently
ignores every other `corev1.Container` field — plus
`results/base-differential-sidecar.out`, which pins base's unconditional
fragment injection (the differential premise for F2). Component match: the PR
touches exactly the render pipeline, API surfaces and webhooks the KEP names.

## Observed vs expected

| finding | layer | polarity | observed on head | expected | evidence |
|---|---|---|---|---|---|
| **F1** profile-side env ownership hole across stacked profiles | unit (deterministic) | contract (RED = reproduction) | Two profile-side passes on a role-declared env name: profile A defaults at `/env/0`, then profile B **appends** at `/env/-` — no render error. Rendered env: `[A, role, B]` → B (profile content) wins the name under kubelet last-occurrence. | KEP-531 Patch Grammar: "a `-` append or an insert at any other index is a render error" for a role-owned name, whatever another profile did earlier in the render ("profile content never wins a same-name contest with role content"). | `results/unit-harness-red.out` |
| **F2** LWS + non-leaderWorkerPattern roles silently drop pod-level fragments | unit (deterministic) + base differential | contract (RED = reproduction) | A standalonePattern role with the LWS workload-type annotation and a bare `profileName`-only reference: the resolver keys `FragmentApplications` by the pattern vocabulary (`role.Name`), the LWS construct path reads position `"leader"` → nothing injected, no event, no error (`containers=[nginx] volumes=[]`). Base injects the sidecar+volume into the leader template. | KEP-531 compatibility guarantee #1: "references that set only profileName render identically to main", and the default-delivery table: a deprecated-path render "keeps main's behavior verbatim for the references it can carry". | `results/unit-harness-red.out`, `results/base-differential-sidecar.out` |

Controls in both harness tests (single-profile rejection, leaderWorkerPattern
LWS leader injection, fresh-name cross-profile stacking) pass on the head, so
the red results are the findings, not fixture noise.

## Harness-bites check (proposed fixes verified, then reverted)

- **F1 fix** — record into `envCreationTracker` only names the container did
  not already carry (a default onto a role-owned name is not a creation):
  applied temporarily to `pkg/discovery/container_patch.go`; the F1 contract
  test goes green and the PR's own env-grammar suite stays green
  (`results/harness-bites-f1-fix-green.out`). Reverted.
- **F2 fix** — `positionVocabulary` gains an LWS case collapsing to the
  `{leader, worker}` vocabulary with `leader` as the unscoped default (where
  main injected), instead of the pattern vocabulary the LWS construct path
  never reads: applied temporarily to `pkg/discovery/runtime_resolver.go`;
  the F2 contract test goes green
  (`results/harness-bites-f2-fix-green.out`). Reverted.

Both are minimal, review-suggested fixes for the author to adapt, not part of
the harness.

## Full-suite re-run (this round, independent of the PR's CI)

- `go build ./...`, `go vet ./...`: clean.
- All unit packages: green.
- Full envtest suite against a real kube-apiserver 1.31
  (`KUBEBUILDER_ASSETS=… go test ./test/envtest/testcase/...`): **8/8 packages
  ok** (`results/envtest-full-suite-green.out`) — the PR's "green envtest"
  claim independently reproduced.

## How to re-run

From a checkout of **this branch**:

```bash
# unit layer only (deterministic, no cluster):
bash docs/verification/kep531-engine-runtime/scripts/re-verify.sh <fixed-ref>   # or no arg: current PR head
# exit 0 iff every finding is Fixed (contract tests green)

# integration layer (needs envtest assets):
export KUBEBUILDER_ASSETS="$(setup-envtest use 1.31.0 --bin-dir bin -p path)"
go test ./test/envtest/testcase/rbg/ -run TestRBGController -ginkgo.focus 'engine runtime'
```

The manifest (`verify-manifest.json`) maps each finding to its test and
polarity; `re-verify.sh` fetches the PR head from the manifest's `pr` URL,
grafts this harness onto it, and prints per finding Fixed / Still-broken /
Partial / Harness-update.

## Continuing after the fix

Round-1 table (both now fixed on `f3d4849a`; kept for history):

| test | on round-1 head | after a correct fix |
|---|---|---|
| `TestReview_EnvOwnershipAfterDefault` | FAIL (main sub-case) | PASS (both controls must stay green) — fixed at 52794c9e |
| `TestReview_LWSNonLWPPatternFragmentRoutingVerify` (was `…PatternFragmentRouting`, renamed after the author grafted a same-named test into the PR) | FAIL (standalone sub-case) | PASS (leaderWorker control must stay green) — fixed at dbc8f208 |

The F-COLLISION-EXTRAPATCHES canary
(`TestVerifyPR571_CollisionSkipThenRolePatch`, grafted from the Codex verify
branch) PASSES while the gray zone is present; a fix must FLIP it to fail —
then invert its assertion or promote the corrected behavior to a contract
test.

A round-1 dedupe corner (unaimed warning keyed by profileName) was fixed by
the author in f7ea2b43 (`ShouldWarnUnaimed` now keys by the reference's
engineRuntimes index).

## Kickoff prompt (fresh agent, another machine)

```
Checkout branch verify/kep531-engine-runtime-claude from
https://github.com/cheyang/rbg.git (fork of sgl-project/rbg). Read
docs/verification/kep531-engine-runtime/README.md and verify-manifest.json.
Run bash docs/verification/kep531-engine-runtime/scripts/re-verify.sh (no
argument — it resolves the current PR #571 head). Report the per-finding
verdict table; F1 and F2 are contract tests, so "STILL-BROKEN" means the
PR head still carries the defect and "FIXED" means the author landed a fix.
Do not touch production code; do not publish anything to GitHub.
```
