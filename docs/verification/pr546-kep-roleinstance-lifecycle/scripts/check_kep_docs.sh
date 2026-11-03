#!/usr/bin/env bash
# KEP-527 (PR #546) documentation-level verification.
# Layers: L1 (deterministic doc checks). No cluster, no kubelet.
# Usage: bash docs/verification/pr546-kep-roleinstance-lifecycle/scripts/check_kep_docs.sh [repo-root]
set -u
ROOT="${1:-$(cd "$(dirname "$0")/../../../.." && pwd)}"
KEP_DIR="$ROOT/keps/527-roleinstance-lifecycle"
OUT_DIR="${CHECK_KEP_DOCS_OUT:-$ROOT/docs/verification/pr546-kep-roleinstance-lifecycle/results}"
mkdir -p "$OUT_DIR"

overall=0
section() { printf '\n== %s ==\n' "$1"; }

section "1. markdownlint (repo CI config .github/.markdownlint.json)"
if command -v npx >/dev/null 2>&1; then
  if npx --yes markdownlint-cli2@latest --config "$ROOT/.github/.markdownlint.json" \
      "$KEP_DIR/README.md" "$KEP_DIR/README.zh-CN.md" 2>&1 | tee "$OUT_DIR/markdownlint.txt" | tail -3; then
    echo "markdownlint: PASS"
  else
    echo "markdownlint: FAIL"; overall=1
  fi
else
  echo "markdownlint: NOT-RUN (npx unavailable)"; : >"$OUT_DIR/markdownlint.txt"
fi

section "2. markdownlint (default config, as the PR body claims)"
if command -v npx >/dev/null 2>&1; then
  if npx --yes markdownlint-cli2@latest "$KEP_DIR/README.md" "$KEP_DIR/README.zh-CN.md" \
      2>&1 | tee "$OUT_DIR/markdownlint-default.txt" | tail -3; then
    echo "markdownlint-default: PASS"
  else
    echo "markdownlint-default: FAIL"; overall=1
  fi
else
  echo "markdownlint-default: NOT-RUN (npx unavailable)"; : >"$OUT_DIR/markdownlint-default.txt"
fi

section "3. whitespace check (git diff --check against merge-base)"
BASE="$(git -C "$ROOT" merge-base origin/main HEAD 2>/dev/null || true)"
if [ -n "$BASE" ]; then
  if git -C "$ROOT" diff --check "$BASE...HEAD" >"$OUT_DIR/whitespace.txt" 2>&1; then
    echo "whitespace: PASS"
  else
    echo "whitespace: FAIL"; overall=1
  fi
else
  echo "whitespace: NOT-RUN (no merge-base)"; : >"$OUT_DIR/whitespace.txt"
fi

section "4. kep.yaml see-also targets exist in this repository (CONTRACT: red while a reference dangles)"
: >"$OUT_DIR/see-also.txt"
refs_status=0
while IFS= read -r ref; do
  [ -z "$ref" ] && continue
  target="${ref#/}"
  if [ -e "$ROOT/$target" ]; then
    echo "OK      $ref" | tee -a "$OUT_DIR/see-also.txt"
  else
    echo "MISSING $ref (not present in repo; if it is a future KEP, reference the issue instead)" | tee -a "$OUT_DIR/see-also.txt"
    refs_status=1
  fi
done < <(sed -n '/^see-also:/,/^[a-z]/p' "$KEP_DIR/kep.yaml" | grep -E '^\s*-\s*/' | sed 's/^[[:space:]]*-[[:space:]]*//; s/"//g')
if [ "$refs_status" -eq 0 ]; then
  echo "see-also: PASS"
else
  echo "see-also: FAIL (dangling reference — reproduces finding F-SEEALSO-502)"; overall=1
fi

section "5. en/zh structural parity (headings, tables, code fences, hook mentions)"
en="$KEP_DIR/README.md"; zh="$KEP_DIR/README.zh-CN.md"
parity_status=0
en_h=$(grep -c '^#' "$en"); zh_h=$(grep -c '^#' "$zh")
en_f=$(grep -c '^```' "$en"); zh_f=$(grep -c '^```' "$zh")
en_t=$(grep -c '^|' "$en"); zh_t=$(grep -c '^|' "$zh")
for hook in preUpdate preRecover postReady preDelete; do
  en_m=$(grep -c "$hook" "$en"); zh_m=$(grep -c "$hook" "$zh")
  echo "hook $hook: en=$en_m zh=$zh_m" | tee -a "$OUT_DIR/parity.txt"
  if [ "${en_m:-0}" -eq 0 ] || [ "${zh_m:-0}" -eq 0 ]; then
    echo "parity: FAIL (hook $hook missing in one language)" | tee -a "$OUT_DIR/parity.txt"; parity_status=1; overall=1
  fi
done
{
  echo "headings: en=$en_h zh=$zh_h"
  echo "code-fences: en=$en_f zh=$zh_f"
  echo "table-lines: en=$en_t zh=$zh_t"
} | tee "$OUT_DIR/parity.txt"
if [ "${en_h:-0}" -eq 0 ] || [ "${zh_h:-0}" -eq 0 ]; then
  echo "parity: FAIL (a README is missing)" | tee -a "$OUT_DIR/parity.txt"; parity_status=1; overall=1
fi
if [ "$parity_status" -eq 0 ]; then
  echo "parity: PASS (structure and all four hooks present in both languages)"
fi

section "Summary"
if [ "$overall" -eq 0 ]; then
  echo "ALL DOC CHECKS PASS"
else
  echo "DOC CHECK FAILURES PRESENT (see above)"
fi
exit "$overall"
