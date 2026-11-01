#!/usr/bin/env bash
# check-lint.sh — repro for finding F2 (PR-introduced CI lint failure).
#
# Runs the repo-pinned golangci-lint (Makefile: GOLANGCI_LINT_VERSION ?= v2.13.1)
# on the packages this PR touches. On the PR head (d9d7c4cf) it must report:
#   pkg/reconciler/roleinstance/lifecycle_readiness_test.go:45:5: prealloc
# which is the exact failure that made the "lint" (Project Check) CI job fail.
#
# Requires: golangci-lint. Install the pinned version with
#   GOBIN=<dir> go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.1
set -uo pipefail

LINT="$(command -v golangci-lint || echo /tmp/rbg-bin/golangci-lint)"
if [ ! -x "$LINT" ]; then
  echo "check-lint: golangci-lint not found (install the pinned v2.13.1, see header)" >&2
  exit 3
fi
"$LINT" run \
  pkg/reconciler/roleinstance/... \
  pkg/inplace/instance/lifecycle/... \
  pkg/inplace/instance/readiness/...
status=$?
echo "check-lint: golangci-lint exit=$status (0 = clean, 1 = findings present)"
exit $status
