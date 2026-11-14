#!/usr/bin/env bash
# re-verify.sh — re-run the exclusive-topology verification harness (PR #588,
# Reviewer A / Claude) against the current PR head.
#
# Usage: bash docs/verification/exclusive-topology-claude/scripts/re-verify.sh [<fixed-ref>]
# With no argument it fetches the current PR head from the manifest's "pr" URL.
# Exit 0 iff all findings are fixed (contract tests green, canary still
# documenting current behavior).
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MANIFEST="$HERE/../verify-manifest.json"
LAST_REVIEWED="$HERE/../.last-reviewed"
RESULTS="$HERE/../results"

PR_URL="$(python3 -c "import json,sys;print(json.load(open('$MANIFEST'))['pr'])" 2>/dev/null)"
PR_NUM="${PR_URL##*/}"

WORKDIR="$(mktemp -d /tmp/rbg-verify-exclusive.XXXXXX)"
echo "workdir: $WORKDIR"

git clone -q https://github.com/sgl-project/rbg.git "$WORKDIR/repo" 2>/dev/null || {
  echo "clone failed"; exit 2; }
cd "$WORKDIR/repo"

if [ -n "${1:-}" ]; then
  FIXED_REF="$1"
  git fetch -q origin "$FIXED_REF" 2>/dev/null || git checkout -q "$FIXED_REF" || { echo "bad ref $FIXED_REF"; exit 2; }
  [ -d .git ] && git checkout -q FETCH_HEAD 2>/dev/null || true
else
  git fetch -q "$PR_URL" "head" || git fetch -q origin "refs/pull/$PR_NUM/head"
  git checkout -q FETCH_HEAD
fi
HEAD_SHA="$(git rev-parse HEAD)"
echo "grafting harness onto $HEAD_SHA"

git fetch -q https://github.com/cheyang/rbg.git verify/exclusive-topology-claude
git checkout -q FETCH_HEAD -- \
  docs/verification/exclusive-topology-claude \
  pkg/reconciler/verify_exclusive_topology_claude_test.go \
  test/envtest/testcase/rbg/verify_exclusive_topology_claude_test.go

echo "delta since last reviewed: $([ -f "$LAST_REVIEWED" ] && cat "$LAST_REVIEWED" || echo '<none>')..$HEAD_SHA"

STATUS=0

echo "=== L1 unit ==="
if go test ./pkg/reconciler -count=1 -run 'TestVerifyClaude' -v 2>&1 | tee "$RESULTS/L1-reverify.log" | grep -E '^(--- FAIL|FAIL|ok )'; then :; fi
if grep -q '^FAIL' "$RESULTS/L1-reverify.log"; then STATUS=1; fi

echo "=== L2 integration (envtest) ==="
ENVTEST_ASSETS="${KUBEBUILDER_ASSETS:-}"
if [ -z "$ENVTEST_ASSETS" ]; then
  for d in /home/ecs-user/.local/share/kubebuilder-envtest/k8s/1.31.0-linux-amd64 \
           /usr/local/kubebuilder/bin \
           "$WORKDIR/repo/bin/k8s/1.31.0-linux-amd64"; do
    [ -x "$d/kube-apiserver" ] && ENVTEST_ASSETS="$d" && break
  done
fi
if [ -n "$ENVTEST_ASSETS" ] && [ -x "$ENVTEST_ASSETS/kube-apiserver" ]; then
  KUBEBUILDER_ASSETS="$ENVTEST_ASSETS" go test ./test/envtest/testcase/rbg/ \
    -count=1 -timeout=8m -ginkgo.focus 'reviewer verify' 2>&1 \
    | tee "$RESULTS/L2-reverify.log" | grep -E 'Ran [0-9]+ of|FAIL|ok '
  grep -q 'FAIL' "$RESULTS/L2-reverify.log" && STATUS=1
else
  echo "envtest assets not found (set KUBEBUILDER_ASSETS); L2 not run"
  STATUS=1
fi

if [ "$STATUS" -eq 0 ]; then
  echo "ALL FINDINGS FIXED (L1+L2 green; canary F4 documents current conflict-guard behavior)"
  echo "$HEAD_SHA" > "$LAST_REVIEWED"
  echo "advance .last-reviewed to $HEAD_SHA and commit it on the harness branch"
else
  echo "STILL BROKEN (or harness needs update) — see $RESULTS/*-reverify.log"
fi
exit $STATUS
