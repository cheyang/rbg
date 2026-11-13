#!/usr/bin/env bash
# Re-verification for PR https://github.com/sgl-project/rbg/pull/572 (KEP-502 status model).
# Usage: bash scripts/re-verify.sh [<fixed-ref>]
# With no ref, fetches the current PR head from the manifest's "pr" URL and grafts
# this harness onto it. Unit + integration layers only; the live layer is skipped.
set -uo pipefail

MANIFEST="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/verify-manifest.json"
HERE="$(cd "$(dirname "${MANIFEST}")" && pwd)"           # docs/verification/kep502-status
REPO_ROOT="$(cd "$HERE/../../.." && pwd)"

# --- resolve the ref under test -------------------------------------------------
REF="${1:-}"
if [[ -z "$REF" ]]; then
  PR_URL=$(python3 -c "import json,sys; print(json.load(open('$MANIFEST'))['pr'])" 2>/dev/null)
  PR_NUM="${PR_URL##*/}"
  echo ">> fetching current head of PR #$PR_NUM"
  git -C "$REPO_ROOT" fetch origin "pull/${PR_NUM}/head" || { echo "fetch failed"; exit 2; }
  REF="FETCH_HEAD"
fi
echo ">> ref under test: $REF ($(git -C "$REPO_ROOT" rev-parse "$REF"))"

# --- graft the harness onto the ref (worktree; production code untouched) -------
GRAFT="$(mktemp -d)/rbg-verify"
git -C "$REPO_ROOT" worktree add --detach "$GRAFT" "$REF" || exit 2
trap 'git -C "$REPO_ROOT" worktree remove --force "$GRAFT" 2>/dev/null' EXIT

# copy the harness files (tests + docs) from this branch's checkout onto the grafted tree
for rel in \
  pkg/reconciler/roleinstanceset/progressing_contractverify_test.go \
  pkg/reconciler/roleinstance/progressing_contractverify_test.go \
  internal/controller/workloads/rolebasedgroup_progressing_contractverify_test.go; do
  mkdir -p "$GRAFT/$(dirname "$rel")"
  cp "$REPO_ROOT/$rel" "$GRAFT/$rel" || { echo "missing harness file: $rel"; exit 2; }
done
mkdir -p "$GRAFT/docs/verification"
cp -r "$HERE" "$GRAFT/docs/verification/" 2>/dev/null

cd "$GRAFT" || exit 2

declare -A RESULT
overall=0

run_contract() { # <go package> <test name> <finding id>
  # -v so "no tests to run" cannot masquerade as a pass
  local out
  if ! out=$(go test "$1" -run "$2" -count=1 -v 2>&1); then
    RESULT["$3"]="still-broken"
    overall=1
    return
  fi
  if ! grep -q "^=== RUN" <<<"$out"; then
    RESULT["$3"]="harness-error (test did not run)"
    overall=1
    return
  fi
  RESULT["$3"]="fixed"
}

echo ">> L1 unit: contract tests"
run_contract ./pkg/reconciler/roleinstanceset "TestEstablishedSetMissingInstanceStaysRecovering" "F1-ris-recovery-scaling-flap"
go test ./pkg/reconciler/roleinstanceset -run "TestScaleUpAfterConvergenceStillReportsScalingUp" -count=1 >/dev/null 2>&1 || { RESULT["F1-control"]="still-broken"; overall=1; }
run_contract ./internal/controller/workloads "TestRBGAggregatesRISInvalidSpecAsConfigurationBlocker" "F2-ris-invalidspec-not-propagated"
run_contract ./internal/controller/workloads "TestMixedBackendRBGCleansUpProgressing" "F3-mixed-backend-stale-progressing"
run_contract ./pkg/reconciler/roleinstance "TestInstanceBlockerSelectionFollowsFamilyPriority" "F4-ri-blocker-append-order"
run_contract ./internal/controller/workloads "TestRBGBlockerFamilySurvivesRecomputeAndUnclassifiedRetry" "F5-rbg-entry-blocker-retention"
run_contract ./pkg/reconciler/roleinstanceset "TestEstablishedSetReadinessFlapThenMemberLossStaysRecovering" "F11-ris-readiness-flap-loss-regression"
run_contract ./pkg/reconciler/roleinstanceset "TestEstablishedSetRecoveryAfterBlockerInterruptionStaysRecovering" "F12-ris-blocker-interruption-establishment-loss"

echo ">> L1 unit: full package suites (regression guard)"
for pkg in ./pkg/reconciler/roleinstance/ ./pkg/reconciler/roleinstanceset/ ./internal/controller/workloads/; do
  if ! go test "$pkg" -count=1 >/dev/null 2>&1; then
    echo "   package suite failed: $pkg"
    overall=1
  fi
done

echo ">> L2 integration: envtest (needs KUBEBUILDER_ASSETS)"
if [[ -n "${KUBEBUILDER_ASSETS:-}" ]]; then
  if ! go test ./test/envtest/testcase/rbg/ ./test/envtest/testcase/stateful/ -count=1 >/dev/null 2>&1; then
    echo "   envtest suites failed"
    overall=1
  fi
else
  echo "   skipped (KUBEBUILDER_ASSETS not set)"
fi

echo
echo "== per-finding verdicts =="
for k in F1-ris-recovery-scaling-flap F2-ris-invalidspec-not-propagated F3-mixed-backend-stale-progressing F4-ri-blocker-append-order F5-rbg-entry-blocker-retention F11-ris-readiness-flap-loss-regression F12-ris-blocker-interruption-establishment-loss; do
  echo "  $k: ${RESULT[$k]:-not-run}"
done
echo "F6/F7/F8 (legacy phase default, RIS preflight status write, FailedScale cleanup ordering) are code-path findings without contract tests — re-check by inspection."
[[ $overall -eq 0 ]] && echo "ALL FINDINGS FIXED" || echo "SOME FINDINGS STILL BROKEN"
exit $overall
