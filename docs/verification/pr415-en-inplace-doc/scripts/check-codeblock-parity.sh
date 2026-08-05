#!/usr/bin/env bash
# check-codeblock-parity.sh -- translation-fidelity regression guard for
# doc/best-practice/{zh,en}.
#
# Part of the verification harness for PR #415 (sgl-project/rbg), finding F5.
# POLARITY: contract -- must PASS on a faithful translation; goes RED as soon as a
# fenced code block's EXECUTABLE content drifts between the zh original and the en
# translation.
#
# Why this matters: these documents are operational runbooks. Prose is translated,
# but every command, YAML key/value, flag and sample output MUST stay identical,
# or a reader following the en copy runs different commands than the zh copy
# documents.
#
# What counts as a legitimate translated difference (normalized away):
#   * a whole-line comment  (`# ...`, `// ...`)
#   * a TRAILING comment    (`gracePeriodSeconds: 30  # wait 30s`)
#   * the prose inside a ```plain fence. In doc/best-practice, ```plain is used
#     exclusively for ASCII-art diagrams and step-flow sketches (22 occurrences,
#     verified), whose wording IS meant to be translated. For those blocks only
#     the LINE COUNT is enforced, so the drawing's shape must still line up.
# Everything else is compared byte-for-byte.
#
# Generic over the whole doc/best-practice tree: for every file present in BOTH
# zh/ and en/, extract all fenced blocks and compare. Files present on only one
# side are SKIPPED and reported (the en set legitimately lags: PR #400 adds 07,
# PR #402 adds 05).
#
# Usage:  check-codeblock-parity.sh [repo-root] [--only <substr>]
#   --only <substr>  restrict to files whose basename contains <substr>. Use
#                    `--only 04-configuring-inplace` to check just the pair added
#                    by PR #415, separating its (clean) baseline from pre-existing
#                    drift elsewhere in the tree.
# Exit:   0 = all checked files parity-clean; 1 = drift found; 2 = usage error.
set -uo pipefail

ROOT=""; ONLY=""
while [ $# -gt 0 ]; do
  case "$1" in
    --only) ONLY="${2:-}"; shift 2;;
    -h|--help) sed -n '2,40p' "$0"; exit 0;;
    *) [ -z "$ROOT" ] && ROOT="$1" && shift || { echo "unexpected arg: $1" >&2; exit 2; };;
  esac
done
if [ -z "$ROOT" ]; then
  ROOT="$(git -C "$(dirname "${BASH_SOURCE[0]}")" rev-parse --show-toplevel 2>/dev/null)" || {
    echo "usage: $0 [repo-root]" >&2; exit 2; }
fi
ZH="$ROOT/doc/best-practice/zh"
EN="$ROOT/doc/best-practice/en"
[ -d "$ZH" ] && [ -d "$EN" ] || { echo "check-codeblock-parity: missing $ZH or $EN" >&2; exit 2; }

TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT

# extract_blocks <file> <outdir>
# Per fenced block writes:
#   <outdir>/NNN.raw    verbatim body
#   <outdir>/NNN.norm   comment-normalized body (what we diff for code blocks)
#   <outdir>/index      "NNN<TAB>startline<TAB>fenceinfo<TAB>kind"
# kind = "code" | "prose". "prose" = a ```plain fence, this doc set's convention
# for an ASCII-art diagram; such a block is prose, so only its LINE COUNT is
# enforced, not its words.
extract_blocks() {
  awk -v outdir="$2" '
    function flush_block() {
      # A fence tagged "plain" is the doc-set convention for an ASCII-art
      # diagram / step-flow sketch: its words are prose and ARE expected to be
      # translated, so only its structure is enforced. Every other fence (bash,
      # yaml, or untagged sample output) is code, compared byte-for-byte.
      tag = tolower(info)
      if (tag == "plain" || tag == "text" || tag == "txt") kind = "prose"
      else kind = "code"
      printf "%03d\t%d\t%s\t%s\n", n, start, info, kind >> (outdir "/index")
      for (i = 1; i <= bn; i++) {
        print braw[i] >> sprintf("%s/%03d.raw",  outdir, n)
        print bnorm[i] >> sprintf("%s/%03d.norm", outdir, n)
      }
      # ensure the files exist even for an empty block
      if (bn == 0) {
        printf "" >> sprintf("%s/%03d.raw",  outdir, n)
        printf "" >> sprintf("%s/%03d.norm", outdir, n)
      }
    }
    BEGIN { n = 0; inblock = 0; bn = 0 }
    {
      line = $0
      if (line ~ /^[ \t]*(```|~~~)/) {
        if (inblock == 0) {
          inblock = 1; n++; start = NR; bn = 0
          info = line; sub(/^[ \t]*(```|~~~)/, "", info); gsub(/[ \t]+$/, "", info)
          next
        } else {
          flush_block(); inblock = 0; next
        }
      }
      if (inblock == 1) {
        bn++
        braw[bn] = line
        probe = line; sub(/^[ \t]+/, "", probe)
        if (substr(probe, 1, 1) == "#" || substr(probe, 1, 2) == "//") {
          # whole-line comment: prose, collapse to a positional token
          bnorm[bn] = "@@COMMENT@@"
        } else {
          norm = line
          # strip a trailing comment: whitespace + # + rest of line.
          # (safe for the shell/yaml/bash content in these runbooks)
          if (norm ~ /[ \t]#/) { sub(/[ \t]+#.*$/, " @@TRAILCOMMENT@@", norm) }
          gsub(/[ \t]+$/, "", norm)
          bnorm[bn] = norm
        }
      }
    }
    END { if (inblock == 1) flush_block() }
  ' "$1"
}

