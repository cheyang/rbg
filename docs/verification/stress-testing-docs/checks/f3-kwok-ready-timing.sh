#!/bin/bash
# F3 (contract): The docs claim "each simulated Pod takes about 1.5 seconds from
# creation to Ready" (pod-initialize 0ms -> pod-running 500ms -> pod-ready 1000ms).
# In test/stress/templates/kwok-stage.yaml, pod-running's statusTemplate ALREADY sets
# Ready="True" (at ~500ms), and pod-ready's selector requires Ready NotIn True, so
# pod-ready can never fire after pod-running. Ready happens at ~500ms, not ~1.5s.
set -u
cd "$(git rev-parse --show-toplevel)"
f=test/stress/templates/kwok-stage.yaml

# Extract per-stage blocks.
running_block=$(awk '/name: pod-running$/,/^---$/' "$f")
ready_block=$(awk '/name: pod-ready$/,/^---$/' "$f")

echo "--- pod-running block sets Ready=True? ---"
echo "$running_block" | grep -B1 -A1 'type: Ready' || true
running_sets_ready=$(echo "$running_block" | grep -A1 'type: Ready' | grep -c 'status: "True"')
echo "running_sets_ready=$running_sets_ready"

echo "--- pod-ready selector excludes Ready=True pods? ---"
ready_excludes=$(echo "$ready_block" | grep -c 'NotIn')
echo "$ready_block" | grep -E 'NotIn|Ready' || true

fail=0
if [ "$running_sets_ready" -ge 1 ] && [ "$ready_excludes" -ge 1 ]; then
  echo "MECHANISM CONFIRMED: pod becomes Ready in pod-running (~500ms); pod-ready is unreachable."
  if grep -qE '1\.5 (seconds|秒)' doc/best-practice/en/09-stress-testing-and-tuning.md \
       doc/best-practice/zh/09-stress-testing-and-tuning.md \
       doc/best-practice/en/09-stress-testing-and-tuning-guide.md \
       doc/best-practice/zh/09-stress-testing-and-tuning-guide.md; then
    echo "F3 REPRODUCED: docs claim ~1.5s to Ready; stage config makes Ready at ~500ms."
    echo "Corroboration: guide's own sample data shows min create latency 1024ms end-to-end"
    echo "(client-side, incl. controller reconcile) < 1500ms of pure KWOK delay -> impossible"
    echo "if the 1.5s claim were true."
    fail=1
  fi
fi
[ "$fail" = 1 ] && exit 1
echo "F3 OK"
