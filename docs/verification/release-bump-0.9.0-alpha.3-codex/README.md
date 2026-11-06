# Verification — PR #555 — chore(rbgs): update Helm chart and default image versions to 0.9.0-alpha.3

Reviewer: Codex (Reviewer B). Round: initial. Reviewed head: `679f790929c4eacf823c7576b700550ead35c664`; base (merge-base): `73e05db4213950a5dd1f230279e9344a4ffe8435`.

## Premise (P0) — CONFIRMED

The PR body claims: *"Release-manifest bump for the `0.9.0-alpha.3` cut, based on `main` @ `73e05db4`. Only version strings change; no functional/template edits."*

Verified against the base branch: on `73e05db4` the four release files still pin the previous cut
(chart `0.9.0-alpha.2`, image tag `v0.9.0-d88b3186` in 5 places). The bump task is real, the new tag
`v0.9.0-73e05db4` matches the merge-base short SHA, and the diff touches exactly those strings and
nothing else (4 files, 6 lines). Component match: exact. Evidence: `results/p0-base-state.txt`.
The PR is precisely what `tools/release/update-release.sh` produces (Chart.yaml version+appVersion,
values.yaml controller+crdUpgrade tags, kustomization newTag, regenerated manifests.yaml).

## Findings

No defects found. The PR is a complete, consistent, generated-artifact-fresh release bump.
One pre-existing, unrelated nit (not introduced by this PR): `test/chart` uses
`helm template` via `CombinedOutput()`, so helm's kubeconfig-permission warnings corrupt the
rendered YAML on any dev machine that has a `~/.kube/config`; CI is unaffected (no kube config).

## Acceptance checks (all contract polarity — pass = correct)

| ID  | contract | layer | evidence |
|-----|----------|-------|----------|
| A1  | chart appVersion / values.yaml controller+crdUpgrade tags / kustomize newTag / manifests.yaml image all pin the same tag; appVersion == tag minus "v"; no stale tag left in release files | unit (`TestReleasePinsAreConsistent`) | `results/a1a2a3-unit-tests.txt` |
| A2  | `deploy/kubectl/manifests.yaml` is byte-identical to `kustomize build config/default` | unit (`TestKubectlManifestsInSyncWithKustomize`) | same |
| A3  | `helm template` renders both pinned images and the version ConfigMap carries Chart version/appVersion; `helm lint` clean; repo's own `make test-chart` passes | unit (`TestHelmChartRendersPinnedVersions`) + manual | same |
| A4  | published images `rolebasedgroup/rbgs-controller:v0.9.0-73e05db4` and `rbgs-upgrade-crd:v0.9.0-73e05db4` exist and match commit 73e05db4 | network script + CI logs | `results/a4-image-exists.txt` (HTTP 200 both), `results/ci-attribution.txt` |

Harness-bites proof: simulating an incomplete bump (kustomization newTag reverted to the old tag)
turns A1 and A2 red; reverted afterwards, production diff empty. See `results/harness-bites.txt`.

## CI attribution

`Release Test / e2e-test-helm` FAILED for infrastructure reasons: the job could not pull the
unrelated e2e fixture image `registry.cn-hangzhou.aliyuncs.com/acs-sample/nginx:latest`
(connection reset by the Alibaba registry). The PR's own images pulled and ran in that same job;
`e2e-test-manifest` and `e2e-test-upgrade` (v0.7.0/v0.8.0 → PR chart) passed. Re-run the job for a
green required-check signal; no code change needed. Details: `results/ci-attribution.txt`.

## How to re-run

```bash
# full loop (grafts this harness onto the current PR head and re-runs the unit layer):
bash docs/verification/release-bump-0.9.0-alpha.3-codex/scripts/re-verify.sh
# or against a specific ref:
bash docs/verification/release-bump-0.9.0-alpha.3-codex/scripts/re-verify.sh <sha>

# direct:
go test ./test/release/ -count=1 -v            # A1-A3 (needs helm; kustomize via bin/ or make kustomize)
bash docs/verification/release-bump-0.9.0-alpha.3-codex/scripts/check-image-exists.sh [tag]   # A4 (network)
```

Prereqs: go, helm, and kustomize (`bin/kustomize`, PATH, or `make kustomize`). No cluster needed.

## Continuing after a re-cut

If the PR is re-pinned to a newer SHA, only the pinned strings change: A1-A3 are
value-agnostic consistency contracts and stay valid as-is. Pass the new tag to
`check-image-exists.sh` (or `NEW_TAG=...`) for A4. If the release tooling changes the
file set (e.g. new release file), extend `TestReleasePinsAreConsistent`'s file list —
that is a Harness-update, not a finding flip.