RC=0
SHARED=0; SKIPPED=0; MATCHED=0
CODE_CMP=0; PROSE_CMP=0
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
  zd="$TMP/zh-$base"; ed="$TMP/en-$base"
  mkdir -p "$zd" "$ed"; : > "$zd/index"; : > "$ed/index"
  extract_blocks "$zhfile" "$zd"
  extract_blocks "$enfile" "$ed"

  zn=$(wc -l < "$zd/index" | tr -d ' ')
  en=$(wc -l < "$ed/index" | tr -d ' ')
  if [ "$zn" != "$en" ]; then
    echo "FAIL $base: fenced block COUNT differs -- zh has $zn, en has $en"
    echo "     doc/best-practice/zh/$base:1"
    echo "     doc/best-practice/en/$base:1"
    RC=1
    continue
  fi

  filefail=0 ncode=0 nprose=0
  for i in $(seq -f "%03g" 1 "$zn"); do
    zline=$(awk -F'\t' -v k="$i" '$1==k{print $2}' "$zd/index")
    eline=$(awk -F'\t' -v k="$i" '$1==k{print $2}' "$ed/index")
    zinfo=$(awk -F'\t' -v k="$i" '$1==k{print $3}' "$zd/index")
    einfo=$(awk -F'\t' -v k="$i" '$1==k{print $3}' "$ed/index")
    zkind=$(awk -F'\t' -v k="$i" '$1==k{print $4}' "$zd/index")
    ekind=$(awk -F'\t' -v k="$i" '$1==k{print $4}' "$ed/index")
    for f in "$zd/$i.raw" "$zd/$i.norm" "$ed/$i.raw" "$ed/$i.norm"; do
      [ -f "$f" ] || : > "$f"
    done

    if [ "$zinfo" != "$einfo" ]; then
      echo "FAIL $base block #$i: fence language tag differs: zh=\"$zinfo\" en=\"$einfo\""
      echo "     doc/best-practice/zh/$base:$zline"
      echo "     doc/best-practice/en/$base:$eline"
      RC=1; filefail=1
    fi

    if [ "$zkind" = "code" ] || [ "$ekind" = "code" ]; then
      ncode=$((ncode+1)); CODE_CMP=$((CODE_CMP+1))
      if ! diff -q "$zd/$i.norm" "$ed/$i.norm" >/dev/null 2>&1; then
        echo "FAIL $base block #$i: CODE content drifted between zh and en"
        echo "     doc/best-practice/zh/$base:$zline"
        echo "     doc/best-practice/en/$base:$eline"
        diff -u "$zd/$i.norm" "$ed/$i.norm" \
          | tail -n +3 | sed 's/^/       /'
        RC=1; filefail=1
      fi
    else
      # prose/diagram block: enforce structure (line count) only
      nprose=$((nprose+1)); PROSE_CMP=$((PROSE_CMP+1))
      zl=$(wc -l < "$zd/$i.raw" | tr -d ' ')
      el=$(wc -l < "$ed/$i.raw" | tr -d ' ')
      if [ "$zl" != "$el" ]; then
        echo "FAIL $base block #$i: diagram/prose block LINE COUNT differs (zh=$zl en=$el)"
        echo "     doc/best-practice/zh/$base:$zline"
        echo "     doc/best-practice/en/$base:$eline"
        RC=1; filefail=1
      fi
    fi
  done
  if [ "$filefail" -eq 0 ]; then
    echo "ok   $base: $zn blocks parity-clean ($ncode code byte-identical, $nprose diagram structure-identical)"
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
echo "--- code block parity summary ---"
[ -n "$ONLY" ] && echo "filter (--only)          : $ONLY"
echo "shared files compared    : $SHARED"
echo "files parity-clean       : $MATCHED"
echo "code blocks compared     : $CODE_CMP  (comment text normalized away)"
echo "diagram blocks compared  : $PROSE_CMP (line-count structure only)"
echo "files skipped (one-sided): $SKIPPED"
for s in "${SKIPPED_FILES[@]+"${SKIPPED_FILES[@]}"}"; do echo "  skip: $s"; done
if [ "$RC" -eq 0 ]; then echo "RESULT: PASS"; else echo "RESULT: FAIL (zh/en code block drift)"; fi
exit $RC
