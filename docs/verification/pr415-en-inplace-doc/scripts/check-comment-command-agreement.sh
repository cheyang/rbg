#!/usr/bin/env bash
# check-comment-command-agreement.sh -- flags a code-block comment that describes
# something other than the command directly beneath it.
#
# Part of the verification harness for PR #415 (sgl-project/rbg), finding F4.
# POLARITY: contract -- SHOULD FAIL on the reviewed code (285761e9). The failure
# IS the reproduction of F4; it turns green once the stale comment is corrected.
#
# F4 (nit, and REVERSED relative to the other findings -- here the English
# translation is RIGHT and the Chinese original is wrong):
#
#   doc/best-practice/zh/04-...-guide.md:344   # 确认所有 Pod 使用新镜像
#   doc/best-practice/en/04-...-guide.md:344   # Confirm all Pods contain the new
#                                              #   environment variable
#   :345 (both)  kubectl get pods ... jsonpath='...env[?(@.name=="new_env")].value}...'
#
# The command inspects a container ENV VAR, and the surrounding walkthrough
# ("operation 3") never changes any image -- so the zh comment is a copy/paste
# leftover from operation 1. The en translator silently fixed it.
#
# Rule enforced (mechanical, narrow, language-agnostic): for each comment line
# inside a fenced block, look at the command lines until the next blank line. If
# the comment talks about an IMAGE but the command only inspects `env`, or the
# comment talks about an ENV VAR but the command only inspects `.image`, that is a
# mismatch. Comments that mention neither concept, and commands that inspect
# both, are ignored -- so this cannot fire on ordinary prose comments.
#
# Usage:  check-comment-command-agreement.sh [repo-root] [--only <substr>]
# Exit:   0 = no mismatch; 1 = mismatch found; 2 = usage error.
set -uo pipefail

ROOT=""; ONLY=""
while [ $# -gt 0 ]; do
  case "$1" in
    --only) ONLY="${2:-}"; shift 2;;
    -h|--help) sed -n '2,30p' "$0"; exit 0;;
    *) [ -z "$ROOT" ] && ROOT="$1" && shift || { echo "unexpected arg: $1" >&2; exit 2; };;
  esac
done
if [ -z "$ROOT" ]; then
  ROOT="$(git -C "$(dirname "${BASH_SOURCE[0]}")" rev-parse --show-toplevel 2>/dev/null)" || {
    echo "usage: $0 [repo-root] [--only <substr>]" >&2; exit 2; }
fi
BP="$ROOT/doc/best-practice"
[ -d "$BP" ] || { echo "check-comment-command-agreement: missing $BP" >&2; exit 2; }

RC=0
NFILES=0; NCOMMENTS=0; NCHECKED=0; NBAD=0

while IFS= read -r mdfile; do
  base="$(basename "$mdfile")"
  if [ -n "$ONLY" ] && [ "${base#*$ONLY}" = "$base" ]; then continue; fi
  NFILES=$((NFILES+1))
  shown="${mdfile#$ROOT/}"

  # awk emits one record per (comment, following-command-run) pair:
  #   lineno <TAB> commenttext <TAB> joined command text
  while IFS=$'\t' read -r lineno ctext cmd; do
    [ -n "$lineno" ] || continue
    NCOMMENTS=$((NCOMMENTS+1))
    [ -n "$cmd" ] || continue

    # Does the comment talk about an image / an env var?
    c_image=0; c_env=0
    case "$ctext" in
      *image*|*Image*|*IMAGE*|*镜像*) c_image=1;;
    esac
    case "$ctext" in
      *"environment variable"*|*"env var"*|*ENV*|*env*|*环境变量*) c_env=1;;
    esac

    # Does the command inspect an image / an env var?
    q_image=0; q_env=0
    case "$cmd" in
      *".image"*|*"containers[*].image"*|*"-o jsonpath"*".image"*) q_image=1;;
    esac
    case "$cmd" in
      *".env"*|*"env[?"*|*"env["*) q_env=1;;
    esac

    # Only judge when the comment names exactly one of the two concepts AND the
    # command inspects exactly the other one. Anything ambiguous is skipped.
    if [ "$c_image" -eq 1 ] && [ "$c_env" -eq 0 ] && [ "$q_env" -eq 1 ] && [ "$q_image" -eq 0 ]; then
      NCHECKED=$((NCHECKED+1))
      echo "FAIL comment/command mismatch (comment says IMAGE, command inspects ENV):"
      echo "     $shown:$lineno"
      echo "     comment: $ctext"
      echo "     command: $cmd"
      echo "     FIX: describe the env var instead (the en translation of this same"
      echo "          line already does)."
      RC=1; NBAD=$((NBAD+1))
    elif [ "$c_env" -eq 1 ] && [ "$c_image" -eq 0 ] && [ "$q_image" -eq 1 ] && [ "$q_env" -eq 0 ]; then
      NCHECKED=$((NCHECKED+1))
      echo "FAIL comment/command mismatch (comment says ENV VAR, command inspects IMAGE):"
      echo "     $shown:$lineno"
      echo "     comment: $ctext"
      echo "     command: $cmd"
      RC=1; NBAD=$((NBAD+1))
    else
      NCHECKED=$((NCHECKED+1))
    fi
  done < <(awk '
    function emit() {
      if (clineno > 0 && cmdtext != "") {
        gsub(/\t/, " ", ctext); gsub(/\t/, " ", cmdtext)
        printf "%d\t%s\t%s\n", clineno, ctext, cmdtext
      }
      clineno = 0; ctext = ""; cmdtext = ""
    }
    /^[ \t]*(```|~~~)/ { emit(); inblock = 1 - inblock; next }
    inblock == 0 { next }
    {
      line = $0
      probe = line; sub(/^[ \t]+/, "", probe)
      if (probe == "") { emit(); next }            # blank line ends the run
      if (substr(probe, 1, 1) == "#") {
        # a new comment starts a new pair
        emit()
        clineno = NR; ctext = probe
        next
      }
      if (substr(probe, 1, 1) == ">") next          # sample output, not a command
      if (clineno > 0) {
        cmdtext = (cmdtext == "" ? probe : cmdtext " " probe)
      }
    }
    END { emit() }
  ' "$mdfile")
done < <(find "$BP" -name '*.md' | sort)

echo
echo "--- comment/command agreement summary ---"
[ -n "$ONLY" ] && echo "filter (--only)         : $ONLY"
echo "markdown files scanned  : $NFILES"
echo "comment+command pairs   : $NCHECKED"
echo "mismatches              : $NBAD"
if [ "$RC" -eq 0 ]; then
  echo "RESULT: PASS"
else
  echo "RESULT: FAIL (a code-block comment describes something the command does not do)"
fi
exit $RC
