#!/usr/bin/env bash
# re-verify.sh — re-run the PR #532 host-port-scheduling verification harness
# against the current (or given) PR head and report per-claim outcomes.
#
# Run from a checkout of the branch that CONTAINS the harness
# (verify/pr532-host-port-scheduling-claude):
#
#   bash docs/verification/pr532-host-port-scheduling/scripts/re-verify.sh [fixed-ref]
#
# With no ref it fetches the current PR head from the manifest's "pr" URL
# (https://github.com/sgl-project/rbg/pull/532 -> git fetch ... pull/532/head),
# grafts the harness onto that ref, runs the unit (L1) and envtest integration
# (L2) layers, restores the tree, and prints a per-claim table with polarity
# applied (contract tests must pass; canaries must flip to fail = gap closed).
#
# Layers: unit + integration only (no live cluster layer). L2 needs envtest
# binaries:  export KUBEBUILDER_ASSETS="$(setup-envtest use 1.34.1 -p path)"
# (falls back to ~/.local/share/kubebuilder-envtest/k8s/1.34.1-linux-amd64).
#
# Exit 0 iff every contract claim passed (a flipped canary is good news and does
# not fail the run; a canary that stays red-as-bug is not possible by design).
set -euo pipefail

HARNESS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ROOT="$(git rev-parse --show-toplevel)"
MANIFEST="$HARNESS_DIR/verify-manifest.json"
FIXED_REF="${1:-}"

command -v git >/dev/null || { echo "re-verify: git required" >&2; exit 2; }
command -v jq >/dev/null || { echo "re-verify: jq required" >&2; exit 2; }
if ! git diff --quiet || ! git diff --cached --quiet; then
  echo "re-verify: working tree has uncommitted tracked changes; commit first" >&2
  exit 2
fi

if [ -z "$FIXED_REF" ]; then
  PR_URL="$(jq -r '.pr // empty' "$MANIFEST")"
  num="$(printf '%s' "$PR_URL" | sed -E 's#.*/pull/([0-9]+).*#\1#')"
  [ -n "$num" ] || { echo "re-verify: cannot derive PR number from manifest" >&2; exit 2; }
  echo "re-verify: resolving current head of PR $num"
  git fetch --quiet https://github.com/sgl-project/rbg.git "pull/$num/head"
  FIXED_REF="$(git rev-parse FETCH_HEAD)"
fi
echo "re-verify: head = $FIXED_REF"

LAST_REVIEWED="$HARNESS_DIR/.last-reviewed"
if [ -f "$LAST_REVIEWED" ]; then
  LR="$(cat "$LAST_REVIEWED")"
  echo "re-verify: last reviewed = $LR"
  echo "re-verify: incremental delta (last-reviewed..head):"
  git log --oneline "$LR..$FIXED_REF" | sed 's/^/    /' | head -20 || true
  echo "re-verify: after the round, advance the marker:"
  echo "    echo $FIXED_REF > $LAST_REVIEWED"
fi

HARNESS_FILES=(
  pkg/port-allocator/verify_hostport_scheduling_test.go
  test/envtest/testcase/portallocator/suite_test.go
  test/envtest/testcase/portallocator/hostport_injection_test.go
)

RUNTIME_DIR="$(mktemp -d)"
ORIG_HEAD="$(git rev-parse HEAD)"
# Prefer the branch name so restoring returns to a branch, not a detached sha.
ORIG_REF="$(git symbolic-ref -q --short HEAD || echo "$ORIG_HEAD")"
restore() {
  git checkout -f --quiet "$ORIG_REF" || git checkout -f --quiet "$ORIG_HEAD" || true
  rm -rf "$RUNTIME_DIR"
}
trap restore EXIT

for relpath in "${HARNESS_FILES[@]}"; do
  mkdir -p "$RUNTIME_DIR/$(dirname "$relpath")"
  cp "$ROOT/$relpath" "$RUNTIME_DIR/$relpath"
done

