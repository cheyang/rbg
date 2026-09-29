#!/usr/bin/env bash
# F2 check: evidence availability on the branch.
#
# (a) README.md B5 section points at `results/live-offbyone-evidence.log`,
#     which was never committed (the live B5 evidence actually lives in
#     results/controller-backoff-lines.log and results/live-backoff.log).
# (b) README rounds 2–5 point at `results/reverify/` raw outputs, which are
#     excluded by the harness's own .gitignore — the round 2–5 verdict tables
#     (incl. the pivotal "B2 FIXED" round-4 result) have no committed raw
#     evidence. (Mitigation: the historical upstream heads are still
#     fetchable, so re-verify.sh can re-run them.)
#
# CONFIRMED = the referenced evidence is absent from the branch.
set -uo pipefail

RESULTS="$(cd "$(dirname "${BASH_SOURCE[0]}")/../results" && pwd)"
VDIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"   # docs/verification/pr5-harness-claude
PR394="$VDIR/../pr394-restart-backoff"
OUT="$RESULTS/f2-evidence-files.txt"
: >"$OUT"

say() { echo "$*" | tee -a "$OUT"; }

say "=== F2: evidence files referenced by the pr394 harness README ==="

ok=0
if [ ! -f "$PR394/results/live-offbyone-evidence.log" ]; then
  say "  results/live-offbyone-evidence.log: MISSING (referenced at README.md:191)"
  ok=1
else
  say "  results/live-offbyone-evidence.log: present"
fi

if [ -d "$PR394/results/reverify" ]; then
  say "  results/reverify/: present"
else
  say "  results/reverify/: MISSING on a fresh clone (gitignored by docs/verification/pr394-restart-backoff/.gitignore)"
  [ "$ok" -eq 1 ] || ok=1
fi

# The reference itself:
grep -n "live-offbyone-evidence" "$PR394/README.md" | head -2 | while read -r l; do say "  README ref: $l"; done

# Where the live B5 evidence actually is:
say "  actual live B5 evidence (controller-backoff-lines.log):"
grep -m2 "delay=120s" "$PR394/results/controller-backoff-lines.log" | sed 's/^/    /' | tee -a "$OUT"

if [ "$ok" -eq 1 ]; then
  say "VERDICT: CONFIRMED — README references evidence not present on the branch."
  echo "F2 CONFIRMED"
  exit 0
fi
say "VERDICT: REFUTED — all referenced evidence files exist."
echo "F2 REFUTED"
exit 0
