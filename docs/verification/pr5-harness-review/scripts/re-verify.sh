#!/usr/bin/env bash
# Re-verify the F1 claim for cheyang/rbg PR #5 (reviewer B / Codex harness):
# "the harness branch does not compile its own added test packages against its
# own base (pr394-base)". Contract: on a correctly-based branch this vet passes.
# Usage: run from a checkout of verify/pr394-harness-review-codex; no args.
set -euo pipefail
git rev-parse --git-dir >/dev/null
echo "== go vet the two packages PR #5 adds test files to =="
if go vet ./pkg/reconciler/roleinstance/sync/ ./test/envtest/testcase/restart_policy/ ; then
  echo "F1: FIXED (branch compiles its harness)"
  exit 0
else
  echo "F1: STILL-PRESENT (harness does not compile on its own base)"
  exit 1
fi
