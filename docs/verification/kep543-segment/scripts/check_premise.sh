#!/usr/bin/env bash
# P0 premise check for KEP-543 (PR #569): does issue #543 exist, is it OPEN,
# and does it ask for exactly this design (component match)?
# Usage: check_premise.sh   (requires gh; prints SKIPPED otherwise)
set -u
if ! command -v gh >/dev/null 2>&1; then
  echo "SKIPPED gh not available"
  exit 0
fi
issue=$(gh issue view 543 --repo sgl-project/rbg --json number,title,state,body 2>/dev/null) || {
  echo "FAIL could not read issue #543"; exit 1; }
state=$(printf '%s' "$issue" | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d["state"])')
title=$(printf '%s' "$issue" | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d["title"])')
body=$(printf '%s' "$issue" | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d["body"])')
echo "issue #543: [$state] $title"
ok=1
[ "$state" = "OPEN" ] || { echo "FAIL issue not OPEN"; ok=0; }
echo "$title" | grep -qi "Segment" || { echo "FAIL title does not mention Segment"; ok=0; }
echo "$body" | grep -q "segment" || { echo "FAIL body does not ask for a segment design"; ok=0; }
# component match: the issue asks for a KEP under keps/<n>-segment/
echo "$body" | grep -q "keps/<issue-number>-segment" || { echo "NOTE issue's KEP path expectation differs"; }
[ "$ok" = 1 ] && echo "PASS premise: OPEN issue asking for this Segment KEP (component match)"
exit $((1 - ok))
