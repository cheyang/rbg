#!/usr/bin/env bash
# re-verify.sh — re-run the review harness for PR
# https://github.com/sgl-project/rbg/pull/549 against the current PR head
# (or an explicit ref). All tests are contract tests: red = still broken,
# green = fixed. Exit 0 iff every finding is fixed.
#
# Usage: bash scripts/re-verify.sh [<git-ref>]
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MANIFEST="${HERE}/verify-manifest.json"
PR_URL="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["pr"])' "${MANIFEST}")"
PR_NUM="${PR_URL##*/}"
REPO_URL="https://github.com/sgl-project/rbg"

REF="${1:-}"
if [[ -z "${REF}" ]]; then
    git fetch "${REPO_URL}" "pull/${PR_NUM}/head"
    REF="FETCH_HEAD"
fi

WORKTREE="$(mktemp -d /tmp/rbg-verify-XXXXXX)"
trap 'rm -rf "${WORKTREE}"' EXIT

git worktree add --detach "${WORKTREE}" "${REF}" >/dev/null 2>&1

# Graft the harness (additive only; production code untouched).
mkdir -p "${WORKTREE}/internal/verifyschema" "${WORKTREE}/docs/verification/clientgo-apply-schema"
cp "${HERE}/../../../internal/verifyschema/"*.go "${WORKTREE}/internal/verifyschema/"

echo "=== harness on ${REF} ($(git -C "${WORKTREE}" rev-parse HEAD)) ==="
echo "--- incremental review delta ---"
LAST="$(cat "${HERE}/.last-reviewed" 2>/dev/null || true)"
if [[ -n "${LAST}" ]]; then
    echo "last-reviewed..head: ${LAST}..$(git -C "${WORKTREE}" rev-parse HEAD)"
    git -C "${WORKTREE}" log --oneline "${LAST}..HEAD" 2>/dev/null || true
else
    echo "no .last-reviewed marker; falling back to merge-base"
fi

echo "--- unit + integration layers ---"
OUT="${HERE}/results/reverify-$(date +%Y%m%d%H%M%S).txt"
(
  cd "${WORKTREE}"
  set +e
  GOFLAGS=-mod=vendor go test ./internal/verifyschema/ -count=1 -v > "${OUT}" 2>&1
  echo $? > "${OUT}.status"
  set -e
  tail -40 "${OUT}"
)
STATUS="$(cat "${OUT}.status")"
if [[ "${STATUS}" -eq 0 ]]; then
    echo "VERDICT: all contract tests green -> findings FIXED"
    echo "advance the marker:  git rev-parse HEAD > docs/verification/clientgo-apply-schema/.last-reviewed"
    exit 0
else
    echo "VERDICT: contract tests red -> findings STILL BROKEN (see output above)"
    exit 1
fi
