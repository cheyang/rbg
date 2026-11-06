# rbgs-555-alpha3-version-bump — release-manifest verification

Reproducible evidence for the review of [PR #555](https://github.com/sgl-project/rbg/pull/555)
(chore(rbgs): update Helm chart and default image versions to 0.9.0-alpha.3).

Harness branch: `verify/rbgs-555-alpha3-version-bump-claude` on the reviewer fork
(cheyang/rbg). Production code is untouched; the only additive test file is
`test/chart/version_bump_test.go`.

Layers run against the **code under review** (PR head `679f7909`, base `73e05db4`):

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit | cross-file version-reference consistency (Chart.yaml appVersion vs values.yaml / kustomization / kubectl manifests) | `go test ./test/chart/ -run 'TestVersionBumpReferencesConsistent' -v` |
| 2. Integration | real `helm template` render of the chart; asserts controller + crd-upgrade images, version ConfigMap, no stale tags | `go test ./test/chart/ -run 'TestRenderedChartMatchesChartVersions' -v` |
| (network, non-gating) | published Docker Hub artifacts for the bumped tag | `bash scripts/check-release-artifacts.sh` |
| (build, non-gating) | `deploy/kubectl/manifests.yaml` == `kustomize build config/default` | `bash scripts/check-manifest-drift.sh` |

> Polarity: all harness tests are **contract** tests — they PASS on a correct bump and go
> RED if any reference site drifts from Chart.yaml's appVersion (harness-bites verified,
> see below).

## Problem premise (P0)

| | |
|---|---|
| Claimed symptom | "Release-manifest bump for the `0.9.0-alpha.3` cut, based on `main` @ `73e05db4`. Only version strings change; no functional/template edits." |
| Linked issue | none (release chore; carve-out applies to "why", not to consistency) |
| Reported component | Helm chart + kustomize + generated kubectl manifests |
| Patched component | exactly those four files — match |
| **Verdict** | **Confirmed** — the release this PR cuts is real and consistent |
| Evidence | `results/release-artifacts.txt`: both images exist on Docker Hub (amd64+arm64); controller binary embeds `Version=v0.9.0-73e05db4`, `GitCommit=73e05db4213950a5dd1f230279e9344a4ffe8435`; upgrader image CRDs byte-identical to `config/crd/bases` @ 73e05db4 (10 files), `ensure-crds-up-to-date.sh` identical. Pattern matches the previous cuts (85c6b3db for alpha.2, fc201cdf for alpha.1). |

## Summary of results

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| P0 | release artifacts for v0.9.0-73e05db4 exist and match main @ 73e05db4 | network | Confirmed | `results/release-artifacts.txt` (exit 0) |
| VB-CONSISTENCY | all five version reference sites agree with Chart.yaml appVersion | 1 | Confirmed (green) | `results/unit-head.txt` |
| VB-RENDER | rendered chart carries exactly the new tags (controller, crd-upgrade hook, version ConfigMap) | 2 | Confirmed (green) | `results/integration-head.txt` |
| DRIFT | `deploy/kubectl/manifests.yaml` regenerates byte-identically via kustomize v5.5.0 | build | Confirmed | `results/manifest-drift.txt` (99290 lines, 0 diff) |
| F1 (pre-existing) | chart test package breaks when helm prints a stderr warning | script | Confirmed on base too | `results/f1-repro.txt` |

