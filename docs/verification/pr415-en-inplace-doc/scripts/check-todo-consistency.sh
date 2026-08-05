#!/usr/bin/env bash
# check-todo-consistency.sh -- flags TODO placeholders that contradict the links
# immediately following them.
#
# Part of the verification harness for PR #415 (sgl-project/rbg), finding F2.
# POLARITY: contract -- SHOULD FAIL on the reviewed code (285761e9). That failure
# IS the reproduction of F2; it turns green once the stale comment is removed.
#
# F2 (minor): the whole point of PR #415's change to the zh document was to
# replace the "these documents do not exist yet" TODO placeholder with real links.
# The zh side was cleaned up; the en side kept the TODO comment even though the
# two links right below it are live and resolvable:
#
#   doc/best-practice/en/04-configuring-inplace-update-and-scheduling-policies.md:371
#     <!-- TODO: The following documents have not been created yet; links will be
#          added once they are complete -->
#   :373  + [Deploying Inference Services with RBG](./01-deploy-inference-service.md)
#   :374  + [Configuring Rolling Update Strategies](./03-configuring-rolling-updates.md)
#
# The rule enforced here is deliberately narrow and mechanical, so it cannot
# produce false positives on a legitimately-pending TODO: a TODO comment is a
# defect ONLY IF it claims the following documents are not yet created AND at
# least one relative link within the next few lines already RESOLVES ON DISK.
# A TODO sitting above genuinely unwritten (unlinked, or dead-linked) entries is
# correct and is left alone.
#
# Usage:  check-todo-consistency.sh [repo-root] [--only <substr>]
# Exit:   0 = no self-contradicting TODO; 1 = contradiction found; 2 = usage error.
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
[ -d "$BP" ] || { echo "check-todo-consistency: missing $BP" >&2; exit 2; }

# how many lines after the TODO count as "immediately following"
WINDOW=8

RC=0
NFILES=0; NTODO=0; NBAD=0

while IFS= read -r mdfile; do
  base="$(basename "$mdfile")"
  if [ -n "$ONLY" ] && [ "${base#*$ONLY}" = "$base" ]; then continue; fi
  NFILES=$((NFILES+1))
  reldir="$(dirname "$mdfile")"
  shown="${mdfile#$ROOT/}"

  # find TODO comments that assert the linked docs do not exist yet.
  # Matches the zh and en phrasings actually used in this doc set.
  while IFS= read -r hit; do
    lineno="${hit%%:*}"
    text="${hit#*:}"
    NTODO=$((NTODO+1))

    # collect relative markdown links in the following WINDOW lines
    resolved=""
    unresolved=""
    end=$((lineno + WINDOW))
    while IFS=$'\t' read -r ln target; do
      [ -n "$target" ] || continue
      case "$target" in http://*|https://*|mailto:*|'#'*) continue;; esac
      path="${target%%#*}"
      [ -n "$path" ] || continue
      if [ -e "$reldir/$path" ]; then
        resolved="$resolved $shown:$ln->$target"
      else
        unresolved="$unresolved $shown:$ln->$target"
      fi
    done < <(awk -v a="$lineno" -v b="$end" '
      NR > a && NR <= b {
        line = $0
        while (match(line, /\[[^]]*\]\([^)]+\)/)) {
          m = substr(line, RSTART, RLENGTH)
          t = m; sub(/^\[[^]]*\]\(/, "", t); sub(/\)$/, "", t)
          sub(/[ \t]+"[^"]*"$/, "", t)
          gsub(/^[ \t]+|[ \t]+$/, "", t)
          printf "%d\t%s\n", NR, t
          line = substr(line, RSTART + RLENGTH)
        }
      }' "$mdfile")

    if [ -n "$resolved" ]; then
      echo "FAIL self-contradicting TODO placeholder:"
      echo "     $shown:$lineno"
      echo "     comment claims the following documents are NOT yet created:"
      echo "       $(printf '%s' "$text" | sed 's/^[ \t]*//')"
      echo "     but these link(s) below it already resolve on disk:"
      for r in $resolved; do echo "       ${r/->/  ->  }"; done
      [ -n "$unresolved" ] && {
        echo "     (still-unresolved entries in the same block, which the TODO may"
        echo "      legitimately refer to:)"
        for u in $unresolved; do echo "       ${u/->/  ->  }"; done
      }
      echo "     FIX: drop the TODO comment (as the zh document already did in PR #415),"
      echo "          or move it below the entries that really are still missing."
      RC=1; NBAD=$((NBAD+1))
    fi
  done < <(grep -nE '<!--[^>]*(TODO|todo)[^>]*(have not been created|not been created|not yet been created|尚未创建|还未创建|未创建)' "$mdfile" || true)
done < <(find "$BP" -name '*.md' | sort)

echo
echo "--- TODO/link consistency summary ---"
[ -n "$ONLY" ] && echo "filter (--only)       : $ONLY"
echo "markdown files scanned: $NFILES"
echo "TODO placeholders seen: $NTODO"
echo "self-contradicting    : $NBAD"
if [ "$RC" -eq 0 ]; then
  echo "RESULT: PASS"
else
  echo "RESULT: FAIL (a TODO says the docs are missing while their links already work)"
fi
exit $RC
