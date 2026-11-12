#!/usr/bin/env bash
# PR #552 re-verification (codex harness, round 3+).
#
# Usage (from a checkout of verify/chart-deprecation-webhooks-codex):
#   bash docs/verification/chart-deprecation-webhooks-codex/scripts/re-verify.sh
#
# Resolves the current PR head from the manifest's `pr` URL, merges it into
# origin/main (the PR is docs-only and its claims describe the post-#556
# composition, so the verification target is the merge result), grafts the
# harness onto it, and runs the unit + integration + tracker layers.
# Exit 0 iff every layer passes.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DOC_DIR="$(dirname "$HERE")"
MANIFEST="$DOC_DIR/verify-manifest.json"
HARNESS_ROOT="$(git rev-parse --show-toplevel)"

PR_URL="$(python3 -c "import json;print(json.load(open('$MANIFEST'))['pr'])")"
PR_NUM="${PR_URL##*/}"

echo "== resolving PR head and base =="
git -C "$HARNESS_ROOT" fetch https://github.com/sgl-project/rbg.git "pull/$PR_NUM/head" main 2>&1 | tail -2
HEAD_SHA="$(git -C "$HARNESS_ROOT" rev-parse FETCH_HEAD^{commit} 2>/dev/null || true)"
# fetch writes the last ref to FETCH_HEAD; fetch head explicitly to be safe
git -C "$HARNESS_ROOT" fetch https://github.com/sgl-project/rbg.git "pull/$PR_NUM/head" 2>/dev/null
HEAD_SHA="$(git -C "$HARNESS_ROOT" rev-parse FETCH_HEAD^{commit})"
BASE_SHA="$(git -C "$HARNESS_ROOT" rev-parse origin/main)"
echo "head=$HEAD_SHA base=$BASE_SHA"

LAST="$(cat "$DOC_DIR/.last-reviewed" 2>/dev/null || true)"
if [ -n "$LAST" ]; then
  echo "incremental delta since last review:"
  git -C "$HARNESS_ROOT" log --oneline "$LAST..$HEAD_SHA" | head -20 || true
  echo "advance with: echo $HEAD_SHA > docs/verification/chart-deprecation-webhooks-codex/.last-reviewed"
fi

echo "== building verification target: merge($HEAD_SHA, $BASE_SHA) =="
WT="$(mktemp -d /tmp/pr552-reverify.XXXXXX)"
git -C "$HARNESS_ROOT" worktree add --detach "$WT" "$BASE_SHA" >/dev/null 2>&1
cleanup() { git -C "$HARNESS_ROOT" worktree remove --force "$WT" >/dev/null 2>&1 || true; }
trap cleanup EXIT
git -C "$WT" -c user.email=review@localhost -c user.name=review merge --no-ff --no-edit "$HEAD_SHA" -m "re-verify merge" >/dev/null
echo "merged tree at $WT"

echo "== grafting harness =="
for p in \
  test/chart/webhook_manifests_test.go \
  test/chart/inventory_command_test.go \
  test/chart/testdata/inventory \
  test/envtest/testcase/deprecationdocs ; do
  rm -rf "$WT/$p"
  mkdir -p "$WT/$(dirname "$p")"
  cp -r "$HARNESS_ROOT/$p" "$WT/$p"
done

rc=0

echo "== L1 unit: chart render/parity/inventory fixtures =="
KC="$(mktemp)"; printf 'apiVersion: v1\nkind: Config\n' > "$KC"; chmod 600 "$KC"
( cd "$WT" && KUBECONFIG="$KC" go test ./test/chart/ -count=1 -v ) && echo "L1 PASS" || { echo "L1 FAIL"; rc=1; }

echo "== L2 integration: envtest gate contract (compat / strict / nowebhook control) =="
# envtest assets are gitignored; resolve them from the harness checkout.
KBA="$([ -x "$HARNESS_ROOT/bin/setup-envtest" ] && "$HARNESS_ROOT/bin/setup-envtest" use 1.31.0 --bin-dir "$HARNESS_ROOT/bin" -p path 2>/dev/null || echo "$HARNESS_ROOT/bin/k8s/1.31.0-linux-amd64")"
for MODE in compat strict nowebhook; do
  ( cd "$WT" && RBGS_DOCS_SUITE_MODE=$MODE KUBEBUILDER_ASSETS="$KBA" go test ./test/envtest/testcase/deprecationdocs/ -count=1 ) \
    && echo "L2[$MODE] PASS" || { echo "L2[$MODE] FAIL"; rc=1; }
done

echo "== tracker: default image serves every rendered webhook path =="
( cd "$WT" && bash "$DOC_DIR/scripts/check-default-image-paths.sh" ) && echo "TRACKER PASS" || { echo "TRACKER FAIL"; rc=1; }

echo "== tracker: NOTES.txt guidance matrix =="
( cd "$HARNESS_ROOT" && CHART_OVERRIDE="$WT/deploy/helm/rbgs" bash "$DOC_DIR/scripts/check-notes.sh" ) && echo "NOTES PASS" || { echo "NOTES FAIL"; rc=1; }

echo
if [ "$rc" -eq 0 ]; then echo "ALL LAYERS PASS"; else echo "SOME LAYERS FAILED"; fi
exit "$rc"
