#!/usr/bin/env bash
# Run the PR #389 docs verification harness against a doc tree (default: cwd).
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="${1:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}"
OUT="$HERE/../results"
mkdir -p "$OUT"
echo "harness root: $ROOT"
python3 "$HERE/check_docs.py" "$ROOT" | tee "$OUT/last-run.txt"
