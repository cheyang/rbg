#!/usr/bin/env bash
# lint.sh — integration layer for the KEP-502 harness: reproduce the PR body's
# own verification claim (markdownlint-cli2 with the repo config) against the
# KEP files. Emits "C7-markdownlint<TAB>pass|fail|not-run" for re-verify.sh.
#
# Usage: lint.sh [<repo-root>]   (default: parent of this script's repo layout)
set -uo pipefail
REPO="${1:-.}"
cd "$REPO" || { echo "C7-markdownlint	not-run"; exit 0; }
if ! command -v npx >/dev/null 2>&1; then
  echo "C7-markdownlint	not-run"
  exit 0
fi
if [ ! -f .github/.markdownlint.json ]; then
  echo "C7-markdownlint	not-run"
  exit 0
fi
if timeout 180 npx --yes --offline markdownlint-cli2 \
    --config .github/.markdownlint.json \
    keps/502-workload-status-model/README.md >/dev/null 2>&1; then
  echo "C7-markdownlint	pass"
else
  echo "C7-markdownlint	fail"
fi
