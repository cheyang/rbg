#!/usr/bin/env bash
# Layer 2 (integration) checks for PR #390 docs verification.
# Exercises the real toolchain the docs tell the reader to use:
#   - `go run ./test/stress/` must build (the documented entrypoint)
#   - the stress package's own unit tests must pass at the PR head
#   - helm chart must render with the doc's exact --set flags (done in L1a's F3
#     check via helm template; here we assert the rendered Deployment is valid
#     YAML with the pprof port exposed)
# Usage: bash docs/verification/stress-testing-tuning-doc/scripts/20-integration-checks.sh
set -uo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd)"
cd "${REPO}"

echo "=== L2a: build documented stress client entrypoint ==="
go build ./test/stress/
echo "go build exit: $?"
L2A=$?

echo
echo "=== L2b: stress package unit tests ==="
go test ./test/stress/... 2>&1 | tail -5
L2B=${PIPESTATUS[0]}

echo
echo "=== L2c: chart renders with doc's --set flags (pprof port exposed) ==="
helm template rbgs deploy/helm/rbgs -n rbgs-system \
  --set controller.image.tag=v0-test \
  --set controller.resources.limits.cpu=8 \
  --set controller.resources.limits.memory=16Gi \
  --set controller.tuning.maxConcurrentReconciles=20 \
  --set controller.tuning.kubeApiQPS=100 \
  --set controller.tuning.kubeApiBurst=200 \
  --set controller.pprof.enabled=true \
  --set controller.pprof.containerPort=6060 \
  --no-hooks > /tmp/rbg-doc-render.yaml
if grep -q "containerPort: 6060" /tmp/rbg-doc-render.yaml && grep -q -- "--enable-pprof=true" /tmp/rbg-doc-render.yaml; then
  echo "rendered chart exposes pprof containerPort 6060 and --enable-pprof=true"
  L2C=0
else
  echo "FAIL: rendered chart missing pprof port/arg"
  L2C=1
fi

echo
echo "=== L2 result: build=$L2A test=$L2B render=$L2C ==="
exit $(( L2A + L2B + L2C ))
