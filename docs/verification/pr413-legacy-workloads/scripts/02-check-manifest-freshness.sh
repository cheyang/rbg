#!/usr/bin/env bash
# F2 [CONTRACT]: `make manifests` must leave the tree clean. This is what the
# repo's own `lint` job (project-check.yml) enforces.
#
# On PR head e5f4bd60 this FAILED: config/manager/manager.yaml gained
# --enable-legacy-workloads but deploy/kubectl/manifests.yaml was not
# regenerated, so the committed installer ran the controller with no flag at all.
# Re-run this after any change that touches config/ or the kubebuilder markers.
#
# Exit 0 = tree clean (fixed). Exit 1 = drift (finding reproduced).
set -uo pipefail
cd "$(git rev-parse --show-toplevel)"

echo "== committed installer: is the flag present? =="
if grep -q 'enable-legacy-workloads' deploy/kubectl/manifests.yaml; then
  grep -n 'enable-legacy-workloads' deploy/kubectl/manifests.yaml | sed 's/^/  /'
else
  echo "  ABSENT - installer would run the controller with the Go default"
fi

echo "== running 'make manifests' =="
make manifests >/tmp/f2-make.log 2>&1
MAKE_RC=$?
tail -3 /tmp/f2-make.log | sed 's/^/  /'
if [ $MAKE_RC -ne 0 ]; then
  echo "RESULT: INCONCLUSIVE - 'make manifests' itself failed (rc=$MAKE_RC), see /tmp/f2-make.log"
  exit 2
fi

# Only tracked files matter; the harness adds untracked test files.
DRIFT=$(git diff --name-only)
echo "== tracked files modified by 'make manifests' =="
if [ -z "$DRIFT" ]; then
  echo "  (none)"
  echo
  echo "RESULT: PASS - generated artifacts are in sync"
  exit 0
fi
echo "$DRIFT" | sed 's/^/  M /'
echo
echo "RESULT: FAIL - F2 REPRODUCED, generated artifacts are stale"
git diff --stat | tail -5 | sed 's/^/  /'
exit 1
