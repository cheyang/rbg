#!/usr/bin/env bash
# re-verify.sh — re-run the PR #390 docs verification harness against updated code.
#
# Adapted from the review-finding-verifier skill's re-verify.sh for a docs-PR
# harness: layers are shell scripts whose output lines are `CHECK <id>: PASS|FAIL`,
# not gotest/ginkgo reports. Polarity is honored the same way: every finding here
# is a CONTRACT check (docs must match the code they document), so Fixed == all
# its CHECK lines PASS.
#
# Usage (from a checkout holding this harness, e.g. verify/stress-testing-tuning-doc-claude):
#   re-verify.sh [fixed-ref]
#     no ref  -> fetches the current PR head from manifest.pr (machine-independent)
#     ref     -> the updated code to check the docs against
#
# Requires: git, python3, helm, go, jq (jq only for manifest parsing).
# Exit 0 iff every finding is Fixed.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MANIFEST="$HERE/../verify-manifest.json"
FIXED_REF="${1:-}"

command -v jq >/dev/null || { echo "re-verify: jq is required" >&2; exit 2; }
if ! git diff --quiet || ! git diff --cached --quiet; then
  echo "re-verify: working tree has uncommitted tracked changes; commit or stash first" >&2
  exit 2
fi

# resolve current PR head when no ref given
if [ -z "$FIXED_REF" ]; then
  PR_URL="$(jq -r '.pr // empty' "$MANIFEST")"
  rest="${PR_URL#*://}"; host="${rest%%/*}"; path="${rest#*/}"
  owner="$(printf '%s' "$path" | cut -d/ -f1)"
  repo="$(printf '%s' "$path" | cut -d/ -f2)"
  num="$(printf '%s' "$path" | sed -E 's#.*/pull/([0-9]+).*#\1#')"
  [ -n "$owner" ] && [ -n "$repo" ] && [ -n "$num" ] || { echo "re-verify: bad pr URL $PR_URL" >&2; exit 2; }
  git fetch --quiet "https://$host/$owner/$repo.git" "pull/$num/head"
  FIXED_REF="$(git rev-parse FETCH_HEAD)"
fi
echo "re-verify: fixed-ref = $FIXED_REF"

# last-reviewed marker for the incremental review delta
LAST_REVIEWED_FILE="$HERE/../.last-reviewed"
LAST_REVIEWED="$(tr -d '[:space:]' < "$LAST_REVIEWED_FILE" 2>/dev/null || true)"
echo "re-verify: last-reviewed = ${LAST_REVIEWED:-<none>}  (delta = ${LAST_REVIEWED:-base}..$FIXED_REF)"

ORIG_REF="$(git symbolic-ref --quiet --short HEAD || git rev-parse HEAD)"
HARNESS_SRC="$(git rev-parse HEAD)"
RUNTIME_DIR="$(mktemp -d)"
# The manifest must survive `git checkout -f` (the fixed ref has no harness),
# so work from a copy outside the repo.
cp "$MANIFEST" "$RUNTIME_DIR/manifest.json"
MANIFEST="$RUNTIME_DIR/manifest.json"
cleanup() { git checkout -f "$ORIG_REF" >/dev/null 2>&1 || true; rm -rf "$RUNTIME_DIR" 2>/dev/null || true; }
trap cleanup EXIT

git fetch --quiet origin main 2>/dev/null || true   # F1 compares docs vs merge target
git checkout -f "$FIXED_REF" >/dev/null 2>&1 || { echo "re-verify: cannot checkout $FIXED_REF" >&2; exit 2; }
# graft harness onto the fixed code (pathspecs only — never a commit switch)
HPATHS=()
while IFS= read -r p; do [ -n "$p" ] && HPATHS+=("$p"); done < <(jq -r '.harnessPaths[]' "$MANIFEST")
[ "${#HPATHS[@]}" -gt 0 ] || { echo "re-verify: harnessPaths empty in manifest" >&2; exit 2; }
git checkout "$HARNESS_SRC" -- "${HPATHS[@]}" || { echo "re-verify: failed to graft harness" >&2; exit 2; }

echo; echo "re-verify: running L1 (static) + L2 (integration)"
L1LOG="$RUNTIME_DIR/l1.log"; L2LOG="$RUNTIME_DIR/l2.log"
bash "$HERE/10-static-checks.sh" >"$L1LOG" 2>&1 || true
bash "$HERE/20-integration-checks.sh" >"$L2LOG" 2>&1 || true

printf '\n================  RE-VERIFY  ================\n'
printf '%-6s %-9s %-12s %-14s %s\n' ID POLARITY LAYER VERDICT DETAIL
ALL_FIXED=1
N="$(jq '.findings|length' "$MANIFEST")"
for i in $(seq 0 $((N-1))); do
  id="$(jq -r ".findings[$i].id" "$MANIFEST")"
  pol="$(jq -r ".findings[$i].polarity" "$MANIFEST")"
  layer="$(jq -r ".findings[$i].layer" "$MANIFEST")"
  log="$L1LOG"; [ "$layer" = "integration" ] && log="$L2LOG"
  pass=0; fail=0; miss=0
  while IFS= read -r m; do
    [ -n "$m" ] || continue
    r="$(grep -F "CHECK ${m}: " "$log" | tail -1 | sed -E 's/^CHECK [^:]+: (PASS|FAIL).*/\1/')"
    case "$r" in PASS) pass=$((pass+1));; FAIL) fail=$((fail+1));; *) miss=$((miss+1));; esac
  done < <(jq -r ".findings[$i].match[]" "$MANIFEST")
  if [ "$miss" -gt 0 ]; then
    verdict="HARNESS-UPDATE"; detail="check not found: pass=$pass fail=$fail miss=$miss"; ALL_FIXED=0
  elif [ "$pol" = "contract" ]; then
    if [ "$fail" -eq 0 ]; then verdict="FIXED"; else verdict="STILL-BROKEN"; ALL_FIXED=0; fi
    detail="pass=$pass fail=$fail"
  else
    if [ "$pass" -eq 0 ]; then verdict="FIXED(flipped)"; else verdict="STILL-PRESENT"; ALL_FIXED=0; fi
    detail="pass=$pass fail=$fail"
  fi
  printf '%-6s %-9s %-12s %-14s %s\n' "$id" "$pol" "$layer" "$verdict" "$detail"
done

# persist raw output into the in-repo results dir (survives the cleanup checkout)
PERSIST="$HERE/../results/reverify"
mkdir -p "$PERSIST" && cp -f "$L1LOG" "$L2LOG" "$PERSIST"/ 2>/dev/null || true
echo; echo "Raw output: $PERSIST/"
echo "Review delta this round: ${LAST_REVIEWED:-base}..$FIXED_REF"
echo "After reviewing that delta, advance the marker:"
echo "  echo $FIXED_REF > $LAST_REVIEWED_FILE && git add $LAST_REVIEWED_FILE && git commit -m 'review: advance last-reviewed'"
[ "$ALL_FIXED" -eq 1 ] && { echo "RESULT: all findings fixed."; exit 0; } || { echo "RESULT: not all findings fixed."; exit 1; }
