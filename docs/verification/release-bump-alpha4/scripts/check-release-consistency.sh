#!/usr/bin/env bash
# Contract checks for the 0.9.0-alpha.4 release-manifest bump (PR #590).
# Polarity: CONTRACT. Fails on base (1fc69f70), passes on PR head (76963d62).
# Usage: bash docs/verification/release-bump-alpha4/scripts/check-release-consistency.sh [repo-root]
set -euo pipefail
ROOT="${1:-$(git rev-parse --show-toplevel)}"
NEW_TAG="v0.9.0-1fc69f70"; OLD_TAG="v0.9.0-73e05db4"
NEW_CHART="0.9.0-alpha.4"; NEW_APP="0.9.0-1fc69f70"
fail=0
chk() { if eval "$2"; then echo "PASS: $1"; else echo "FAIL: $1"; fail=1; fi }

cd "$ROOT"
chk "C1 no stale $OLD_TAG in release files" \
  "! grep -rq '$OLD_TAG' config/manager/kustomization.yaml deploy/helm/rbgs/ deploy/kubectl/manifests.yaml"
chk "C2a kustomization pins $NEW_TAG" \
  "grep -q 'newTag: $NEW_TAG' config/manager/kustomization.yaml"
chk "C2b values.yaml pins $NEW_TAG twice (controller + crdUpgrade)" \
  "[ \$(grep -c 'tag: \"$NEW_TAG\"' deploy/helm/rbgs/values.yaml) -eq 2 ]"
chk "C2c manifests.yaml pins controller $NEW_TAG" \
  "grep -q 'image: rolebasedgroup/rbgs-controller:$NEW_TAG' deploy/kubectl/manifests.yaml"
chk "C2d Chart.yaml version=$NEW_CHART appVersion=$NEW_APP" \
  "grep -q '^version: $NEW_CHART' deploy/helm/rbgs/Chart.yaml && grep -q '^appVersion: \"$NEW_APP\"' deploy/helm/rbgs/Chart.yaml"
if command -v kustomize >/dev/null 2>&1 || [ -x bin/kustomize ]; then
  KZ=$(command -v kustomize || echo bin/kustomize)
  chk "C3 checked-in manifests.yaml == kustomize build config/default" \
    "diff -q deploy/kubectl/manifests.yaml <('$KZ' build config/default) >/dev/null"
else
  echo "SKIP: C3 (kustomize not found; install v5.5.0)"
fi
if command -v helm >/dev/null 2>&1; then
  chk "C4 helm template renders both images with $NEW_TAG" \
    "helm template rbgs deploy/helm/rbgs | grep -q 'rbgs-upgrade-crd:$NEW_TAG' && helm template rbgs deploy/helm/rbgs | grep -q 'rbgs-controller:$NEW_TAG'"
else
  echo "SKIP: C4 (helm not found)"
fi
exit $fail
