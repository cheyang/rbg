#!/usr/bin/env bash
# re-verify.sh for PR cheyang/rbg#5 findings (F1/F2/F3).
# Fetches the PR head machine-independently via manifest.pr and checks, in a temp
# worktree:
#   F1: the two harness test packages COMPILE (go test build only)
#   F2: every results/ path the harness README references is git-tracked
#   F3: README no longer recommends cherry-picking the round-1 harness commit
#       f592f533 without qualification
# Contract polarity throughout: FAIL on the reviewed head (da0e366d), PASS when fixed.
# Usage: re-verify.sh [<ref>]   (default: current head of the PR in manifest.pr)
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MANIFEST="$HERE/verify-manifest.json"
REF="${1:-}"
if [ -z "$REF" ]; then
  PR_URL="$(jq -r '.pr' "$MANIFEST")"
  rest="${PR_URL#*://}"; host="${rest%%/*}"; path="${rest#*/}"
  owner="$(printf '%s' "$path" | cut -d/ -f1)"; repo="$(printf '%s' "$path" | cut -d/ -f2)"
  num="$(printf '%s' "$path" | sed -E 's#.*/pull/([0-9]+).*#\1#')"
  git fetch --quiet "https://$host/$owner/$repo.git" "pull/$num/head"
  REF="$(git rev-parse FETCH_HEAD)"
  echo "re-verify: resolved PR head via $PR_URL -> $REF"
fi
WT="$(mktemp -d)/wt"
git worktree add --detach "$WT" "$REF" >/dev/null 2>&1
cleanup() { git worktree remove --force "$WT" >/dev/null 2>&1 || true; }
trap cleanup EXIT
cd "$WT"

FAILURES=0
echo "== F1: harness packages compile against the PR's base =="
if go test -count=1 -run XXXNONE ./pkg/reconciler/roleinstance/sync/ ./test/envtest/testcase/restart_policy/ >/tmp/f1.out 2>&1; then
  echo "F1: PASS (both packages build)"
else
  echo "F1: FAIL (build errors:)"; grep -E '\.go:[0-9]+' /tmp/f1.out | head -6; FAILURES=$((FAILURES+1))
fi

echo "== F2: README-referenced evidence is tracked =="
D=docs/verification/pr394-restart-backoff
missing=0
for f in $(grep -o "results/[A-Za-z0-9._/-]*" "$D/README.md" | sort -u); do
  case "$f" in results/) continue;; esac
  if ! git ls-files --error-unmatch "$D/$f" >/dev/null 2>&1; then
    echo "  missing/untracked: $f"; missing=$((missing+1))
  fi
done
if [ "$missing" -eq 0 ]; then echo "F2: PASS"; else echo "F2: FAIL ($missing referenced paths not tracked)"; FAILURES=$((FAILURES+1)); fi

echo "== F3: Option B (cherry-pick round-1 harness) no longer unqualified =="
if grep -q "cherry-pick f592f533" "$D/README.md"; then
  echo "F3: FAIL (README still recommends cherry-picking the round-1 harness, which does not compile on post-refactor heads)"
  FAILURES=$((FAILURES+1))
else
  echo "F3: PASS"
fi

echo
[ "$FAILURES" -eq 0 ] && { echo "RESULT: all checks pass."; exit 0; } || { echo "RESULT: $FAILURES check(s) failing."; exit 1; }
