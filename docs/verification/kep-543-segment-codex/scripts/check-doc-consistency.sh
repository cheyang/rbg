#!/usr/bin/env bash
# Contract checks for review findings F2 (round 1), F3 and F4 (round 2) on
# PR #569 (KEP-543). Run from anywhere inside the repo whose docs are under
# test (verify branch checkout, or grafted onto the PR head by re-verify.sh).
#
# F2: examples must use the normative <ruleID>-<groupIndex> label scheme.
# F3: the test plan must not require accepting a zero-budget-without-surge
#     combination the normative rule now rejects/reports (revision regression).
# F4: the discovery-owner paragraph must not claim the enumeration source is
#     "the child RIS committed ordinals" — unreachable for Segment members.
#
# Polarity: CONTRACT — exit 1 while a defect is present, 0 when clean.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

fail=0
for pair in "README.md" "README.zh-CN.md"; do :; done
EN=keps/543-segment/README.md
ZH=keps/543-segment/README.zh-CN.md

# --- F2: stale rule-name-based label examples (fixed at d8d53364) ----------
for f in "$EN" "$ZH"; do
  if grep -nE '`kv-cache-[0-9]+`|segment=kv-cache-[0-9]+' "$f"; then
    echo "FAIL(F2): $f contains stale rule-name-based segment label values (normative scheme is <ruleID>-<groupIndex>)"
    fail=1
  fi
done
grep -q 'SHA-256' "$EN" || { echo "FAIL(F2-guard): normative ruleID scheme missing from EN README"; fail=1; }
grep -q 'SHA-256' "$ZH" || { echo "FAIL(F2-guard): normative ruleID scheme missing from ZH README"; fail=1; }

# --- F3: stale zero-budget acceptance bullet (RED at d8d53364) -------------
grep -qiE 'accept a Segment rolling-update configuration.*both resolve to zero' "$EN" \
  && { echo "FAIL(F3): EN test plan still requires accepting zero-budget-without-surge (normative rule rejects/reports it)"; fail=1; }
grep -q '同时解析为 0 时仍接受' "$ZH" \
  && { echo "FAIL(F3): ZH test plan still requires accepting zero-budget-without-surge"; fail=1; }
grep -q 'requires a positive `maxSurge`' "$EN" || { echo "FAIL(F3-guard): zero-budget validation rule missing from EN README"; fail=1; }
grep -q '要求 `maxSurge` 为正' "$ZH" || { echo "FAIL(F3-guard): zero-budget validation rule missing from ZH README"; fail=1; }

# --- F4: inaccurate discovery enumeration source (RED at d8d53364) ---------
grep -q 'otherwise the child RIS committed ordinals' "$EN" \
  && { echo "FAIL(F4): EN claims discovery derives from child RIS committed ordinals; unreachable for Segment members (memberOrdinals dense path)"; fail=1; }
grep -q '创建后使用子 RIS 的 committed ordinals' "$ZH" \
  && { echo "FAIL(F4): ZH claims discovery derives from child RIS committed ordinals; unreachable for Segment members"; fail=1; }
grep -q 'must enumerate only the aligned base-group target' "$EN" || { echo "FAIL(F4-guard): discovery invariant missing from EN README"; fail=1; }
grep -q '必须只枚举对齐后的基础组目标' "$ZH" || { echo "FAIL(F4-guard): discovery invariant missing from ZH README"; fail=1; }

if [ "$fail" -eq 0 ]; then
  echo "PASS: doc consistency checks F2, F3, F4 all clean"
fi
exit "$fail"
