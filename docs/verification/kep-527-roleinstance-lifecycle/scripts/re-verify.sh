#!/usr/bin/env bash
# re-verify.sh (shell-kind adaptation of the review-finding-verifier stock script)
# for the docs-only KEP-527 PR #546 harness.
#
# Same contract as the stock script: resolve the current PR head from manifest.pr
# (machine-independent), graft the harness onto it, run the checks, print a
# per-finding Fixed / Still-broken / Partial / Harness-update table honoring
# polarity (all findings here are contract polarity), restore the tree.
#
# Usage: bash scripts/re-verify.sh [<fixed-ref>] [--manifest <path>]
set -euo pipefail

FIXED_REF=""; MANIFEST=""
while [ $# -gt 0 ]; do
  case "$1" in
    --manifest) MANIFEST="$2"; shift 2;;
    -h|--help) sed -n '2,12p' "$0"; exit 0;;
    *) [ -z "$FIXED_REF" ] && FIXED_REF="$1" && shift || { echo "unexpected arg: $1" >&2; exit 2; };;
  esac
done
command -v jq >/dev/null || { echo "re-verify: jq is required" >&2; exit 2; }
git rev-parse --git-dir >/dev/null 2>&1 || { echo "re-verify: not a git repo" >&2; exit 2; }
if ! git diff --quiet || ! git diff --cached --quiet; then
  echo "re-verify: working tree has uncommitted tracked changes; commit or stash first" >&2
  exit 2
fi

if [ -z "$MANIFEST" ]; then
  MANIFEST="$(ls docs/verification/*/verify-manifest.json 2>/dev/null | head -1 || true)"
  [ -n "$MANIFEST" ] || MANIFEST="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/verify-manifest.json"
fi
[ -f "$MANIFEST" ] || { echo "re-verify: manifest not found" >&2; exit 2; }
MANIFEST_ORIG="$MANIFEST"
RUNTIME_DIR="$(mktemp -d)"; cp "$MANIFEST" "$RUNTIME_DIR/manifest.json"; MANIFEST="$RUNTIME_DIR/manifest.json"

if [ -z "$FIXED_REF" ]; then
  PR_URL="$(jq -r '.pr // empty' "$MANIFEST")"
  if [ -n "$PR_URL" ]; then
    rest="${PR_URL#*://}"; host="${rest%%/*}"; path="${rest#*/}"
    owner="$(printf '%s' "$path" | cut -d/ -f1)"; repo="$(printf '%s' "$path" | cut -d/ -f2)"
    num="$(printf '%s' "$path" | sed -E 's#.*/pull/([0-9]+).*#\1#')"
    git fetch --quiet "https://$host/$owner/$repo.git" "pull/$num/head" || { echo "re-verify: fetch failed" >&2; exit 2; }
    FIXED_REF="$(git rev-parse FETCH_HEAD)"
  else
    echo "usage: re-verify.sh <fixed-ref> (or set manifest.pr)" >&2; exit 2
  fi
fi
echo "re-verify: head = $FIXED_REF"

LAST_REVIEWED_FILE="$(dirname "$MANIFEST_ORIG")/.last-reviewed"
LAST_REVIEWED=""
[ -s "$LAST_REVIEWED_FILE" ] && LAST_REVIEWED="$(tr -d '[:space:]' < "$LAST_REVIEWED_FILE")"
[ -n "${LAST_REVIEWED:-}" ] || LAST_REVIEWED="$(git merge-base HEAD "$FIXED_REF" 2>/dev/null || echo base)"
echo "re-verify: last-reviewed = $LAST_REVIEWED (review delta = $LAST_REVIEWED..$FIXED_REF)"

ORIG_REF="$(git symbolic-ref --quiet --short HEAD || git rev-parse HEAD)"
HARNESS_SRC="$(git rev-parse HEAD)"
PERSIST=""; OUT=""
cleanup() {
  # Restore the branch first, THEN persist the fresh results: "git checkout -f"
  # restores tracked files under docs/verification/, so copying before the
  # checkout leaves stale content in the branch worktree (round-4 bug).
  git checkout -f "$ORIG_REF" >/dev/null 2>&1 || true
  if [ -n "$PERSIST" ] && [ -f "$OUT" ]; then
    mkdir -p "$PERSIST" 2>/dev/null && cp -f "$OUT" "$PERSIST/unit.tsv" 2>/dev/null || true
  fi
  rm -rf "$RUNTIME_DIR" 2>/dev/null || true
}
trap cleanup EXIT

