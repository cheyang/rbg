#!/usr/bin/env bash
# F1 doc contract check — KEP-531 (PR #548, docs-only).
#
# Contract: the KEP README must specify ONE consistent scope for the
# "shared env-name conflict check". It FAILS while the three contradictory
# statements are present (the F1 defect), and PASSES once the author aligns
# them — update the phrases below if the fix rewords them.
set -uo pipefail
README="${1:-keps/531-engine-runtime-profile-injection/README.md}"
if [ ! -f "$README" ]; then
  # On the base branch the KEP README does not exist: the defect cannot be
  # present there, so the contract holds vacuously (base = pass).
  echo "F1-CONTRACT: PASS (README absent at $README — no doc, no contradiction)"
  exit 0
fi

both=$(grep -c "check spans both passes" "$README" || true)
three=$(grep -c "spanning all three passes" "$README" || true)
guard=$(grep -c "still guarding the result" "$README" || true)

if [ "$three" -gt 0 ] || [ "$guard" -gt 0 ]; then
  echo "F1-CONTRACT: FAIL — contradiction present on head:"
  [ "$both" -gt 0 ] && echo "  - 'check spans both passes' (profile+role passes only): $both occurrence(s)"
  [ "$three" -gt 0 ] && echo "  - 'spanning all three passes' (legacy merge included):   $three occurrence(s)"
  [ "$guard" -gt 0 ] && echo "  - 'still guarding the result' (over legacy output):      $guard occurrence(s)"
  echo "  These cannot all describe the same check; one reading breaks the"
  echo "  byte-for-byte guarantee (see results/harness-bites-check.txt)."
  exit 1
fi
echo "F1-CONTRACT: PASS — no contradictory conflict-check scope statements found."
exit 0
