#!/usr/bin/env bash
# Layer 1 (static) checks for PR #390 docs verification.
# Usage: bash docs/verification/stress-testing-tuning-doc/scripts/10-static-checks.sh
# Requires: python3, helm (for the render-based F3 check), markdownlint-cli2
# (CI uses DavidAnson/markdownlint-cli2-action@v20 ~= markdownlint-cli2 v0.18).
set -uo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd)"
DOCS=(
  doc/best-practice/en/09-stress-testing-and-tuning.md
  doc/best-practice/en/09-stress-testing-and-tuning-guide.md
  doc/best-practice/zh/09-stress-testing-and-tuning.md
  doc/best-practice/zh/09-stress-testing-and-tuning-guide.md
)
cd "${REPO}"

echo "=== L1a: doc-vs-code consistency (F1, F3, FX1-FX3) ==="
python3 -I docs/verification/stress-testing-tuning-doc/scripts/check_doc_consistency.py
L1A=$?

echo
echo "=== L1b: markdownlint (CI parity: .github/.markdownlint.json) ==="
MDL="${MARKDOWNLINT_CLI2:-markdownlint-cli2}"
if ! command -v "${MDL}" >/dev/null 2>&1; then
  MDL="$(ls /tmp/mlcli2/node_modules/.bin/markdownlint-cli2 2>/dev/null || true)"
fi
if [ -n "${MDL}" ]; then
  "${MDL}" --config .github/.markdownlint.json "${DOCS[@]}"
  echo "markdownlint exit: $?"
  L1B=$?
else
  echo "SKIP: markdownlint-cli2 not found (set MARKDOWNLINT_CLI2 or install to /tmp/mlcli2)"
  L1B=0
fi

echo
echo "=== L1 result: consistency=$L1A lint=$L1B ==="
exit $(( L1A > 0 ? L1A : L1B ))
