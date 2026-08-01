#!/usr/bin/env bash
# Step 4 of the verifier skill: prove the harness BITES.
#
# A red test that is red for the wrong reason proves nothing. This script temporarily applies
# the *proposed* fix for each finding to the production code, re-runs the corresponding test,
# confirms it flips, then REVERTS the fix and confirms the production diff is empty again.
#
# This script never leaves production code modified: it restores from git on every exit path.
set -uo pipefail
cd "$(dirname "$0")/../../../.." || exit 2
HELM=${HELM:-helm}
R=docs/verification/pr416-api-compat-toggle

VALUES=deploy/helm/rbgs/values.yaml
WLREC=pkg/reconciler/workload_reconciler.go

cleanup() {
  echo
  echo "--- reverting all production edits ---"
  git checkout -- "$VALUES" "$WLREC" 2>/dev/null
  echo "production diff (must be empty, harness files excluded):"
  dirty=$(git status --porcelain -- "$VALUES" "$WLREC")
  if [ -n "$dirty" ]; then
    echo "!! PRODUCTION CODE STILL DIRTY:"; printf '%s\n' "$dirty"; exit 3
  fi
  echo "  (clean)"
}
trap cleanup EXIT

rc=0

# ---------------------------------------------------------------------------------------
echo "=== BITE CHECK F1: flip the values.yaml default to the documented 'false' ==="
echo "before: $(grep -A2 '^compatibility:' $VALUES | tail -1)"
# Only touch the compatibility block's `enabled:` line.
perl -0pi -e 's/(compatibility:\n  v1alpha1:\n    enabled: )true/${1}false/' "$VALUES"
echo "after:  $(grep -A2 '^compatibility:' $VALUES | tail -1)"
if ! git diff --quiet -- "$VALUES"; then
  echo "  (edit applied)"
else
  echo "  HARNESS PROBLEM: the values.yaml edit did not apply"; rc=3
fi

if bash "$R/scripts/01-helm-default-rbac.sh" >/tmp/bite-f1.txt 2>&1; then
  echo "  -> 01-helm-default-rbac.sh now PASSES. The test bites: it was red purely because of"
  echo "     the default value, and the documented default makes it green."
else
  echo "  -> 01-helm-default-rbac.sh STILL FAILS with the fix applied. The test does NOT bite"
  echo "     as claimed; F1 needs re-examination:"
  tail -12 /tmp/bite-f1.txt | sed 's/^/       /'
  rc=1
fi
git checkout -- "$VALUES"
echo

# ---------------------------------------------------------------------------------------
echo "=== BITE CHECK F3: gate NewWorkloadReconciler so legacy types are refused ==="
# The proposed fix: refuse to build a legacy reconciler. We emulate the minimal form of the
# fix -- returning an error for the three legacy types -- to prove the test observes it.
cp "$WLREC" /tmp/wlrec.orig
python3 - "$WLREC" <<'PY'
import re, sys
p = sys.argv[1]
src = open(p).read()
needle = "\tswitch {\n\tcase workload.String() == constants.DeploymentWorkloadType:\n\t\treturn NewDeploymentReconciler(scheme, client), nil"
repl = ("\tswitch {\n"
        "\tcase workload.String() == constants.DeploymentWorkloadType:\n"
        "\t\treturn nil, fmt.Errorf(\"BITECHECK: v1alpha1 compatibility disabled: %s\", workload.String())")
assert needle in src, "anchor not found -- bite check cannot be applied"
open(p, "w").write(src.replace(needle, repl, 1))
PY
if [ $? -ne 0 ]; then
  echo "  HARNESS PROBLEM: could not apply the F3 bite edit"; rc=3
else
  echo "  (edit applied: Deployment case now returns an error)"
  out=$(go test ./internal/controller/workloads/ \
          -run 'TestVerifyPR416_F3_LegacyReconcilerStillBuiltWhenCompatDisabled/apps/v1/Deployment' \
          -count=1 -v 2>&1)
  if printf '%s\n' "$out" | grep -q 'F3 FIXED'; then
    echo "  -> the Deployment subtest reports F3 FIXED. The test bites: it observes the gate."
  else
    echo "  -> the Deployment subtest did NOT flip. F3 needs re-examination:"
    printf '%s\n' "$out" | grep -E 'FAIL|PASS|REPRODUCED|FIXED|cannot|error' | head -12 | sed 's/^/       /'
    rc=1
  fi
fi
git checkout -- "$WLREC"
echo

if [ "$rc" -eq 0 ]; then
  echo "RESULT: harness bites for F1 and F3 -- both flip when the proposed fix is applied."
else
  echo "RESULT: bite check FAILED (rc=$rc). Do not trust the affected findings until resolved."
fi
exit $rc
