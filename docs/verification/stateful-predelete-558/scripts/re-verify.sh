#!/usr/bin/env bash
# Re-verify the PR #558 findings harness against the current PR head.
# Usage: bash docs/verification/stateful-predelete-558/scripts/re-verify.sh [<fixed-ref>]
# With no ref, the current PR head is fetched from the upstream repo
# (refs/pull/558/head) so this works from any clone.
set -uo pipefail

HERE="$(cd "$(dirname "$0")/.." && pwd)"
MANIFEST="$HERE/verify-manifest.json"
TOPIC_DIR="$(dirname "$HERE")"
REPO_ROOT="$(git rev-parse --show-toplevel)"
PR_URL="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["pr"])' "$MANIFEST")"
PR_NUM="${PR_URL##*/}"
REPO_URL="$(dirname "$(dirname "$PR_URL")")"
SCRATCH="$(mktemp -d)"
trap 'git worktree remove --force "$SCRATCH/work" 2>/dev/null; rm -rf "$SCRATCH"' EXIT

echo "== harness: $TOPIC_DIR"
echo "== pr: $PR_URL"

if [ -n "${1:-}" ]; then
  TARGET_REF="$1"
else
  echo "== fetching current PR head"
  git fetch "${REPO_URL}.git" "pull/${PR_NUM}/head" 2>/dev/null
  TARGET_REF="FETCH_HEAD"
fi
TARGET_SHA="$(git rev-parse "$TARGET_REF")"
echo "== target: $TARGET_SHA"

git worktree add --detach "$SCRATCH/work" "$TARGET_SHA" >/dev/null 2>&1 || { echo "FATAL: cannot check out $TARGET_SHA"; exit 2; }

# Graft the additive harness files onto the target ref.
cp "$REPO_ROOT/pkg/reconciler/roleinstanceset/statefulmode/verify_predelete_558_test.go" \
   "$SCRATCH/work/pkg/reconciler/roleinstanceset/statefulmode/" \
   || { echo "FATAL: harness file missing on this branch"; exit 2; }
cp "$REPO_ROOT/pkg/inplace/instance/lifecycle/lifecycle_announce_conflict_test.go" \
   "$SCRATCH/work/pkg/inplace/instance/lifecycle/" \
   || { echo "FATAL: harness file missing on this branch"; exit 2; }

cd "$SCRATCH/work" || exit 2

echo "== running unit layer"
go test -mod=vendor -count=1 -timeout 10m \
  -run 'TestVerify558|TestStatefulRollingUpdateAndScaleDownWaitForPreDeleteHook' \
  ./pkg/reconciler/roleinstanceset/statefulmode/ ./pkg/inplace/instance/lifecycle/ \
  > "$HERE/results/reverify-unit.txt" 2>&1
grep -E "^(--- |ok|FAIL)" "$HERE/results/reverify-unit.txt" | sed 's/^/   /'

declare -A VERDICT
report() { # test-name polarity
  if grep -q -- "--- FAIL: $1 " "$HERE/results/reverify-unit.txt"; then
    if [ "$2" = canary ]; then VERDICT[$1]="Fixed (canary flipped red - invert it)"; else VERDICT[$1]="Still-broken"; fi
  else
    if [ "$2" = canary ]; then VERDICT[$1]="Still-broken (canary green)"; else VERDICT[$1]="Passed"; fi
  fi
}
report TestVerify558RegretRevertsPreparingDeleteState contract
report TestVerify558MarkNotReadyAnnounceSingleCall contract
report TestVerify558RegretLeavesInstanceUnavailableCanary canary
report TestVerify558RegretOrderedReadyFreezesScaleUpCanary canary
report TestVerify558OrderedReadyMarkNotReadyReleaseResumes contract
report TestStatefulRollingUpdateAndScaleDownWaitForPreDeleteHook contract

echo
echo "== per-finding verdicts (head $TARGET_SHA)"
for k in TestVerify558RegretRevertsPreparingDeleteState \
         TestVerify558RegretLeavesInstanceUnavailableCanary \
         TestVerify558RegretOrderedReadyFreezesScaleUpCanary \
         TestVerify558MarkNotReadyAnnounceSingleCall \
         TestVerify558OrderedReadyMarkNotReadyReleaseResumes \
         TestStatefulRollingUpdateAndScaleDownWaitForPreDeleteHook; do
  echo "   ${k}: ${VERDICT[$k]}"
done

BLOCKERS=0
for k in TestVerify558RegretRevertsPreparingDeleteState TestVerify558MarkNotReadyAnnounceSingleCall; do
  [ "${VERDICT[$k]}" = "Still-broken" ] && BLOCKERS=$((BLOCKERS+1))
done
if [ $BLOCKERS -gt 0 ]; then
  echo "== result: $BLOCKERS finding(s) still broken"
  exit 1
fi
echo "== result: all contract findings green; invert the canaries that flipped"
exit 0
