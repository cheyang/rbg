#!/usr/bin/env bash
# F1 check: do the harness test files at the PR head compile against the PR's
# own base (pr394-base = 0c0fcc11, PR #394 round-1 head)?
#
# The PR (#5) carries the pr394 verification harness, but its Layers 1–2 test
# files were adapted in rounds 3–5 to the UPSTREAM PR #394 API shape
# (RestartPolicyConfig, ctx-less checkRestartBackoff(instance, fresh, pods,
# inactive)) — shapes that do not exist in this branch's own base.
#
# CONFIRMED  = build fails at head (finding F1 present).
# REFUTED    = both packages build at head.
#
# Control run: the same packages build fine at the base WITHOUT the harness
# test files, proving the PR (not the base) introduces the break.
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd)"
RESULTS="$(cd "$(dirname "${BASH_SOURCE[0]}")/../results" && pwd)"
BASE_SHA="0c0fcc1183b9fdd5ae705e06500515eedf6ced48"   # pr394-base (PR #5 base)
ROUND1="f592f533"                                       # round-1 harness commit (base-compatible)
OUT="$RESULTS/f1-compile-at-head.txt"
: >"$OUT"

say() { echo "$*" | tee -a "$OUT"; }

say "=== F1: harness compile check at PR head ($(git -C "$REPO_ROOT" rev-parse --short HEAD)) ==="

cd "$REPO_ROOT"
UNIT_RC=0; INTEG_RC=0
go test -c -o /dev/null ./pkg/reconciler/roleinstance/sync/ >>"$OUT" 2>&1 || UNIT_RC=$?
go test -c -o /dev/null ./test/envtest/testcase/restart_policy/ >>"$OUT" 2>&1 || INTEG_RC=$?
say "unit package build rc=$UNIT_RC (sync), integration package build rc=$INTEG_RC (restart_policy)"

# Control: base WITHOUT the harness files must build.
WT="$(mktemp -d /tmp/pr5-f1.XXXXXX)"
git -C "$REPO_ROOT" worktree add --detach "$WT" "$BASE_SHA" >/dev/null 2>&1
rm -f "$WT/pkg/reconciler/roleinstance/sync/restart_backoff_verify_test.go" \
      "$WT/test/envtest/testcase/restart_policy/backoff_bug_verify_test.go"
CTRL_RC=0
(cd "$WT" && go test -c -o /dev/null ./pkg/reconciler/roleinstance/sync/) >>"$OUT" 2>&1 || CTRL_RC=$?
say "control: base ($BASE_SHA) WITHOUT harness test files builds rc=$CTRL_RC"
git -C "$REPO_ROOT" worktree remove --force "$WT" >/dev/null 2>&1

if [ "$UNIT_RC" -ne 0 ] || [ "$INTEG_RC" -ne 0 ]; then
  if [ "$CTRL_RC" -eq 0 ]; then
    say "VERDICT: CONFIRMED — harness breaks the test build of its own base; base alone builds fine."
    echo "F1 CONFIRMED"
    exit 0
  fi
  say "VERDICT: INCONCLUSIVE — base without harness also fails to build (pre-existing breakage?)."
  echo "F1 INCONCLUSIVE"
  exit 1
fi
say "VERDICT: REFUTED — harness compiles against its own base."
echo "F1 REFUTED"
exit 0
