#!/usr/bin/env bash
# re-verify.sh — re-run the pr463 verification harness against updated (fixed) code.
#
# Run from a checkout of the branch that CONTAINS the harness
# (verify/pr463-legacy-strategy-heal). With no <fixed-ref> it fetches the current PR
# head from the manifest `pr` URL; it resolves the incremental review delta from
# `.last-reviewed` and prints the last-reviewed..head range.
#
# Layers:
#   unit + envtest run here (deterministic). The live A/B (ACK cluster) is reminded,
#   not run — see scripts/10-live-ab.sh.
#
# Usage: bash scripts/re-verify.sh [<fixed-ref>]
set -euo pipefail

TOP="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MANIFEST="$TOP/verify-manifest.json"
LAST="$TOP/.last-reviewed"
FIXED_REF="${1:-}"

command -v jq >/dev/null || { echo "re-verify: jq required" >&2; exit 2; }
git rev-parse --git-dir >/dev/null 2>&1 || { echo "re-verify: not a git repo" >&2; exit 2; }

# Resolve PR head if no ref given.
if [ -z "$FIXED_REF" ]; then
  PR_URL="$(jq -r '.pr // empty' "$MANIFEST")"
  if [[ "$PR_URL" == *"/pull/"* ]]; then
    REPO="${PR_URL%/pull/*}"; PR="${PR_URL##*/pull/}"
    git fetch "$REPO" "pull/$PR/head:pr-$PR" >/dev/null 2>&1 || true
    FIXED_REF="pr-$PR"
  fi
fi
FIXED_REF="${FIXED_REF:-HEAD}"
echo "re-verify: fixed-ref = $FIXED_REF"

# Incremental delta
if [ -f "$LAST" ]; then
  LR="$(cat "$LAST")"
  echo "re-verify: last-reviewed = $LR"
  echo "re-verify: incremental delta (review this range):"
  git log --oneline "$LR..$FIXED_REF" 2>/dev/null | sed 's/^/    /' || echo "    (no new commits)"
  echo "  advance marker after this round:  echo $FIXED_REF > docs/verification/pr463-legacy-strategy-heal/.last-reviewed"
fi

echo
echo "=== L1: unit tests (defaulters, conversion, reconciler, certmanager) ==="
go test ./api/workloads/v1alpha2/... ./api/workloads/v1alpha1/... ./pkg/reconciler/... ./pkg/webhook/... 2>&1 | tail -25

echo
echo "=== L2: envtest (real isolated kube-apiserver) ==="
if command -v setup-envtest >/dev/null 2>&1; then
  export KUBEBUILDER_ASSETS="$(setup-envtest use 1.36.2 -p path)"
elif [ -d "$HOME/.local/share/kubebuilder-envtest/k8s/1.36.2-linux-amd64" ]; then
  export KUBEBUILDER_ASSETS="$HOME/.local/share/kubebuilder-envtest/k8s/1.36.2-linux-amd64"
fi
if [ -n "${KUBEBUILDER_ASSETS:-}" ]; then
  go test ./test/envtest/testcase/rbg/ 2>&1 | tail -8
else
  echo "  (KUBEBUILDER_ASSETS not set; run 'setup-envtest use 1.36.x -p path')"
fi

echo
echo "=== L3: live A/B (ACK cluster) — NOT auto-run ==="
echo "  Run: bash docs/verification/pr463-legacy-strategy-heal/scripts/10-live-ab.sh"
echo "  Requires KUBECONFIG pointing at a cluster running a pre-enum release (v0.8.0)."
echo "  Polarity: contract tests go GREEN on the fix; base A/B canary (RIS absent + 422) flips to RIS-created."
