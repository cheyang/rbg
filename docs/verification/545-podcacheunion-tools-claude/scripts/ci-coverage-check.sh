#!/usr/bin/env bash
# Contract test for finding F1 (carried from PR #530 round 2 as R2-F2, adapted
# to PR #545's target branch).
#
# Claim: no CI workflow covers this PR. Two parts:
#   (a) every .github/workflows trigger filters pull_request/push to `main`
#       only, so a PR into `kep-284-e2e-test` runs zero checks;
#   (b) no workflow, Makefile target, or golangci config references the
#       `podcacheunion` build tag, so the tagged package is never compiled by
#       CI even on `main`.
#
# Contract polarity: this script asserts the DESIRED state (branch covered +
# tag compiled). It is RED on the PR head (documents the gap) and goes GREEN
# once CI is extended. A red result here is the repro for F1, not a harness
# error. If the repo intentionally keeps this branch manual-only, retire this
# check with the maintainer's decision recorded in the manifest.
set -uo pipefail

REPO_ROOT=$(cd "$(dirname "$0")/../../../.." && pwd)
cd "$REPO_ROOT"

fail=0

# (a) PRs to kep-284-e2e-test must trigger at least one workflow.
covered=0
for f in .github/workflows/*.yml .github/workflows/*.yaml; do
  [ -f "$f" ] || continue
  if grep -A6 -E '^(on:|[Tt]rue:)' "$f" 2>/dev/null | grep -B2 -A4 'pull_request' | grep -q 'kep-284-e2e-test'; then
    covered=1
    echo "ci-coverage: $f triggers on kep-284-e2e-test"
  fi
done
if [ "$covered" -eq 0 ]; then
  echo "ci-coverage: FAIL (a) — no workflow pull_request trigger includes kep-284-e2e-test; PRs to that branch run zero checks" >&2
  fail=1
else
  echo "ci-coverage: PASS (a)"
fi

# (b) Something in CI must at least compile the tagged package.
tagged=0
if grep -rq 'podcacheunion' .github/workflows/ 2>/dev/null; then tagged=1; fi
if grep -q 'podcacheunion' Makefile 2>/dev/null; then tagged=1; fi
if [ -f .golangci.yml ] && grep -q 'podcacheunion' .golangci.yml 2>/dev/null; then tagged=1; fi
if [ "$tagged" -eq 0 ]; then
  echo "ci-coverage: FAIL (b) — nothing references the podcacheunion build tag; the tagged package is never compiled by CI" >&2
  fail=1
else
  echo "ci-coverage: PASS (b)"
fi

if [ "$fail" -ne 0 ]; then
  echo "ci-coverage: RED — F1 reproduced (no CI coverage for this branch/package)" >&2
fi
exit $fail
