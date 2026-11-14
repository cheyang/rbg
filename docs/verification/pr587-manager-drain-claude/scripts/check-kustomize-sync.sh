#!/usr/bin/env bash
# check-kustomize-sync.sh — assert that the committed deploy/kubectl/manifests.yaml
# is exactly what `kustomize build config/default` produces.
#
# Uses `kubectl kustomize` as a stand-in for the kustomize CLI (same library).
# Fidelity of the stand-in was validated on the merge-base 1fc69f70 where the
# committed file is reproduced byte for byte (see results/kustomize-sync-base.txt).
#
# Usage: run from the repo root (or any dir inside the repo).
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT
kubectl kustomize config/default > "$tmp"
if diff -u deploy/kubectl/manifests.yaml "$tmp"; then
  echo "KUSTOMIZE-SYNC: identical"
else
  echo "KUSTOMIZE-SYNC: DRIFT — regenerate with 'make manifests' (or build-installer)" >&2
  exit 1
fi
