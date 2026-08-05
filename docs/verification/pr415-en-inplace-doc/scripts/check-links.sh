#!/usr/bin/env bash
# check-links.sh -- relative-link resolver for doc/best-practice.
#
# Part of the verification harness for PR #415 (sgl-project/rbg), finding F5
# (and the mechanism behind F2).
# POLARITY: contract -- must PASS. Goes RED on the first dead relative link.
#
# For every markdown file under doc/best-practice/, extract inline links
# [text](target) and verify that every RELATIVE target resolves on disk. A target
# with an #anchor is resolved on its path part; a pure "#anchor" link is checked
# against the headings of its own file. Absolute http(s):/mailto: targets are
# counted but not fetched (no network dependency -- this must run in CI offline).
#
# Usage:  check-links.sh [repo-root] [--only <substr>]
# Exit:   0 = every relative link resolves; 1 = dead link(s); 2 = usage error.
set -uo pipefail

ROOT=""; ONLY=""
while [ $# -gt 0 ]; do
  case "$1" in
    --only) ONLY="${2:-}"; shift 2;;
    -h|--help) sed -n '2,17p' "$0"; exit 0;;
    *) [ -z "$ROOT" ] && ROOT="$1" && shift || { echo "unexpected arg: $1" >&2; exit 2; };;
  esac
done
if [ -z "$ROOT" ]; then
  ROOT="$(git -C "$(dirname "${BASH_SOURCE[0]}")" rev-parse --show-toplevel 2>/dev/null)" || {
    echo "usage: $0 [repo-root] [--only <substr>]" >&2; exit 2; }
fi
BP="$ROOT/doc/best-practice"
[ -d "$BP" ] || { echo "check-links: missing $BP" >&2; exit 2; }

# slugify a heading the GitHub way (lowercase, strip punctuation, spaces->dashes).
slug() {
  printf '%s' "$1" \
    | tr '[:upper:]' '[:lower:]' \
    | sed -e 's/[^a-z0-9 _-]//g' -e 's/ /-/g'
}

RC=0
NFILES=0; NREL=0; NABS=0; NANCHOR=0; NDEAD=0

while IFS= read -r mdfile; do
  base="$(basename "$mdfile")"
  if [ -n "$ONLY" ] && [ "${base#*$ONLY}" = "$base" ]; then continue; fi
  NFILES=$((NFILES+1))
  reldir="$(dirname "$mdfile")"
  shown="${mdfile#$ROOT/}"

  # emit "linenumber<TAB>target" for each inline link, skipping fenced blocks
  while IFS=$'\t' read -r lineno target; do
    [ -n "$target" ] || continue
    case "$target" in
      http://*|https://*|mailto:*) NABS=$((NABS+1)); continue;;
      '#'*)
        NANCHOR=$((NANCHOR+1))
        want="${target#\#}"
        found=0
        while IFS= read -r h; do
          [ "$(slug "$h")" = "$want" ] && { found=1; break; }
        done < <(sed -n 's/^#\{1,6\}[ \t]\+//p' "$mdfile")
        if [ "$found" -eq 0 ]; then
          echo "DEAD anchor: $shown:$lineno -> $target (no matching heading in this file)"
          RC=1; NDEAD=$((NDEAD+1))
        fi
        continue;;
    esac
    NREL=$((NREL+1))
    path="${target%%#*}"
    [ -n "$path" ] || continue
    if [ ! -e "$reldir/$path" ]; then
      echo "DEAD link: $shown:$lineno -> $target (resolves to $reldir/$path)"
      RC=1; NDEAD=$((NDEAD+1))
    fi
  done < <(awk '
    /^[ \t]*(```|~~~)/ { inblock = 1 - inblock; next }
    inblock == 1 { next }
    {
      line = $0
      while (match(line, /\[[^]]*\]\([^)]+\)/)) {
        m = substr(line, RSTART, RLENGTH)
        t = m; sub(/^\[[^]]*\]\(/, "", t); sub(/\)$/, "", t)
        # strip an optional link title:  (target "Title")
        sub(/[ \t]+"[^"]*"$/, "", t)
        gsub(/^[ \t]+|[ \t]+$/, "", t)
        printf "%d\t%s\n", NR, t
        line = substr(line, RSTART + RLENGTH)
      }
    }
  ' "$mdfile")
done < <(find "$BP" -name '*.md' | sort)

echo
echo "--- link check summary ---"
[ -n "$ONLY" ] && echo "filter (--only)     : $ONLY"
echo "markdown files      : $NFILES"
echo "relative links      : $NREL"
echo "in-page anchors     : $NANCHOR"
echo "absolute links      : $NABS (not fetched -- offline check)"
echo "dead                : $NDEAD"
if [ "$RC" -eq 0 ]; then echo "RESULT: PASS"; else echo "RESULT: FAIL (dead relative link(s))"; fi
exit $RC
