#!/usr/bin/env bash
# Contract test for carried finding R2-F1 (PR #530 round 2), re-checked on PR #545.
#
# Claim: the podcacheunion fixture starts an ISOLATED envtest control plane even
# when the caller environment would otherwise escape to a kubeconfig cluster
# (USE_EXISTING_CLUSTER=true plus a kubeconfig path that must not be used).
#
# Contract polarity: PASS (exit 0) when the fixture prints its isolation banner
# and the fast correctness subset passes; FAIL if the harness regresses and
# envtest falls back to USE_EXISTING_CLUSTER / the caller's kubeconfig.
#
# Deliberately hostile environment:
#   USE_EXISTING_CLUSTER=true, KUBECONFIG=/nonexistent/kubeconfig
# plus whatever ~/.kube/config happens to exist on the machine.
set -euo pipefail

REPO_ROOT=$(cd "$(dirname "$0")/../../../.." && pwd) # scripts/ -> topic -> verification -> docs -> repo root
cd "$REPO_ROOT"

ASSETS=${KUBEBUILDER_ASSETS:-/tmp/rbg-assets/k8s/1.31.0-linux-amd64}
if [ ! -x "$ASSETS/kube-apiserver" ]; then
  echo "isolation-check: no envtest assets at $ASSETS; set KUBEBUILDER_ASSETS" >&2
  exit 2
fi

LOG=$(mktemp)
trap 'rm -f "$LOG"' EXIT

USE_EXISTING_CLUSTER=true \
KUBECONFIG=/nonexistent/kubeconfig \
KUBEBUILDER_ASSETS="$ASSETS" \
KUBE_FEATURE_WatchListClient=false \
RBG_POD_UNION_APISERVER_FEATURE_GATES= \
KUBE_CACHE_MUTATION_DETECTOR=true \
CGO_ENABLED=1 \
go test -mod=vendor -tags=podcacheunion ./test/podcacheunion \
  -run '^TestTransformIsIdempotent$|^TestMedianRange$|^TestWatchStreamWrapper' \
  -count=1 -v -timeout=10m > "$LOG" 2>&1

if ! grep -q "UNION_ENVTEST: isolated Kubernetes" "$LOG"; then
  echo "isolation-check: FAILED — fixture did not start an isolated control plane under USE_EXISTING_CLUSTER=true" >&2
  grep -E "UNION_ENVTEST|FAIL|isolated" "$LOG" >&2 || true
  exit 1
fi
echo "isolation-check: PASS — isolated control plane started under hostile USE_EXISTING_CLUSTER=true/KUBECONFIG"
