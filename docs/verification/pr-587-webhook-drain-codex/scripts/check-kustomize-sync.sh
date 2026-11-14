#!/usr/bin/env bash
# check-kustomize-sync.sh — verify deploy/kubectl/manifests.yaml is byte-identical
# to `kustomize build config/default` on the ref under test (the PR claims the
# file was regenerated, and CI's `make manifests`+git-status gate enforces it;
# this re-checks it standalone). Run from the repo root. Exit 0 iff in sync.
set -euo pipefail
[ -d config/default ] || { echo "run from repo root" >&2; exit 2; }
KUSTOMIZE="${KUSTOMIZE:-}"
if [ -z "$KUSTOMIZE" ]; then
  if [ -x bin/kustomize ]; then KUSTOMIZE=bin/kustomize;
  elif command -v kustomize >/dev/null; then KUSTOMIZE=kustomize;
  else
    echo "kustomize not found; installing pinned v5.5.0 into bin/ via go" >&2
    GOBIN="$PWD/bin" go install sigs.k8s.io/kustomize/kustomize/v5@v5.5.0 >&2
    KUSTOMIZE=bin/kustomize
  fi
fi
TMP="$(mktemp)"; trap 'rm -f "$TMP"' EXIT
"$KUSTOMIZE" build config/default > "$TMP"
if diff -q "$TMP" deploy/kubectl/manifests.yaml >/dev/null; then
  echo "OK: kustomize build config/default == deploy/kubectl/manifests.yaml (byte-identical)"
else
  echo "FAIL: deploy/kubectl/manifests.yaml is out of sync with config/default:" >&2
  diff "$TMP" deploy/kubectl/manifests.yaml | head -40
  exit 1
fi
