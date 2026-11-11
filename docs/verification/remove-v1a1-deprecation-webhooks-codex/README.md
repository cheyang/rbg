# Verification — PR #556: remove v1alpha1 admission webhooks

Reviewer: Codex (Reviewer B). Round: 4 (incremental). Reviewed head: `15386660954fe1216af9610e8c7e258a1de38e5c` (base `73e05db4`).
Harness: additive only — `test/envtest/testutil/v1a1gate_setup.go` + `test/envtest/testcase/v1a1gate/`. No production code touched.

## Premise (P0) — CONFIRMED

The PR claims the v1alpha1 deprecation admission webhooks are redundant because
(a) the CRD `deprecationWarning` fires on every v1alpha1 request with no webhook
dependency, and (b) the v1alpha2 validating webhooks (default
`matchPolicy: Equivalent`) route v1alpha1 writes through the conversion webhook
into the v1alpha2 deprecated-workload-type gate.

- **Base run** (73e05db4 + base-only registration of the removed webhooks):
  a v1alpha1 RBG create returned exactly the **two overlapping warnings** the PR
  describes — CRD warning + webhook warning (see `results/p0-premise-base.txt`,
  the spec-3 failure dump lists both). Duplication confirmed.
- **Head run** (6108f204, production shape — shipped CRDs + shipped webhook
  manifests + production registrations, conversion patched onto the two CRDs):
  7/7 specs pass (see `results/l2-envtest-head.txt`).

## Observed vs expected

| # | Claim (all contract polarity) | Observed on head | Verdict |
|---|---|---|---|
| A1 | v1alpha1 RBG create w/ explicit deprecated workload (apps/v1 StatefulSet) rejected via the v1alpha2 gate, with migration hint | rejected, `new uses are not accepted` + `RoleInstanceSet` hint | Confirmed |
| A2 | v1alpha1 RBG create omitting `workload` (schema-defaulted to apps/v1 StatefulSet, unstructured client) rejected | rejected, same gate message | Confirmed |
| A3 | accepted v1alpha1 write returns exactly ONE deprecation warning, carrying `will be removed in 0.10.0` | exactly 1 warning; text matches updated CRD warning | Confirmed |
| A4 | pre-gate (grandfathered) v1alpha1 object stays updatable | update replicas 1→2 succeeded | Confirmed |
| A5 | switching grandfathered role StatefulSet→Deployment rejected | rejected, `new uses are not accepted` | Confirmed |
| A6 | v1alpha1 RBGSet with deprecated template rejected | rejected, `new uses are not accepted` | Confirmed |
| A7 | v1alpha1 deletes ungated | delete succeeded | Confirmed |

Harness-bites check: with the shipped webhook configs NOT installed, A1 fails
(the deprecated create is accepted) — the assertions exercise the gate, not
vacuous pass conditions. `make manifests` on the head tree produces no diff
(idempotent). Neighboring suites `test/envtest/testcase/deprecation` and
`.../webhook` pass unchanged.


## Round 2 (incremental, head 22b2212b)

Delta 6108f204..22b2212b is test-only (+390): the author landed
`test/envtest/testcase/v1a1gate/` + `test/envtest/testutil/v1a1gate_setup.go`
(their own version, 4 specs) in response to round 1's F1.

- **Re-verify of this harness on 22b2212b**: all 7 contract specs FIXED/green
  (`results/reverify/integration.parsed`, run via `scripts/re-verify.sh`).
  Production behavior unchanged by the test-only delta, as expected.
- **Author's suite verified**: passes 4/4 at 22b2212b
  (`results/round2-author-suite-head.txt`).
- **Author's suite bites**: flipping its installed webhook config to
  `matchPolicy: Exact` makes the deprecated-v1alpha1-create spec fail
  ("Expected an error to have occurred") — the suite genuinely exercises the
  Equivalent-match conversion routing
  (`results/round2-author-suite-bites-exact-matchpolicy.txt`).
- **Coverage differential vs the deleted deprecationv1a1 suite**: the landed
  suite covers rejection (RBG + RBGSet) and ungated deletes. It does NOT
  assert the on-the-wire warning count/text (no Warning-handler recorder) and
  does NOT cover v1alpha1-path grandfathering/type-switch updates (A3/A4/A5
  here remain the only executable checks of those; v1alpha2 validator
  grandfathering is unit-tested in `api/workloads/v1alpha2`). F1 stays open,
  narrowed to those two legs.

## Round 3 (incremental, head 91d244f5)

Delta 22b2212b..91d244f5 is a single test-only commit (+141/-6) adding a second
Describe (`v1alpha1 wire contract`, 3 specs: exactly-one-warning-with-0.10.0,
no-warning-on-v1alpha2, reject-omitted-workload) to the author's v1a1gate
suite, wiring a `GateWarningRecorder` into the suite client, and fixing the
PrepWithoutInstalling comment (round-2 F3 -> fixed).

- **Re-verify of this harness on 91d244f5**: all 7 contract specs green
  (`results/round3-reverify-head.txt`, `results/reverify/`). Behavior
  unchanged (test-only delta).
