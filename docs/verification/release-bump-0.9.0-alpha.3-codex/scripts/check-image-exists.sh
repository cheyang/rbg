#!/usr/bin/env bash
# Informational (network) check: the images pinned by this release bump exist on
# Docker Hub. Not part of re-verify.sh gating; CI release tests already proved
# pullability. Usage: check-image-exists.sh [tag]
set -euo pipefail
TAG="${1:-${NEW_TAG:-v0.9.0-73e05db4}}"
token() { curl -fsSL "https://auth.docker.io/token?service=registry.docker.io&scope=repository:$1:pull" | sed -E 's/.*"token":"([^"]+)".*/\1/'; }
for repo in rolebasedgroup/rbgs-controller rolebasedgroup/rbgs-upgrade-crd; do
  tok="$(token "$repo")"
  code="$(curl -s -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $tok" \
    -H 'Accept: application/vnd.docker.distribution.manifest.v2+json, application/vnd.oci.image.index.v1+json' \
    "https://registry-1.docker.io/v2/$repo/manifests/$TAG")"
  echo "$repo:$TAG -> HTTP $code"
  [ "$code" = 200 ] || { echo "MISSING: $repo:$TAG"; exit 1; }
done
echo "both images exist"
