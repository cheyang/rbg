#!/usr/bin/env bash
# P0 premise evidence (runs against the BASE ref): the PR claims the E2E workflow is
# monolithic and expensive — multiple jobs rebuilding the same images and repeating the
# same Kind/LWS/Helm setup. Count the duplications in the merge-base workflow.
set -euo pipefail
BASE="${1:-798e5d6f5eda5f8f67bba35831381921c88d1e3f}"
wf="$(git show "$BASE:.github/workflows/e2e-test.yml")"
echo "base ref: $BASE"
echo "lines in base e2e-test.yml: $(printf '%s\n' "$wf" | wc -l)"
for pat in 'kind create cluster' 'docker-build-controller' 'docker-build-crd-upgrader' 'Install LeaderWorkerSet' 'get-helm-3' 'Wait for controller to be ready'; do
  echo "  occurrences of '$pat': $(printf '%s\n' "$wf" | grep -c "$pat" || true)"
done
