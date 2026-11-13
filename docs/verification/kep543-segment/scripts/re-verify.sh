#!/usr/bin/env bash
# re-verify.sh for KEP-543 / PR #569 verification harness (docs-only PR).
#
# Usage: bash scripts/re-verify.sh [<ref>]
#   no ref  -> fetches the current PR head from the manifest's `pr` URL
#              (git fetch <repo> pull/<n>/head) and verifies that
#   <ref>   -> verifies that specific commit instead
#
# Re-runs the unit + integration layers of this harness against the PR head:
#   P0  premise (issue #543 open + asks for this KEP; needs gh, else SKIPPED)
#   AC  doc quality (markdownlint, links/anchors, EN/ZH parity)
#   REF "existing behavior" claims vs the code at the head
#   F1  Segment-aware address discovery: mechanism/owner + test coverage
#       in the design doc (CONTRACT polarity: red while the gap exists,
#       green once the doc names the mechanism and covers it in tests)
#
# Prints a per-finding verdict table and the last-reviewed..head delta range.
# Exit 0 iff every executed check passes (F1 fixed included).
set -u
HERE="$(cd "$(dirname "$0")/.." && pwd)"   # docs/verification/kep543-segment
BRANCH_ROOT="$(cd "$HERE/../../.." && pwd)"
MANIFEST="$HERE/verify-manifest.json"
PR_URL=$(python3 -c 'import json;print(json.load(open("'"$MANIFEST"'"))["pr"])' 2>/dev/null)
PR_NUM=${PR_URL##*/}
REPO_URL=${PR_URL%/pull/*}

if [ $# -ge 1 ]; then
  REF="$1"
else
  echo "Fetching current head of $PR_URL ..."
  git -C "$BRANCH_ROOT" fetch "$REPO_URL" "pull/$PR_NUM/head" || exit 2
  REF=$(git -C "$BRANCH_ROOT" rev-parse FETCH_HEAD)
fi
echo "Verifying PR head: $REF"

WT=$(mktemp -d /tmp/kep543-verify.XXXXXX)
git -C "$BRANCH_ROOT" worktree add --detach "$WT" "$REF" >/dev/null 2>&1 || { echo "worktree failed"; exit 2; }
# graft the harness scripts onto the head tree (additive only)
mkdir -p "$WT/docs/verification"
cp -r "$HERE/scripts" "$WT/docs/verification/kep543-segment-scripts"

declare -a OUT
overall=0

echo "== P0 premise =="
bash "$HERE/scripts/check_premise.sh" || overall=1
echo
echo "== Doc quality (markdownlint / links / EN-ZH parity) =="
bash "$WT/docs/verification/kep543-segment-scripts/check_docs.sh" "$WT" || overall=1
echo
echo "== 'Existing behavior' claims vs code =="
bash "$HERE/scripts/check_code_refs.sh" "$WT" "$REF" | tee /tmp/kep543-coderefs.txt
grep -q "^FAIL" /tmp/kep543-coderefs.txt && overall=1
echo
echo "== F1: Segment-aware address discovery (contract: red = gap present) =="
if bash "$HERE/scripts/check_f1_discovery.sh" "$WT"; then
  echo "F1: Fixed — design now names a discovery mechanism with test coverage"
else
  echo "F1: Still-broken — discovery requirement has no mechanism/owner/coverage"
  overall=1
fi

echo
echo "== KEP-543 code-claim canaries (unit layer, Go) =="
CANARY_SRC="$BRANCH_ROOT/pkg/reconciler/roleinstanceset/statefulmode/kep543_segment_claims_test.go"
CANARY_DST="$WT/pkg/reconciler/roleinstanceset/statefulmode/kep543_segment_claims_test.go"
if [ -f "$CANARY_SRC" ]; then
  cp "$CANARY_SRC" "$CANARY_DST"
  if (cd "$WT" && go test ./pkg/reconciler/roleinstanceset/statefulmode/ -run 'TestVerifyKEP543' -count=1) 2>&1 | tail -5; then
    :
  else
    echo "KEP-543 code claims: FAIL or Harness-update (code under review changed shape)"
    overall=1
  fi
else
  echo "KEP-543 code claims: Harness-update — canary test file missing from harness branch"
  overall=1
fi

echo
echo "== B-F1: zero-budget redesign present (contract: green = fixed) =="
if bash "$HERE/scripts/check_b1_zero_budget.sh" "$WT"; then
  echo "B-F1: Fixed — B=0 requires positive effective maxSurge and aligns with the RIS floor"
else
  echo "B-F1: Still-broken — zero-budget rule still contradicts the RIS floor"
  overall=1
fi

echo
echo "== R2-F1: fail-fast vs per-rule isolation consistency (contract: red = stale sentence) =="
if bash "$HERE/scripts/check_r2_consistency.sh" "$WT"; then
  echo "R2-F1: Fixed — isolation language consistent with fail-fast semantics"
else
  echo "R2-F1: Still-broken — 'Non-Segment Stateless roles are unaffected' is stale under whole-RBG fail-fast"
  overall=1
fi

echo
echo "== Incremental review delta =="
LAST=$(cat "$HERE/.last-reviewed" 2>/dev/null)
if [ -n "$LAST" ]; then
  echo "last-reviewed..head: $LAST..$REF"
  git -C "$BRANCH_ROOT" log --oneline "$LAST..$REF" 2>/dev/null | head -20
  echo "after this round: echo $REF > $HERE/.last-reviewed"
else
  echo "no .last-reviewed marker; set it now: echo $REF > $HERE/.last-reviewed"
fi

git -C "$BRANCH_ROOT" worktree remove --force "$WT" >/dev/null 2>&1
rm -rf /tmp/kep543-coderefs.txt
echo
if [ "$overall" = 0 ]; then
  echo "RESULT: all checks pass (F1 fixed)"; exit 0
else
  echo "RESULT: failing checks remain"; exit 1
fi
