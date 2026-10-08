#!/bin/bash
# F7 (contract, integration layer): render the chart with the guide's exact helm
# --set flags and compare the rendered controller args against the guide's
# "expected output" block (en + zh). Every rendered arg key must appear in the doc.
set -u
cd "$(git rev-parse --show-toplevel)"

rendered=$(helm template rbgs deploy/helm/rbgs -n rbgs-system \
  --set controller.image.tag=vX \
  --set controller.resources.limits.cpu=8 \
  --set controller.resources.limits.memory=16Gi \
  --set controller.tuning.maxConcurrentReconciles=20 \
  --set controller.tuning.kubeApiQPS=100 \
  --set controller.tuning.kubeApiBurst=200 \
  --set controller.pprof.enabled=true \
  --set controller.pprof.containerPort=6060 2>/dev/null)

args=$(printf '%s' "$rendered" | awk '/name: rbgs-controller-manager/,/^---$/' | grep -E '^\s+- --' | sed 's/^\s*- //')
echo "--- rendered controller args ---"
printf '%s\n' "$args"

fail=0
for doc in doc/best-practice/en/09-stress-testing-and-tuning-guide.md \
           doc/best-practice/zh/09-stress-testing-and-tuning-guide.md; do
  while IFS= read -r a; do
    key=$(printf '%s' "$a" | cut -d= -f1)
    if ! grep -q -- "$key" "$doc"; then
      echo "MISSING FROM $doc EXPECTED ARGS: $a"
      fail=1
    fi
  done <<< "$args"
done

if [ "$fail" = 1 ]; then
  echo "F7 REPRODUCED: the chart renders args the guide's expected-output block omits"
  exit 1
fi
echo "F7 OK"
