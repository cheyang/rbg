#!/usr/bin/env bash
# Re-verify PR sgl-project/rbg#514 harness (reviewer: codex).
# Runs the unit + integration (no-cluster) layers against the current PR head.
# Usage: bash docs/verification/durable-pod-failure-injection-codex/scripts/re-verify.sh [<ref>]
#   <ref> defaults to the current PR head fetched from the manifest's pr URL.
set -u

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$HERE/../../../.." && pwd)"
MANIFEST="$HERE/../verify-manifest.json"
cd "$REPO_ROOT"

REF="${1:-}"
if [ -z "$REF" ]; then
  PR_URL=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["pr"])' "$MANIFEST")
  PR_NUM=${PR_URL##*/}
  git fetch https://github.com/sgl-project/rbg.git "pull/${PR_NUM}/head" >/dev/null 2>&1
  REF=$(git rev-parse FETCH_HEAD)
fi
echo "== re-verify against ref: $REF"

# Graft the harness tripwire test onto the target ref via a temp worktree.
WT=$(mktemp -d)
git worktree add --detach "$WT" "$REF" >/dev/null 2>&1
cp "$REPO_ROOT/pkg/reconciler/roleinstance/sync/e2e_anchor_assumptions_review_test.go" \
   "$WT/pkg/reconciler/roleinstance/sync/"
cd "$WT"
trap 'cd /; git worktree remove --force "$WT" >/dev/null 2>&1 || true' EXIT

status=0

echo "--- F1 (contract): vendor-mode build must succeed (fixed iff 'go mod vendor' committed)"
if go build ./... >f1.log 2>&1; then
  echo "F1: Fixed (go build ./... clean in vendor mode)"
else
  echo "F1: Still-broken:"; head -3 f1.log
  status=1
fi

echo "--- P1a (contract): PR helper unit test"
if go test -mod=mod ./test/utils -count=1 -run TestPodFailureInjection >p1a.log 2>&1; then
  echo "P1a: PASS"
else
  echo "P1a: FAIL"; tail -5 p1a.log; status=1
fi

echo "--- P1b (contract): controller anchor identities the e2e specs rely on"
if go test -mod=mod -run 'TestCalculateRestartDelay|TestShouldRecreateInstance$|TestCheckRestartBackoff|TestReviewAnchor' ./pkg/reconciler/roleinstance/sync/ -count=1 >p1b.log 2>&1; then
  echo "P1b: PASS"
else
  echo "P1b: FAIL (anchor identity drifted; the migrated e2e specs' assumptions changed)"; tail -5 p1b.log; status=1
fi

echo "--- P1c (integration, no cluster): e2e suite compiles and spec tree constructs"
if go test -c -mod=mod -o /tmp/rbg-reverify-e2e.test ./test/e2e >p1c.log 2>&1 \
   && /tmp/rbg-reverify-e2e.test -ginkgo.dry-run -ginkgo.label-filter='update && !rollout && !volcano && !scheduler-plugins && !v1alpha1' >>p1c.log 2>&1; then
  echo "P1c: PASS"
else
  echo "P1c: FAIL"; tail -5 p1c.log; status=1
fi

LAST="$HERE/../.last-reviewed"
[ -f "$LAST" ] && echo "== incremental review range: $(cat "$LAST")..$REF"
exit $status
