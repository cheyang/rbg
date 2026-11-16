# manager-pdb-spread — verification (PR #589)

Reproducible evidence for the review of https://github.com/sgl-project/rbg/pull/589
("feat(deploy): add a PDB and node spreading for the controller manager",
head `24cdf951`, base `1fc69f70`).

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit | chart render + kubectl manifests content (the PR's own tests, shown to fail on base) | `go test ./test/chart/ -run 'TestChartManagerDisruption\|TestKubectlManifestsManagerDisruption' -v` |
| 1b. Unit | helm values edge cases (coalescing, opt-outs) | `bash docs/verification/manager-pdb-spread-codex/scripts/helm-values-edges.sh` |
| 1c. Unit | `helm upgrade --reuse-values` value resolution, replicating helm v3.16.3 `reuseValues` | `cd docs/verification/manager-pdb-spread-codex/scripts/reusevalues && go run . <old-chart-dir> <new-chart-dir>` |
| 2. Integration | shipped PDB object from `deploy/kubectl/manifests.yaml` vs a real API server (envtest 1.31.0): eviction subresource | `KUBEBUILDER_ASSETS=$(pwd)/bin/k8s/1.31.0-linux-amd64 go test ./test/envtest/testcase/pdbeviction/ -v` |
| 3. Live | not run this round (no cluster) | see `liveNote` in verify-manifest.json |

> Polarity: all tests are contract tests. The PR's own chart tests FAIL on base
> (`results/chart-tests-base.out`) and PASS on the head (`results/chart-tests-head.out`) —
> that base run doubles as the harness-bites check.

## Problem premise (P0)

| | |
|---|---|
| Claimed symptom | "Every RBG admission webhook has `failurePolicy: Fail`, so every RoleBasedGroup create or update depends on at least one of the two controller replicas being reachable... draining that node evicts both at once." (issue #584) |
| Linked issue | #584, OPEN, filed 2026-11-13, still valid |
| Reported component | Helm chart + `config/` controller manager install paths |
| Patched component | exactly those (`deploy/helm/rbgs`, `config/manager`, `deploy/kubectl/manifests.yaml`) |
| Component match | Yes |
| **Verdict** | **Confirmed** |
| Evidence | `failurePolicy: Fail` on all 6 webhook configs in both install paths; the new tests fail on base with `PodDisruptionBudget "rbgs-controller-manager" not found` (`results/chart-tests-base.out`) |

## Summary of results

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| A1 | Both install paths ship a PDB (`maxUnavailable: 1`, `AlwaysAllow`, selector matches manager pods) + hostname `ScheduleAnyway` spread; opt-outs work | 1 | Confirmed | `results/chart-tests-head.out`, `results/helm-values-edges.out` |
| A2 | `deploy/kubectl/manifests.yaml` is drift-free w.r.t. `config/` | 1 | Confirmed | `make manifests` on the head produces zero diff (`results/make-manifests.log`) |
| A3 | Author's upgrade note: plain upgrade / fresh install get the PDB + spread; `--reuse-values` leaves them off unless set explicitly | 1c | Confirmed (author's note is accurate) | `results/reuse-values-sim.out`; helm v3.16.3 `pkg/action/upgrade.go` `reuseValues` sets `chart.Values = oldVals`, so new chart defaults do not fill in |
| A4 | The shipped PDB's semantics: Ready manager pod refused at `DisruptionsAllowed=0` (429), evicted at 1 with decrement; unready pod evictable at 0 **only** because of `AlwaysAllow` | 2 | Confirmed | `results/envtest-pdb-eviction.out` (cases 1-4 incl. IfHealthyBudget control) |
| A5 | `--set-json controller.podDisruptionBudget={}` must not disable the PDB (chart's own "map present, no enabled key" precedent) | 1b | Confirmed safe: defaults coalesce, PDB still renders | `results/helm-values-edges.out` |
| A6 | `helm lint`, yamllint clean; CI green | 1 | Confirmed | `results/helm-lint.out`; PR statusCheckRollup success |

## Notes behind the findings filed in the review

- **Single-node drain blocking (review F1, minor)**: with the default `replicaCount: 2` on a
  single-node cluster (or any setup where all replicas sit on the drained node), a drain evicts
  one Ready pod (budget 2-1=1) and then blocks on the second — the replacement cannot schedule
  while the node is cordoned. This is standard PDB behavior and is exactly the protection issue
  #584 asked for; it is filed as a minor documentation suggestion, not a defect.
- **`--reuse-values` discoverability (review F2, minor)**: the fact that `--reuse-values`
  upgrades keep the PDB/spread off exists only in the PR body; the chart README does not
  mention it. Scenario B of `results/reuse-values-sim.out` proves the behavior.

## Proposed fixes (NOT applied to production here)

- F1/F2: documentation-only — mention both behaviors in `deploy/helm/rbgs/README.md`.

## Continuing after the fix (possibly on another machine)

The harness is on branch `verify/manager-pdb-spread-codex` (production code untouched).

```bash
git fetch https://github.com/cheyang/rbg.git verify/manager-pdb-spread-codex
git checkout verify/manager-pdb-spread-codex
bash docs/verification/manager-pdb-spread-codex/scripts/re-verify.sh   # resolves the PR head itself
```

The integration layer needs envtest assets: `make setup-envtest` then
`$(pwd)/bin/setup-envtest use 1.31.0 --bin-dir $(pwd)/bin -p path` (or adjust the
`KUBEBUILDER_ASSETS` path in the manifest). The reuse-values simulator needs network
access for `go mod tidy` once per machine. All contract tests should stay green on a
good head; a regression flips them red.
