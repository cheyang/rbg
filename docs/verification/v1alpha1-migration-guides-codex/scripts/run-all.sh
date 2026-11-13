#!/usr/bin/env bash
# Run the full PR #586 verification harness against the code in the current
# checkout (expected: the PR head, with the harness grafted).
set -uo pipefail
cd "$(git rev-parse --show-toplevel)"

echo "== unit: gate semantics (api/workloads/v1alpha2) =="
go test ./api/workloads/v1alpha2/ -run 'TestDocClaim' -count=1 -v
echo
echo "== unit: controller claims (pkg/reconciler) =="
go test ./pkg/reconciler/ -run 'TestDocClaim' -count=1 -v
echo
echo "== unit: guide content (test/verification/pr586guides; needs jq) =="
go test ./test/verification/pr586guides/ -count=1 -v
echo
echo "== integration: chart toggle suite =="
go test ./test/chart/ -count=1
echo
echo "== integration: helm lint (needs helm) =="
helm lint deploy/helm/rbgs
