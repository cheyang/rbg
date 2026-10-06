#!/usr/bin/env bash
# F1 contract test (PR head): the fixed backoff e2e must PASS on a real cluster.
# Runs the single focused spec from a checkout of the PR head.
set -euo pipefail
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd)"
RESULTS="$REPO_ROOT/docs/verification/pr489-restart-backoff-e2e/results"
mkdir -p "$RESULTS"
cd "$REPO_ROOT"
go test ./test/e2e/ -run TestE2E -v -ginkgo.v \
  --ginkgo.focus='RecreateRoleInstanceOnPodRestart with backoff delays second recreation' \
  -timeout 15m 2>&1 | tee "$RESULTS/runA-new-test.log"
