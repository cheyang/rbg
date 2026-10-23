#!/usr/bin/env bash
# Re-run the PR #508 verification harness against the current PR head.
# Resolves the PR head from verify-manifest.json; the delta start is
# docs/verification/pr508-rollback-replacement-state/.last-reviewed.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
repo_root="$(git -C "$here" rev-parse --show-toplevel)"
pr_url="$(python3 -c "import json;print(json.load(open('$here/verify-manifest.json'))['pr'])")"
pr_number="${pr_url##*/}"

cd "$repo_root"
remote_url="$(git remote get-url upstream 2>/dev/null || true)"
if [[ -z "$remote_url" ]]; then
  echo "upstream remote not configured; fetching pull/$pr_number/head from origin" >&2
  git fetch origin "pull/$pr_number/head:pr-$pr_number"
else
  git fetch upstream "pull/$pr_number/head:pr-$pr_number"
fi

head="$(git rev-parse "pr-$pr_number")"
echo "PR head: $head"
if [[ -f "$here/.last-reviewed" ]]; then
  echo "Delta start: $(cat "$here/.last-reviewed")"
  git log --oneline "$(cat "$here/.last-reviewed")..$head" || true
fi

git checkout "$head" 2>/dev/null || git checkout -B "pr-$pr_number-verify" "$head"

# Overlay the reviewer harness (tracked on the verify branch, not in the PR)
# onto the checked-out head so the contracts run against current PR code.
VERIFY_BRANCH="verify/pr508-rollback-replacement-state"
git checkout "$VERIFY_BRANCH" -- \
  pkg/reconciler/roleinstanceset/statefulmode/rollback_replacement_verification_test.go \
  2>/dev/null || echo "WARN: harness overlay failed; is $VERIFY_BRANCH present?" >&2

echo
echo "== unit harness =="
go test -mod=vendor -count=1 -v -run 'TestVerifyPR508' ./pkg/reconciler/roleinstanceset/statefulmode/

echo
echo "== full package sweep (harness coexistence) =="
go test -mod=vendor -count=1 -timeout 300s ./pkg/reconciler/roleinstanceset/statefulmode/...

if [[ -n "${KUBEBUILDER_ASSETS:-}" ]]; then
  echo
  echo "== envtest stateful suite =="
  go test -mod=vendor -count=1 -timeout 10m ./test/envtest/testcase/stateful/
else
  echo
  echo "KUBEBUILDER_ASSETS not set; skipping envtest layer (unit verdicts above stand alone)"
fi
