#!/usr/bin/env bash
# re-verify.sh — re-run the PR #469 verification harness against updated code.
#
# Run from a checkout of the branch that CONTAINS the harness
# (verify/pr469-upgrade-content-assert). With no <fixed-ref> it fetches the
# current PR head from the manifest `pr` URL; it resolves the incremental review
# delta from `.last-reviewed`.
#
# Layer: unit only (deterministic). The upgrade e2e (RunUpgradeSpecs) is not run
# here — it is gated behind /release-test against published images (not per-PR
# CI), and the ACK live cluster carries legacy restartPolicy data that blocks a
# from-source PR deploy. See the verify-manifest.json `limitation`.
#
# Usage: bash scripts/re-verify.sh [<fixed-ref>]
set -euo pipefail

TOP="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MANIFEST="$TOP/verify-manifest.json"
LAST="$TOP/.last-reviewed"
FIXED_REF="${1:-}"

command -v jq >/dev/null || { echo "re-verify: jq required" >&2; exit 2; }
git rev-parse --git-dir >/dev/null 2>&1 || { echo "re-verify: not a git repo" >&2; exit 2; }

# Resolve PR head if no ref given.
if [ -z "$FIXED_REF" ]; then
  PR_URL="$(jq -r '.pr // empty' "$MANIFEST")"
  if [[ "$PR_URL" == *"/pull/"* ]]; then
    REPO="${PR_URL%/pull/*}"; PR="${PR_URL##*/pull/}"
    git fetch "$REPO" "pull/$PR/head:pr-$PR" >/dev/null 2>&1 || true
    FIXED_REF="pr-$PR"
  fi
fi
FIXED_REF="${FIXED_REF:-HEAD}"
echo "re-verify: fixed-ref = $FIXED_REF"

# Incremental delta
if [ -f "$LAST" ]; then
  LR="$(cat "$LAST")"
  echo "re-verify: last-reviewed = $LR"
  echo "re-verify: incremental delta (review this range):"
  git log --oneline "$LR..$FIXED_REF" 2>/dev/null | sed 's/^/    /' || echo "    (no new commits)"
  echo "  advance marker after this round:  echo $FIXED_REF > docs/verification/pr469-upgrade-content-assert/.last-reviewed"
fi

echo
echo "=== L1: premise harness (PR #469 child-heal premise) ==="
echo "  Polarity: contract (LegacyChildNotReapplied) asserts needsUpdate==false -> premise refuted;"
echo "            canary (NormalizedGroupTemplateDoesHeal) confirms normalize works, isolating the cause."
go test ./internal/controller/workloads/ -run 'TestPR469' -v 2>&1 | tee "$TOP/results/premise-unit.txt" | tail -25

echo
echo "=== L1: existing RBGS convergence suite (regression sanity) ==="
go test ./internal/controller/workloads/ \
  -run 'TestRoleBasedGroupSetReconciler_needsUpdate|TestRolesEqual|TestNewRBGForSet_Normalizes|TestRoleBasedGroupSetReconciler_updateExistingRBGs_Normalizes' -v 2>&1 | tail -25

echo
echo "=== L1: upgrade e2e package compiles (no run; needs /release-test cluster) ==="
go build ./test/e2e/upgrade/ && echo "build: ok"