- **NEW F4 (blocker, revision regression)**: the author's suite is now
  order-flaky. The two Ordered Describes share `gateNamespace`; the first
  hard-fails on AlreadyExists (`testutil.CreateNamespace`), the second
  tolerates it. Ginkgo randomizes top-level container order per seed, so when
  the wire-contract Describe runs first the suite fails with
  `namespaces "v1a1-gate-test" already exists`. Evidence:
  `results/round3-author-suite-seed-sweep.txt` (5/8 seeds FAIL at 91d244f5),
  `results/round3-author-suite-head-seed-fail.txt` (fixed-seed repro),
  `results/round3-flake-fixcheck.txt` (all failing seeds pass once the first
  BeforeAll tolerates AlreadyExists). Runnable repro:
  `scripts/flake-repro.sh`. CI envtest job is green on this head by luck (no
  seed pinning, `.github/workflows/envtest.yml:119`).
- **F1 leg 1 (warning count/text) now covered in-repo**: the new wire-contract
  specs pass and BITE — stripping the timeline from the CRD deprecationWarning
  fails the spec on the `0.10.0` assertion
  (`results/round3-author-suite-bites-crd-timeline.txt`).
- **F1 leg 3 partially covered**: the author's omitted-workload spec exercises
  only the typed-Go-client variant (the struct field serializes explicit empty
  strings, so schema pattern validation on `workload.apiVersion` rejects
  before defaulting/admission). The wire-level variant (key absent -> CRD
  defaults to apps/v1 StatefulSet -> v1alpha2 gate rejects with the migration
  hint) remains harness-only (A2, green at head). The spec's comment ("the
  v1alpha1 path cannot silently default to a deprecated workload type") is
  wrong for wire clients such as kubectl.
- **F1 leg 2 (v1alpha1 grandfathering through conversion) still harness-only**
  (A4/A5, green at head). The new in-file comment claims it is covered by
  `test/envtest/testcase/deprecation` "through the v1alpha1 conversion chain";
  that suite imports and exercises v1alpha2 types only.
- **F5 (nit)**: `GateWarningRecorder.Containing` is added at
  `test/envtest/testutil/v1a1gate_setup.go:269` but never called anywhere in
  the tree (the suite uses `All()`/`Reset()`).

## How to run

```bash
export KUBEBUILDER_ASSETS=$(setup-envtest use 1.31.0 -p path)   # or .../k8s/1.31.0-linux-amd64
go test ./test/envtest/testcase/v1a1gate -count=1 -timeout 10m
```

## Continuing after changes

`bash docs/verification/remove-v1a1-deprecation-webhooks-codex/scripts/re-verify.sh`
from a checkout of this branch grafts the harness onto the current PR head and
re-runs the layers. All seven specs are contract tests: green = behavior
preserved. If the PR is later changed such that a spec fails for a *good*
reason (e.g. the 0.10.0 removal lands), update the assertion and the manifest.

## Proposed follow-up (not a PR defect)

The repo's own CI has no equivalent of this suite after the PR: the removed
`deprecationv1a1` contract suite tested the v1alpha1 webhooks directly, and the
surviving `deprecation` suite only writes v1alpha2. The
v1alpha1→conversion→v1alpha2-gate path this PR makes sole is exercised only by
this reviewer harness. Upstreaming a trimmed version of it would close that
regression window.

## Round 4 (incremental, head 15386660954f)

Delta 91d244f5..15386660 is a single test-only commit (+24/-37):
`test(v1a1gate): fix order flake and correct coverage claims`.

- **F4 (round-3 blocker) FIXED**: namespace creation hoisted from the two
  per-Describe `BeforeAll`s into `BeforeSuite`
  (`suite_test.go`: `testutil.CreateNamespace("v1a1-gate-test")` once, before
  Ginkgo randomizes container order). Verified on a pristine head worktree:
  `results/round4-flake-fixcheck-1538666.txt` — 0/10 seeds fail, including all
  eight previously-failing seeds (7, 1794329300, 1794329328, 1794329513, 2, 4,
  5, 8) and both previously-passing controls (42, 1794329336). At wire-first
  seed 7 the suite now runs 7 of 7 specs, 0 skipped
  (`results/round4-author-suite-seed7-speccount.txt`; round-3 failure mode was
  "Ran 4 of 7, 1 Failed | 3 Skipped"). Same runnable check:
  `scripts/flake-repro.sh`.
- **F5 (false coverage attribution) FIXED**: the trailing comment now states
  v1alpha1-path grandfathering / type-exact switch have *no* in-repo coverage
  (previously credited to the v1alpha2-only deprecation suite); the spec is
  retitled "rejects a v1alpha1 create with a present-but-empty workload" with
  an accurate comment and an explicit zero-value `WorkloadSpec{}` (wire-identical
  to before); the PR body no longer claims "UPDATE coverage" and now discloses
  both residual legs as not covered in-repo.
- **F6 (dead code) FIXED**: `GateWarningRecorder.Containing` removed; zero
  callers remain (`grep -rn '\.Containing(' --include='*.go' .` clean), package
  still compiles (`strings` still used at v1a1gate_setup.go:242).
- **Re-verify of this harness on 15386660**: 7/7 contract specs green
  (`results/reverify/`, run via `scripts/re-verify.sh`). Behavior unchanged
  (test-only delta). Unit layer + build/vet clean at head; envtest
  `deprecation` and `webhook` suites green at head.
- **F1 stays open (minor)**: the two residual legs — v1alpha1-path
  grandfathering UPDATE and genuine key-absent workload defaulting — remain
  covered only by this harness (A2/A4/A5, green at head). Now honestly
  disclosed in the PR body and code comments, but no tracked issue exists yet.