git checkout -f "$FIXED_REF" >/dev/null 2>&1 || { echo "re-verify: cannot checkout $FIXED_REF" >&2; exit 2; }
HPATHS=()
while IFS= read -r p; do [ -n "$p" ] && HPATHS+=("$p"); done < <(jq -r '.harnessPaths[]' "$MANIFEST")
git checkout "$HARNESS_SRC" -- "${HPATHS[@]}" 2>/dev/null || { echo "re-verify: failed to graft harness" >&2; exit 2; }

CMD="$(jq -r '.layers.unit.cmd' "$MANIFEST")"
OUT="$RUNTIME_DIR/unit.out"
bash -c "$CMD" >"$OUT" 2>&1 || true

PERSIST="$(dirname "$MANIFEST_ORIG")/results/reverify"

lookup() { # $1 = match substring -> pass|fail|skip|missing
  local hit; hit="$(awk -F'\t' -v m="$1" 'index($1,m){print $2}' "$OUT" | tail -1)"
  [ -n "$hit" ] && echo "$hit" || echo "missing"
}

printf '\n================  RE-VERIFY  ================\n'
printf '%-6s %-9s %-12s %-16s %s\n' ID POLARITY LAYER VERDICT DETAIL
ALL_FIXED=1
N="$(jq '.findings|length' "$MANIFEST")"
for i in $(seq 0 $((N-1))); do
  id="$(jq -r ".findings[$i].id" "$MANIFEST")"; pol="$(jq -r ".findings[$i].polarity" "$MANIFEST")"
  matches=(); while IFS= read -r m; do [ -n "$m" ] && matches+=("$m"); done < <(jq -r ".findings[$i].match[]" "$MANIFEST")
  pass=0; fail=0; miss=0; skipn=0
  for m in "${matches[@]}"; do
    r="$(lookup "$m")"
    case "$r" in pass) pass=$((pass+1));; fail) fail=$((fail+1));; skip) skipn=$((skipn+1));; *) miss=$((miss+1));; esac
  done
  detail="pass=$pass fail=$fail miss=$miss skip=$skipn"; verdict=""
  if [ "$miss" -gt 0 ]; then verdict="HARNESS-UPDATE"; ALL_FIXED=0
  elif [ "$pol" = "contract" ]; then
    if [ "$fail" -eq 0 ]; then verdict="FIXED"; elif [ "$pass" -eq 0 ]; then verdict="STILL-BROKEN"; ALL_FIXED=0; else verdict="PARTIAL"; ALL_FIXED=0; fi
  else
    if [ "$pass" -eq 0 ]; then verdict="FIXED(flipped)"; elif [ "$fail" -eq 0 ]; then verdict="STILL-PRESENT"; ALL_FIXED=0; else verdict="PARTIAL"; ALL_FIXED=0; fi
  fi
  printf '%-6s %-9s %-12s %-16s %s\n' "$id" "$pol" unit "$verdict" "$detail"
done
LIVE="$(jq -r '.liveNote // empty' "$MANIFEST")"
[ -n "$LIVE" ] && printf '\nLive layer (manual): %s\n' "$LIVE"
printf '\nCodebase-truth claims (must stay pass; a fail = Harness-update against moved code):\n'
grep -E '^Check(CustomReady|ReadinessGate|RIReadiness|RISLifecycle|ExecutorRegistration|PodOwnedByRI|RINoFinalizer|RINoPhaseField|AdmissionWebhook|APIGroupVersion)' "$OUT" || true
printf '\nReview delta this round: %s..%s\n' "$LAST_REVIEWED" "$FIXED_REF"
printf 'Advance the marker after review: echo %s > %s\n' "$FIXED_REF" "$LAST_REVIEWED_FILE"
[ "$ALL_FIXED" -eq 1 ] && { echo "RESULT: all findings fixed."; exit 0; } || { echo "RESULT: not all findings fixed."; exit 1; }
