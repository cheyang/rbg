#!/usr/bin/env bash
# Hypothesis (CLEARED): the composite action rewrites the LWS image tag
#   lws_image="registry.k8s.io/lws/lws:${lws_version}"; lws_image="${lws_image/:v/:}"
# Verify the rewritten tag matches the image reference in the LWS release manifest,
# otherwise the docker pull / kind load preload would target a non-existent tag.
set -euo pipefail
lws_version="${1:-v0.7.0}"
lws_image="registry.k8s.io/lws/lws:${lws_version}"
lws_image="${lws_image/:v/:}"
echo "rewritten preload image: $lws_image"
manifest_image="$(curl -sL "https://github.com/kubernetes-sigs/lws/releases/download/${lws_version}/manifests.yaml" | grep -oE 'registry\.k8s\.io/lws/lws:[^ "]+' | sort -u)"
echo "image referenced by LWS ${lws_version} manifests.yaml: $manifest_image"
if [ "$lws_image" = "$manifest_image" ]; then
  echo "PASS: preload tag matches the manifest (rewrite is correct and necessary)"
else
  echo "FAIL: tag mismatch"
  exit 1
fi
