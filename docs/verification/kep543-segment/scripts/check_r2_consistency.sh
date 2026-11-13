#!/usr/bin/env bash
# check_r2_consistency.sh — R2-F1 contract check (docs-only PR #569).
#
# Contract: the Member Mode Validation section must not pair the round-2
# fail-fast semantics ("stops the entire RBG reconciliation") with the
# round-1 per-rule isolation sentence ("Non-Segment Stateless roles are
# unaffected"). Under fail-fast, a non-Segment Stateless role in the same
# RBG IS affected at the reconciliation level (its targets, parameters and
# discovery updates freeze while the invalid Segment configuration
# persists); only its running instances are unaffected.
#
# Polarity: CONTRACT — exit 0 (pass) when the doc is internally consistent.
#   bd378ebd (previous head): PASS — per-rule blocking, "unaffected" was true.
#   d8d53364 (round-2 head):  FAIL — fail-fast + stale "unaffected" sentence.
# Passes again once the sentence is reworded or the consequence stated.
set -u
ROOT="${1:?usage: check_r2_consistency.sh <repo-root>}"
EN="$ROOT/keps/543-segment/README.md"
ZH="$ROOT/keps/543-segment/README.zh-CN.md"
fail=0

mode_section() { # file, start heading, next heading
  awk "/#### Member Mode Validation|#### 成员模式校验/{f=1} f && /^#### / && !/Member Mode Validation|成员模式校验/ && NR>1{exit} f" "$1"
}

en_section=$(mode_section "$EN")
zh_section=$(mode_section "$ZH")

if grep -q "stops the entire RBG reconciliation" <<<"$en_section"; then
  if grep -q "Non-Segment Stateless roles are unaffected" <<<"$en_section"; then
    echo "FAIL README.md: Member Mode Validation pairs fail-fast ('stops the entire RBG reconciliation') with the stale per-rule isolation sentence ('Non-Segment Stateless roles are unaffected') — under fail-fast, non-Segment roles' RBG-level reconciliation is affected (frozen), only their instances are not"
    fail=1
  fi
else
  echo "PASS README.md: no fail-fast/isolation pairing (per-rule semantics or already reworded)"
fi

if grep -q "停止整个 RBG reconcile" <<<"$zh_section"; then
  if grep -q "不受该限制影响" <<<"$zh_section"; then
    echo "FAIL README.zh-CN.md: 成员模式校验 同时包含 fail-fast（停止整个 RBG reconcile）与逐规则隔离表述（不受该限制影响）"
    fail=1
  fi
else
  echo "PASS README.zh-CN.md: no fail-fast/isolation pairing (per-rule semantics or already reworded)"
fi

# Note: if the fail-fast language itself vanished (doc reverted to per-rule
# blocking, or was restructured), this check can no longer detect R2-F1 —
# treat that as Harness-update, not a silent pass or fail.
if ! grep -q "stops the entire RBG reconciliation\|stops the RBG reconciliation" "$EN"; then
  echo "NOTE README.md: fail-fast language not found — if round-2 semantics were reverted/restructured, update this check (Harness-update)"
fi

if [ "$fail" = 0 ]; then
  echo "R2-F1 consistency check: PASS"
  exit 0
else
  echo "R2-F1 consistency check: FAIL"
  exit 1
fi
