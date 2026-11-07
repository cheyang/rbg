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
