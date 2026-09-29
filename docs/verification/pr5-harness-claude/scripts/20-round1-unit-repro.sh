#!/usr/bin/env bash
# Premise/substance check (unit layer): the round-1 harness (commit f592f533,
# written against the round-1 API) reproduces the four unit-level bug claims
# (B1 int64 overflow, B5 first-delay = 2*base canary, B4a negative-base bypass)
# when run against the PR #5 base production code (= PR #394 round-1 head).
#
# Expected (bug present, matching results/layer1-gotest.txt committed in the PR):
#   TestRestartBackoffVerify_B1_Overflow          FAIL   (bug reproduced)
#   TestRestartBackoffVerify_B1_NoCapOverflow     FAIL   (bug reproduced)
#   TestRestartBackoffVerify_B5_OffByOne          PASS   (canary: 2*base behavior present)
#   TestRestartBackoffVerify_B5_Count0IsUnreachableAtRuntime PASS
#   TestRestartBackoffVerify_B4_NegativeDelayBypass FAIL  (bug reproduced)
#
# REFUTED would mean the round-1 evidence cannot be regenerated — i.e. the
# committed evidence does not correspond to reproducible behavior.
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd)"
RESULTS="$(cd "$(dirname "${BASH_SOURCE[0]}")/../results" && pwd)"
BASE_SHA="0c0fcc1183b9fdd5ae705e06500515eedf6ced48"
ROUND1="f592f533"
OUT="$RESULTS/round1-unit-repro.txt"
: >"$OUT"

say() { echo "$*" | tee -a "$OUT"; }

say "=== Round-1 unit-layer reproduction at base $BASE_SHA with round-1 harness $ROUND1 ==="

WT="$(mktemp -d /tmp/pr5-u.XXXXXX)"
git -C "$REPO_ROOT" worktree add --detach "$WT" "$BASE_SHA" >/dev/null 2>&1
git -C "$WT" checkout "$ROUND1" -- \
  pkg/reconciler/roleinstance/sync/restart_backoff_verify_test.go \
  test/envtest/testcase/restart_policy/backoff_bug_verify_test.go

(cd "$WT" && go test ./pkg/reconciler/roleinstance/sync/ -run RestartBackoffVerify -v) >>"$OUT" 2>&1
RUN_RC=$?
git -C "$REPO_ROOT" worktree remove --force "$WT" >/dev/null 2>&1
say "go test rc=$RUN_RC"

check() { # $1=test name $2=expected FAIL|PASS
  if grep -qE "^--- $2: $1 " "$OUT"; then say "  $1: $2 (as expected)"; return 0; fi
  say "  $1: NOT $2 (unexpected!)"; return 1
}

ok=1
check TestRestartBackoffVerify_B1_Overflow FAIL || ok=0
check TestRestartBackoffVerify_B1_NoCapOverflow FAIL || ok=0
check TestRestartBackoffVerify_B5_OffByOne PASS || ok=0
check TestRestartBackoffVerify_B5_Count0IsUnreachableAtRuntime PASS || ok=0
check TestRestartBackoffVerify_B4_NegativeDelayBypass FAIL || ok=0

if [ "$ok" -eq 1 ]; then
  say "VERDICT: REPRODUCED — round-1 unit evidence regenerates exactly (B1/B4a bugs present, B5 canary green)."
  echo "UNIT-REPRO REPRODUCED"
  exit 0
fi
say "VERDICT: NOT REPRODUCED — round-1 unit evidence could not be regenerated."
echo "UNIT-REPRO NOT-REPRODUCED"
exit 1
