#!/usr/bin/env bash
# 20-lint-diff.sh — F-lint-gocyclo evidence: run golangci-lint v2.13.1 with the repo config
# on the tree this script is executed in, and diff against the recorded base result.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LINT="${LINT:-}"
for cand in /home/ecs-user/.pr-debate/homes/claude/tools/golangci-lint "$(go env GOPATH)/bin/golangci-lint" "$(command -v golangci-lint || true)"; do
  if [ -n "$cand" ] && [ -x "$cand" ]; then LINT="$cand"; break; fi
done
if [ -z "$LINT" ]; then
  echo "golangci-lint not found; install v2.13.1 (repo Makefile pins GOLANGCI_LINT_VERSION)"
  exit 3
fi
cd "$(git rev-parse --show-toplevel 2>/dev/null || echo "$HERE/../../../..")"
"$LINT" run ./internal/controller/workloads/... ./pkg/reconciler/roleinstanceset/... 2>&1
status=$?
echo "---- recorded base result (a0b11e6, same command): ----"
cat "$HERE/../results/lint-base.txt"
exit $status
