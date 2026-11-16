# Verification — PR #589: PDB and node spreading for the controller manager

Reviewer: Claude (Reviewer A, debate round 2026-11-17). Branch: `verify/manager-disruption-claude`,
based on PR head `24cdf951150e62a9ad36c7ca0c68569e08c6d9b0` (base `1fc69f703695bc9355f5730ee3fb3f75cd0331b6`).
Production code is untouched — this branch adds only this `docs/verification/` harness.

## Premise (P0) — Confirmed

The PR claims (issue #584, quoted): "Neither the Helm chart nor `config/` ships a
PodDisruptionBudget for the controller" and "The manager Deployment template only exposes
`nodeSelector` and `tolerations`, with no `affinity` or `topologySpreadConstraints`" — while
every admission webhook has `failurePolicy: Fail`, so draining the node that hosts both
replicas takes admission down.

Reproduced on **base**: the PR's own contract tests (`test/chart/manager_disruption_test.go`),
copied verbatim onto base `1fc69f70`, **all three FAIL** — `results/base/chart-tests-grafted.out`.
Supporting facts checked on base: `failurePolicy: Fail` on all webhooks in
`deploy/kubectl/manifests.yaml`; the readiness probe (`/readyz`) from #575 is present, so a PDB
counts only pods passing readiness. Component match: the PR touches exactly the components the
issue names (chart, `config/manager`, regenerated kubectl manifests). No refutation path found.

## Findings — observed vs expected

| id | claim | polarity | layer | verdict | evidence |
| --- | --- | --- | --- | --- | --- |
| P0 | Gap exists on base (no PDB / no spreading / no affinity knob on either install path) | contract | unit | **Confirmed (gap real)** | `results/base/chart-tests-grafted.out` — 3/3 FAIL on base; 3/3 PASS on head (`results/head/chart-tests.out`) |
| F1 | No eviction/drain-level coverage of the new PDB's actual contract | — | live (not run) | **Open (minor)** | Render-level tests are green and CI's manifest e2e applies the PDB to kind v1.31, but nothing asserts "evict one Ready pod OK, second blocked". Author acknowledged in the PR body. See `liveNote` in the manifest. |
| F2 | `maxUnavailable` / `unhealthyPodEvictionPolicy` hardcoded, not a values knob | — | — | Open (nit) | `deploy/helm/rbgs/templates/manager/poddisruptionbudget.yaml:10-16`; author offered to add it. |
| F3 | `--reuse-values` upgrade caveat documented only in the PR body, not the chart README | — | — | Open (nit) | `deploy/helm/rbgs/README.md:181` (Upgrading section) is where existing-install guidance lives. |

No blocker/major findings. F1–F3 are advisory; nothing in this PR is a proven defect.

## Extra checks captured this round

- **Kustomize sync (Project Check contract)**: `kustomize build config/default` with kustomize
  **v5.5.0** (the Makefile's pinned version) reproduces `deploy/kubectl/manifests.yaml`
  byte-identically. `results/head/kustomize-sync.out`.
- **Quality gates**: `helm lint` (0 failed), `go vet ./test/chart/`, `gofmt`, `yamllint`
  (exit 0), `golangci-lint run ./test/chart/...` (0 issues). `results/head/quality-gates.out`.
- **Value coalescing edge cases**: `podDisruptionBudget={}` keeps the chart default (PDB on),
  `null` / `enabled=false` opt out — sensible; no `enabled`-key tri-state bug of the kind the
  deprecated-workload-types toggle guards against. `results/head/pdb-value-edge-cases.out`.

## How to re-run (unit layer)

From a checkout of this branch:

```bash
bash docs/verification/manager-disruption/scripts/re-verify.sh
# or against a specific ref:
bash docs/verification/manager-disruption/scripts/re-verify.sh <ref>
```

No ref argument: the script resolves the current PR head from `verify-manifest.json`'s `pr`
URL, grafts the harness onto it, and runs the unit layer. Requires: git, go, jq, helm
(`helm` is needed by `renderChart`; tests skip gracefully without it outside CI).
Kustomize sync and the quality gates are recorded artifacts, not part of `re-verify.sh`;
re-run them by hand if the manifests change:

```bash
go install sigs.k8s.io/kustomize/kustomize/v5@v5.5.0   # Makefile pins v5.5.0
kustomize build config/default | diff - deploy/kubectl/manifests.yaml && echo IN-SYNC
```

## Continuing after the fix

F1's live check (kind drain) is the only open verification: create a 2-node kind cluster,
install from `deploy/kubectl/manifests.yaml` (or the chart), wait for both manager pods Ready,
`kubectl drain <node-with-one-pod>` → eviction succeeds; a simultaneous second eviction of the
surviving Ready pod should be blocked by the PDB (`kubectl get pdb rbgs-controller-manager`
shows `DISRUPTIONS ALLOWED` drop to 0). If the PR is revised, re-run `re-verify.sh` — P0's
contract tests must stay green (they are the PR's own tests, kept as this PR's regression
suite). Kickoff prompt for a fresh agent: "You are resuming a PR review verification. Read
`docs/verification/manager-disruption/README.md` and `verify-manifest.json` in this branch,
run `scripts/re-verify.sh`, report per-finding verdicts, then review `last-reviewed..head`."
