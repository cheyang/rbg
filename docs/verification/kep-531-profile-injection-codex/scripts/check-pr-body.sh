#!/usr/bin/env bash
# check-pr-body.sh — F-STALE-PR-BODY contract: the PR description must not
# describe machinery the head has removed. Round 5 removed the in-use
# finalizer, the alpha-warning/beta-rejection staging, and the
# --disable-profile-immutability escape hatch; the PR body still describes all
# three (fetched live via gh). Requires: gh (authenticated). Not part of
# re-verify.sh's layers (needs GitHub credentials, machine-dependent).
set -uo pipefail
BODY="$(gh pr view 548 --repo sgl-project/rbg --json body --jq .body 2>/dev/null)" || { echo "SKIP  PR-BODY-FRESHNESS (gh unavailable)"; exit 0; }
fail=0
for t in "in-use finalizer" "disable-profile-immutability" "alpha-warning"; do
  if printf '%s' "$BODY" | grep -qF "$t"; then
    echo "FAIL  PR-BODY-FRESHNESS body still describes removed machinery: '$t'"; fail=1
  fi
done
[ "$fail" -eq 0 ] && echo "PASS  PR-BODY-FRESHNESS PR body matches the head design (no removed machinery described)"
exit $fail
