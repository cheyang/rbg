# Verification: PR #505 — release-manifest bump to 0.9.0-alpha.2

Reviewer: Codex (Reviewer B). Round: initial. Head reviewed: `7499b7c05dd9ea39d0dfdfb0de2a18aabcb1f17a` (base `d88b318640aa4592bbf75d18b18613c761b49e47`).

## Premise (P0) — verdict: CONFIRMED

The PR claims: "Release-manifest bump for the `0.9.0-alpha.2` cut, based on `main` @ `d88b3186`. Only version strings change; no functional/template edits."

Verified against base and the registry:

- The diff touches exactly 4 files and only version strings (`git diff d88b3186...7499b7c0`).
- `d88b3186` is `main` HEAD and the PR merge-base, so the bumped-to commit is exactly what the chart/manifests bundle.
- Both default images exist on Docker Hub, multi-arch (amd64+arm64):
  `rolebasedgroup/rbgs-controller:v0.9.0-d88b3186` (pushed 2026-10-19T13:00:44Z) and
  `rolebasedgroup/rbgs-upgrade-crd:v0.9.0-d88b3186` (2026-10-19T13:04:21Z) — see `results/p0-dockerhub-tags.txt`.
- The pattern matches the previous cut (#468 → `v0.9.0-eadb6c20` / chart `0.9.0-alpha.1`, git tag `v0.9.0-alpha.1` exists).

## Contract checks (no bug findings this round)

This PR is a mechanical release bump; the harness encodes the invariants such a
bump must keep. All four PASS on the reviewed head (polarity: contract).

| ID | claim | layer | test | observed |
|----|-------|-------|------|----------|
| C1 | chart appVersion, values.yaml tags, config/manager newTag all agree (`v`+appVersion) | unit | `TestVersionStringsConsistent` | PASS (all = `v0.9.0-d88b3186`) |
| C2 | `deploy/kubectl/manifests.yaml` == `kustomize build config/default` | unit | `TestGeneratedManifestsInSync` | PASS (byte-identical; also in sync at base) |
| C3 | `helm template` renders both images with the values tags + version ConfigMap from Chart.yaml | integration | `TestChartRenderMatchesValues` | PASS; 2 image refs to the new tag, 0 stale refs |
| C4 | both default image tags exist on Docker Hub | integration | `TestReferencedImagesPublished` | PASS (both exist, 2 archs each) |

Raw evidence: `results/verify-tests-head.txt`, `results/kustomize-drift.txt`,
`results/helm-render-head.txt`, `results/p0-dockerhub-tags.txt`.

## How to run

Prerequisites: go, kustomize v5.5.0 (`make kustomize`, or PATH), helm v3. C2/C3
skip if the tool is missing; C4 skips when the registry is unreachable.

```bash
go test ./test/verify/ -v            # all four contracts
make test-chart                      # repo's own chart suite (also passes; see note)
```

Note for local runs: run with a kubeconfig that is not group/world-readable (or
`KUBECONFIG=/nonexistent`). The repo's `test/chart` suite uses
`cmd.CombinedOutput()`, so helm's kubeconfig-permission warnings (stderr) pollute
the parsed manifest stream and every test fails with `parse rendered doc: ...
mapping values are not allowed in this context`. Environment issue, pre-existing,
unrelated to this PR; see `results/test-chart-envnote.txt`.

## Harness-bites check

Corrupting `config/manager/kustomization.yaml` (`newTag: v0.9.0-deadbeef`) makes C1
and C2 FAIL as expected; reverted afterwards, production diff empty
(`results/harness-bites.txt`). C3/C4 fail by construction on any stale/missing tag.

## Continuing after a revision

```bash
git checkout verify/release-bump-0.9.0-alpha.2-codex
bash docs/verification/release-bump-0.9.0-alpha.2/scripts/re-verify.sh   # auto-fetches PR head
```

All findings are contract polarity: PASS on a correct bump = "FIXED" in
re-verify's wording; any FAIL = the bump broke the invariant. If a future round
changes the expected version strings, C1 derives expectations from Chart.yaml, so
no test edit is needed. Advance `.last-reviewed` after reviewing the new delta.

Kickoff prompt for a fresh agent:

> Review sgl-project/rbg PR #505 round N. Pull verify/release-bump-0.9.0-alpha.2-codex
> from https://github.com/cheyang/rbg.git, read
> docs/verification/release-bump-0.9.0-alpha.2/README.md, run its scripts/re-verify.sh,
> then review the delta in .last-reviewed..head.
