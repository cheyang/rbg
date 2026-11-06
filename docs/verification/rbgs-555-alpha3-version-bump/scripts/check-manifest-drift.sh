#!/usr/bin/env bash
# check-manifest-drift.sh — assert deploy/kubectl/manifests.yaml is exactly what
# `kustomize build config/default` produces with the checked-in kustomization
# (the regeneration step of `make manifests` / `make build-installer`).
#
# Requires: kustomize (v5.5.0 to match Makefile KUSTOMIZE_VERSION). If not found,
# installs it to a temp GOBIN via `go install`.
#
# Usage: bash check-manifest-drift.sh   (run from anywhere inside the repo)
set -euo pipefail

KUSTOMIZE_BIN="$(command -v kustomize || true)"
if [ -z "$KUSTOMIZE_BIN" ] && [ -x "$(git rev-parse --show-toplevel)/bin/kustomize" ]; then
  KUSTOMIZE_BIN="$(git rev-parse --show-toplevel)/bin/kustomize"
fi
if [ -z "$KUSTOMIZE_BIN" ]; then
  echo "kustomize not found; installing sigs.k8s.io/kustomize/kustomize/v5@v5.5.0 to a temp dir"
  TMPBIN="$(mktemp -d)"
  GOBIN="$TMPBIN" go install sigs.k8s.io/kustomize/kustomize/v5@v5.5.0
  KUSTOMIZE_BIN="$TMPBIN/kustomize"
fi

REPO_ROOT="$(git rev-parse --show-toplevel)"
cd "$REPO_ROOT"
"$KUSTOMIZE_BIN" build config/default > /tmp/regen-manifests.$$.yaml
if diff -q /tmp/regen-manifests.$$.yaml deploy/kubectl/manifests.yaml >/dev/null; then
  echo "OK: deploy/kubectl/manifests.yaml is byte-identical to kustomize build config/default ($(wc -l < deploy/kubectl/manifests.yaml) lines)"
  rm -f /tmp/regen-manifests.$$.yaml
else
  echo "DRIFT: deploy/kubectl/manifests.yaml differs from kustomize build config/default:"
  diff /tmp/regen-manifests.$$.yaml deploy/kubectl/manifests.yaml | head -30
  rm -f /tmp/regen-manifests.$$.yaml
  exit 1
fi
