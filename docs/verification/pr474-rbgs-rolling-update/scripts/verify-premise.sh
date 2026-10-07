#!/usr/bin/env bash
# verify-premise.sh — run the P0 premise test against the BASE branch (merge-base),
# without the PR patch. The premise is "today, a groupTemplate change updates every
# outdated child within a single reconcile, with no pacing"; only the unpatched code
# can answer that.
#
# Usage: bash verify-premise.sh [repo-root] [base-ref]
# Defaults: repo root = the repo containing this script; base-ref = origin/main.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="${1:-$(cd "$SCRIPT_DIR/../../../.." && pwd)}"
BASE_REF="${2:-origin/main}"

cd "$REPO_ROOT"
BASE_SHA="$(git merge-base "$BASE_REF" HEAD 2>/dev/null || git rev-parse "$BASE_REF")"
echo "verify-premise: base = $BASE_SHA"

WT="$(mktemp -d)/base-worktree"
cleanup() { git worktree remove --force "$WT" >/dev/null 2>&1 || true; }
trap cleanup EXIT

git worktree add "$WT" "$BASE_SHA" >/dev/null
cp "$SCRIPT_DIR/verify_p0_premise_codex_test.go" "$WT/internal/controller/workloads/"
cd "$WT"
go test ./internal/controller/workloads/ -run 'TestVerify_P0' -count=1 -v
