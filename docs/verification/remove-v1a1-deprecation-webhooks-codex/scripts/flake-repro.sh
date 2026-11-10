#!/usr/bin/env bash
# flake-repro.sh — repro for F4: the author's v1a1gate suite at PR head is
# order-flaky. Two Ordered Describe containers share gateNamespace; the first
# hard-fails on AlreadyExists (testutil.CreateNamespace), the second tolerates
# it. Ginkgo randomizes top-level container order per seed, so when the
# wire-contract Describe runs first, the conversion-chain Describe's BeforeAll
# fails with `namespaces "v1a1-gate-test" already exists`.
#
# Usage:  bash flake-repro.sh [ref] [seeds...]
#   ref   PR ref to test (default: resolved from ../verify-manifest.json's pr URL)
#   seeds default: 1 2 3 4 5 6
#
# Expected at 91d244f5 (broken): roughly half the seeds FAIL.
# Expected after the author makes namespace creation idempotent: all PASS.
#
# Runs in a throwaway worktree; the calling tree is never modified.
set -euo pipefail
REPO_ROOT="$(git rev-parse --show-toplevel)"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REF="${1:-}"; shift || true
SEEDS="${*:-1 2 3 4 5 6}"
if [ -z "$REF" ]; then
  PR_URL="$(jq -r '.pr' "$HERE/../verify-manifest.json")"
  REF="$(git -C "$REPO_ROOT" ls-remote "$PR_URL" 2>/dev/null | true)"
  # simpler: fetch the PR head from the repo URL embedded in the manifest
  REPO_URL="${PR_URL%/pull/*}.git"
  PR_NUM="${PR_URL##*/}"
  git -C "$REPO_ROOT" fetch -q "$REPO_URL" "pull/${PR_NUM}/head"
  REF="$(git -C "$REPO_ROOT" rev-parse FETCH_HEAD)"
fi
export KUBEBUILDER_ASSETS="${KUBEBUILDER_ASSETS:-$HOME/.local/share/kubebuilder-envtest/k8s/1.31.0-linux-amd64}"
WT="$(mktemp -d)"
trap 'git -C "$REPO_ROOT" worktree remove --force "$WT" 2>/dev/null || rm -rf "$WT"' EXIT
git -C "$REPO_ROOT" worktree add --detach "$WT" "$REF" >/dev/null 2>&1
echo "flake-repro: testing $WT ($REF)"
fails=0; total=0
for seed in $SEEDS; do
  total=$((total+1))
  if (cd "$WT" && go test ./test/envtest/testcase/v1a1gate -count=1 -timeout 10m -args -ginkgo.seed="$seed") >/dev/null 2>&1; then
    echo "  seed $seed: PASS"
  else
    echo "  seed $seed: FAIL (namespaces \"v1a1-gate-test\" already exists)"
    fails=$((fails+1))
  fi
done
echo "flake-repro: $fails/$total seeds failed"
[ "$fails" -eq 0 ]
