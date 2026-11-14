#!/usr/bin/env bash
# re-verify.sh — re-run the PR #587 verification harness against an updated PR
# head and report, per finding, Fixed / Still-broken / Partial / Harness-update,
# applying test polarity. Adapted from the review-finding-verifier stock script;
# adds a "script" layer kind alongside "gotest".
#
# Drive it from a checkout of THIS branch (verify/pr-587-webhook-drain-codex).
# Usage: re-verify.sh [<fixed-ref>]   (no ref => fetch current PR head via manifest.pr)
# Requires: git, go, jq, python3 (+ pyyaml), helm (chart checks skip without it).
set -euo pipefail

FIXED_REF=""
if [ $# -ge 1 ]; then FIXED_REF="$1"; fi
command -v jq >/dev/null || { echo "jq is required" >&2; exit 2; }
git rev-parse --git-dir >/dev/null 2>&1 || { echo "not a git repo" >&2; exit 2; }
if ! git diff --quiet || ! git diff --cached --quiet; then
  echo "re-verify: dirty tree; commit or stash first (this script runs git checkout -f)" >&2; exit 2
fi

MANIFEST="$(ls docs/verification/*/verify-manifest.json 2>/dev/null | head -1 || true)"
[ -n "$MANIFEST" ] || { echo "manifest not found" >&2; exit 2; }
TOPIC_DIR="$(dirname "$MANIFEST")"
echo "re-verify: manifest = $MANIFEST"

RUNTIME_DIR="$(mktemp -d)"
cp "$MANIFEST" "$RUNTIME_DIR/manifest.json"
MANIFEST="$RUNTIME_DIR/manifest.json"

if [ -z "$FIXED_REF" ]; then
  PR_URL="$(jq -r '.pr // empty' "$MANIFEST")"
  rest="${PR_URL#*://}"; path="${rest#*/}"
  owner="$(printf '%s' "$path" | cut -d/ -f1)"; repo="$(printf '%s' "$path" | cut -d/ -f2)"
  num="$(printf '%s' "$path" | sed -E 's#.*/pull/([0-9]+).*#\1#')"
  git fetch --quiet "https://github.com/$owner/$repo.git" "pull/$num/head"
  FIXED_REF="$(git rev-parse FETCH_HEAD)"
  echo "re-verify: resolved PR head = $FIXED_REF"
fi

LAST_REVIEWED_FILE="$TOPIC_DIR/.last-reviewed"
if [ -s "$LAST_REVIEWED_FILE" ]; then LAST_REVIEWED="$(tr -d '[:space:]' < "$LAST_REVIEWED_FILE")";
else LAST_REVIEWED="$(git merge-base origin/main "$FIXED_REF" 2>/dev/null || echo '')"; fi
echo "re-verify: last-reviewed = ${LAST_REVIEWED:-<none>}  (review delta = ${LAST_REVIEWED:-base}..$FIXED_REF)"

RESULTS_DIR="$RUNTIME_DIR/results"; mkdir -p "$RESULTS_DIR"
ORIG_REF="$(git symbolic-ref --quiet --short HEAD || git rev-parse HEAD)"
HARNESS_SRC="$(git rev-parse HEAD)"
cleanup() { git checkout -f "$ORIG_REF" >/dev/null 2>&1 || true; rm -rf "$RUNTIME_DIR" 2>/dev/null || true; }
trap cleanup EXIT

echo "re-verify: fixed-ref=$FIXED_REF  harness-from=$ORIG_REF"
git fetch --quiet --all 2>/dev/null || true
git checkout -f "$FIXED_REF" >/dev/null 2>&1 || { echo "cannot checkout $FIXED_REF" >&2; exit 2; }

HPATHS=()
while IFS= read -r p; do [ -n "$p" ] && HPATHS+=("$p"); done < <(jq -r '.harnessPaths[]' "$MANIFEST")
git checkout "$HARNESS_SRC" -- "${HPATHS[@]}" 2>/dev/null || { echo "failed to graft harness" >&2; exit 2; }

run_layer() {
  local layer="$1" kind cmd out
  kind="$(jq -r --arg l "$layer" '.layers[$l].kind // empty' "$MANIFEST")"
  cmd="$(jq -r --arg l "$layer" '.layers[$l].cmd // empty' "$MANIFEST")"
  [ -n "$cmd" ] || return 0
  out="$RESULTS_DIR/${layer}.out"
  echo "re-verify: running layer '$layer' ($kind)" >&2
  if [ "$kind" = "gotest" ]; then
    bash -c "$cmd" >"$out" 2>&1 || true
    if grep -q '"Action":"build-fail"' "$out" || grep -q 'build failed' "$out"; then echo "__BUILD_OR_NO_TESTS__"; return 0; fi
    if ! grep -q '"Action":"\(pass\|fail\)"' "$out"; then echo "__BUILD_OR_NO_TESTS__"; return 0; fi
    jq -rs '[.[]|select(.Test!=null and (.Action=="pass" or .Action=="fail"))]|.[]|"\(.Test)\t\(.Action)"' "$out" 2>/dev/null || echo "__PARSE_ERROR__"
  elif [ "$kind" = "script" ]; then
    if bash -c "$cmd" >"$out" 2>&1; then echo "$layer	pass"; else echo "$layer	fail"; cat "$out"; fi
  fi
}

for L in unit integration; do run_layer "$L" > "$RESULTS_DIR/$L.parsed"; done

lookup() { local f="$RESULTS_DIR/$1.parsed"; [ -f "$f" ] || { echo missing; return; }
  grep -q '__BUILD_OR_NO_TESTS__' "$f" && { echo build; return; }
  local hit; hit="$(awk -F'\t' -v m="$2" 'index($1,m){print $2}' "$f" | tail -1)"
  [ -n "$hit" ] && echo "$hit" || echo missing; }

printf '\n================  RE-VERIFY  ================\n'
printf '%-6s %-9s %-12s %-14s %s\n' ID POLARITY LAYER VERDICT DETAIL
ALL_FIXED=1
N="$(jq '.findings|length' "$MANIFEST")"
for i in $(seq 0 $((N-1))); do
  id="$(jq -r ".findings[$i].id" "$MANIFEST")"; pol="$(jq -r ".findings[$i].polarity" "$MANIFEST")"; layer="$(jq -r ".findings[$i].layer" "$MANIFEST")"
  matches=(); while IFS= read -r m; do [ -n "$m" ] && matches+=("$m"); done < <(jq -r ".findings[$i].match[]" "$MANIFEST")
  pass=0; fail=0; miss=0; build=0
  for m in "${matches[@]}"; do r="$(lookup "$layer" "$m")"; case "$r" in pass) pass=$((pass+1));; fail) fail=$((fail+1));; build) build=$((build+1));; *) miss=$((miss+1));; esac; done
  if [ "$build" -gt 0 ] || [ "$miss" -gt 0 ]; then verdict="HARNESS-UPDATE"; detail="pass=$pass fail=$fail miss=$miss build=$build"; ALL_FIXED=0
  elif [ "$pol" = "contract" ]; then
    if [ "$fail" -eq 0 ]; then verdict="FIXED"; elif [ "$pass" -eq 0 ]; then verdict="STILL-BROKEN"; ALL_FIXED=0; else verdict="PARTIAL"; ALL_FIXED=0; fi
  else
    if [ "$pass" -eq 0 ]; then verdict="FIXED(flipped)"; elif [ "$fail" -eq 0 ]; then verdict="STILL-PRESENT"; ALL_FIXED=0; else verdict="PARTIAL"; ALL_FIXED=0; fi
  fi
  printf '%-6s %-9s %-12s %-14s %s\n' "$id" "$pol" "$layer" "$verdict" "${detail:-pass=$pass fail=$fail}"
done
echo "layers output: $RESULTS_DIR (removed on exit; re-run with bash -x to inspect)"
[ "$ALL_FIXED" -eq 1 ]
