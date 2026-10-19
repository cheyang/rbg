# rbgs-release-manifest-bump — verification harness (PR #505)

Reviewer: **Claude** (round 1). Harness branch: `verify/rbgs-release-manifest-bump-claude`,
based on PR head `7499b7c0` (merge base `d88b3186`). Production diff of the reviewed PR is
untouched; the harness is purely additive (one new test file + this directory).

Layers (all deterministic, no cluster needed):

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Unit | release-tag consistency across values.yaml / kustomization.yaml / manifests.yaml / Chart.yaml; `helm template` image rendering; kustomize<->manifests sync | `K=$(mktemp) && printf 'apiVersion: v1\nkind: Config\n' >"$K" && KUBECONFIG="$K" go test ./test/chart/ -run 'TestReleaseImageTagsConsistentAcrossManifests|TestRenderedImagesMatchValuesTag|TestKustomizeBuildMatchesShippedManifests' -v` |
| 2. Integration | full shipped chart-test package (`helm template` with the real helm binary, incl. the deprecated-workload-types toggle guards) | `KUBECONFIG=<0600-mode file> go test ./test/chart/ -v` |
| Network (manual) | published Docker Hub images for the new default tags | `bash scripts/check-images-published.sh v0.9.0-d88b3186` |

> Polarity: all tests are **contract** tests — they assert the intended correct state.
> On the merge base (previous cut `v0.9.0-eadb6c20`) the two release-pinning tests FAIL;
> that red run is the proof the harness bites (see `results/chart-tests-base-bites.txt`).

## Problem premise (P0)

| | |
|---|---|
| Claimed symptom | "Release-manifest bump for the `0.9.0-alpha.2` cut, based on `main` @ `d88b3186`. Only version strings change; no functional/template edits." |
| Linked issue | none (chore/release PR — no `fixes #N`; problem taken from the PR body, which states it plainly) |
| Reported component | shipped release defaults (chart version/appVersion, values.yaml image tags, kustomize manager image, generated kubectl manifests) |
| Patched component | exactly those four files — component match |
| **Verdict** | **Confirmed** |
| Evidence | the cut is real: `rolebasedgroup/rbgs-controller:v0.9.0-d88b3186` and `rolebasedgroup/rbgs-upgrade-crd:v0.9.0-d88b3186` exist on Docker Hub, multi-arch (amd64+arm64, active), pushed 2026-10-19 — `results/dockerhub-*.json`; bump follows the identical 4-file pattern of the previous cut (`fc201cdf`, 0.9.0-alpha.1); no stale references to the old tag remain — `results/stale-tag-grep.txt` |

## Summary of results

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| A1 | `helm template deploy/helm/rbgs` renders controller and crd-upgrade images at `v0.9.0-d88b3186` | 1 | Pass (red on base) | `TestRenderedImagesMatchValuesTag`; `results/helm-render.txt`, `results/chart-tests-base-bites.txt` |
| A2 | values.yaml (controller + crdUpgrade), kustomization.yaml, manifests.yaml and Chart.yaml all reference the same cut; appVersion = tag minus leading `v`; chart version `0.9.0-alpha.2` | 1 | Pass (red on base) | `TestReleaseImageTagsConsistentAcrossManifests`; `results/chart-tests-base-bites.txt` |
| A3 | generated `deploy/kubectl/manifests.yaml` is byte-identical to `kubectl kustomize config/default` | 1 | Pass | `TestKustomizeBuildMatchesShippedManifests`; `results/kustomize-sync.txt` |
| A4 | the PR's own test plan (`make test-chart`) passes on the head | 2 | Pass | all 6 tests in `results/chart-tests-head.txt` |
| A5 | the new default tags are published images (not dangling defaults) | network | Pass | `scripts/check-images-published.sh`; `results/dockerhub-*.json` |
| A6 | no stale reference to the previous cut's tag remains in tracked files | manual | Pass | `results/stale-tag-grep.txt` (empty) |

No blocking findings were raised against this PR. Two pre-existing (not PR-introduced)
observations were recorded in the review ledger:

- **F1 [minor, pre-existing]** `test/chart/deprecated_workload_types_test.go:86` —
  `renderChart()` uses `cmd.CombinedOutput()`, so any helm stderr (e.g. the kubeconfig
  permission warning) is concatenated into the manifest stream and YAML parsing fails.
  Reproduced on base and head with a group/world-readable kubeconfig, passes with a
  0600-mode one — `results/chart-tests-kubeconfig-caveat.txt`. Fix sketch: use
  `cmd.Output()` with a separate stderr buffer. Not caused by PR #505.
- **F2 [nit, pre-existing]** `Chart.yaml` appVersion `0.9.0-d88b3186` has no leading `v`
  while the image tags do; templates fall back to `.Chart.AppVersion` when a tag value
  is unset, which would reference the non-existent `0.9.0-d88b3186` image. Dormant in
  the default install path (values.yaml pins the tag); same asymmetry in every prior
  release. Not caused by PR #505.

## Harness-bites check

`TestReleaseImageTagsConsistentAcrossManifests` and `TestRenderedImagesMatchValuesTag`
were run against the merge base `d88b3186` (harness grafted via
`git checkout verify/rbgs-release-manifest-bump-claude -- test/chart/...`): both FAIL
with precise observed-vs-expected diffs (old tag `v0.9.0-eadb6c20` vs `v0.9.0-d88b3186`,
chart `0.9.0-alpha.1` vs `0.9.0-alpha.2`) — `results/chart-tests-base-bites.txt`.
`TestKustomizeBuildMatchesShippedManifests` passes on both (it is a general sync
invariant, not release-specific). Production code was never modified.

## Continuing after the fix (or the next round)

The harness lives on `verify/rbgs-release-manifest-bump-claude` and grafts onto any ref:

```bash
git fetch https://github.com/cheyang/rbg.git verify/rbgs-release-manifest-bump-claude
git checkout <ref-to-check>
git checkout FETCH_HEAD -- docs/verification/rbgs-release-manifest-bump test/chart/release_manifest_consistency_test.go
# then either the layer cmds above, or the whole loop:
bash docs/verification/rbgs-release-manifest-bump/scripts/re-verify.sh   # auto-resolves current PR head
```

- re-verify exit code is 0 iff every mapped finding is Fixed; canaries would need
  inverting (none here — all contract).
- To re-pin to a later release cut: `RBG_EXPECTED_RELEASE_TAG=v0.9.0-<sha> RBG_EXPECTED_CHART_VERSION=0.9.0-alpha.N go test ./test/chart/ -run TestRe...`
- The layer cmds bake in a 0600-mode KUBECONFIG because of F1 — without it, the repo's
  own chart tests fail on machines with a world-readable kubeconfig, for reasons
  unrelated to any PR.
- The network check (A5) is manual by design: image publication is a property of the
  release pipeline, not of the repo tree.
