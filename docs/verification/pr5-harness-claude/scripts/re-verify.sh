#!/usr/bin/env bash
# re-verify.sh — re-run the PR #5 (harness) verification checks and report,
# per finding, CONFIRMED (still present) / REFUTED (addressed by the author).
#
# Usage: re-verify.sh [--layers unit,integration]
#   unit        = 10-compile-at-head.sh (F1) + 20-round1-unit-repro.sh (premise)
#   integration = 30-round1-envtest-repro.sh (premise; needs envtest assets,
#                 auto-downloaded on first use)
#   evidence    = 40-evidence-files.sh (F2)
#
# Exit code 0 iff every FINDING check is REFUTED (drops into /loop or CI).
# The premise checks (round-1 reproduction) are informational: they are
# expected to stay REPRODUCED while the PR base is the round-1 PR #394 head.
#
# The PR head is resolved from verify-manifest.json (.pr) via
# `git fetch <clone-url> pull/<n>/head` so this works from any clone.
set -euo pipefail

VDIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPTS="$VDIR/scripts"
MANIFEST="$VDIR/verify-manifest.json"
LAYERS="unit,integration,evidence"

while [ $# -gt 0 ]; do
  case "$1" in
    --layers) LAYERS="$2"; shift 2;;
    -h|--help) sed -n '2,17p' "$0"; exit 0;;
    *) echo "unexpected arg: $1" >&2; exit 2;;
  esac
done

# Resolve current PR head (machine-independent), like the pr394 re-verify.sh.
PR_URL="$(jq -r '.pr // empty' "$MANIFEST")"
if [ -n "$PR_URL" ]; then
  rest="${PR_URL#*://}"; host="${rest%%/*}"; path="${rest#*/}"
  owner="$(printf '%s' "$path" | cut -d/ -f1)"
  repo="$(printf '%s' "$path" | cut -d/ -f2)"
  num="$(printf '%s' "$path" | sed -E 's#.*/pull/([0-9]+).*#\1#')"
  cloneurl="https://$host/$owner/$repo.git"
  git fetch --quiet "$cloneurl" "pull/$num/head" 2>/dev/null || true
  HEAD_SHA="$(git rev-parse FETCH_HEAD 2>/dev/null || git rev-parse HEAD)"
else
  HEAD_SHA="$(git rev-parse HEAD)"
fi

echo "re-verify: PR head = $HEAD_SHA"
echo "re-verify: last-reviewed = $(cat "$VDIR/.last-reviewed" 2>/dev/null || echo '<none>')"

run() { bash "$SCRIPTS/$1" || true; }

F1=""; F2=""; UNIT=""; INTEG=""
case ",$LAYERS," in *,unit,*) F1="$(run 10-compile-at-head.sh)"; UNIT="$(run 20-round1-unit-repro.sh)";; esac
case ",$LAYERS," in *,integration,*) INTEG="$(run 30-round1-envtest-repro.sh)";; esac
case ",$LAYERS," in *,evidence,*) F2="$(run 40-evidence-files.sh)";; esac

# F3 (PR body staleness): the PR body still describes round-1 state ("B5 tests
# are bug-canaries … flip to red after a correct off-by-one fix") while the
# head's B5 test is a contract test ("must equal base (fixed)"). Checked
# mechanically when gh is available; otherwise left to reasoning (README.md).
F3="SKIP(reasoning-only)"
REPO_ROOT="$(cd "$VDIR/../../.." && pwd)"
if command -v gh >/dev/null 2>&1; then
  BODY="$(gh pr view 5 --repo cheyang/rbg --json body -q .body 2>/dev/null || true)"
  if [ -n "$BODY" ] && [ -f "$REPO_ROOT/pkg/reconciler/roleinstance/sync/restart_backoff_verify_test.go" ]; then
    if printf '%s' "$BODY" | grep -q "bug-canaries" && \
       grep -q "must equal base (fixed)" "$REPO_ROOT/pkg/reconciler/roleinstance/sync/restart_backoff_verify_test.go"; then
      F3="CONFIRMED"
    else
      F3="REFUTED"
    fi
  fi
fi

printf '\n================  RE-VERIFY (PR #5 harness)  ================\n'
printf '%-8s %-12s %s\n' ID CHECK VERDICT
printf '%-8s %-12s %s\n' F1 compile "$(echo "$F1" | grep -oE 'F1 (CONFIRMED|REFUTED|INCONCLUSIVE)' || echo 'SKIPPED')"
printf '%-8s %-12s %s\n' F2 evidence "$(echo "$F2" | grep -oE 'F2 (CONFIRMED|REFUTED)' || echo 'SKIPPED')"
printf '%-8s %-12s %s\n' F3 body-stale "$F3"
printf '%-8s %-12s %s\n' P1 unit-repro "$(echo "$UNIT" | grep -oE 'UNIT-REPRO (REPRODUCED|NOT-REPRODUCED)' || echo 'SKIPPED')"
printf '%-8s %-12s %s\n' P2 integ-repro "$(echo "$INTEG" | grep -oE 'INTEG-REPRO (REPRODUCED|NOT-REPRODUCED)' || echo 'SKIPPED')"

printf '\nF1/F2 are findings on the harness PR: REFUTED = the author fixed the branch.\n'
printf 'P1/P2 reproduce the round-1 bug evidence the PR exists to carry: they are\n'
printf 'expected to stay REPRODUCED while pr394-base is the round-1 PR #394 head, and\n'
printf 'to stop mattering once the harness is rebased on fixed code.\n'
printf 'Raw output: %s/results/\n' "$VDIR"

BAD=0
echo "$F1" | grep -q "F1 REFUTED" || BAD=1
echo "$F2" | grep -q "F2 REFUTED" || BAD=1
[ "$BAD" -eq 0 ] && { echo "RESULT: all harness findings addressed."; exit 0; } \
                 || { echo "RESULT: harness findings still present."; exit 1; }
