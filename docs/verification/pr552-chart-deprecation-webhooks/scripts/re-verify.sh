#!/usr/bin/env bash
# Re-verify the PR #552 chart deprecation webhook findings against a ref.
#
# Usage: bash docs/verification/pr552-chart-deprecation-webhooks/scripts/re-verify.sh [<ref>]
#   no ref  -> fetch the current PR head from the manifest's "pr" URL and graft onto it
#   <ref>   -> any commit-ish (fixed PR head, post-merge commit)
#
# Prints per finding: Fixed / Still-broken / Partial / Harness-update.
# Exit 0 iff every contract test passes (F1's tracker included).
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"          # docs/verification/<topic>
HARNESS_ROOT="$(cd "$HERE/../.." && pwd)"                        # repo checkout holding the harness branch
MANIFEST="$HERE/verify-manifest.json"
TOPIC="pr552-chart-deprecation-webhooks"

PR_URL=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["pr"])' "$MANIFEST")
PR_NUM="${PR_URL##*/}"

HARNESS_PATHS=(
  test/chart/webhook_config_test.go
  test/envtest/testutil/chart_contract_setup.go
  test/envtest/testcase/deprecationchart
  test/envtest/testcase/deprecationchartlegacy
)

TARGET_REF="${1:-}"
if [[ -z "$TARGET_REF" ]]; then
  echo ">> resolving current PR head for $PR_URL"
  git -C "$HARNESS_ROOT" fetch "$PR_URL" "pull/${PR_NUM}/head"
  TARGET_REF="FETCH_HEAD"
fi
TARGET_SHA=$(git -C "$HARNESS_ROOT" rev-parse "$TARGET_REF")
echo ">> re-verifying findings against ${TARGET_SHA}"

LAST_REVIEWED="$HERE/.last-reviewed"
if [[ -f "$LAST_REVIEWED" ]]; then
  DELTA_START=$(cat "$LAST_REVIEWED")
  if [[ "$DELTA_START" != "$TARGET_SHA" ]]; then
    echo ">> incremental review delta: ${DELTA_START}..${TARGET_SHA}"
    git -C "$HARNESS_ROOT" log --oneline "${DELTA_START}..${TARGET_SHA}" || true
    echo ">> after the round, advance the marker: echo ${TARGET_SHA} > $LAST_REVIEWED"
  fi
fi

# Graft the harness onto the target ref in a scratch worktree.
SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/${TOPIC}-XXXXXX")
cleanup() { git -C "$HARNESS_ROOT" worktree remove --force "$SCRATCH" >/dev/null 2>&1 || rm -rf "$SCRATCH"; }
trap cleanup EXIT
git -C "$HARNESS_ROOT" worktree add --detach "$SCRATCH" "$TARGET_SHA" >/dev/null
for p in "${HARNESS_PATHS[@]}"; do
  mkdir -p "$SCRATCH/$(dirname "$p")"
  cp -r "$HERE/../../../$p" "$SCRATCH/$p"
done
cd "$SCRATCH"

# helm's kubeconfig-permission warnings pollute CombinedOutput in the chart
# tests; point KUBECONFIG at a missing path so stderr stays empty.
export KUBECONFIG="${KUBECONFIG:-/tmp/${TOPIC}-missing-kubeconfig}"

ENVTEST_ASSETS="${KUBEBUILDER_ASSETS:-$(ls -d "${HOME}/.local/share/kubebuilder-envtest/k8s/"1.3*-linux-amd64 2>/dev/null | sort -V | tail -1 || true)}"
[[ -n "$ENVTEST_ASSETS" ]] || { echo "!! envtest assets not found (setup-envtest use 1.36.2)"; exit 2; }
export KUBEBUILDER_ASSETS="$ENVTEST_ASSETS"

RESULTS="$HERE/results"
mkdir -p "$RESULTS"
L1_OUT="$RESULTS/reverify-$(git rev-parse --short HEAD)-l1.txt"
L2_OUT="$RESULTS/reverify-$(git rev-parse --short HEAD)-l2.txt"

echo ">> L1: go test ./test/chart/ -count=1"
(go test ./test/chart/ -count=1 -v 2>&1 || true) | tee "$L1_OUT"
echo ">> L2: envtest suites"
(go test ./test/envtest/testcase/deprecationchart/ ./test/envtest/testcase/deprecationchartlegacy/ \
   -count=1 -v --ginkgo.v -timeout 10m 2>&1 || true) | tee "$L2_OUT"

declare -A STATUS
classify() {
  # $1 id, $2 output file, $3 space-separated test-name patterns
  local id="$1" out="$2" patterns="$3" verdict="Harness-update"
  for pat in $patterns; do
    if grep -q -- "^--- FAIL: ${pat}" "$out"; then
      verdict="Still-broken"
    elif grep -q -- "^--- PASS: ${pat}" "$out"; then
      verdict="Fixed"
    fi
  done
  STATUS[$id]="$verdict"
  echo "   $id: $verdict"
}

echo ">> per-finding verdicts"
echo "   (F1 tracker = TestChartDefaultImageServesRenderedWebhookPaths; contract = green when fixed)"
classify F1 "$L1_OUT" "TestChartDefaultImageServesRenderedWebhookPaths"
classify F2 "$L1_OUT" "TestChartWebhookConfigurationsMatchShippedManifests TestChartV1alpha1DeprecationWebhookFields"

# F3/F4/F5 are docs-only; no runnable test decides them.
echo "   F3: docs-only — re-read README.md / README-zh_CN.md flag-table section for the stale paragraphs"
echo "   F4: docs-only — check whether the project migration guide exists yet"
echo "   F5: docs-only — check deploy/helm/rbgs/Chart.yaml version against the last published chart"

# The legacy suite is a mechanism witness (stays green by design); the chart
# contract suite must stay green.
FAILED=0
[[ "${STATUS[F1]}" == "Fixed" ]] || FAILED=1
[[ "${STATUS[F2]}" == "Fixed" ]] || FAILED=1
if grep -q -- "--- FAIL: TestChartLegacyBinaryContract" "$L2_OUT"; then
  echo "   legacy mechanism witness FAILED -> Harness-update (harness shape changed)"
  FAILED=1
fi
if grep -q -- "--- FAIL: TestChartDeprecationContract" "$L2_OUT"; then
  echo "   chart contract suite FAILED -> chart-rendered webhooks no longer gate like the shipped manifests"
  FAILED=1
fi

echo
if [[ "$FAILED" -eq 0 ]]; then
  echo "ALL FINDINGS FIXED (contract tests green; mechanism witness green)"
else
  echo "NOT ALL FINDINGS FIXED — see verdicts above"
fi
exit "$FAILED"
