#!/usr/bin/env bash
# run-integration.sh — run the PR's own prototype correctness suite
# (test/podcacheunion, //go:build podcacheunion) as the round-2 integration
# layer, in both WatchListClient configurations, emitting ONLY go test -json
# events on stdout so re-verify.sh (which merges stderr into the captured
# stream before jq-parsing it) can parse the result. Diagnostics go to a log
# file next to this script (run-integration.log) and never to stdout/stderr.
#
# Resolves envtest control-plane binaries:
#   1. $KUBEBUILDER_ASSETS if set
#   2. bin/k8s/1.31.0-<GOOS>-<GOARCH> if present
#   3. bin/setup-envtest (installed on demand via `go install`)
# If no assets can be resolved, no JSON is emitted and re-verify reports
# HARNESS-UPDATE for this layer.
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$HERE/../../../.." && pwd)"
LOG="$HERE/run-integration.log"
: > "$LOG"

run_suites() { # everything here writes to stdout+stderr, captured by the caller
  cd "$REPO" || exit 1

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

  KUBE_FEATURE_WatchListClient=false \
  RBG_POD_UNION_APISERVER_FEATURE_GATES= \
  KUBE_CACHE_MUTATION_DETECTOR=true \
  go test -mod=vendor -tags=podcacheunion ./test/podcacheunion \
    -count=1 -timeout=10m -json

  KUBE_FEATURE_WatchListClient=true \
  RBG_POD_UNION_APISERVER_FEATURE_GATES=WatchList=true \
  KUBE_CACHE_MUTATION_DETECTOR=true \
  go test -mod=vendor -tags=podcacheunion ./test/podcacheunion \
    -count=1 -timeout=10m -json
}

CAPTURE="$(mktemp)"
run_suites > "$CAPTURE" 2>&1 || true
# Only the go test -json event lines (one JSON object per line) reach stdout;
# diagnostics and any build failure output stay in the log.
grep '^{' "$CAPTURE" || true
cp "$CAPTURE" "$LOG"
rm -f "$CAPTURE"
