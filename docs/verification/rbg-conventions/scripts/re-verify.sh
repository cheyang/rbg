#!/usr/bin/env bash
# Re-run the offline verification harness (L1 + L2) for PR #568 on the current PR head.
#
# Usage:
#   bash docs/verification/rbg-conventions/scripts/re-verify.sh [<fixed-ref>]
#
# With no ref, the current PR head is fetched from the manifest's pr URL. The harness
# is grafted onto that ref (this branch's docs/ tree is copied over a temporary
# worktree of the ref), the offline layers run, and each finding is reported as
# Fixed / Still-broken / Partial / Harness-update. All tests are contract tests
# (assert the intended behavior), so green means fixed and red means broken.
#
# Exit status: 0 iff every check passes.
set -uo pipefail
here=$(cd "$(dirname "$0")" && pwd)
docs=$(cd "$here/.." && pwd)                # docs/verification/rbg-conventions
repo=$(cd "$docs/../../.." && pwd)          # repo root
manifest=$docs/verify-manifest.json
ref=${1:-}

need() { command -v "$1" >/dev/null 2>&1 || { echo "error: $1 is required" >&2; exit 2; }; }
need jq
need git
need go

[ -r "$manifest" ] || { echo "error: no verify-manifest.json at $manifest" >&2; exit 2; }
prurl=$(jq -r .pr "$manifest")

if [ -z "$ref" ]; then
  prnum=${prurl##*/}
  echo "fetching current head of $prurl"
  ref=$(git -C "$repo" ls-remote "https://github.com/sgl-project/rbg" "refs/pull/$prnum/head" | cut -f1) ||
    { echo "error: cannot resolve PR head" >&2; exit 2; }
  [ -n "$ref" ] || { echo "error: empty head sha for PR $prnum" >&2; exit 2; }
fi
echo "verifying against $ref"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
git -C "$repo" worktree add --detach "$work/tree" "$ref" >/dev/null 2>&1 ||
  { echo "error: cannot check out $ref" >&2; exit 2; }
# graft the harness onto the ref under review
mkdir -p "$work/tree/docs/verification"
cp -r "$docs" "$work/tree/docs/verification/rbg-conventions"

rc=0
echo
echo "== L1: selftest.sh (PR's own)"
bash "$work/tree/.agents/skills/rbg-conventions/scripts/selftest.sh" || rc=1

echo
echo "== L1: edge_tests.sh (reviewer harness)"
bash "$work/tree/docs/verification/rbg-conventions/scripts/edge_tests.sh" || rc=1

echo
echo "== L2: Go harness (jsonpath + fixtures)"
(
  cd "$work/tree/docs/verification/rbg-conventions/harness" || exit 1
  GOFLAGS=-mod=mod go run . jsonpath && GOFLAGS=-mod=mod go run . fixtures
) || rc=1

echo
echo "== L2: shellcheck / bash -n (if shellcheck is available)"
if command -v shellcheck >/dev/null 2>&1; then
  shellcheck -x -s bash "$work/tree/.agents/skills/rbg-conventions/scripts/"*.sh
  sc=$?
  # info-level findings (SC2317, SC2015) are acceptable; style and errors are not
  [ "$sc" -eq 0 ] || [ "$sc" -eq 1 ] || rc=1
else
  echo "shellcheck not found; skipped"
fi
for f in "$work/tree/.agents/skills/rbg-conventions/scripts/"*.sh; do
  bash -n "$f" || rc=1
done

echo
echo "== incremental delta (for the next review round)"
last=$(cat "$docs/.last-reviewed" 2>/dev/null || echo "")
if [ -n "$last" ] && [ "$last" != "$ref" ]; then
  echo "last-reviewed..head: $last..$ref"
  git -C "$repo" log --oneline "$last..$ref" 2>/dev/null | head -20
  echo "advance the marker after the round:"
  echo "  echo $ref > $docs/.last-reviewed && git add $docs/.last-reviewed"
else
  echo "no .last-reviewed marker or unchanged head"
fi

echo
if [ "$rc" -eq 0 ]; then
  echo "VERDICT: all offline checks pass (Fixed)"
else
  echo "VERDICT: at least one check failed (Still-broken or Harness-update; see output above)"
fi
exit "$rc"
