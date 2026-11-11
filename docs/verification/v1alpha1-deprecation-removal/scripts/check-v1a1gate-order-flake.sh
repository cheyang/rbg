#!/usr/bin/env bash
# check-v1a1gate-order-flake.sh — CONTRACT test for finding V5-v1a1gate-order-flake
# (round-3 blocker, introduced by PR #556 head 91d244f, FIXED in head 15386660).
#
# History: this script started life (round 3) as a BUG-CANARY asserting the
# observed order-flake — it exited 0 while the suite failed at wire-first seeds.
# Head 15386660 ("test(v1a1gate): fix order flake...") hoisted the shared
# namespace creation into BeforeSuite and removed both per-Describe BeforeAll
# hooks; the canary flipped (all seeds pass), so per the polarity rule it is
# now INVERTED/promoted to this CONTRACT test: the v1a1gate suite must pass
# deterministically regardless of Ginkgo's randomized top-level container
# order.
#
# POLARITY: contract (inverted from canary at round 4).
#   On fixed code (>= 15386660): suite passes at every seed below, exit 0.
#   On a regression (order-dependent shared state reintroduced — e.g. a
#   per-Describe BeforeAll creating a shared namespace or other cross-Describe
#   state): the suite fails at the wire-first seeds, exit 1.
#
# Seeds: 7, 1794329300, 1794329328, 1794329513 were wire-first FAILING seeds
# at 91d244f (round 3, both reviewers); 42, 1794329336 were chain-first
# PASSING controls. Round-4 sweep (results/round4-15386660/seed-sweep-r4.txt):
# 10/10 pass at head 15386660.
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
echo "order-contract: testing ref $REF"

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
git worktree add --detach "$WORK" "$REF" >/dev/null 2>&1 || { echo "order-contract: cannot check out $REF"; exit 2; }
trap 'git worktree remove --force "$WORK" >/dev/null 2>&1' EXIT

FAILED=0
for seed in 7 1794329300 1794329328 1794329513; do
  if run_suite_at_seed "$seed" "$WORK"; then
    echo "order-contract: wire-first seed $seed PASSED"
  else
    echo "order-contract: wire-first seed $seed FAILED — order-dependent shared state is back (V5 regression)"
    FAILED=1
  fi
done
for seed in 42 1794329336; do
  if run_suite_at_seed "$seed" "$WORK"; then
    echo "order-contract: chain-first control seed $seed PASSED"
  else
    echo "order-contract: chain-first control seed $seed FAILED — suite is broken outright, not just order-flaky"
    exit 2
  fi
done

if [ "$FAILED" -eq 0 ]; then
  echo "order-contract: GREEN — suite is order-independent at all tested seeds."
  exit 0
else
  echo "order-contract: RED — the v1a1gate suite is order-flaky again."
  exit 1
fi
