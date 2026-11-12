#!/usr/bin/env bash
# Re-verify the PR #552 chart deprecation webhook findings against a ref.
#
# Usage: bash docs/verification/pr552-chart-deprecation-webhooks/scripts/re-verify.sh [<ref>]
#   no ref  -> fetch the current PR head from the manifest's "pr" URL, merge it
#              into the current PR base (what GitHub's merge commit will hold),
#              and graft the harness onto that merge result. Round 3+ keeps
#              this merge step: the PR branch lags the base (the base moved
#              past #556 between rounds), and the shipping tree is the merge,
#              not the branch.
#   <ref>   -> any commit-ish (fixed PR head, post-merge commit) — used as-is.
#
# Prints per finding: Fixed / Still-broken / Partial / Harness-update.
# Exit 0 iff every contract test passes except the documented open finding
# (F9's tracker is expected RED until the crdUpgrade pin moves past #556).
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"          # docs/verification/<topic>
HARNESS_ROOT="$(cd "$HERE/../.." && pwd)"                        # repo checkout holding the harness branch
MANIFEST="$HERE/verify-manifest.json"
TOPIC="pr552-chart-deprecation-webhooks"

PR_URL=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["pr"])' "$MANIFEST")
PR_NUM="${PR_URL##*/}"
REPO_URL="${PR_URL%/pull/*}"          # https://github.com/sgl-project/rbg

HARNESS_PATHS=(
  test/chart/webhook_config_test.go
  test/envtest/testutil/chart_contract_setup.go
  test/envtest/testcase/deprecationchart
)

TARGET_REF="${1:-}"
HEAD_SHA=""
if [[ -z "$TARGET_REF" ]]; then
  echo ">> resolving current PR head for $PR_URL"
  git -C "$HARNESS_ROOT" fetch "$REPO_URL" "pull/${PR_NUM}/head"
  HEAD_SHA=$(git -C "$HARNESS_ROOT" rev-parse FETCH_HEAD)
  BASE_SHA=$(gh pr view "$PR_NUM" --repo "$REPO_URL" --json baseRefOid --jq .baseRefOid 2>/dev/null || true)
  if [[ -n "$BASE_SHA" ]] && ! git -C "$HARNESS_ROOT" cat-file -e "${BASE_SHA}^{commit}" 2>/dev/null; then
    git -C "$HARNESS_ROOT" fetch origin main >/dev/null 2>&1 || \
      git -C "$HARNESS_ROOT" fetch "$REPO_URL" main >/dev/null 2>&1 || true
  fi
  if [[ -n "$BASE_SHA" ]] && git -C "$HARNESS_ROOT" cat-file -e "${BASE_SHA}^{commit}" 2>/dev/null; then
    echo ">> merging PR head ${HEAD_SHA:0:8} into base ${BASE_SHA:0:8} (the shipping tree)"
    if TREE=$(git -C "$HARNESS_ROOT" merge-tree --write-tree "$BASE_SHA" "$HEAD_SHA"); then
      TARGET_SHA=$(git -C "$HARNESS_ROOT" commit-tree "$TREE" -p "$BASE_SHA" -p "$HEAD_SHA" -m "verify: ephemeral merge of PR head into base")
    else
      echo "!! merge of PR head into base CONFLICTS — the PR must be rebased/resolved by the author"
      exit 2
    fi
  else
    echo "!! base sha unavailable; grafting onto the PR head as-is"
    TARGET_SHA="$HEAD_SHA"
  fi
else
  TARGET_SHA=$(git -C "$HARNESS_ROOT" rev-parse "$TARGET_REF")
fi
echo ">> re-verifying findings against ${TARGET_SHA}"

LAST_REVIEWED="$HERE/.last-reviewed"
if [[ -f "$LAST_REVIEWED" ]]; then
  DELTA_START=$(cat "$LAST_REVIEWED")
  DELTA_HEAD="${HEAD_SHA:-$(git -C "$HARNESS_ROOT" rev-parse "$TARGET_REF" 2>/dev/null || echo "$TARGET_SHA")}"
  if [[ "$DELTA_START" != "$DELTA_HEAD" ]]; then
    echo ">> incremental review delta (last-reviewed..PR head): ${DELTA_START}..${DELTA_HEAD}"
    git -C "$HARNESS_ROOT" log --oneline "${DELTA_START}..${DELTA_HEAD}" 2>/dev/null || true
    echo ">> after the round, advance the marker: echo ${DELTA_HEAD} > $LAST_REVIEWED"
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
SHORT=$(git rev-parse --short HEAD)
L1_OUT="$RESULTS/reverify-${SHORT}-l1.txt"
L2_OUT="$RESULTS/reverify-${SHORT}-l2.txt"

echo ">> L1: go test ./test/chart/ -count=1"
(go test ./test/chart/ -count=1 -v 2>&1 || true) | tee "$L1_OUT"
echo ">> L2: envtest chart contract suite"
(go test ./test/envtest/testcase/deprecationchart/ \
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
  echo "   $id: $verdict"
  STATUS[$id]="$verdict"
}

echo ">> per-finding verdicts (on the PR head merged into the current base)"
echo "   (F1 tracker = TestChartDefaultImageServesRenderedWebhookPaths)"
classify F1 "$L1_OUT" "TestChartDefaultImageServesRenderedWebhookPaths"
classify F2 "$L1_OUT" "TestChartWebhookConfigurationsMatchShippedManifests"
classify F10 "$L1_OUT" "TestChartRendersNoV1alpha1DeprecationWebhookEntries"
classify F11 "$L1_OUT" "TestCRDDeprecationWarningsCarryDocumentedTimeline"
classify F9 "$L1_OUT" "TestPinnedCrdUpgraderImageCarriesDocumentedWarningText"

echo "   F3: docs-only — fixed in round 2 (verified); retained as regression contract"
echo "   F4: docs-only — fixed in round 2 (verified); retained as regression contract"
echo "   F5: resolved on the base by #555 (chart 0.9.0-alpha.3, pin v0.9.0-73e05db4) — check Chart.yaml on the target ref"

FAILED=0
for id in F1 F2 F10 F11; do
  [[ "${STATUS[$id]}" == "Fixed" ]] || FAILED=1
done
# F9 is the documented open finding (crdUpgrade pin predates #556): its tracker
# is expected RED until the pin moves; treat anything else as a harness problem.
if [[ "${STATUS[F9]}" != "Still-broken" ]]; then
  echo "   F9: unexpected state '${STATUS[F9]}' (expected Still-broken) -> Harness-update"
  FAILED=1
fi
if grep -q -- "--- FAIL: TestChartDeprecationContract" "$L2_OUT"; then
  echo "   chart contract suite FAILED -> the chart-rendered webhooks no longer drive the documented gate"
  FAILED=1
fi

echo
if [[ "$FAILED" -eq 0 ]]; then
  echo "ALL CONTRACT TESTS GREEN (F9 tracker RED as documented — the crdUpgrade pin predates #556)"
else
  echo "NOT ALL FINDINGS RESOLVED — see verdicts above"
fi
exit "$FAILED"