`make test-chart` (the PR test plan's unchecked box) passes on the PR head once the
reviewer kubeconfig is `chmod 600`: `results/make-test-chart-head.txt` (`ok sigs.k8s.io/rbgs/test/chart`).

## Per-finding detail

### P0 — premise
The images referenced by the bump are already published and provably built from the
commit the manifests name: the manager binary's ldflags block inside the amd64 image
layer reads
`-X sigs.k8s.io/rbgs/version.Version=v0.9.0-73e05db4 -X sigs.k8s.io/rbgs/version.GitCommit=73e05db4213950a5dd1f230279e9344a4ffe8435`.
The CRD-upgrade image carries the same CRD set as `config/crd/bases` at that commit, so
chart upgrades will apply exactly the CRDs in this tree. Additionally, GitHub's
`/release-test` run 42213206716 (against PR head 679f7909) installed the chart with these
default images: pre-install CRD hook ran, controller became ready, all CRDs verified.

### VB-CONSISTENCY / VB-RENDER — the bump's own contracts
`test/chart/version_bump_test.go` (additive):
- `TestVersionBumpReferencesConsistent` — Chart.yaml is the source of truth; asserts
  `values.yaml` controller+crdUpgrade tags, `config/manager/kustomization.yaml` newTag and
  the single controller image line in `deploy/kubectl/manifests.yaml` all equal
  `v`+appVersion, and no stale `vX.Y.Z-<sha>` token remains in the reference files.
- `TestRenderedChartMatchesChartVersions` — renders via `helm template` (stdout-only
  capture; see F1) and asserts the manager Deployment image, the CRD-upgrade hook Job
  image, and the `rbgs-controller-version-info` ConfigMap (`chartVersion`, `appVersion`).

### Harness-bites check (Step 4)
Injected the bug class on purpose, then reverted (production diff empty afterwards):
- `config/manager/kustomization.yaml` newTag reverted to `v0.9.0-d88b3186` →
  `TestVersionBumpReferencesConsistent` FAILS ("stale release tag v0.9.0-d88b3186").
- `deploy/helm/rbgs/values.yaml` controller tag reverted →
  `TestRenderedChartMatchesChartVersions` FAILS ("rendered chart carries stale release tag").

### F1 — pre-existing chart-test stderr pollution (NOT introduced by this PR)
`test/chart/deprecated_workload_types_test.go` `renderChart()` uses `cmd.CombinedOutput()`;
helm's "Kubernetes configuration file is group-readable" warning lands in the stream that
`splitObjects()` parses as YAML and every test in the package fails with
`yaml: mapping values are not allowed in this context`. Reproduced identically on base
`73e05db4` (differential evidence) — `scripts/repro-f1-chart-test-stderr-pollution.sh`
makes it deterministic (world-readable KUBECONFIG → FAIL, 0600 → PASS).
Proposed fix (NOT applied): capture stdout and stderr separately in `renderChart`.
Filed as a non-blocking, pre-existing finding; excluded from the re-verify exit gate.

## CI note
The PR head's only failing status, `Release Test / e2e-test-helm` (run 42213206716), is a
network flake: `docker pull registry.cn-hangzhou.aliyuncs.com/acs-sample/nginx:latest`
failed with "connection reset by peer" from the Aliyun auth endpoint — after the chart had
already deployed successfully with the new images. All GitHub Actions checks on the PR are
green. Attribution: unrelated infrastructure, not the PR.

## Proposed fixes (NOT applied to production here)
- F1: `renderChart` → `cmd.Output()` (or separate buffers), so stderr warnings cannot
  corrupt the parsed manifest stream.

## Continuing after the fix (possibly on another machine)

The harness is on branch `verify/rbgs-555-alpha3-version-bump-claude` (fork
`cheyang/rbg`), production code untouched, so it grafts onto whatever the fixed code is.

1. Get it onto the fixed code:
   ```bash
   git fetch https://github.com/cheyang/rbg.git verify/rbgs-555-alpha3-version-bump-claude
   git checkout <fixed-branch>
   git checkout FETCH_HEAD -- docs/verification/rbgs-555-alpha3-version-bump test/chart/version_bump_test.go
   ```
2. Prereqs: Layer 1 = Go toolchain only; Layer 2 = `helm` on PATH; drift check =
   kustomize v5.5.0 (auto-installed via `go install` if missing); artifact check =
   curl + network.
3. Re-run:
   ```bash
   go test ./test/chart/ -run 'TestVersionBumpReferencesConsistent' -v
   go test ./test/chart/ -run 'TestRenderedChartMatchesChartVersions' -v
   bash docs/verification/rbgs-555-alpha3-version-bump/scripts/check-manifest-drift.sh
   bash docs/verification/rbgs-555-alpha3-version-bump/scripts/check-release-artifacts.sh <base-sha>
   bash docs/verification/rbgs-555-alpha3-version-bump/scripts/repro-f1-chart-test-stderr-pollution.sh
   ```
   Or everything at once: `bash docs/verification/rbgs-555-alpha3-version-bump/scripts/re-verify.sh`
   (auto-resolves the current PR head from the manifest's `pr` URL).
4. Polarity: both tests are contract tests — they should be GREEN. If a future bump
   forgets a reference site they go RED with a precise message.
5. Harness-bites: revert one tag reference to a stale value and confirm RED before
   trusting a green run.

### Kickoff prompt for a fresh agent
```text
Continue a verification task on branch verify/rbgs-555-alpha3-version-bump-claude
(fork cheyang/rbg). Background: a review of PR sgl-project/rbg#555 (0.9.0-alpha.3
release-manifest bump) produced contract tests for the version bump plus one
pre-existing finding (F1, chart-test stderr pollution). Read
docs/verification/rbgs-555-alpha3-version-bump/README.md ("Continuing after the fix")
and follow it: graft the harness onto the fixed code, re-run all layers
(re-verify.sh resolves the PR head itself), mind the polarity table (all contract
tests), run the harness-bites check, and report an observed-vs-expected table.
The release-artifact script takes the release base sha as its argument. Do not run
cluster-wide destructive actions.
```
