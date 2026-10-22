#!/usr/bin/env bash
# re-verify.sh — Reviewer B (Codex) harness for PR #508 (verify/stateful-rollback-codex).
#
# Stages:
#   premise  — run the base-only premise test against the merge-base (must FAIL there).
#   unit     — graft zz_codex_contracts_test.go onto the target ref; go test -race (must PASS).
#   integration — envtest stateful suite on the target ref (needs envtest assets).
#
# Usage: re-verify.sh [<fixed-ref>]   (no ref => resolve current PR head from manifest.pr)
# Exit 0 iff premise reproduced on base AND all head-side contract tests pass.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"   # docs/verification/stateful-rollback-codex
MANIFEST="$HERE/verify-manifest.json"
REPO_ROOT="$(cd "$HERE/../../.." && pwd)"
cd "$REPO_ROOT"
command -v jq >/dev/null || { echo "jq required" >&2; exit 2; }

FIXED_REF="${1:-}"
if [ -z "$FIXED_REF" ]; then
  PR_URL="$(jq -r '.pr' "$MANIFEST")"
  rest="${PR_URL#*://}"; path="${rest#*/}"
  owner="$(printf '%s' "$path" | cut -d/ -f1)"; repo="$(printf '%s' "$path" | cut -d/ -f2)"
  num="$(printf '%s' "$path" | sed -E 's#.*/pull/([0-9]+).*#\1#')"
  git fetch --quiet "https://${rest%%/*}/$owner/$repo.git" "pull/$num/head"
  FIXED_REF="$(git rev-parse FETCH_HEAD)"
  echo "re-verify: resolved PR head $FIXED_REF"
fi
BASE_SHA="$(jq -r '.baseSha' "$MANIFEST")"
LAST="$(cat "$HERE/.last-reviewed" 2>/dev/null || echo "$BASE_SHA")"
echo "re-verify: base=$BASE_SHA target=$FIXED_REF delta=$LAST..$FIXED_REF"

RESULTS="$HERE/results/reverify"; mkdir -p "$RESULTS"
WT_BASE="$(mktemp -d /tmp/rbg-verify-base.XXXXXX)"
WT_HEAD="$(mktemp -d /tmp/rbg-verify-head.XXXXXX)"
cleanup() { git worktree remove --force "$WT_BASE" >/dev/null 2>&1 || true; git worktree remove --force "$WT_HEAD" >/dev/null 2>&1 || true; }
trap cleanup EXIT

FAILURES=0

echo "== premise (P0 against base $BASE_SHA) =="
git worktree add --force "$WT_BASE" "$BASE_SHA" >/dev/null 2>&1
cp "$HERE/harness/zz_codex_premise_base_test.go" "$WT_BASE/pkg/reconciler/roleinstanceset/statefulmode/"
if (cd "$WT_BASE" && go test -mod=vendor -count=1 -run 'TestCodexP0' ./pkg/reconciler/roleinstanceset/statefulmode/) >"$RESULTS/premise.out" 2>&1; then
  echo "P0  premise        REFUTED  (test passed on base — the bug does not reproduce; investigate!)"
  FAILURES=1
else
  echo "P0  premise        REPRODUCED on base (expected FAIL) — see results/reverify/premise.out"
fi

echo "== unit contracts (C1-C7 against $FIXED_REF) =="
git worktree add --force "$WT_HEAD" "$FIXED_REF" >/dev/null 2>&1
cp "$REPO_ROOT/pkg/reconciler/roleinstanceset/statefulmode/zz_codex_contracts_test.go" \
   "$WT_HEAD/pkg/reconciler/roleinstanceset/statefulmode/"
if (cd "$WT_HEAD" && go test -mod=vendor -race -count=1 -run 'TestCodex' -v ./pkg/reconciler/roleinstanceset/statefulmode/) >"$RESULTS/unit.out" 2>&1; then
  grep -E '^--- (PASS|FAIL)' "$RESULTS/unit.out" | sed 's/^/  /'
  echo "C1-C7  contract     FIXED (all pass)"
else
  grep -E '^--- (PASS|FAIL)' "$RESULTS/unit.out" | sed 's/^/  /' || tail -20 "$RESULTS/unit.out"
  echo "C1-C7  contract     STILL-BROKEN or harness-update — see results/reverify/unit.out"
  FAILURES=1
fi

echo "== integration (envtest stateful suite) =="
# The suite runs inside a grafted worktree, so the assets path must be absolute;
# a relative KUBEBUILDER_ASSETS (e.g. "bin/k8s/...") resolves against the wrong root.
SETUP_ENVTEST="${SETUP_ENVTEST:-}"
if [ -z "$SETUP_ENVTEST" ] && [ -x "$REPO_ROOT/bin/setup-envtest" ]; then SETUP_ENVTEST="$REPO_ROOT/bin/setup-envtest"; fi
if [ -z "$SETUP_ENVTEST" ]; then SETUP_ENVTEST="$(command -v setup-envtest || true)"; fi
if [ -z "$SETUP_ENVTEST" ] && [ -x "$HOME/go/bin/setup-envtest" ]; then SETUP_ENVTEST="$HOME/go/bin/setup-envtest"; fi
ASSETS="${KUBEBUILDER_ASSETS:-}"
if [ -z "$ASSETS" ] && [ -n "$SETUP_ENVTEST" ]; then
  ASSETS="$("$SETUP_ENVTEST" use 1.31.0 --bin-dir "$REPO_ROOT/bin" -p path 2>/dev/null || true)"
fi
if [ -n "$ASSETS" ] && [ "${ASSETS#/}" = "$ASSETS" ]; then ASSETS="$REPO_ROOT/$ASSETS"; fi
if [ -z "$ASSETS" ]; then
  echo "I1  integration    SKIPPED (no KUBEBUILDER_ASSETS / setup-envtest)"
else
  if (cd "$WT_HEAD" && KUBEBUILDER_ASSETS="$ASSETS" go test -mod=vendor ./test/envtest/testcase/stateful -count=1 -timeout 10m) >"$RESULTS/integration.out" 2>&1; then
    echo "I1  integration    PASS (stateful envtest suite)"
  else
    echo "I1  integration    FAIL — see results/reverify/integration.out"
    FAILURES=1
  fi
fi

echo
echo "Review delta this round: $LAST..$FIXED_REF"
echo "Advance the marker after reviewing:  echo $FIXED_REF > $HERE/.last-reviewed"
[ "$FAILURES" -eq 0 ] && { echo "RESULT: premise reproduced on base; all contracts pass on target."; exit 0; } || { echo "RESULT: failures — see above."; exit 1; }
