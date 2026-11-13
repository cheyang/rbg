#!/usr/bin/env bash
# check_b1_zero_budget.sh — B-F1 fixed-contract check (docs-only PR #569).
#
# Contract: the KEP's B=0 execution-window rule must be implementable with
# unchanged RIS code. Round 1 (bd378ebd) required a literal
# maxUnavailable: 0 even without surge, which the existing Stateful RIS
# floor (computeMaxUnavailable floors to 1 when maxSurge == 0) contradicts.
# Round 2 (d8d53364) redesigns B=0: positive effective maxSurge required,
# admission rejects explicit 0 with omitted/zero maxSurge, reconciliation
# reports percentage budgets that round down to zero without surge, and the
# doc explicitly acknowledges the surge-conditional floor.
#
# Polarity: CONTRACT — exit 0 (pass) when the redesign is present.
#   bd378ebd: FAIL (old rule).   d8d53364: PASS.
# Complements the Go canary TestVerifyKEP543_ExplicitZeroFloorsToOneOnlyWithoutSurge,
# which proves the code claims the redesign relies on.
set -u
ROOT="${1:?usage: check_b1_zero_budget.sh <repo-root>}"
EN="$ROOT/keps/543-segment/README.md"
ZH="$ROOT/keps/543-segment/README.zh-CN.md"
fail=0

need() { # file, pattern, description
  if grep -q "$2" "$1"; then
    echo "PASS $3"
  else
    echo "FAIL $3"
    fail=1
  fi
}

need "$EN" 'An explicit `maxUnavailable` value of `0` requires a positive `maxSurge`' "validation table: explicit 0 requires positive maxSurge (EN)"
need "$EN" 'A zero group budget requires positive effective `maxSurge`' "zero-budget rule paragraph (EN)"
need "$EN" 'floors a resolved `maxUnavailable` to `1` only when `maxSurge` is zero' "acknowledges the surge-conditional RIS floor (EN)"
need "$EN" 'the literal `0` written by RBG survives existing RIS logic unchanged' "literal-0-survives claim (EN)"
need "$EN" 'Valid only with positive effective `maxSurge`' "B=0 table row gated on surge (EN)"
need "$EN" 'reconciliation reports percentage budgets that round down to zero without surge' "round-down-to-zero handled by reconciliation (EN)"
need "$ZH" '显式 `maxUnavailable` 为 `0` 时要求 `maxSurge` 为正' "validation table (ZH)"
need "$ZH" '零组级预算要求有效 `maxSurge` 为正' "zero-budget rule paragraph (ZH)"

if [ "$fail" = 0 ]; then
  echo "B-F1 zero-budget redesign check: PASS"
  exit 0
else
  echo "B-F1 zero-budget redesign check: FAIL"
  exit 1
fi
