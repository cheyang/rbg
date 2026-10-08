#!/usr/bin/env bash
# Run every layer of the PR #389 autoscaling-docs verification and refresh results/.
# Live (L3) layer intentionally not included — see README.md.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VDIR="$(cd "$HERE/.." && pwd)"
ROOT="$(cd "$VDIR/../../.." && pwd)"
RES="$VDIR/results"
mkdir -p "$RES"

echo "== L1 static checks"
( cd "$ROOT" && python3 -I "$HERE/run_l1_checks.py" ) | tee "$RES/l1-static-checks.txt"

echo "== L1 live link/registry checks"
( cd "$ROOT" && bash "$HERE/run_l1_network_checks.sh" )

echo "== L1 behavior: repo RBGSA controller tests"
( cd "$ROOT" && go test ./internal/controller/workloads/ -run 'ScalingAdapter|ReadyReplicasSyncWithScale|RBGScalingAdapterPredicate' -count=1 -v ) \
  | tee "$RES/unit-rbgsa-controller.txt" | tail -2

echo "== L2 envtest admission of doc manifests"
if [ -z "${KUBEBUILDER_ASSETS:-}" ]; then
  d="$HOME/.local/share/kubebuilder-envtest/k8s/1.36.2-linux-amd64"
  [ -d "$d" ] && export KUBEBUILDER_ASSETS="$d"
fi
( cd "$ROOT" && go test ./docs/verification/autoscaling-docs/harness/ -v -timeout 10m ) \
  | tee "$RES/l2-envtest.txt" | grep -E "^(--- (PASS|FAIL)|ok|FAIL|PASS)"
echo "done — results in $RES"
