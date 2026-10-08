#!/bin/bash
# F1 (contract): The new stress-testing docs must state a Kubernetes version floor
# consistent with doc/install.md (the repo's authoritative install prerequisite).
# FAILS on PR head if the docs understate the requirement.
set -u
cd "$(git rev-parse --show-toplevel)"
fail=0

install_floor=$(grep -oE 'version >= 1\.[0-9]+' doc/install.md | grep -oE '1\.[0-9]+' | head -1)
echo "doc/install.md floor: >= ${install_floor}"

for f in doc/best-practice/en/09-stress-testing-and-tuning.md \
         doc/best-practice/en/09-stress-testing-and-tuning-guide.md \
         doc/best-practice/zh/09-stress-testing-and-tuning.md \
         doc/best-practice/zh/09-stress-testing-and-tuning-guide.md; do
  doc_floor=$(grep -oE '(>=|＞=|≥)\s*1\.[0-9]+' "$f" | grep -oE '1\.[0-9]+' | head -1)
  echo "$f floor: >= ${doc_floor}"
  if [ "$doc_floor" != "$install_floor" ]; then
    echo "MISMATCH: $f claims >= ${doc_floor}, install.md requires >= ${install_floor}"
    fail=1
  fi
done

if [ "$fail" = 1 ]; then
  echo "F1 REPRODUCED: new docs state a lower Kubernetes floor than doc/install.md"
  exit 1
fi
echo "F1 OK: version floors consistent"
