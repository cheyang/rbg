#!/usr/bin/env bash
# repro-f1-chart-test-stderr-pollution.sh — reproduction for finding F1
# (pre-existing, NOT introduced by PR #555):
#
#   test/chart/deprecated_workload_types_test.go renderChart() uses
#   cmd.CombinedOutput(), so any warning helm prints on stderr is spliced into
#   the "rendered manifest" string that splitObjects() then parses as YAML.
#   With a group/world-readable KUBECONFIG (common on shared dev boxes), helm
#   prints:
#     WARNING: Kubernetes configuration file is group-readable. This is insecure. ...
#   and every chart test fails with
#     "parse rendered doc: error converting YAML to JSON: yaml: mapping values
#      are not allowed in this context".
#
# This script makes the condition deterministic: it creates a world-readable
# kubeconfig, points KUBECONFIG at it, runs the pre-existing chart tests, and
# shows them FAIL; then shows the same tests PASS once the perms are fixed
# (the harness's own render helper captures stdout only, so the added
# version_bump_test.go tests are immune either way).
#
# Bug-canary polarity: currently FAILS (bug present, pre-existing on base
# 73e05db4). When renderChart is switched to separate stdout/stderr, the
# failing leg of this script turns green.
set -uo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"
cd "$REPO_ROOT"

BAD_KC="$(mktemp)"
printf 'apiVersion: v1\nkind: Config\nclusters: []\n' > "$BAD_KC"
chmod 644 "$BAD_KC"

echo "== leg 1: KUBECONFIG world-readable (0644) -> pre-existing tests fail =="
if ! command -v helm >/dev/null; then
  echo "helm not installed; cannot reproduce F1 here"; exit 3
fi
KUBECONFIG="$BAD_KC" go test ./test/chart/ -run 'TestManagerFlagReflectsToggle' 2>&1 | tail -5
LEG1_RC=${PIPESTATUS[0]}

chmod 600 "$BAD_KC"
echo
echo "== leg 2: KUBECONFIG 0600 -> same tests pass =="
KUBECONFIG="$BAD_KC" go test ./test/chart/ -run 'TestManagerFlagReflectsToggle' 2>&1 | tail -3
LEG2_RC=${PIPESTATUS[0]}

rm -f "$BAD_KC"
echo
if [ "$LEG1_RC" -ne 0 ] && [ "$LEG2_RC" -eq 0 ]; then
  echo "F1 REPRODUCED: helm stderr warning pollutes CombinedOutput and breaks the chart tests."
  exit 0
else
  echo "F1 not reproduced on this machine (helm may not warn for this kubeconfig); leg1=$LEG1_RC leg2=$LEG2_RC"
  exit 1
fi
