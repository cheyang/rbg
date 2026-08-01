#!/usr/bin/env bash
# F5b (CONTRACT test) -- the committed generated artefacts must match what the generator
# produces, so a reviewer can trust config/rbac/role.yaml, config/webhook/manifests.yaml and
# deploy/kubectl/manifests.yaml as evidence of what the code declares.
#
# This matters more in this PR than usual: it adds a new kubebuilder webhook marker
# (RoleBasedGroupSetValidator) and it REMOVES the step that copied the generated ClusterRole
# into the chart, so `make manifests` is now the only thing keeping the kustomize side honest.
set -uo pipefail
cd "$(dirname "$0")/../../../.." || exit 2

echo "=== F5b: is the generated output committed and up to date? ==="
before=$(git status --porcelain)
if [ -n "$before" ]; then
  echo "  NOTE: tree already dirty before generating (harness files); only tracked"
  echo "        generated paths are judged below."
fi

echo "--- running: make manifests generate ---"
if ! make manifests generate >/tmp/mkman.txt 2>&1; then
  echo "  make failed:"; tail -20 /tmp/mkman.txt | sed 's/^/    /'
  exit 2
fi
grep -E 'NOTE:|manually sync|controllerrevisions' /tmp/mkman.txt | sed 's/^/  /' | head -8

echo
echo "--- diff in generated paths ---"
paths="config/ deploy/kubectl/ api/ client-go/"
# shellcheck disable=SC2086
drift=$(git status --porcelain -- $paths)
if [ -z "$drift" ]; then
  echo "  (none) -- generated artefacts are in sync"
  echo
  echo "RESULT: F5b clean. Generated output matches the committed files."
  exit 0
fi
printf '%s\n' "$drift" | sed 's/^/  /'
echo
# shellcheck disable=SC2086
git --no-pager diff --stat -- $paths | sed 's/^/  /'
echo
echo "RESULT: F5b REPRODUCED -- 'make manifests generate' produces output that differs from"
echo "        what is committed. Regenerate and commit."
exit 1
