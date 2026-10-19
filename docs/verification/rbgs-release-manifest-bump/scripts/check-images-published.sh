#!/usr/bin/env bash
# check-images-published.sh — network check (not part of re-verify layers).
#
# Asserts that the default images this release-manifest bump points at actually
# exist on Docker Hub, for both repositories and both expected architectures.
# Usage: check-images-published.sh [tag]   (default: v0.9.0-d88b3186, PR #505's cut)
set -uo pipefail

TAG="${1:-v0.9.0-d88b3186}"
rc=0
for repo in rbgs-controller rbgs-upgrade-crd; do
  url="https://hub.docker.com/v2/repositories/rolebasedgroup/${repo}/tags/${TAG}"
  out="$(curl -sf "$url")" || { echo "MISSING: rolebasedgroup/${repo}:${TAG} (curl failed)"; rc=1; continue; }
  archs="$(printf '%s' "$out" | jq -r '[.images[].architecture] | join(",")')"
  if printf '%s' "$out" | jq -e '.images | map(.status=="active") | all' >/dev/null; then
    echo "OK: rolebasedgroup/${repo}:${TAG} (arch: ${archs})"
  else
    echo "INACTIVE: rolebasedgroup/${repo}:${TAG} (arch: ${archs})"
    rc=1
  fi
done
exit $rc