git checkout -f --quiet "$FIXED_REF"
for relpath in "${HARNESS_FILES[@]}"; do
  mkdir -p "$ROOT/$(dirname "$relpath")"
  cp "$RUNTIME_DIR/$relpath" "$ROOT/$relpath"
done

echo "== L1 (unit): go test ./pkg/port-allocator/ -run TestVerify =="
L1_OUT="$(go test ./pkg/port-allocator/ -run 'TestVerify' -v 2>&1)" || true
printf '%s\n' "$L1_OUT" | tail -5

L2_STATUS="not-run"
ASSETS_CANDIDATE="${KUBEBUILDER_ASSETS:-$HOME/.local/share/kubebuilder-envtest/k8s/1.34.1-linux-amd64}"
if [ -d "$ASSETS_CANDIDATE" ] && [ -x "$ASSETS_CANDIDATE/kube-apiserver" ]; then
  echo "== L2 (integration): envtest portallocator suite =="
  if L2_OUT="$(KUBEBUILDER_ASSETS="$ASSETS_CANDIDATE" go test ./test/envtest/testcase/portallocator/... -v 2>&1)"; then
    L2_STATUS="pass"
    printf '%s\n' "$L2_OUT" | grep -E "Ran [0-9]+ of|SUCCESS|FAIL" | head -3
  else
    L2_STATUS="fail"
    printf '%s\n' "$L2_OUT" | grep -E "FAIL|FAILED" | head -20
  fi
else
  echo "== L2 skipped: no envtest assets (export KUBEBUILDER_ASSETS) =="
fi

ROWS_FILE="$RUNTIME_DIR/rows.txt"
: > "$ROWS_FILE"
row() { printf '%-6s %-11s %s\n' "$1" "$2" "$3" >> "$ROWS_FILE"; }

# polarity-aware pass/fail probes against the -v output
tp() { printf '%s\n' "$L1_OUT" | grep -qE "^(---|    ---) PASS: $2( |\$)"; }

if tp P0 TestVerifyP0HostNetworkPodDeclaresSchedulerVisibleHostPort; then row P0 contract "pass — head declares scheduler-visible hostPort"; else row P0 contract "FAIL — host-network pods invisible to NodePorts (regression)"; fi
if tp F1 TestVerifyInjectedPodSpecsPassRealAPIServerValidation; then row F1 contract "pass — injected specs pass real apiserver validation"; else row F1 contract "FAIL — injected pod spec rejected by kube-apiserver"; fi
if tp F1c TestVerifyControlApIServerHostNetworkPortSemantics; then row F1c contract "pass — apiserver hostNetwork defaulting semantics pinned"; else row F1c contract "FAIL — apiserver semantics diverged"; fi
if tp F2 TestVerifyInjectionIsIdempotentAcrossReinjection; then row F2 contract "pass — idempotent re-injection"; else row F2 contract "FAIL — re-injection duplicates declarations"; fi
if tp V5 TestVerifyP0NonHostNetworkPodStaysPortReservationFree; then row V5 contract "pass — non-host-network pods reserve no node port"; else row V5 contract "FAIL — non-host-network pods reserve a node port"; fi
# F3 is a canary: it PASSES while the upgrade gap exists. If it FAILS, the gap
# got closed — invert it (promote to a contract test), do not count it broken.
if tp F3 TestVerifyCanaryLegacyPodWithoutDeclarationIsInvisibleToNodePorts; then row F3 canary "pass — gap still present, docs finding F3 stays open"; else row F3 canary "FLIPPED — gap closed; invert this canary into a contract test"; fi
case "$L2_STATUS" in
  pass) row V4 integration "pass — full RBG->pod chain through a real apiserver";;
  fail) row V4 integration "FAIL — see L2 output above (Harness-update if code shape changed)";;
  *)    row V4 integration "not-run — no envtest assets on this machine";;
esac

echo
echo "=============== per-claim outcome (polarity applied) ==============="
cat "$ROWS_FILE"
echo "====================================================================="

if grep -q '^FAIL' "$ROWS_FILE"; then
  echo "re-verify: one or more contract claims are broken."
  exit 1
fi
echo "re-verify: all contract claims green."
exit 0
