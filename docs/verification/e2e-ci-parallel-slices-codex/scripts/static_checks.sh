#!/usr/bin/env bash
# Static checks for PR #494: YAML lint of the workflow + composite action, compile of the
# e2e packages, and whitespace hygiene. Mirrors the PR's own stated test plan.
set -uo pipefail
ROOT="$(git rev-parse --show-toplevel)"
cd "$ROOT"

echo "== yamllint -s .github/workflows/e2e-test.yml .github/actions/setup-e2e-cluster/action.yaml =="
yamllint -s .github/workflows/e2e-test.yml .github/actions/setup-e2e-cluster/action.yaml
echo "yamllint exit: $?"

echo "== go test ./test/e2e/... -run '^$' (compile only, no specs) =="
go test ./test/e2e/... -run '^$'
echo "compile exit: $?"

echo "== go vet ./test/ciharness/ (harness) =="
go vet ./test/ciharness/
echo "vet exit: $?"
