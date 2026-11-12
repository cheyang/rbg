#!/usr/bin/env bash
# Re-verification script for KEP-133 (PR #570) reviewer harness (Reviewer A, Claude).
# Usage: bash scripts/re-verify.sh [<ref>]
#   no ref  -> fetches the current PR head from the PR URL in the manifest
#              (git fetch https://github.com/sgl-project/rbg pull/570/head)
#   <ref>   -> grafts the harness onto that exact ref instead (any rev-parse-able ref)
# Exit 0 iff all harness tests pass on the target ref.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(git -C "${SCRIPT_DIR}" rev-parse --show-toplevel)"
PR_URL="$(python3 -c "import json,sys;print(json.load(open('${SCRIPT_DIR}/../verify-manifest.json'))['pr'])")"
PR_NUM="${PR_URL##*/}"
UPSTREAM="$(dirname "$(dirname "${PR_URL}")")"

TARGET_REF="${1:-}"
WORKDIR="$(mktemp -d)"
trap 'rm -rf "${WORKDIR}"' EXIT

git init -q "${WORKDIR}"
if [[ -z "${TARGET_REF}" ]]; then
  echo "Fetching current PR head for ${PR_URL} ..."
  git -C "${WORKDIR}" fetch -q "${UPSTREAM}" "pull/${PR_NUM}/head"
  TARGET_REF="$(git -C "${WORKDIR}" rev-parse FETCH_HEAD)"
  echo "PR head resolved to ${TARGET_REF}"
else
  git -C "${WORKDIR}" fetch -q "${UPSTREAM}" "${TARGET_REF}"
  TARGET_REF="$(git -C "${WORKDIR}" rev-parse FETCH_HEAD)"
fi
git -C "${WORKDIR}" checkout -q "${TARGET_REF}"

HARNESS_FILES=(
  pkg/discovery/kep133_verify_claude_test.go
  internal/controller/workloads/kep133_verify_claude_test.go
)
for f in "${HARNESS_FILES[@]}"; do
  mkdir -p "${WORKDIR}/$(dirname "${f}")"
  cp "${REPO_ROOT}/${f}" "${WORKDIR}/${f}"
done

echo ""
echo "Harness grafted onto ${TARGET_REF}; running L1 unit layer ..."
echo ""
cd "${WORKDIR}"
# This PR is documentation-only: code is identical between base and head, so a
# green run here re-establishes the same codebase facts (see README polarity table).
GOFLAGS=-mod=vendor go test -count=1 -v -run 'TestKEP133Claude' \
  ./pkg/discovery/ ./internal/controller/workloads/

echo ""
echo "Per-finding verdicts (all canaries of current behavior):"
echo "  P0/F1 mode+single-enum-CM   : pass  -> KEP 'today/rollout' claims remain refuted"
echo "  F2  short-form addresses    : pass  -> KEP base-template FQDN claim remains refuted"
echo "  F3  committed-target governs: pass  -> KEP 'annotation projected verbatim' claim remains refuted"
echo "  F4  CCP spec-order instances: pass  -> KEP sorted-by-name rendering divergence remains real"
echo "  F6  group.size == len(roles): pass  -> KEP motivation example remains wrong"
echo "  P0  enumeration O(replicas) : pass  -> KEP size premise remains confirmed"
