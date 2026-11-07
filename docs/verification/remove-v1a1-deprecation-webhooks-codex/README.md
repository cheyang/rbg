# Verification — PR #556: remove v1alpha1 admission webhooks

Reviewer: Codex (Reviewer B). Round: initial. Reviewed head: `6108f204a1eec7b6c1300447ef7150178150b248` (base `73e05db4`).
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
