#!/usr/bin/env bash
# Regression guard carried over from the PR#413 round (that round's F2 was a
# stale deploy/kubectl/manifests.yaml that broke the lint job). This PR changes
# the `manifests` target itself -- it no longer copies config/rbac/role.yaml
# over the chart -- so re-check that `make manifests` is still a no-op on a
# clean tree.
#
# POLARITY: contract. Expected GREEN. Any diff here is a CI-breaking finding.
# No cluster required. Mutates the worktree, then restores it.
set -uo pipefail
ROOT="$(git rev-parse --show-toplevel)"
cd "$ROOT"

DIRTY=$(git status --porcelain -- Makefile config deploy api internal pkg cmd)
if [ -n "$DIRTY" ]; then
  echo "TREE DIRTY before generation -- refusing to run, the result would be meaningless:"
  echo "$DIRTY" | sed 's/^/    /'
  exit 3
fi

echo "== running: make manifests generate =="
if ! make manifests generate >/tmp/pr414-manifests.log 2>&1; then
  echo "make FAILED:"
  tail -25 /tmp/pr414-manifests.log | sed 's/^/    /'
  exit 2
fi

AFTER=$(git status --porcelain -- Makefile config deploy api internal pkg cmd)
if [ -n "$AFTER" ]; then
  echo "RESULT: generation is NOT idempotent -- committed artifacts are stale:"
  echo "$AFTER" | sed 's/^/    /'
  echo
  for f in $(echo "$AFTER" | awk '{print $2}'); do
    echo "--- $f (first 40 diff lines) ---"
    git diff -- "$f" | head -40 | sed 's/^/    /'
  done
  git checkout -- Makefile config deploy api internal pkg cmd 2>/dev/null || true
  exit 1
fi
echo "RESULT: clean -- `make manifests generate` produced no diff."
exit 0
