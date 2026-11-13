#!/usr/bin/env bash
# Contract test for review finding F2 (PR #569, KEP-543).
#
# The normative "Group Assignment and Persistence" section defines the managed
# group label value as `<ruleID>-<groupIndex>` where ruleID is the first 12
# lowercase hex chars of SHA-256(ruleName). Any example that still shows the
# raw rule name as the segment label value prefix (the round-1 scheme)
# contradicts the normative scheme.
#
# Polarity: CONTRACT — RED on the reviewed head, GREEN once the stale
# examples are fixed.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

fail=0
for f in keps/543-segment/README.md keps/543-segment/README.zh-CN.md; do
  # segment label values shown as <ruleName>-<groupIndex> instead of <ruleID>-<groupIndex>
  if grep -nE '`kv-cache-[0-9]+`|segment=kv-cache-[0-9]+' "$f"; then
    echo "FAIL: $f contains stale rule-name-based segment label values (normative scheme is <ruleID>-<groupIndex>)"
    fail=1
  fi
done

# Sanity: the normative scheme itself must be present (guards against the
# check going vacuously green if the section is renamed away).
grep -q 'SHA-256' keps/543-segment/README.md || { echo "FAIL: normative ruleID scheme missing from EN README"; fail=1; }
grep -q 'SHA-256' keps/543-segment/README.zh-CN.md || { echo "FAIL: normative ruleID scheme missing from ZH README"; fail=1; }

if [ "$fail" -eq 0 ]; then
  echo "PASS: no stale label-value examples; normative scheme present"
fi
exit "$fail"
