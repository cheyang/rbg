#!/usr/bin/env bash
# check-heading-parity.sh -- structural parity guard for doc/best-practice/{zh,en}.
#
# Part of the verification harness for PR #415 (sgl-project/rbg), finding F5.
# POLARITY: contract -- must PASS on a faithful translation. Goes RED if a
# translated document gains, loses, or re-nests a section relative to its zh
# source, which is how translated docs silently drift out of sync.
#
# Compares the ATX heading LEVEL SEQUENCE (the "#" depths, in document order).
# Heading TEXT is intentionally not compared -- it is prose and is translated.
# Also enforced, because they are structural rather than linguistic:
#   * table row count per document (a dropped parameter row is a real defect)
#   * top-level bullet count per document
#
# Generic over the whole tree: any file present in both zh/ and en/ is compared;
# one-sided files are SKIPPED and reported (en legitimately lags -- PR #400 adds
# 07, PR #402 adds 05).
#
# Usage:  check-heading-parity.sh [repo-root] [--only <substr>]
# Exit:   0 = structural parity; 1 = drift; 2 = usage error.
set -uo pipefail

ROOT=""; ONLY=""
while [ $# -gt 0 ]; do
  case "$1" in
    --only) ONLY="${2:-}"; shift 2;;
    -h|--help) sed -n '2,22p' "$0"; exit 0;;
    *) [ -z "$ROOT" ] && ROOT="$1" && shift || { echo "unexpected arg: $1" >&2; exit 2; };;
  esac
done
if [ -z "$ROOT" ]; then
  ROOT="$(git -C "$(dirname "${BASH_SOURCE[0]}")" rev-parse --show-toplevel 2>/dev/null)" || {
    echo "usage: $0 [repo-root] [--only <substr>]" >&2; exit 2; }
fi
ZH="$ROOT/doc/best-practice/zh"
EN="$ROOT/doc/best-practice/en"
[ -d "$ZH" ] && [ -d "$EN" ] || { echo "check-heading-parity: missing $ZH or $EN" >&2; exit 2; }

TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT

# headings <file> -> "level<TAB>linenumber" per ATX heading OUTSIDE fenced blocks
# (a '#' inside a bash block is a comment, not a heading).
headings() {
  awk '
    /^[ \t]*(```|~~~)/ { inblock = 1 - inblock; next }
    inblock == 1 { next }
    /^#{1,6}[ \t]/ {
      lvl = 0
      while (substr($0, lvl + 1, 1) == "#") lvl++
      printf "%d\t%d\n", lvl, NR
    }
  ' "$1"
}

# table_rows <file> -> count of markdown table body rows (lines starting with |)
# outside fenced blocks, excluding the separator row.
table_rows() {
  awk '
    /^[ \t]*(```|~~~)/ { inblock = 1 - inblock; next }
    inblock == 1 { next }
    /^[ \t]*\|/ {
      if ($0 ~ /^[ \t]*\|[ \t:|-]+\|[ \t]*$/) next   # separator row
      n++
    }
    END { print n + 0 }
  ' "$1"
}

# bullets <file> -> count of top-level list items outside fenced blocks
bullets() {
  awk '
    /^[ \t]*(```|~~~)/ { inblock = 1 - inblock; next }
    inblock == 1 { next }
    /^([-+*]|[0-9]+\.)[ \t]/ { n++ }
    END { print n + 0 }
  ' "$1"
}

RC=0
SHARED=0; MATCHED=0; SKIPPED=0
declare -a SKIPPED_FILES=()

for zhfile in "$ZH"/*.md; do
  [ -e "$zhfile" ] || continue
  base="$(basename "$zhfile")"
  if [ -n "$ONLY" ] && [ "${base#*$ONLY}" = "$base" ]; then continue; fi
  enfile="$EN/$base"
  if [ ! -f "$enfile" ]; then
    SKIPPED=$((SKIPPED+1)); SKIPPED_FILES+=("en/$base (missing -- translation not yet contributed)")
    continue
  fi
  SHARED=$((SHARED+1))
  headings "$zhfile" > "$TMP/zh.h"
  headings "$enfile" > "$TMP/en.h"
  cut -f1 "$TMP/zh.h" > "$TMP/zh.lvl"
  cut -f1 "$TMP/en.h" > "$TMP/en.lvl"
  zc=$(wc -l < "$TMP/zh.h" | tr -d ' ')
  ec=$(wc -l < "$TMP/en.h" | tr -d ' ')

  filefail=0
  if [ "$zc" != "$ec" ]; then
    echo "FAIL $base: heading COUNT differs -- zh has $zc, en has $ec"
    echo "     doc/best-practice/zh/$base:1"
    echo "     doc/best-practice/en/$base:1"
    RC=1; filefail=1
  elif ! diff -q "$TMP/zh.lvl" "$TMP/en.lvl" >/dev/null 2>&1; then
    echo "FAIL $base: heading LEVEL sequence differs (section nesting drifted)"
    # point at the first divergent heading in both files
    idx=$(diff "$TMP/zh.lvl" "$TMP/en.lvl" | awk -F'[c,d a]' '/^[0-9]/{print $1; exit}')
    [ -n "${idx:-}" ] || idx=1
    zl=$(awk -v k="$idx" 'NR==k{print $2}' "$TMP/zh.h")
    el=$(awk -v k="$idx" 'NR==k{print $2}' "$TMP/en.h")
    echo "     first divergence at heading #$idx:"
    echo "     doc/best-practice/zh/$base:${zl:-?}"
    echo "     doc/best-practice/en/$base:${el:-?}"
    RC=1; filefail=1
  fi

  ztr=$(table_rows "$zhfile"); etr=$(table_rows "$enfile")
  if [ "$ztr" != "$etr" ]; then
    echo "FAIL $base: markdown table ROW count differs -- zh=$ztr en=$etr"
    echo "     doc/best-practice/zh/$base:1"
    echo "     doc/best-practice/en/$base:1"
    RC=1; filefail=1
  fi

  zb=$(bullets "$zhfile"); eb=$(bullets "$enfile")
  if [ "$zb" != "$eb" ]; then
    echo "FAIL $base: top-level bullet count differs -- zh=$zb en=$eb"
    echo "     doc/best-practice/zh/$base:1"
    echo "     doc/best-practice/en/$base:1"
    RC=1; filefail=1
  fi

  if [ "$filefail" -eq 0 ]; then
    echo "ok   $base: headings $zc/$ec level-aligned, table rows $ztr/$etr, bullets $zb/$eb"
    MATCHED=$((MATCHED+1))
  fi
done

for enfile in "$EN"/*.md; do
  [ -e "$enfile" ] || continue
  base="$(basename "$enfile")"
  if [ -n "$ONLY" ] && [ "${base#*$ONLY}" = "$base" ]; then continue; fi
  [ -f "$ZH/$base" ] || { SKIPPED=$((SKIPPED+1)); SKIPPED_FILES+=("zh/$base (missing)"); }
done

echo
echo "--- heading/structure parity summary ---"
[ -n "$ONLY" ] && echo "filter (--only)          : $ONLY"
echo "shared files compared    : $SHARED"
echo "files structurally equal : $MATCHED"
echo "files skipped (one-sided): $SKIPPED"
for s in "${SKIPPED_FILES[@]+"${SKIPPED_FILES[@]}"}"; do echo "  skip: $s"; done
if [ "$RC" -eq 0 ]; then echo "RESULT: PASS"; else echo "RESULT: FAIL (zh/en structural drift)"; fi
exit $RC
