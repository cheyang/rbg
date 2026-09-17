#!/usr/bin/env bash
# Re-verify harness for RBG PR #471 — see ../README.md.
# Usage: bash docs/verification/pr471-discovery-configmap-fqdn/scripts/re-verify.sh [<fixed-ref>]
#   <fixed-ref> = a git ref with the fix applied (defaults to the current PR head fetched from the
#   manifest's `pr` URL, so no sha is needed on a fresh machine).
#
# Runs the L1 unit layer (deterministic, cross-machine) and prints per-finding status, honoring
# polarity: TestLWS_NamingContract is a CONTRACT test (RED on buggy PR head = reproduction; should
# flip to PASS when fixed). The PR's own `...on_LeaderWorkerSet` subtest is a BUG-CANARY (asserts the
# wrong behavior); when fixed it flips to RED and must be inverted — reported as "needs-invert".
#
# The L3 live layer (TestLiveProbe_ConfigMapVsPods) needs a cluster: set KUBECONFIG, deploy the
# 3-pattern RBG via deploy_scenario.sh, and run it with RBG_LIVE_PROBE=1. It is not run here.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MANIFEST="$HERE/../verify-manifest.json"
ROOT="$(git -C "$HERE" rev-parse --show-toplevel 2>/dev/null || echo "$HERE/../../..")"
cd "$ROOT"

PR_URL="$(python3 -c 'import json;print(json.load(open("'"$MANIFEST"'"))["pr"])')"
PR_NUM="$(printf '%s' "$PR_URL" | grep -oE '[0-9]+$')"
# Derive the clone URL (…/pull/N -> ….git) so `git fetch <url> pull/N/head` works.
REPO_URL="$(printf '%s' "$PR_URL" | sed -E 's#/pull/[0-9]+$#.git#')"
FIXED_REF="${1:-}"

# Resolve the ref to test against: explicit arg > fetch current PR head.
if [ -z "$FIXED_REF" ]; then
  echo ">> fetching current PR head for PR $PR_NUM from $REPO_URL"
  git fetch "$REPO_URL" "pull/$PR_NUM/head"
  PR_HEAD_SHA="$(git rev-parse FETCH_HEAD)"
else
  PR_HEAD_SHA="$(git rev-parse "$FIXED_REF")"
fi
echo ">> testing against: $FIXED_REF ($PR_HEAD_SHA)"

LAST="$(cat "$HERE/../.last-reviewed" 2>/dev/null || echo "")"
if [ -n "$LAST" ]; then
  echo ">> incremental delta since last review ($LAST):"
  git log --oneline "$LAST".."$PR_HEAD_SHA" 2>/dev/null | head -40 || true
  echo ">> to advance the marker after this round:  git -C \"$ROOT\" update-ref ... ; echo $PR_HEAD_SHA > \"$HERE/../.last-reviewed\""
fi

echo
echo "================ L1 unit layer ================"
export GOFLAGS="${GOFLAGS:--mod=mod}" GOCACHE="${GOCACHE:-/tmp/gocache-rbg-pr471}"

echo ">> PR's own ConfigBuilder tests (expect green):"
if go test ./pkg/discovery/ -run TestConfigBuilder -count=1 2>&1 | tail -3; then
  echo "[F2/F4 unit] OK (PR tests green)"
else
  echo "[F2/F4 unit] PR tests red — inspect (may include the F1 bug-canary flipping, which is expected after a fix)"
fi

echo
echo ">> F1 contract canary TestLWS_NamingContract (RED on buggy PR head; PASS when fixed):"
if go test ./pkg/discovery/ -run TestLWS_NamingContract -count=1 2>&1 | tail -3; then
  echo "[F1] FIXED — contract canary is green"
else
  echo "[F1] STILL-BROKEN — contract canary is red (the LWS contiguous-ordinal bug reproduces)"
fi

echo
echo ">> F1 PR bug-canary (the PR's own on_LeaderWorkerSet subtest):"
if go test ./pkg/discovery/ -run 'TestConfigBuilder_Build/leader_worker_pattern_with_size=2_on_LeaderWorkerSet' -count=1 2>&1 | tail -3; then
  echo "[F1 canary] still asserts OLD (wrong) behavior — if the fix landed, INVERT this test's expected"
else
  echo "[F1 canary] flipped to red — the fix changed the output; invert the expected and re-run"
fi

echo
echo "================ L3 live layer (manual) ================"
echo "Set KUBECONFIG, run scripts/deploy_scenario.sh, then:"
echo "  RBG_LIVE_PROBE=1 go test ./pkg/discovery/ -run TestLiveProbe_ConfigMapVsPods -v -count=1"
echo
echo "Per-finding summary:"
echo "  P0 premise            — see results/01 (live, against base controller)"
echo "  F1 LWS naming         — CONTRACT canary above (RED=broken / PASS=fixed)"
echo "  F2 LWP-RIS / F3 CCP / F4 size — live probe results/02"
echo "  F5 worker non-resolve — live nslookup results/03"
