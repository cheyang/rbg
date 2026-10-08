#!/usr/bin/env bash
# re-verify.sh — re-run the PR #389 docs harness against the current PR head and
# report per finding: Fixed / Still-broken (all checks here are CONTRACT polarity:
# failing on the reviewed head, green once fixed).
#
# Usage: bash docs/verification/pr389-autoscaling-docs/scripts/re-verify.sh [<fixed-ref>]
#   No ref  -> fetch current head of the PR recorded in verify-manifest.json
#   <ref>   -> any local ref/sha (e.g. during iterative fix review)
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VDIR="$(dirname "$HERE")"
MANIFEST="$VDIR/verify-manifest.json"
REPO_ROOT="$(git rev-parse --show-toplevel)"
PR_URL="$(python3 -c "import json;print(json.load(open('$MANIFEST'))['pr'])")"
PR_NUM="$(basename "$PR_URL")"
REF="${1:-}"
if [ -z "$REF" ]; then
  echo "re-verify: fetching current head of $PR_URL"
  git -C "$REPO_ROOT" fetch https://github.com/sgl-project/rbg.git "pull/$PR_NUM/head" >/dev/null 2>&1
  REF=FETCH_HEAD
fi
SHA="$(git -C "$REPO_ROOT" rev-parse "$REF")"
echo "re-verify: testing $REF ($SHA)"
LAST="$(cat "$VDIR/.last-reviewed" 2>/dev/null || true)"
[ -n "$LAST" ] && { echo "re-verify: delta since last reviewed ($LAST..$SHA):"; git -C "$REPO_ROOT" log --oneline "$LAST..$SHA" -- doc/ 2>/dev/null | head -20; }
WORK="$(mktemp -d)"
git -C "$REPO_ROOT" worktree add --detach "$WORK" "$SHA" >/dev/null 2>&1
python3 "$HERE/check_docs.py" "$WORK" | tee "$VDIR/results/re-verify-$(date +%Y%m%d-%H%M%S).txt" | grep -E '^\[F' > /tmp/rv.$$
git -C "$REPO_ROOT" worktree remove --force "$WORK" >/dev/null 2>&1
echo
echo "==== per-finding verdict ===="
for F in F1 F2 F3 F4 F5 F6 F7; do
  total=$(grep "^\[$F\]" /tmp/rv.$$ | grep -cv "evidence" || true)
  fails=$(grep "^\[$F\]" /tmp/rv.$$ | grep -v "evidence" | grep -c " FAIL " || true)
  evfail=$(grep "^\[$F\]" /tmp/rv.$$ | grep "evidence" | grep -c " FAIL " || true)
  note=""; [ "$evfail" -gt 0 ] && note=" (evidence drift: upstream changed - re-pin upstreamPinned.sha)"
  if [ "$total" -eq 0 ]; then echo "$F: Harness-update (no contract checks ran)$note";
  elif [ "$fails" -eq 0 ]; then echo "$F: Fixed (all $total contract checks green)$note";
  elif [ "$fails" -eq "$total" ]; then echo "$F: Still-broken ($fails/$total contract checks failing)$note";
  else echo "$F: Partial ($fails/$total contract checks failing)$note"; fi
done
rm -f /tmp/rv.$$
