#!/usr/bin/env bash
# Run the P0 premise check (issue #482 symptom) against the PR BASE branch.
# The premise test uses base-only symbols, so it is compiled with the
# rbg_premise_base build tag inside a throwaway worktree of the base ref.
# Production code and the PR head are never modified.
#
# Usage: bash scripts/run-premise-base.sh [base-sha]
#   (default: the baseSha recorded in verify-manifest.json)
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"   # docs/verification/<topic>
REPO_ROOT="$(git -C "$HERE" rev-parse --show-toplevel)"
BASE_SHA="${1:-$(jq -r '.baseSha // "ecdfd6e601de64adc0d0892c7ad05f75e85873a8"' "$HERE/verify-manifest.json")}"
OUT="$HERE/results/premise-base.out"

WT="$(mktemp -d /tmp/rbg-premise-XXXX)"
trap 'git -C "$REPO_ROOT" worktree remove --force "$WT" >/dev/null 2>&1 || rm -rf "$WT"' EXIT
git -C "$REPO_ROOT" worktree add --detach "$WT" "$BASE_SHA" >/dev/null
cp "$HERE/premise/premise_base_test.go" "$WT/pkg/reconciler/roleinstanceset/statefulmode/"

echo "premise check against base ${BASE_SHA}" | tee "$OUT"
( cd "$WT" && go test -mod=vendor -tags rbg_premise_base -count=1 \
    -run 'TestClaudePremise' -v ./pkg/reconciler/roleinstanceset/statefulmode/ ) 2>&1 | tee -a "$OUT"
echo "exit=$?" | tee -a "$OUT"
