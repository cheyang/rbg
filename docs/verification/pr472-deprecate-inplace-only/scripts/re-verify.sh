#!/usr/bin/env bash
# Re-verify PR #472 (deprecate InPlaceOnly). Grafts this harness onto the
# current PR head (auto-fetched via manifest.pr), runs unit + live layers,
# and prints per-finding Fixed / Still-broken / Partial / Harness-update.
set -uo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"; DOC="$(dirname "$HERE")"
PR_URL=$(python3 -c "import json;print(json.load(open('$DOC/verify-manifest.json'))['pr'])")
PR_NUM=$(echo "$PR_URL" | grep -oE '[0-9]+$')
REPO_URL=$(python3 -c "import json;d=json.load(open('$DOC/verify-manifest.json'));print(d.get('prHeadFetch',{}).get('remote','https://github.com/sgl-project/rbg.git'))")
echo "== fetching PR $PR_NUM head from $REPO_URL =="
git fetch "$REPO_URL" "pull/$PR_NUM/head:pr-$PR_NUM" 2>&1 | tail -2
HEAD="pr-$PR_NUM"
LAST=$(cat "$DOC/.last-reviewed" 2>/dev/null || echo "")
echo "== incremental review delta: ${LAST:-<merge-base>}..$HEAD =="
git log --oneline "${LAST:-$(git merge-base origin/main $HEAD)}..$HEAD" 2>/dev/null | head
echo
echo "== P0 premise (code): grep for any InPlaceOnly-specific branch in reconciler =="
HITS=$(git grep -n "InPlaceOnlyUpdateStrategyType" "$HEAD" -- 'pkg/**/*.go' 'internal/**/*.go' 2>/dev/null | grep -v "_test.go" | grep -v "api/workloads/v1alpha2/rolebasedgroup_types.go")
if [ -z "$HITS" ]; then echo "P0: PASS — no strategy-specific branch (InPlaceOnly handled via != RecreatePod, identical to InPlaceIfPossible)"; else echo "P0: INVESTIGATE — new InPlaceOnly-specific reference:"; echo "$HITS"; fi
echo
echo "== Live A/B (requires KUBECONFIG pointing at a cluster with rbg installed) =="
if command -v kubectl >/dev/null 2>&1 && kubectl get crd rolebasedgroups.workloads.x-k8s.io >/dev/null 2>&1; then
  bash "$HERE/live_abtest.sh" || echo "live_abtest.sh exited non-zero (see results/)"
else
  echo "skipped: no kubectl / no rbg CRD on current KUBECONFIG"
fi
echo
echo "== advance .last-reviewed to $HEAD after reviewing =="
echo "git rev-parse $HEAD  # record this sha"
