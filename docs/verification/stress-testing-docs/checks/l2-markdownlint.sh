#!/bin/bash
# L2 integration: mirror .github/workflows/docs-check.yml (markdownlint-cli2 with the
# repo config) over the four new docs. Expected: clean.
set -u
cd "$(git rev-parse --show-toplevel)"
npx --yes markdownlint-cli2 \
  --config .github/.markdownlint.json \
  "doc/best-practice/en/09-stress-testing-and-tuning.md" \
  "doc/best-practice/en/09-stress-testing-and-tuning-guide.md" \
  "doc/best-practice/zh/09-stress-testing-and-tuning.md" \
  "doc/best-practice/zh/09-stress-testing-and-tuning-guide.md"
