#!/usr/bin/env bash
# re-verify.sh — re-run the doc-consistency harness against an updated PR head and
# report per finding Fixed / Still-broken. All findings are `contract` polarity:
# a check PASSES when the docs are fixed, FAILS while the defect is present.
#
# Usage (from a checkout of this verify branch):
#   bash docs/verification/stress-testing-docs/scripts/re-verify.sh [<fixed-ref>]
# With no <fixed-ref>, the current PR head is fetched via manifest.pr.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

VDIR="docs/verification/stress-testing-docs"
MANIFEST="$VDIR/verify-manifest.json"
FIXED_REF="${1:-}"
if [ -z "$FIXED_REF" ]; then
  PR_URL="$(sed -n 's/.*"pr":[[:space:]]*"\([^"]*\)".*/\1/p' "$MANIFEST" | head -1)"
  num="$(printf '%s' "$PR_URL" | sed -E 's#.*/pull/([0-9]+).*#\1#')"
  base="${PR_URL%/pull/*}"
  echo "re-verify: fetching PR head from ${base}.git pull/${num}/head"
  git fetch --quiet "${base}.git" "pull/${num}/head"
  FIXED_REF="$(git rev-parse FETCH_HEAD)"
fi
echo "re-verify: fixed-ref=$FIXED_REF"

LAST_REVIEWED="$(cat "$VDIR/.last-reviewed" 2>/dev/null || echo '')"
echo "re-verify: last-reviewed=${LAST_REVIEWED:-<none>} (delta ${LAST_REVIEWED:-base}..$FIXED_REF)"

ORIG_REF="$(git symbolic-ref --quiet --short HEAD || git rev-parse HEAD)"
HARNESS_SRC="$(git rev-parse HEAD)"
cleanup() { git checkout -f "$ORIG_REF" >/dev/null 2>&1 || true; }
trap cleanup EXIT

git checkout -f "$FIXED_REF" >/dev/null 2>&1
git checkout "$HARNESS_SRC" -- "$VDIR" >/dev/null 2>&1

overall=0
printf '\n================  RE-VERIFY  ================\n'
printf '%-4s %-9s %-14s %s\n' ID POLARITY VERDICT DETAIL
for id in F1 F2 F3 F5 F6 F7; do
  case "$id" in
    F1) s=f1-k8s-version;; F2) s=f2-go-version;; F3) s=f3-kwok-ready-timing;;
    F5) s=f5-operation5-note;; F6) s=f6-skill-builtin;; F7) s=f7-helm-args;;
  esac
  out="$(bash "$VDIR/checks/$s.sh" 2>&1)" && rc=0 || rc=$?
  line="$(printf '%s' "$out" | grep -E 'MISMATCH|REPRODUCED|MISSING' | head -1)"
  if [ "$rc" -eq 0 ]; then v=FIXED; else v=STILL-PRESENT; overall=1; fi
  printf '%-4s %-9s %-14s %s\n' "$id" contract "$v" "${line:-ok}"
done

printf '\nReview delta this round: %s..%s\n' "${LAST_REVIEWED:-base}" "$FIXED_REF"
printf 'Advance the marker: echo %s > %s/.last-reviewed && git add -A %s && git commit\n' \
  "$FIXED_REF" "$VDIR" "$VDIR"
[ "$overall" -eq 0 ] && { echo "RESULT: all findings fixed."; exit 0; } || { echo "RESULT: not all findings fixed."; exit 1; }
