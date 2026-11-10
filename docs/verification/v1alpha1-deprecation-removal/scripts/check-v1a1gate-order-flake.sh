#!/usr/bin/env bash
# check-v1a1gate-order-flake.sh — BUG-CANARY for finding V5-v1a1gate-order-flake
# (introduced by PR #556 head 91d244f).
#
# Claim: the author's test/envtest/testcase/v1a1gate suite is order-flaky.
# Ginkgo v2 randomizes the order of top-level Describe containers per run
# (time-based seed). The suite has two Ordered containers sharing one
# namespace:
#   - "v1alpha1 request path through the conversion chain" (gate_test.go:53)
#     whose BeforeAll uses testutil.CreateNamespace -> Expect(...).To(Succeed())
#   - "v1alpha1 wire contract" (gate_test.go:114, new in 91d244f)
#     whose BeforeAll creates the same namespace and IGNORES the error.
# When the wire-contract container is ordered FIRST it creates the namespace,
# and the conversion-chain container's BeforeAll then fails
# "namespaces \"v1a1-gate-test\" already exists", skipping its 4 enforcement
# specs. Deterministic per seed.
#
# POLARITY: bug-canary.
#   On the buggy head: the suite FAILS at the wire-first seeds below and this
#   script exits 0 (asserting the observed wrong behavior).
#   When the flake is FIXED (idempotent namespace setup), the suite PASSES at
#   those seeds, this script exits 1 — INVERT it (or promote to a contract
#   test: "suite passes deterministically at both seeds").
#
# Control seed 42 orders the containers chain-first; the suite passes there
# both before and after the fix, proving the failure is order-dependent, not a
# hard failure.
#
# Usage: KUBEBUILDER_ASSETS=... bash scripts/check-v1a1gate-order-flake.sh <ref>
#        <ref> defaults to the PR head (fetched via the manifest's pr URL).
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MANIFEST="$HERE/../verify-manifest.json"
cd "$HERE/../../../.." || exit 2   # repo root (checkout of the harness branch)

REF="${1:-}"
if [ -z "$REF" ]; then
  PR_URL="$(jq -r '.pr // empty' "$MANIFEST")"
  REPO_URL="$(dirname "$(dirname "$PR_URL")").git"
  N="$(basename "$PR_URL")"
  git fetch "$REPO_URL" "pull/$N/head" 2>/dev/null
  REF="$(git rev-parse FETCH_HEAD)"
fi
echo "flake-canary: testing ref $REF"

run_suite_at_seed() {
  local seed="$1" workdir="$2"
  (
    cd "$workdir" || exit 2
    KUBEBUILDER_ASSETS="${KUBEBUILDER_ASSETS:-/home/ecs-user/.local/share/kubebuilder-envtest/k8s/1.36.2-linux-amd64}" \
      go test ./test/envtest/testcase/v1a1gate/ -count=1 -timeout 10m \
      -args -ginkgo.seed="$seed" >/dev/null 2>&1
  )
}

# Graft the head into a temp worktree (author code, no harness needed: the
# suite under test is the author's own).
WORK="$(mktemp -d)"
git worktree add --detach "$WORK" "$REF" >/dev/null 2>&1 || { echo "flake-canary: cannot check out $REF"; exit 2; }
trap 'git worktree remove --force "$WORK" >/dev/null 2>&1' EXIT

BUGGY=0
for seed in 7 1794329328; do
  if run_suite_at_seed "$seed" "$WORK"; then
    echo "flake-canary: seed $seed PASSED (chain-first or flake fixed)"
  else
    echo "flake-canary: seed $seed FAILED (wire-first order -> namespace AlreadyExists)"
    BUGGY=1
  fi
done
if run_suite_at_seed 42 "$WORK"; then
  echo "flake-canary: control seed 42 PASSED (order-dependent, not a hard failure)"
else
  echo "flake-canary: control seed 42 ALSO FAILED — suite is broken outright, not just order-flaky"
  exit 2
fi

if [ "$BUGGY" -eq 1 ]; then
  echo "flake-canary: CANARY GREEN — order-flake reproduced (bug present)."
  exit 0
else
  echo "flake-canary: CANARY FLIPPED — suite passed at the previously-failing seeds: the flake is fixed. Invert this canary."
  exit 1
fi
