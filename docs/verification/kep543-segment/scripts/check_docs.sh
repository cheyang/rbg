#!/usr/bin/env bash
# Deterministic (L1) doc-quality checks for KEP-543 (PR #569, docs-only).
# Usage: check_docs.sh <repo-root>   (run from a tree containing keps/543-segment/)
# Checks:
#   1. markdownlint on both READMEs (skipped, reported SKIPPED, if npx unavailable)
#   2. relative link + TOC anchor resolution
#   3. EN/ZH structural parity (headings, code blocks, table rows, key numbers)
# Polarity: contract — these pass on the reviewed head and must keep passing.
set -u
ROOT="${1:-$(pwd)}"
DOC="$ROOT/keps/543-segment"
rc=0

echo "== [1] markdownlint =="
if command -v npx >/dev/null 2>&1 && [ -f "$ROOT/.github/.markdownlint.json" ]; then
  if (cd "$ROOT" && npx --no-install markdownlint-cli2 --config .github/.markdownlint.json \
        keps/543-segment/README.md keps/543-segment/README.zh-CN.md); then
    echo "PASS markdownlint (0 issues)"
  else
    echo "FAIL markdownlint"; rc=1
  fi
else
  echo "SKIPPED markdownlint (npx or config not available)"
fi

echo "== [2] links + anchors =="
python3 "$(dirname "$0")/check_links.py" "$DOC" || rc=1

echo "== [3] EN/ZH parity =="
python3 "$(dirname "$0")/check_parity.py" "$DOC" || rc=1

exit $rc
