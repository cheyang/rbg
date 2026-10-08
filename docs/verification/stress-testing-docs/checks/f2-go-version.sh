#!/bin/bash
# F2 (contract): The docs' stated Go floor (>= 1.22) must satisfy the module that
# builds `go run ./test/stress/` (test/stress has no own go.mod -> root module).
set -u
cd "$(git rev-parse --show-toplevel)"

if [ -f test/stress/go.mod ]; then
  gomod=test/stress/go.mod
else
  gomod=go.mod
  echo "test/stress has no own go.mod; root go.mod governs 'go run ./test/stress/'"
fi
mod_go=$(awk '/^go /{print $2}' "$gomod")
echo "$gomod go directive: ${mod_go}"

fail=0
for f in doc/best-practice/en/09-stress-testing-and-tuning.md \
         doc/best-practice/en/09-stress-testing-and-tuning-guide.md \
         doc/best-practice/zh/09-stress-testing-and-tuning.md \
         doc/best-practice/zh/09-stress-testing-and-tuning-guide.md; do
  doc_go=$(grep -oE 'go.{0,12}1\.[0-9]+' "$f" | grep -oE '1\.[0-9]+' | head -1)
  echo "$f go floor: >= ${doc_go}"
  mod_minor=$(echo "$mod_go" | cut -d. -f2)
  doc_minor=$(echo "$doc_go" | cut -d. -f2)
  if [ "$doc_minor" -lt "$mod_minor" ]; then
    echo "MISMATCH: $f says go >= 1.${doc_minor}, but $gomod requires go >= ${mod_go}"
    fail=1
  fi
done

if [ "$fail" = 1 ]; then
  echo "F2 REPRODUCED: docs understate the Go toolchain requirement"
  exit 1
fi
echo "F2 OK: go floors consistent"
