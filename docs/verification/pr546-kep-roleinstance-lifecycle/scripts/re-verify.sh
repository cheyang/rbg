#!/usr/bin/env bash
# re-verify.sh — re-run the KEP-527 (PR #546) verification harness against the
# current PR head (or an explicit ref). Prints per-finding
# Fixed / Still-broken / Partial / Harness-update / Manual-review and exits 0
# iff every machine-checkable *gating* finding is fixed and the premise pins
# are green. Canary polarity is honored: a canary counts as "fixed" only when
# it FLIPS to fail (its assertion of the wrong behavior no longer holds).
#
# Round 3 (head fb6c2b36): the legacy-RIS-protocol removal pivot rewrites the
# wording the round-2 pins matched, so round-2 pins are kept for reference but
# no longer gate; the round-3 pins are the gating contract set. A round-2 pin
# failing with the round-3 pin passing means "superseded wording, substance
# preserved" — see check_round3_contracts.sh for the replacement anchors.
#
# Usage: bash docs/verification/pr546-kep-roleinstance-lifecycle/scripts/re-verify.sh [<ref>]
#   <ref> defaults to the current PR head fetched from manifest.pr
set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
TOPIC_DIR="$(dirname "$HERE")"
ROOT="$(cd "$TOPIC_DIR/../../.." && pwd)"
MANIFEST="$TOPIC_DIR/verify-manifest.json"

REF="${1:-}"
if [ -z "$REF" ]; then
  PR_URL="$(python3 -c "import json,sys;print(json.load(open('$MANIFEST'))['pr'])")"
  PR_NUM="${PR_URL##*/}"
  UPSTREAM="$(python3 -c "import json,sys;print('/'.join(json.load(open('$MANIFEST'))['pr'].split('/')[3:5]))")"
  echo "Fetching current head of $PR_URL ..."
  git -C "$ROOT" fetch "https://github.com/$UPSTREAM" "pull/$PR_NUM/head" || exit 2
  REF="$(git -C "$ROOT" rev-parse FETCH_HEAD)"
fi
echo "Re-verifying against ref: $REF"

WORKTREE="$(mktemp -d)/pr546-reverify"
git -C "$ROOT" worktree add --detach "$WORKTREE" "$REF" >/dev/null 2>&1 || { echo "worktree add failed"; exit 2; }
trap 'git -C "$ROOT" worktree remove --force "$WORKTREE" >/dev/null 2>&1' EXIT

# Graft the harness (committed on the verify branch) onto the code under review.
git -C "$ROOT" archive HEAD -- \
  docs/verification/pr546-kep-roleinstance-lifecycle \
  pkg/inplace/instance/readiness/kep527_claims_test.go \
  pkg/inplace/instance/lifecycle/kep527_claims_test.go \
  | tar -x -C "$WORKTREE"

echo "== L1: Go contract/canary tests =="
(cd "$WORKTREE" && timeout 600 go test ./pkg/inplace/instance/readiness/ ./pkg/inplace/instance/lifecycle/ -run 'TestKep527' -v 2>&1 | tee "$TOPIC_DIR/results/gotest-rerun.txt" | grep -E '^(---|=== RUN|ok|FAIL|PASS)' | tail -20)
GO_STATUS=${PIPESTATUS[0]}

echo
echo "== Doc-layer checks =="
bash "$WORKTREE/docs/verification/pr546-kep-roleinstance-lifecycle/scripts/check_kep_docs.sh" "$WORKTREE" \
  2>&1 | tee "$TOPIC_DIR/results/docchecks-rerun.txt" | grep -E '^(markdownlint|markdownlint-default|whitespace|see-also|parity|ALL DOC|DOC CHECK)'
DOC_STATUS="${PIPESTATUS[0]}"

echo
echo "== Round-3 contracts (gating; fixed-findings regression pins, red = regression) =="
CHECK_ROUND3_OUT="$TOPIC_DIR/results" \
  bash "$WORKTREE/docs/verification/pr546-kep-roleinstance-lifecycle/scripts/check_round3_contracts.sh" "$WORKTREE" \
  2>&1 | tee "$TOPIC_DIR/results/round3-contracts-rerun.txt" | grep -E '^(FAIL|OPEN|ROUND3)'
R3_STATUS="${PIPESTATUS[0]}"

echo
echo "== Round-2 contracts (reference only since the round-3 removal pivot; NOT gating) =="
CHECK_ROUND2_OUT="$TOPIC_DIR/results" \
  bash "$WORKTREE/docs/verification/pr546-kep-roleinstance-lifecycle/scripts/check_round2_contracts.sh" "$WORKTREE" \
  2>&1 | tee "$TOPIC_DIR/results/round2-contracts-rerun.txt" | grep -E '^(FAIL|OPEN|ROUND2)' | head -12

