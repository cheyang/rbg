#!/usr/bin/env bash
# Re-verify harness for PR #474 (RoleBasedGroupSet rolling update).
#
# Usage: bash scripts/re-verify.sh [<fixed-ref>]
#   With no ref, fetches the current PR head from the manifest's "pr" URL.
#
# Grafts the harness (this branch's docs/ + verify test files) onto the given
# ref, runs the unit + integration layers, and prints per-claim verdicts.
# Canaries are fixed only when they FLIP to red; contract tests are fixed when
# they go green.
set -euo pipefail

BRANCH_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd)"
MANIFEST="$BRANCH_DIR/docs/verification/pr474-rbgs-rolling-update/verify-manifest.json"
PR_URL="$(python3 -c "import json;print(json.load(open('$MANIFEST'))['pr'])")"
PR_NUM="$(python3 -c "import json;print(json.load(open('$MANIFEST'))['pr'].rstrip('/').split('/')[-1])")"

TARGET_REF="${1:-}"
if [ -z "$TARGET_REF" ]; then
    REPO_URL="$(python3 -c "import json;print('/'.join(json.load(open('$MANIFEST'))['pr'].rstrip('/').split('/')[:5]))")"
    git fetch "$REPO_URL" "pull/$PR_NUM/head" 2>/dev/null || git fetch origin "pull/$PR_NUM/head"
    TARGET_REF="FETCH_HEAD"
fi

WORK="$(mktemp -d)/rbgs-reverify"
git worktree add "$WORK" "$TARGET_REF" >/dev/null 2>&1 || { echo "cannot create worktree at $TARGET_REF"; exit 1; }
trap 'git worktree remove --force "$WORK" >/dev/null 2>&1 || true' EXIT

# Graft the harness onto the target ref.
git -C "$BRANCH_DIR" archive HEAD | tar -x -C "$WORK"

echo "== unit layer =="
(cd "$WORK" && go test ./internal/controller/workloads/ ./api/workloads/v1alpha2/ \
    -run "TestVerifyPR474" -count=1 -v 2>&1) | tee /tmp/reverify-unit.log

echo "== integration layer (envtest) =="
ASSETS="${KUBEBUILDER_ASSETS:-$HOME/envtest-assets/k8s/1.37.0-linux-amd64}"
(cd "$WORK" && KUBEBUILDER_ASSETS="$ASSETS" go test ./test/envtest/testcase/rbg/ \
    -count=1 -ginkgo.focus "PR #474" -timeout 20m 2>&1) | tee /tmp/reverify-integration.log

echo
echo "== verdicts =="
for f in /tmp/reverify-unit.log /tmp/reverify-integration.log; do
    grep -E "^(--- (PASS|FAIL)|    --- (PASS|FAIL))" "$f" | sed 's/ \+/ /g'
done
echo
echo "Interpretation: contract tests are FIXED when green; canaries (names"
echo "containing 'Canary' or flagged 'canary' in the manifest) are FIXED only"
echo "when they flip to RED."
LAST="$(git -C "$BRANCH_DIR" rev-parse "$TARGET_REF")"
echo "Reviewed ref: $LAST  (advance .last-reviewed to this sha if this round is complete)"
