#!/usr/bin/env bash
# run-premise.sh — re-run the P0 premise check (the PR's claimed symptom) against
# the BASE commit. The premise suite registers the v1alpha1 deprecation webhook
# handlers that only exist on the base (they were deleted by PR #556), so it is
# carried as premise-base.patch and applied to a temporary worktree of the base.
#
# Usage: run-premise.sh [<base-sha>]   (default: the baseSha recorded in
#        verify-manifest.json's round, else 73e05db4213950a5dd1f230279e9344a4ffe8435)
#
# Expected outcome on the untouched base: 5/5 specs pass, including the
# duplication canary "returns two overlapping deprecation warnings".
# Requires: git, go, KUBEBUILDER_ASSETS (envtest assets).
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BASE="${1:-73e05db4213950a5dd1f230279e9344a4ffe8435}"
WT="$(mktemp -d /tmp/rbg-556-premise.XXXXXX)"
trap 'git worktree remove --force "$WT" >/dev/null 2>&1 || rm -rf "$WT"' EXIT

git worktree add --detach "$WT" "$BASE"
git -C "$WT" apply "$HERE/premise-base.patch"

export KUBEBUILDER_ASSETS="${KUBEBUILDER_ASSETS:-/home/ecs-user/.local/share/kubebuilder-envtest/k8s/1.36.2-linux-amd64}"
cd "$WT"
go test ./test/envtest/testcase/verifyv1a1base/ -count=1 -timeout 10m
