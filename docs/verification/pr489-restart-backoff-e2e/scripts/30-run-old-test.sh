#!/usr/bin/env bash
# F2 canary (base branch, without the PR): the pre-PR spec must FAIL by timing
# out at "pods should be recreated with new UIDs" after the second injected
# failure, because the patched Failed phase evaporates (see P0).
# Checks out the merge-base in a throwaway worktree and runs the same focus.
set -euo pipefail
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd)"
RESULTS="$REPO_ROOT/docs/verification/pr489-restart-backoff-e2e/results"
BASE="${BASE_REF:-7ed1860c2841dd911ac8c01e1458a6b9ce5ee49a}" # merge-base of PR #489
WT="$(mktemp -d)/rbg-base"
mkdir -p "$RESULTS"
git -C "$REPO_ROOT" worktree add "$WT" "$BASE"
trap 'git -C "$REPO_ROOT" worktree remove --force "$WT" >/dev/null 2>&1 || true' EXIT
cd "$WT"
set +e
go test ./test/e2e/ -run TestE2E -v -ginkgo.v \
  --ginkgo.focus='RecreateRoleInstanceOnPodRestart with backoff delays second recreation' \
  -timeout 15m 2>&1 | tee "$RESULTS/runB-old-test.log"
rc=${PIPESTATUS[0]}
set -e
echo "[F2] old-test exit=$rc (non-zero with the 150s recreation timeout = bug reproduced)"
