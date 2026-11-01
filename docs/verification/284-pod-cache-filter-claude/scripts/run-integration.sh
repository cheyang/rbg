#!/usr/bin/env bash
# run-integration.sh — run the PR's own prototype correctness suite
# (test/podcacheunion, //go:build podcacheunion) as the round-2 integration
# layer, in both WatchListClient configurations, emitting go test -json on
# stdout (logs to stderr) so re-verify.sh can parse it.
#
# Resolves envtest control-plane binaries:
#   1. $KUBEBUILDER_ASSETS if set
#   2. bin/k8s/1.31.0-<GOOS>-<GOARCH> if present
#   3. bin/setup-envtest (installed on demand via `go install`)
# If no assets can be resolved, prints a diagnostic to stderr and exits 0
# without JSON; re-verify then reports HARNESS-UPDATE for this layer.
set -uo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
cd "$REPO"

ASSETS="${KUBEBUILDER_ASSETS:-}"
if [ -z "$ASSETS" ]; then
  guess="$REPO/bin/k8s/1.31.0-$(go env GOOS)-$(go env GOARCH)"
  [ -x "$guess/kube-apiserver" ] && ASSETS="$guess"
fi
if [ -z "$ASSETS" ]; then
  if [ ! -x "$REPO/bin/setup-envtest" ]; then
    echo "run-integration: installing setup-envtest" >&2
    GOBIN="$REPO/bin" go install sigs.k8s.io/controller-runtime/tools/setup-envtest@v0.24.1 >&2 || true
  fi
  if [ -x "$REPO/bin/setup-envtest" ]; then
    ASSETS="$("$REPO/bin/setup-envtest" use 1.31.0 --bin-dir "$REPO/bin" -p path 2>/dev/null || true)"
  fi
fi
if [ -z "$ASSETS" ] || [ ! -x "$ASSETS/kube-apiserver" ]; then
  echo "run-integration: no envtest assets; set KUBEBUILDER_ASSETS or run 'make setup-envtest'" >&2
  exit 0
fi
echo "run-integration: assets = $ASSETS" >&2

export CGO_ENABLED=1
export USE_EXISTING_CLUSTER=false
export KUBEBUILDER_ASSETS="$ASSETS"

run_suite() { # $1 = KUBE_FEATURE_WatchListClient, $2 = server gates
  KUBE_FEATURE_WatchListClient="$1" \
  RBG_POD_UNION_APISERVER_FEATURE_GATES="$2" \
  KUBE_CACHE_MUTATION_DETECTOR=true \
  go test -mod=vendor -tags=podcacheunion ./test/podcacheunion \
    -count=1 -timeout=10m -json
}

run_suite false ""
run_suite true "WatchList=true"
