#!/usr/bin/env bash
# re-verify.sh — Re-run verification for PR #433 (semantic revision equality)
# Usage: bash re-verify.sh [<ref>]
#   Without ref: fetches current PR head from upstream
#   With ref: uses the given ref directly

set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VERIFY_DIR="$(dirname "$SCRIPT_DIR")"
REPO_ROOT="$(cd "$VERIFY_DIR/../.." && pwd)"

# Resolve PR head
PR_URL="https://github.com/sgl-project/rbg/pull/433"
PR_NUM=433

if [[ $# -ge 1 ]]; then
    REF="$1"
else
    echo ">>> Fetching current PR head..."
    git -C "$REPO_ROOT" fetch https://github.com/sgl-project/rbg.git "refs/pull/${PR_NUM}/head" --quiet
    REF="FETCH_HEAD"
fi

CURRENT_SHA=$(git -C "$REPO_ROOT" rev-parse "$REF")
echo ">>> Verifying against: $CURRENT_SHA"

# Read .last-reviewed
LAST_REVIEWED_FILE="$VERIFY_DIR/.last-reviewed"
if [[ -f "$LAST_REVIEWED_FILE" ]]; then
    LAST_REVIEWED=$(cat "$LAST_REVIEWED_FILE")
    echo ">>> Last reviewed: $LAST_REVIEWED"
    echo ">>> Delta: $LAST_REVIEWED..$CURRENT_SHA"
    git -C "$REPO_ROOT" log --oneline "$LAST_REVIEWED".."$CURRENT_SHA" 2>/dev/null || true
else
    echo ">>> No .last-reviewed found; running full verification"
fi

# Checkout PR head for testing
git -C "$REPO_ROOT" checkout "$CURRENT_SHA" --quiet --detach

echo ""
echo "=== L1: Unit Tests (all 4 layers) ==="
echo ""

cd "$REPO_ROOT"
go test ./pkg/utils/... -v -run TestSetMatchesRevision -count=1
go test ./pkg/reconciler/roleinstance/revision/... -v -run TestSetMatchesRevision -count=1
go test ./pkg/reconciler/roleinstanceset/statefulmode/... -v -run TestSetMatchesRevision -count=1
go test ./pkg/reconciler/roleinstanceset/statelessmode/revision/... -v -run TestSetMatchesRevision -count=1

echo ""
echo "=== Build Check ==="
go build ./...

echo ""
echo "=== Full affected-package test suite ==="
go test ./pkg/utils/... ./pkg/reconciler/roleinstance/... ./pkg/reconciler/roleinstanceset/... ./internal/controller/workloads/... -count=1

echo ""
echo "=== All checks PASSED ==="
echo ">>> To advance .last-reviewed: echo '$CURRENT_SHA' > $LAST_REVIEWED_FILE"
