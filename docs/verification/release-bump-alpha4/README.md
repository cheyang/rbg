# Verification: PR #590 — release-manifest bump to 0.9.0-alpha.4 / v0.9.0-1fc69f70

- **PR**: https://github.com/sgl-project/rbg/pull/590
- **Head reviewed**: `76963d62986242e496a8c63b41f4a916f06893cc` (`.last-reviewed`)
- **Base**: `1fc69f703695bc9355f5730ee3fb3f75cd0331b6` (merge-base = main tip the release is cut from)
- **Reviewer**: Codex (debate Reviewer B). Production code untouched; this branch is additive only.

## Premise (P0) — Confirmed

The PR claims: "Release-manifest bump for the `0.9.0-alpha.4` cut, based on `main` @ `1fc69f70`.
Only version strings change; no functional/template edits."

Verified:

1. The release source commit `1fc69f70` is exactly the PR merge-base, and
   `git rev-parse --short 1fc69f70...` = `1fc69f70`, matching the tag suffix per the
   Makefile convention `TAG = $(VERSION)-$(GIT_SHA)` = `v0.9.0-<short-sha>`.
2. Both referenced images exist on Docker Hub, multi-arch (amd64+arm64), pushed
   2026-11-16, *before* this PR's head was observed (2026-11-16T17:46Z):
   - `rolebasedgroup/rbgs-controller:v0.9.0-1fc69f70` (07:08:37Z)
   - `rolebasedgroup/rbgs-upgrade-crd:v0.9.0-1fc69f70` (07:12:08Z)
   See `results/dockerhub-*.json`.
3. The diff touches only version strings (5 lines across 4 files) — same file set and
   line counts as the previous release bumps `fd160cc9` (alpha.3) and `85c6b3db` (alpha.2).

## Contracts verified (all pass on PR head)

| ID | Contract | Layer | Evidence |
|---|---|---|---|
| C1 | No stale `v0.9.0-73e05db4` in any shipped release file | unit (go test + shell) | `results/go-test-head.txt`, `results/contract-checks-head.txt` |
| C2 | New tag `v0.9.0-1fc69f70` pinned in kustomization, values.yaml (×2: controller + crdUpgrade), manifests.yaml; Chart.yaml at version `0.9.0-alpha.4` / appVersion `0.9.0-1fc69f70` | unit | same |
| C3 | Tag suffix equals release short-sha `1fc69f70` | unit | same |
| C4 | Checked-in `deploy/kubectl/manifests.yaml` is byte-identical to `kustomize build config/default` at the head | integration (shell script, kustomize v5.5.0) | `results/contract-checks-head.txt` |
| C5 | `helm template` renders controller + crd-upgrade images with the new tag; base-vs-head render diff contains *only* chart/app version, both image tags, and the derived per-release CRD-upgrade Job name | integration (shell script, helm v3.16.3) | `results/helm-render-base-vs-head.diff` |
| C6 | Published images exist in the registry before merge | registry API | `results/dockerhub-*.json` |

Also run: `make test-chart` (i.e. `go test ./test/chart/`) — PASS on head. The PR body
left that box unchecked; it passes.

## Harness-bites proof

Every test is a **contract** test: red on base, green on head. Grafted onto the base
`1fc69f70`, `TestC1_NoStaleOldTag` and `TestC2_NewTagEverywhere` FAIL (old tag still
present, new tag missing) — see `results/go-test-base-harness-bites.txt`. The same tests
PASS on the head `76963d62` (`results/go-test-head.txt`).

## How to re-run

```bash
# unit layer (pure file assertions, no external tools)
go test ./test/releaseverify/ -v

# integration layer (needs kustomize v5.5.0 and helm on PATH)
bash docs/verification/release-bump-alpha4/scripts/check-release-consistency.sh

# whole harness against a future fixed ref (auto-discovers the PR head)
bash docs/verification/release-bump-alpha4/scripts/re-verify.sh
```

## Notes for future rounds

- If the PR is rebased onto a newer main, only the `.last-reviewed` marker advances; the
  tag suffix must still match the *release* commit, not the PR head.
- `git tag v0.9.0-alpha.4` does not exist yet — that is expected: past releases tag the
  merged bump commit (alpha.3 tag == fd160cc9). Confirm the tag lands post-merge.
- Live layer skipped by pipeline policy; CI's Release Test suite (e2e-test-helm,
  e2e-test-manifest, e2e-test-upgrade, run 43861788568) already exercised the published
  images end-to-end and passed.
