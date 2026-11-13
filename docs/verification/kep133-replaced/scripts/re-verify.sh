#!/usr/bin/env bash
# re-verify.sh for PR 577 (KEP-133: mark replaced by KEP-576) — Reviewer A (Claude).
#
# Re-runs the verification harness against the current PR head, grafted onto a
# temp worktree, without touching this branch's checked-out state.
#
# Usage: bash scripts/re-verify.sh [<fixed-ref>]
#   no ref  -> fetch the current PR head from the upstream repo (pull/577/head)
#   <ref>   -> verify that ref instead (e.g. a commit on the updated PR branch)
#
# Exit 0 iff all contract checks pass AND every bug-canary is in its
# "documented broken" state is no longer required — a canary that FLIPS to
# FAIL means the underlying finding is FIXED (see README polarity table).
set -uo pipefail

UPSTREAM="https://github.com/sgl-project/rbg"
PR_NUM=577
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"        # docs/verification/kep133-replaced
BRANCH_ROOT="$(cd "$HERE/../../.." && pwd)"                     # repo checkout holding this branch
CHECKER="$HERE/scripts/check_kep133_replaced.py"
MANIFEST="$HERE/verify-manifest.json"
LAST_REVIEWED_FILE="$HERE/.last-reviewed"

TMPWT="$(mktemp -d /tmp/kep133-reverify.XXXXXX)"
cleanup() { git -C "$BRANCH_ROOT" worktree remove --force "$TMPWT" >/dev/null 2>&1; rm -rf "$TMPWT"; }
trap cleanup EXIT

# --- resolve the ref to verify -------------------------------------------------
if [[ $# -ge 1 ]]; then
  HEAD_REF="$1"
else
  echo ">> fetching current PR head from $UPSTREAM (refs/pull/$PR_NUM/head)"
  git -C "$BRANCH_ROOT" fetch "$UPSTREAM" "refs/pull/$PR_NUM/head:refs/heads/pr-$PR_NUM-head-tmp" >/dev/null 2>&1 \
    || git -C "$BRANCH_ROOT" fetch "$UPSTREAM" "pull/$PR_NUM/head" || exit 2
  HEAD_REF="refs/heads/pr-$PR_NUM-head-tmp"
fi
HEAD_SHA="$(git -C "$BRANCH_ROOT" rev-parse "$HEAD_REF")"

echo ">> fetching tags (needed by T4 release-containment checks)"
git -C "$BRANCH_ROOT" fetch --tags "$UPSTREAM" >/dev/null 2>&1 || true

BASE_SHA="$(git -C "$BRANCH_ROOT" merge-base "$HEAD_SHA" origin/main 2>/dev/null \
  || git -C "$BRANCH_ROOT" merge-base "$HEAD_SHA" main 2>/dev/null)"
if [[ -z "${BASE_SHA:-}" ]]; then
  echo "!! could not resolve merge-base for $HEAD_SHA" >&2; exit 2
fi

echo ">> PR head: $HEAD_SHA (base: $BASE_SHA)"

# --- incremental review delta --------------------------------------------------
if [[ -f "$LAST_REVIEWED_FILE" ]]; then
  LAST="$(cat "$LAST_REVIEWED_FILE")"
  echo ">> incremental delta: $LAST..$HEAD_SHA"
  git -C "$BRANCH_ROOT" log --oneline "$LAST..$HEAD_SHA" 2>/dev/null | sed 's/^/     /'
  echo ">> advance the marker after this round: echo $HEAD_SHA > $LAST_REVIEWED_FILE"
fi

# --- graft harness onto the ref under test and run ----------------------------
git -C "$BRANCH_ROOT" worktree add --detach "$TMPWT" "$HEAD_SHA" >/dev/null 2>&1 || exit 2
mkdir -p "$TMPWT/docs/verification/kep133-replaced/scripts"
cp "$CHECKER" "$TMPWT/docs/verification/kep133-replaced/scripts/"
echo ">> running harness"
( cd "$TMPWT" && python3 docs/verification/kep133-replaced/scripts/check_kep133_replaced.py "$TMPWT" "$BASE_SHA" "$HEAD_SHA" )
RC=$?

echo
if [[ $RC -eq 0 ]]; then
  echo "VERDICT: all checks in expected state (canaries still documenting the broken state)."
  echo "  T3-successor-link-canary PASS == dead link still present (finding kep133-deadlink-order OPEN)."
  echo "  If T3 flipped to FAIL: KEP-576 is now in the tree -> finding FIXED; invert the canary."
else
  echo "VERDICT: at least one check deviates — inspect output above:"
  echo "  - T1/T2/T4 FAIL  -> contract regression in the PR (investigate)."
  echo "  - T3 FAIL         -> successor KEP present -> finding kep133-deadlink-order FIXED (invert canary)."
fi
exit $RC
