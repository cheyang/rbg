# v1alpha1-deprecation-removal — verification harness

Reproducible evidence for the review of [PR #556](https://github.com/sgl-project/rbg/pull/556)
("refactor(deprecation): remove v1alpha1 admission webhooks").

Harness runs against the **PR head** `6108f204a1eec7b6c1300447ef7150178150b248`
(production code untouched — this branch only adds tests and docs).

## Layers

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit | CRD `deprecationWarning` == source markers; shipped manifests carry no v1alpha1 webhook entries | `go test ./test/envtest/testcase/verifyv1a1/ -run 'TestCRDDeprecationWarningMatchesMarker\|TestNoV1alpha1WebhookRemainsInShippedManifests' -count=1` |
| 2. Integration (envtest) | Real API server + conversion webhook + the shipped `config/webhook/manifests.yaml`; v1alpha1 requests end-to-end | `KUBEBUILDER_ASSETS=<envtest assets> go test ./test/envtest/testcase/verifyv1a1/ -count=1 -timeout 10m` |

envtest assets: any k8s 1.31+ (`setup-envtest use 1.36.2 --bin-dir bin -p path`);
on the reviewing machine `/home/ecs-user/.local/share/kubebuilder-envtest/k8s/1.36.2-linux-amd64` worked.

Layer 2 boots the *shipped* CRDs, registers exactly the webhook handlers
`cmd/rbgs` registers at this commit, and installs the *shipped*
`config/webhook/manifests.yaml` rewritten to the local server — so the rules,
paths, failurePolicy and (unset ⇒ Equivalent) matchPolicy under test are the
bytes the release rendering consumes, not hand-written approximations.

## Problem premise (P0)

Claimed symptom (PR body): *"users saw two overlapping `Warning: 299` headers
(confirmed in the ACK cluster test on #552)"* on every v1alpha1 write — the
CRD-level `deprecationWarning` plus the v1alpha1 admission webhook warning.

| | |
|---|---|
| Claimed symptom | Two overlapping deprecation warnings per v1alpha1 write |
| Reported component | v1alpha1 admission webhooks vs CRD `deprecationWarning` (both in `config/webhook/manifests.yaml` + CRD bases) |
| Patched component | Same — webhooks removed at source, CRD warning text gains the removal timeline |
| Component match | Yes |
| **Verdict** | **Confirmed** (differential: base = 2 warnings, head = 1 warning) |
| Evidence | `results/base-premise-envtest.txt` (base), `results/head-envtest.txt` (head) |

The premise suite (`premise-base.patch`, applied to base `73e05db` in a
worktree; re-run via `scripts/run-premise.sh`) also confirmed on base that the
enforcement, grandfathering, and type-switch rejection all existed — via *both*
the Exact-matched v1alpha1 webhooks *and* the conversion-mediated v1alpha2
gate. The redundancy claim is real: the v1alpha2 webhooks (matchPolicy
Equivalent, the default — unset in the shipped manifests) already cover
v1alpha1 writes for `rolebasedgroups`/`rolebasedgroupsets`, which are
dual-version CRDs whose v1alpha1 is served but not storage.

## Summary of results

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| V1 | A v1alpha1 write introducing a new deprecated workload use is still rejected (conversion → v1alpha2 gate), with the migration hint | 2 | Confirmed (head) | `results/head-envtest.txt` |
| V2 | An accepted v1alpha1 write returns exactly ONE warning, carrying "will be removed in 0.10.0" + the replacement pointer | 2 | Confirmed (head); base showed 2 | `results/head-envtest.txt`, `results/base-premise-envtest.txt` |
| V3 | Grandfathering survives: same-type update accepted, deprecated-type switch rejected, RBGS template path rejected, schema-defaulted workload rejected | 2 | Confirmed (head + base) | same |
| V4 | All six CRD `deprecationWarning`s match their source markers and keep the timeline; shipped manifests have no stale v1alpha1 webhook entries | 1 | Confirmed | `results/head-unit.txt` |
| V5 | The repo's own suites still pass at head (no collateral breakage) | 1+2 | Confirmed | `results/head-repo-own-tests.txt` |

All specs are **contract** tests (assert intended behavior; fail on
regression). The premise suite's "exactly two warnings" assertions are
**bug-canaries for the duplication** — they pass on base and would flip to fail
on any head that still duplicates warnings; they are only runnable on the base
commit (they register the deleted webhook handlers), hence the patch file.

## Per-claim detail

- **V1/V3 (enforcement preserved).** Mechanism: v1alpha1 request → mutating +
  validating v1alpha2 webhooks match via `matchPolicy: Equivalent` (default;
  unset in the shipped manifests) → the API server converts the object through
  the CRD conversion webhook (v1alpha1 → v1alpha2 hub) → `convertRoleV1alpha1ToV2`
  records `role.workload {apiVersion,kind}` in the
  `workloads.x-k8s.io/role-workload-type` role annotation → the v1alpha2
  validator's `validateNoNewDeprecatedWorkloadTypes` / strict validator decides.
  Verified end-to-end: rejection (incl. `spec.template.roles` for RBGS and the
  schema-defaulted `apps/v1 StatefulSet` workload), grandfathered same-type
  update accepted, type-switch rejected. Strict mode
  (`--enable-deprecated-workload-types=false`) is the same webhook +
  conversion chain with the validator's other branch (unit-tested at
  `api/workloads/v1alpha2/rolebasedgroup_validation_test.go`); not re-run as a
  separate envtest suite.
- **V2 (single warning).** The suite client carries a `rest.WarningHandler`
  recorder, so the assertion is on the actual `Warning:` headers returned by
  the API server: exactly 1 on an accepted dry-run create and on a grandfathered
  update, containing the timeline and the replacement pointer, and *not*
  containing the removed webhook's "migration guide" phrasing. On base the same
  observation yields 2 warnings (see P0).
- **V4 (manifest cleanliness / marker↔yaml equality).** Pure-file tests, no
  cluster. `TestNoV1alpha1WebhookRemainsInShippedManifests` guards both
  `config/webhook/manifests.yaml` and `deploy/kubectl/manifests.yaml` against
  stale entries whose handlers no longer exist (those would fail-close every
  v1alpha1 write in that deployment path).

## Harness-bites check

Differential by construction: the same observation (warning count on an
accepted v1alpha1 write) is asserted as **2 at base** (`premise-base.patch`
suite, passes) and **1 at head** (this suite, passes). If the PR's removal had
regressed enforcement or left duplicate warnings, the head specs
(`rejects a v1alpha1 create...`, `returns exactly one deprecation warning...`)
would fail; if the removal were incomplete (stale webhook entries), the L1
tests would fail. Re-introducing the v1alpha1 webhooks on a future head would
flip V2 to fail (two warnings) — the intended bite.

## Findings

- **claude-v1a1-coverage-gap (major, missingTests).** The PR deletes
  `test/envtest/testcase/deprecationv1a1/` — the only suite that exercised the
  v1alpha1 request path end-to-end against the *shipped* webhook manifests and
  asserted on-the-wire warnings — without replacement. After this PR, no
  in-repo test submits a v1alpha1 request through admission; the PR's central
  behavioral claim ("v1alpha1 writes still reach the v1alpha2 gate via
  conversion — same rejection, better message, one warning") is verified by
  this harness but has zero regression coverage in the repo. A future change
  (e.g. someone adds `matchPolicy: Exact` to the v1alpha2 webhooks, or breaks
  the v1alpha1→v1alpha2 workload annotation conversion, or drifts the CRD
  warning text) would pass CI silently. Ask: land an adapted v1alpha1 contract
  suite — this harness (`test/envtest/testcase/verifyv1a1/`) is a working
  template; dropping the reviewer-private framing (this README, premise patch)
  makes it a PR-ready suite.

## Continuing after the fix (possibly on another machine)

The harness lives on branch `verify/v1alpha1-deprecation-removal-claude`
(review fork), production code untouched, so it grafts onto any future head.

1. Get it onto the code to check:
   ```bash
   git fetch https://github.com/cheyang/rbg.git verify/v1alpha1-deprecation-removal-claude
   git checkout <ref-under-review>
   git checkout FETCH_HEAD -- docs/verification/v1alpha1-deprecation-removal test/envtest/testcase/verifyv1a1
   ```
2. Prereqs: Go toolchain; envtest assets for Layer 2
   (`KUBEBUILDER_ASSETS=$(setup-envtest use 1.36.2 -p path)`).
3. Re-run: `bash docs/verification/v1alpha1-deprecation-removal/scripts/re-verify.sh`
   (no ref → resolves the current PR head from `manifest.pr`), or the two layer
   commands above.
4. Polarity: all findings' tests are contracts — they must be GREEN. The base
   premise canary suite ("two warnings") is expected to keep passing only on
   the base commit; at any head it should fail (that is the point).
5. Harness-bites: graft onto the base commit (`premise-base.patch`) and watch
   the canary suite pass while the head suite's single-warning spec fails —
   both directions of the differential.

## Round 2 — head `22b2212b5b7a141c61fcb7fb76907a71964143d6` (incremental)

Delta `6108f204..22b2212b` (commits `0987a208`, `22b2212b`): +390 lines,
test-only — the author landed their own in-repo v1alpha1 request-path suite,
`test/envtest/testcase/v1a1gate/` + `test/envtest/testutil/v1a1gate_setup.go`,
responding to `claude-v1a1-coverage-gap` / ledger F1. No production code
changed; base unchanged (`73e05db4`).

Re-verify on the new head (`results/round2-22b2212b/`):

- This harness, all 7 contract specs **green** (V1 gate ×3, V2 single warning,
  V3 grandfathering ×3) + L1 unit layer green — behavior at `22b2212b` is
  identical to the previously verified `6108f204`.
- The author's new suite: **green** at head (4 specs; `author-v1a1gate-suite-head.txt`),
  wired into CI (`.github/workflows/envtest.yml` runs `./test/envtest/testcase/...`),
  and it **bites**: flipping the installed webhook to `matchPolicy: Exact`
  makes its v1alpha1 reject spec fail (`author-v1a1gate-bitecheck-matchpolicy-exact.txt`)
  — so an Exact flip or a converter regression now turns CI red in-repo.
- Full repo sweep at head: all 6 envtest packages ok (`full-envtest-sweep-head.txt`);
  `go build ./...`, `go vet`, unit tests ok. All 22 GitHub checks SUCCESS on
  this head (envtest check locally reproduced).

Coverage verdict for F1: **partially fixed**. The landed suite now covers
in-repo: explicit deprecated-use rejection via v1alpha1, the RBGS
template path, delete-ungated, and a plain-accept control. Still **not**
covered in-repo (this harness keeps guarding them, reference implementation
on this branch):

1. the single-warning count/text contract (the PR's headline user-facing
   change) — no `Warning:` header assertion anywhere in the repo;
2. grandfathering (pre-gate v1alpha1 object: same-type update accepted,
   deprecated-type switch rejected, v1alpha1 read-back);
3. the schema-defaulted workload rejection (v1alpha1 schema defaults
   `workload` to `apps/v1 StatefulSet` — a create that omits the workload
   entirely; only the explicit variant is tested).

Two notes on the new suite's setup (both verified empirically):

- The comment in `v1a1gate_setup.go` claiming the explicit
  `PrepWithoutInstalling()` call is required (else "conversion strategy None")
  is wrong for controller-runtime v0.22.1: `Environment.Start()` itself calls
  `PrepWithoutInstalling()` (pkg/envtest/server.go:317-319). Removing the
  explicit call, the suite still passes with conversion active
  (`author-v1a1gate-prep-redundancy.txt`). Harmless call, inaccurate rationale.
- The suite installs only the two gate-carrying validating webhooks
  (`vrolebasedgroup`, `vrolebasedgroupset`), matchPolicy unset (Equivalent) —
  the correct composition for this contract; production's
  `config/webhook/manifests.yaml` also defaults to Equivalent.

`.last-reviewed` advanced to `22b2212b`.

## Round 3 — head `91d244f5abe71543b74e11db12db67225b92a51e` (incremental)

Delta `22b2212b..91d244f` (commit `91d244f`, "test(v1a1gate): cover the warning
contract and schema-defaulted rejection"): +141/−6, test-only, touching only
`test/envtest/testcase/v1a1gate/gate_test.go` and
`test/envtest/testutil/v1a1gate_setup.go`. No production code, manifests, CRDs,
or Helm changed; base unchanged (`73e05db4`).

Re-verify on the new head (`results/round3-91d244f/`):

- This harness, all 11 contract specs **green** (V1 gate ×3 incl. the
  schema-defaulted rejection, V2 single warning, V3 grandfathering ×3, V5
  repo-suites, V4 unit ×2) — `reverify-harness-head.txt`: behavior at
  `91d244f` is unchanged from the previously verified heads.
- `go build ./...`, `go vet`, unit tests green (`head-build-unit.log`);
  full envtest sweep green this run (`flake-canary.txt` context; see the
  flake below for why "green this run" is seed luck for one package).
- All 22 GitHub checks SUCCESS on this head (rollup observed read-only).

### NEW (V5): the author's v1a1gate suite is now order-flaky — blocker

`91d244f` adds a second top-level `Describe` ("v1alpha1 wire contract") whose
`BeforeAll` creates the shared namespace `v1a1-gate-test` and **ignores the
error**, while the original container's `BeforeAll` uses the fatal
`testutil.CreateNamespace`. Ginkgo v2 **randomizes the order of top-level
containers by default** (`--randomize-all` only extends this to individual
specs; ginkgo v2.25.3 `types/config.go`: "By default, ginkgo only randomizes
the top level Describe, Context and When containers"), with a time-based seed
per run. When the wire-contract container is ordered first, the original
container's `BeforeAll` fails `namespaces "v1a1-gate-test" already exists` and
its 4 enforcement specs are skipped — the suite is red and the PR's own
enforcement coverage does not run.

Deterministic per seed (`results/round3-91d244f/flake-seed*.json`):

| seed | container order | suite |
|------|-----------------|-------|
| 7, 1794329300, 1794329328 (+ first random run) | wire first | **FAIL** |
| 42, 1794329336 (+ 2 random runs) | chain first | PASS |

6 of 9 runs failed. Differential: previous head `22b2212b` passes at the
failing seed `1794329328` (single container — the flake is mechanically
impossible there), so this is a revision regression introduced by `91d244f`.
CI's envtest job (`.github/workflows/envtest.yml:119`) runs
`go test -v ./test/envtest/testcase/...` with no pinned seed, so the green
envtest check on this head is seed luck; every future CI run on any PR
re-rolls a ~2-in-3 failure.

Repro (bug-canary, on this branch):
`bash docs/verification/v1alpha1-deprecation-removal/scripts/check-v1a1gate-order-flake.sh [ref]`
— exits 0 while the bug is present (suite fails at the wire-first seeds,
passes at control seed 42); flips when fixed. Harness-bites: applying the
proposed one-line fix (make the conversion-chain container's `BeforeAll`
idempotent, mirroring the wire container's error-ignoring create) makes all
seeds pass and the canary report FIXED-flip
(`results/round3-91d244f/flake-canary-bites-check-with-fix.txt`). The fix was
reverted; production/test code on this branch is untouched.

### F1 coverage verdict: warning leg landed; two legs remain, now mis-claimed

The new specs that genuinely land: **warning count/text** — asserts exactly
one warning containing v1alpha1/deprecated/0.10.0 on an accepted v1alpha1
create, plus a v1alpha2 no-warn control (the recorder rides the suite client
via `rest.CopyConfig` + `WarningHandler`). Leg 1 of F1 is fixed in-repo.

Still not covered in-repo, and now *mis-claimed as covered*:

1. **Grandfathering through v1alpha1** — no UPDATE spec anywhere in the repo
   on the v1alpha1 API; the new comment (gate_test.go:187-193) and the PR body
   say it is covered by `test/envtest/testcase/deprecation/`, but that suite
   imports **only** `workloadsv1alpha2`, submits only v1alpha2 objects, and
   contains no type-exact-switch spec at all (its five specs: controller-child
   admission, unrelated new use, forged owner ref, new set, seeded-set update
   — all v1alpha2). The "through the v1alpha1 conversion chain" wording is
   wrong; the v1alpha1-through-conversion grandfathering remains covered only
   by this harness (V3).
2. **Schema-defaulted rejection** — the new spec "rejects a v1alpha1 create
   that omits the workload field" does not omit the field: a zero Go
   `WorkloadSpec` marshals as `workload:{"apiVersion":"","kind":""}` (verified
   by marshaling), which fails the apiVersion **pattern** before any
   defaulting runs. The genuine defaulting case — `workload` key absent,
   kubectl-style YAML, CRD default `{apiVersion: apps/v1, kind: StatefulSet}`
   (`config/crd/bases/workloads.x-k8s.io_rolebasedgroups.yaml:16411`) — is
   still exercised only by this harness (V1 defaults spec). The spec's own
   comment ("pins that the v1alpha1 path cannot silently default to a
   deprecated workload type") describes a contract it does not test. The PR
   body's "UPDATE coverage" claim for the suite is likewise false (no
   `Update` call exists in it).

Also fixed this round: round-2 F2 (the "restore the coverage" PR-body
sentence is gone — replaced by a per-suite enumeration) and round-2 F3 (the
`PrepWithoutInstalling()` comment now states the correct mechanism:
"controller-runtime's Start() itself calls PrepWithoutInstalling() ... The
explicit call is harmless").

`.last-reviewed` advanced to `91d244f5abe71543b74e11db12db67225b92a51e`.

### Kickoff prompt for a fresh agent

```text
Continue a verification task on branch verify/v1alpha1-deprecation-removal-claude
(fork cheyang/rbg). Background: review of sgl-project/rbg PR #556 (removal of the
v1alpha1 deprecation admission webhooks) verified the removal is behavior-preserving
(enforcement via conversion-mediated v1alpha2 gate; one CRD-level warning) and raised
one finding: the v1alpha1 request path lost its in-repo test coverage
(claude-v1a1-coverage-gap). Read
docs/verification/v1alpha1-deprecation-removal/README.md ("Continuing after the fix")
and follow it: graft the harness onto the current PR head, re-run both layers
(envtest assets required), mind the polarity table, re-check the premise patch against
the base if the base moved, and report an observed-vs-expected table. Clean up scoped
test resources; do not run cluster-wide destructive actions.
```
