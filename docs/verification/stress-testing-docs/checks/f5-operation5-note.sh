#!/bin/bash
# F5 (contract): The guide's Summary table points to an "Operation 5 note" explaining
# the Update-phase latency regression. Operation 5 must actually contain that note.
set -u
cd "$(git rev-parse --show-toplevel)"
fail=0

en=doc/best-practice/en/09-stress-testing-and-tuning-guide.md
zh=doc/best-practice/zh/09-stress-testing-and-tuning-guide.md

if grep -q 'see Operation 5 note' "$en"; then
  sec=$(awk '/^## Operation 5/,/^## Operation 6/' "$en")
  if ! printf '%s' "$sec" | grep -qiE 'regress|trade-?off'; then
    echo "F5 REPRODUCED (en): Summary references 'Operation 5 note', but Operation 5 has no"
    echo "note about the Update latency regression its own numbers show (P50 393->1871.5ms)."
    fail=1
  fi
fi
if grep -q '见操作五说明' "$zh"; then
  sec=$(awk '/^## 操作五/,/^## 操作六/' "$zh")
  if ! printf '%s' "$sec" | grep -qE '回归|回退|取舍'; then
    echo "F5 REPRODUCED (zh): Summary references '见操作五说明', but 操作五 contains no such note."
    fail=1
  fi
fi
[ "$fail" = 1 ] && exit 1
echo "F5 OK"
