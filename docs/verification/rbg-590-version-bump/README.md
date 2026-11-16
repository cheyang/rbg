# Verification — rbg-590-version-bump (PR #590, 0.9.0-alpha.4 release bump)

Reviewer: **Claude (Reviewer A)** · Branch: `verify/rbg-590-version-bump-claude` ·
PR head reviewed: `76963d62986242e496a8c63b41f4a916f06893cc` · Base: `1fc69f703695bc9355f5730ee3fb3f75cd0331b6`

## Premise (P0) — verdict: CONFIRMED

PR #590 is a release-manifest bump pointing the chart/kustomize/manifest defaults at the
nightly image built from `main@1fc69f70`. Confirmed against the **base** branch:

- Base `Chart.yaml` appVersion is `0.9.0-73e05db4` (the 0.9.0-alpha.3 cut, PR #555) — the
  defaults were one nightly behind.
- 13 commits landed between `73e05db4` and `1fc69f70`, including real fixes/features
  (#573 restart recreation guard, #575 manager probes, #553 scale-up-before-roll,
  #559 member-loss recovery, #538 selective scale-down) — a genuine reason to cut alpha.4.
- `.github/workflows/docker-build-push.yml` runs nightly on `main` and tags images
  `$(VERSION)-$(git rev-parse --short HEAD)`; Docker Hub returns HTTP 200 for both
  `rolebasedgroup/rbgs-controller:v0.9.0-1fc69f70` and
  `rolebasedgroup/rbgs-upgrade-crd:v0.9.0-1fc69f70` — the images the new defaults
  point at are published.
- The diff is byte-for-byte the same shape as the previously accepted bumps
  (#555 / #505 / #468): 4 files, 6 line changes, version strings only.

## How to run

```bash
bash docs/verification/rbg-590-version-bump/scripts/verify-version-bump.sh
# prints "Cn<TAB>pass|fail" per contract; exit 0 iff all pass
bash docs/verification/rbg-590-version-bump/scripts/re-verify.sh   # grafts onto current PR head
```

Requires: git, helm, go, curl; `bin/kustomize` is downloaded by `make kustomize` when
missing (network needed for C3 and the kustomize download). Raw output lives in
`results/` (`verify-version-bump.log`, `helm-render.yaml`, `kustomize-build.yaml`,
`test-chart.out`, `status.tsv`, `reverify/`).

## Observed vs expected

| ID | Contract (polarity: contract) | Expected | Observed at head 76963d62 | Verdict |
|----|-------------------------------|----------|---------------------------|---------|
| P0 | Premise: bump needed, images published | defaults one nightly behind; new images exist | confirmed on base (see above) | **Confirmed** |
| C1 | Version-string consistency: helm render (controller + crd-upgrade images), version-configmap, kustomize `newTag`, `manifests.yaml` all = `v0.9.0-1fc69f70` / `0.9.0-alpha.4` | all agree | all agree | **PASS** |
| C2 | Previous nightly tag `v0.9.0-73e05db4` fully replaced | zero occurrences in tree | zero occurrences | **PASS** |
| C3 | Images exist in Docker Hub at the new tag | HTTP 200 for both | 200 / 200 | **PASS** |
| C4 | `kustomize build config/default` == committed `deploy/kubectl/manifests.yaml` | byte-identical | `diff` exit 0 | **PASS** |
| C5 | Chart version is a strict semver bump over merge-base | `0.9.0-alpha.3` → `0.9.0-alpha.4` | valid increase | **PASS** |
| C6 | Chart test suite (`make test-chart`) | all tests pass | 5/5 pass | **PASS** |

CI context: `project-check` (green on this PR) runs `make manifests` + clean-tree check,
`make test-chart`, and yamllint — i.e. generated-manifest drift and chart rendering are
also guarded in required CI.

## Harness-bites check (Step 4)

- **Committed full revert** of the bump (anti-fix): C2 (stale tag present) and C5 (no
  version increase) go RED, exit 1. → the harness detects "bump didn't happen".
- **Partial revert** (kustomization.yaml only): C1 (cross-file inconsistency) and C4
  (manifests out of sync with kustomize build) go RED, exit 1. → detects half-done bumps.
- Anti-fixes were `git reset --hard` away afterwards; production diff vs PR head is
  empty (harness is purely additive under `docs/verification/`).

## Layer notes

- L1 (unit/contract): all of the above — helm template, kustomize build, registry API,
  go chart tests. No cluster needed.
- L2 (integration): `go test ./test/chart/` (C6) renders the full chart via helm and
  asserts deployment/RBAC shape — the strongest dependency-touching layer this PR can
  reach. envtest/e2e are unaffected by version strings and are covered by required CI.
- L3 (live): intentionally skipped per round policy (unit + integration only).

## Continuing after the next bump / on another machine

This harness is deliberately **generic across release bumps**: it derives the expected
tag from `Chart.yaml` at the commit under test and the "previous" tag from the
merge-base, so it re-validates any future bump (alpha.5, 0.9.0, …) without edits.

1. Clone this branch: `git fetch <fork> verify/rbg-590-version-bump-claude && git checkout verify/rbg-590-version-bump-claude`
2. `bash docs/verification/rbg-590-version-bump/scripts/re-verify.sh` — fetches the
   current PR head from the manifest's `pr` URL, grafts the harness, prints the
   per-contract verdict table, exit 0 iff all hold.
3. Polarity: every check is a **contract** test — it must PASS. A FAIL means the bump
   contract is broken (stale tag, inconsistent files, unpublished image, manifest drift,
   or chart-version regression). There are no canaries to invert.
4. After reviewing the `last-reviewed..head` delta, advance the marker:
   `echo <new-head> > docs/verification/rbg-590-version-bump/.last-reviewed` and commit.

Kickoff prompt for a fresh agent:
> You are resuming a PR review verification for https://github.com/sgl-project/rbg/pull/590.
> Checkout branch verify/rbg-590-version-bump-claude, read
> docs/verification/rbg-590-version-bump/README.md, run scripts/re-verify.sh, report the
> per-contract verdicts, review the last-reviewed..head delta, advance the marker, commit.
