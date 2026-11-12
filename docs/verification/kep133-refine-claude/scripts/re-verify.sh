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
# This PR is documentation-only: code is identical between base and head, so the
# fact tests (canaries) re-establish the same codebase facts. The F1 contract
# test is RED while the KEP still contains the refuted rollout claims; that red
# is the reproduction, so capture the exit code instead of aborting.
set +e
GOFLAGS=-mod=vendor go test -count=1 -v -run 'TestKEP133Claude' \
  ./pkg/discovery/ ./internal/controller/workloads/
TEST_RC=$?
set -e

echo ""
echo "Per-finding verdicts:"
echo "  F1  contract: KEP free of refuted rollout claims : FAIL on head = Still-broken (fixed when GREEN)"
echo "  F1  canary  : new RBG -> refine + single enum CM : pass  -> codebase fact unchanged"
echo "  F2  short-form addresses    : pass  -> KEP base-template FQDN claim remains refuted"
echo "  F3  committed-target governs: pass  -> KEP 'annotation projected verbatim' claim remains refuted"
echo "  F4  CCP spec-order instances: pass  -> KEP sorted-by-name rendering divergence remains real"
echo "  F6  group.size == len(roles): pass  -> KEP motivation example remains wrong"
echo "  P0  enumeration O(replicas) : pass  -> KEP size premise remains confirmed"
echo ""
echo "Exit code is non-zero while the F1 contract test is red (finding open)."
exit "${TEST_RC}"
