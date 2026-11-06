#!/usr/bin/env bash
# check-release-artifacts.sh — verify the published Docker Hub release artifacts
# referenced by this version bump (read-only network checks; no cluster needed).
#
# Checks, for repo rolebasedgroup, tag v0.9.0-73e05db4 (the PR's appVersion + "v"):
#   1. rbgs-controller and rbgs-upgrade-crd tags exist on Docker Hub (amd64 + arm64).
#   2. The controller image's manager binary embeds Version=v0.9.0-73e05db4 and
#      GitCommit=<full sha of the release base commit> (ldflags-injected), proving the
#      image was built from the commit the manifests point at.
#   3. The crd-upgrader image's /rbgs/crds/* are byte-identical to config/crd/bases
#      at the release base commit (and the ensure-crds script is identical too).
#
# Usage:  bash check-release-artifacts.sh [EXPECT_COMMIT_SHA]  (default: 73e05db4213950a5dd1f230279e9344a4ffe8435)
# Requires: curl, python3, tar, strings. Run from inside the rbg git checkout.
set -uo pipefail

EXPECT_SHA="${1:-73e05db4213950a5dd1f230279e9344a4ffe8435}"
SHORT="${EXPECT_SHA:0:8}"
TAG="v0.9.0-${SHORT}"
REPO="rolebasedgroup"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
rc=0

token() { curl -s "https://auth.docker.io/token?service=registry.docker.io&scope=repository:${REPO}/$1:pull" | python3 -c 'import sys,json;print(json.load(sys.stdin)["token"])'; }

echo "== 1. tag existence =="
for img in rbgs-controller rbgs-upgrade-crd; do
  code=$(curl -s -o /dev/null -w "%{http_code}" "https://hub.docker.com/v2/repositories/${REPO}/${img}/tags/${TAG}")
  echo "  ${img}:${TAG} -> HTTP $code"
  [ "$code" = "200" ] || rc=1
  curl -s "https://hub.docker.com/v2/repositories/${REPO}/${img}/tags/${TAG}" \
    | python3 -c 'import sys,json;d=json.load(sys.stdin);print("   platforms:", ", ".join(sorted(i.get("architecture","?")+"/"+i.get("os","?") for i in d.get("images",[]))))' 2>/dev/null
done

echo "== 2. controller binary embeds the release commit =="
T="$(token rbgs-controller)"
curl -s -H "Authorization: Bearer $T" -H "Accept: application/vnd.oci.image.index.v1+json" \
  "https://registry-1.docker.io/v2/${REPO}/rbgs-controller/manifests/${TAG}" -o "$WORK/idx.json"
AMD=$(python3 -c "import json;m=json.load(open('$WORK/idx.json'));print(next(mf['digest'] for mf in m['manifests'] if mf['platform']['architecture']=='amd64'))")
curl -s -H "Authorization: Bearer $T" -H "Accept: application/vnd.oci.image.manifest.v1+json" \
  "https://registry-1.docker.io/v2/${REPO}/rbgs-controller/manifests/${AMD}" -o "$WORK/m.json"
LAYER=$(python3 -c "
import json;m=json.load(open('$WORK/m.json'))
print(max(m['layers'],key=lambda l:l['size'])['digest'])")
curl -sL -H "Authorization: Bearer $T" "https://registry-1.docker.io/v2/${REPO}/rbgs-controller/blobs/${LAYER}" -o "$WORK/manager-layer.bin"
mkdir -p "$WORK/c"
tar -xf "$WORK/manager-layer.bin" -C "$WORK/c" 2>/dev/null
BIN="$WORK/c/manager"
if [ -f "$BIN" ]; then
  if strings "$BIN" | grep "version.Version=${TAG}" >/dev/null; then
    echo "  embedded Version=${TAG}: OK"
  else
    echo "  embedded Version=${TAG}: MISSING"; rc=1
  fi
  if strings "$BIN" | grep "version.GitCommit=${EXPECT_SHA}" >/dev/null; then
    echo "  embedded GitCommit=${EXPECT_SHA}: OK"
  else
    echo "  embedded GitCommit=${EXPECT_SHA}: MISSING"; rc=1
  fi
else
  echo "  could not extract manager binary from layer; check manually"; rc=1
fi

echo "== 3. crd-upgrader image CRDs match config/crd/bases @ ${SHORT} =="
REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null)"
if [ -n "$REPO_ROOT" ]; then
  T2="$(token rbgs-upgrade-crd)"
  curl -s -H "Authorization: Bearer $T2" -H "Accept: application/vnd.oci.image.index.v1+json" \
    "https://registry-1.docker.io/v2/${REPO}/rbgs-upgrade-crd/manifests/${TAG}" -o "$WORK/u-idx.json"
  UAMD=$(python3 -c "import json;m=json.load(open('$WORK/u-idx.json'));print(next(mf['digest'] for mf in m['manifests'] if mf['platform']['architecture']=='amd64'))")
  curl -s -H "Authorization: Bearer $T2" -H "Accept: application/vnd.oci.image.manifest.v1+json" \
    "https://registry-1.docker.io/v2/${REPO}/rbgs-upgrade-crd/manifests/${UAMD}" -o "$WORK/u-m.json"
  CRD_LAYER=$(python3 -c "
import json;m=json.load(open('$WORK/u-m.json'))
for l in m['layers']: print(l['digest'])" | while read -r d; do
    curl -sL -H "Authorization: Bearer $T2" "https://registry-1.docker.io/v2/${REPO}/rbgs-upgrade-crd/blobs/${d}" -o "$WORK/u-l.bin"
    # extract every layer's files as we scan, so /rbgs (crds + script) is fully present
    mkdir -p "$WORK/u"
    tar -xf "$WORK/u-l.bin" -C "$WORK/u" 2>/dev/null
    if [ -d "$WORK/u/rbgs/crds" ]; then echo "$d"; fi
  done)
  if [ -n "${CRD_LAYER:-}" ]; then
    mkdir -p "$WORK/repo-crds"
    git -C "$REPO_ROOT" archive "${EXPECT_SHA}" config/crd/bases | tar -x -C "$WORK/repo-crds" --strip-components=3
    if diff -r "$WORK/repo-crds" "$WORK/u/rbgs/crds" >/dev/null 2>&1; then
      echo "  CRDs in image identical to config/crd/bases @ ${SHORT}: OK ($(ls "$WORK/u/rbgs/crds" | wc -l) files)"
    else
      echo "  CRDs in image DIFFER from config/crd/bases @ ${SHORT}: MISMATCH"; rc=1
    fi
    SCRIPT_SRC=$(git -C "$REPO_ROOT" show "${EXPECT_SHA}:tools/crd-upgrade/ensure-crds-up-to-date.sh" 2>/dev/null)
    IMG_SRC=$(cat "$WORK/u/rbgs/ensure-crds-up-to-date.sh" 2>/dev/null)
    if [ -n "$SCRIPT_SRC" ] && [ "$SCRIPT_SRC" = "$IMG_SRC" ]; then
      echo "  ensure-crds-up-to-date.sh identical @ ${SHORT}: OK"
    else
      echo "  ensure-crds-up-to-date.sh DIFFERS from ${SHORT}: MISMATCH"; rc=1
    fi
  else
    echo "  could not locate CRD layer in upgrader image; check manually"; rc=1
  fi
else
  echo "  (not inside a git checkout of rbg; skipping repo comparison)"
fi

echo "exit=$rc (0 = all release-artifact contracts hold)"
exit "$rc"