echo
echo "== Incremental review delta =="
LAST_REVIEWED=""
[ -f "$TOPIC_DIR/.last-reviewed" ] && LAST_REVIEWED="$(cat "$TOPIC_DIR/.last-reviewed")"
if [ -n "$LAST_REVIEWED" ]; then
  git -C "$ROOT" log --oneline "$LAST_REVIEWED..$REF" | tee "$TOPIC_DIR/results/delta.txt"
  echo "Advance the marker after this round: echo $REF > $TOPIC_DIR/.last-reviewed"
else
  git -C "$ROOT" log --oneline "$(git -C "$ROOT" merge-base origin/main "$REF")..$REF" | tee "$TOPIC_DIR/results/delta.txt"
fi

echo
echo "== Per-finding verdicts =="
premise_ok=0
if grep -q '^--- PASS: TestKep527ExistingCoordinationModel' "$TOPIC_DIR/results/gotest-rerun.txt" \
   && grep -q '^--- PASS: TestKep527ReadinessGatePrerequisite' "$TOPIC_DIR/results/gotest-rerun.txt"; then
  premise_ok=1; echo "P0  premise / doc code-claims: still accurate (contract pins green)"
else
  echo "P0  premise / doc code-claims: Harness-update or refuted premise — investigate (pins red)"
fi

if [ "$DOC_STATUS" -eq 0 ]; then
  echo "F-SEEALSO-502: Fixed (see-also targets all resolve)"
else
  if grep -q 'see-also: FAIL' "$TOPIC_DIR/results/docchecks-rerun.txt"; then
    echo "F-SEEALSO-502: Still-broken (dangling see-also reference reproduces)"
  else
    echo "F-SEEALSO-502: Still-broken or Harness-update (doc checks failed for another reason — see results/docchecks-rerun.txt)"
  fi
fi

if grep -q '^--- PASS: TestKep527CanaryCustomReadyFirstWriteOmitsStatus' "$TOPIC_DIR/results/gotest-rerun.txt"; then
  echo "F-CUSTOMREADY-STATUS-EMPTY: Still-broken (canary green = pre-existing bug present; pre-existing, NON-GATING for PR #546)"
elif grep -q '^--- FAIL: TestKep527CanaryCustomReadyFirstWriteOmitsStatus' "$TOPIC_DIR/results/gotest-rerun.txt"; then
  echo "F-CUSTOMREADY-STATUS-EMPTY: Fixed upstream (canary flipped) — invert the canary / promote to contract test"
else
  echo "F-CUSTOMREADY-STATUS-EMPTY: Harness-update (test missing or did not run)"
fi

# Round-3 fixed findings: verdicts come from the round-3 contract pins.
if [ "$R3_STATUS" -eq 0 ]; then
  for f in F-SCOPE-PRECREATE F-PROPAGATED-VS-LEGACY F-FINALIZER-BOOTSTRAP-RACE \
           F-NS-TEARDOWN-BYPASS F-RBGSET-FOREGROUND-BOUNDARY \
           F-INFLIGHT-ADMISSION-MUST F-DISABLE-SUPPORTED-CONTRADICTION \
           F-NOPOSTREADY-CLEANUP-CLARIFICATION F-WIRE-NAMES \
           CARRY-PREDELETE-LIVENESS CARRY-GATE-LIFETIME CARRY-ESCAPE-HATCH \
           CARRY-DEAD-ENGINE CARRY-PHASE-STORAGE CARRY-NOPODFINALIZERS \
           CARRY-KEPYAML-NOISE; do
    echo "$f: Fixed (round-3 contract pin green on this ref)"
  done
else
  echo "Round-3 fixed findings: one or more REGRESSED (see results/round3-contracts-rerun.txt)"
fi
echo "F-LEGACY-REMOVAL-COMPAT: Manual-review (design question — removal vs issue #527 compatibility; gate-off behavior of removed fields)"
echo "F-PROPAGATED-SNAPSHOT-EDIT: Manual-review (nit residue — no stated rule for user edits of the controller-written template snapshot)"

echo
if [ "$premise_ok" -eq 1 ] && [ "$GO_STATUS" -eq 0 ] && [ "$R3_STATUS" -eq 0 ] \
   && ! grep -q 'see-also: FAIL' "$TOPIC_DIR/results/docchecks-rerun.txt"; then
  echo "RESULT: all machine-checkable gating findings fixed (F-SEEALSO-502 aside)"
  exit 0
else
  echo "RESULT: gating findings remain open"
  exit 1
fi
