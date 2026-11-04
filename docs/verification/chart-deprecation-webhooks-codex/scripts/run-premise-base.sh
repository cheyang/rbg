#!/usr/bin/env bash
# run-premise-base.sh — reproduce the P0 premise evidence: the BASE chart (the
# merge-base of PR #552, i.e. main after #524) ships no v1alpha1 deprecation
# webhook entries, so Helm installs had no v1alpha1 deprecation warning plane.
#
# It checks out the base chart into a git worktree and runs the harness suite in
# premise mode against it. Requires: git, go, helm, envtest assets
# (KUBEBUILDER_ASSETS or ./bin/k8s/1.31.0-linux-amd64).
set -euo pipefail

BASE_SHA="${1:-73e05db4213950a5dd1f230279e9344a4ffe8435}"
REPO_ROOT="$(git rev-parse --show-toplevel)"
WT="$(mktemp -d)/rbg-base"
trap 'git -C "$REPO_ROOT" worktree remove --force "$WT" >/dev/null 2>&1 || true' EXIT

git -C "$REPO_ROOT" worktree add "$WT" "$BASE_SHA"
echo "premise: base chart at $WT (sha $BASE_SHA)"

ASSETS="${KUBEBUILDER_ASSETS:-$REPO_ROOT/bin/k8s/1.31.0-linux-amd64}"
RBGS_HELM_SUITE_MODE=premise RBGS_CHART_DIR="$WT/deploy/helm/rbgs" \
  KUBEBUILDER_ASSETS="$ASSETS" \
  go test ./test/envtest/testcase/deprecationv1a1helm/ -count=1 -v
