#!/usr/bin/env bash
# re-verify.sh — re-run the PR #510 review harness against the current PR head
# (or an explicit <fixed-ref>) and report per finding Fixed / Still-broken /
# Partial / Harness-update. Exit 0 iff all findings are fixed (all contract
# tests green and lint clean).
#
# Usage: bash docs/verification/status-phase-conditions-claude/scripts/re-verify.sh [<ref>]
# With no argument the current PR head is fetched via `git fetch <repo-url> pull/<n>/head`.
set -uo pipefail

MANIFEST_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TOPIC="$(basename "$MANIFEST_DIR")"
PR_URL="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["pr"])' "$MANIFEST_DIR/verify-manifest.json")"
PR_NUM="${PR_URL##*/}"
REPO_URL="$(dirname "$(dirname "$PR_URL")").git"

WORKTREE="$(mktemp -d "${TMPDIR:-/tmp}/${TOPIC}-XXXXXX")"
REF="${1:-}"
if [ -z "$REF" ]; then
  echo "[re-verify] fetching PR head: $REPO_URL pull/$PR_NUM/head"
  git fetch "$REPO_URL" "pull/$PR_NUM/head" -q || { echo "fetch failed"; exit 3; }
  REF="FETCH_HEAD"
fi
echo "[re-verify] target ref: $REF"

# Graft the harness onto the code under review in a scratch worktree.
git worktree add --detach "$WORKTREE" "$REF" -q || { echo "worktree add failed"; exit 3; }
pushd "$WORKTREE" >/dev/null
HEADSHA="$(git rev-parse HEAD)"
echo "[re-verify] grafting harness (from this checkout of the harness branch) onto $HEADSHA"
HARNESS_ROOT="$(cd "$MANIFEST_DIR/../../.." && pwd)"
for f in \
  "docs/verification/$TOPIC" \
  "internal/controller/workloads/rolebasedgroup_lws_status_claude_test.go" \
  "pkg/reconciler/roleinstanceset/statelessmode/status_churn_claude_test.go"; do
  if [ ! -e "$HARNESS_ROOT/$f" ]; then
    echo "harness file missing in the current checkout: $f (run re-verify.sh from a checkout of the harness branch)"; popd >/dev/null; exit 3
  fi
  mkdir -p "$(dirname "$f")"
  cp -r "$HARNESS_ROOT/$f" "$f"
done

results=()
overall=0

run_go() {
  go test "$1" -run "$2" -count=1 2>&1
}

check() { # name, package, test-regex
  local name="$1" pkg="$2" regex="$3" out
  out="$(run_go "$pkg" "$regex")"
  if echo "$out" | grep -q "^ok\|^--- PASS"; then
    if echo "$out" | grep -q "^--- FAIL\|FAIL"; then results+=("$name: Still-broken"); overall=1
    else results+=("$name: Fixed"); fi
  elif echo "$out" | grep -q "no tests to run\|testing: warning: no tests"; then
    results+=("$name: Harness-update (test not found — code shape changed?)"); overall=1
  elif echo "$out" | grep -q "FAIL"; then results+=("$name: Still-broken"); overall=1
  else results+=("$name: Harness-update (build failure)"); overall=1
  fi
  echo "$out" | tail -5
}

echo; echo "== F-lws-ready (contract: must be GREEN when fixed) =="
check "F-lws-ready" ./internal/controller/workloads/ "TestClaudeRoleWorkloadStatus"

echo; echo "== F-no-churn (regression guard: must stay GREEN) =="
check "F-no-churn" ./pkg/reconciler/roleinstanceset/statelessmode/ "TestClaudeStatelessNoChurn"

echo; echo "== F-lint-gocyclo (contract: golangci-lint must be clean) =="
bash docs/verification/$TOPIC/scripts/20-lint-diff.sh | tail -20
if [ "${PIPESTATUS[0]}" -eq 0 ]; then results+=("F-lint-gocyclo: Fixed"); else results+=("F-lint-gocyclo: Still-broken"); overall=1; fi

popd >/dev/null
git worktree remove --force "$WORKTREE" >/dev/null 2>&1

echo; echo "== Summary (head $HEADSHA) =="
printf '%s\n' "${results[@]}"
LAST_REVIEWED="$MANIFEST_DIR/.last-reviewed"
echo; echo "Delta for the incremental review (if .last-reviewed exists):"
if [ -f "$LAST_REVIEWED" ]; then
  echo "  git log --oneline $(cat "$LAST_REVIEWED")..$HEADSHA"
  echo "  advance the marker after this round:  echo $HEADSHA > $LAST_REVIEWED"
else
  echo "  no .last-reviewed marker (first round)"
fi
exit $overall
