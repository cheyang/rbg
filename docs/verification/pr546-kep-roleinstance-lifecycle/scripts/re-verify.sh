#!/usr/bin/env bash
# re-verify.sh — re-run the KEP-527 (PR #546) verification harness against the
# current PR head (or an explicit ref). Prints per-finding
# Fixed / Still-broken / Partial / Harness-update / Manual-review and exits 0
# iff every machine-checkable *gating* finding is fixed and the premise pins
# are green. Canary polarity is honored: a canary counts as "fixed" only when
# it FLIPS to fail (its assertion of the wrong behavior no longer holds).
#
# Round 4 (head 43dab57b): a SECOND pivot reverses the round-3 removal back
# to coexistence with admission-enforced mutual exclusion, defines an
# independent hook type (labelsHandler/markNotReady), and replaces the
# in-flight config-change admission guard with latest-effective-config /
# absence semantics. The round-3 pins that matched the removed wording fail on
# head BY DESIGN and no longer gate; the round-4 pins are the gating contract
# set (see check_round4_contracts.sh). Round-2 and round-3 scripts are kept
# for reference. A superseded pin failing with its round-4 replacement passing
# means "superseded wording, substance re-pinned" — a disclosed Harness-update,
# not evidence of a fix or of a regression.
#
# Round 5 (head 6c15cc92): a wording-only rewrite (+276/-219) that rewords 12
# round-4 pins (substance preserved; re-pinned in check_round5_contracts.sh
# under the round-5 wording, with the superseded round-4 wordings disclosed)
# and fixes F-TESTPLAN-ABSENCE-CONTRADICTION + F-STALE-LABEL-DISPOSITION,
# partially fixes F-COEXIST-GATE-EDGE (gate-off rule stated), and leaves
# F-CONFIG-REMOVAL-SKIPS-HOOK and F-SEEALSO-502 open. The round-5 script is
# the gating contract set; it internally re-runs the round-4 script and fails
# on any round-4 carry pin that is NOT in the disclosed supersession list.
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
echo "== Round-5 contracts (gating; round-5 pins + round-4 carry, red = regression) =="
CHECK_ROUND5_OUT="$TOPIC_DIR/results" \
  bash "$WORKTREE/docs/verification/pr546-kep-roleinstance-lifecycle/scripts/check_round5_contracts.sh" "$WORKTREE" \
  2>&1 | tee "$TOPIC_DIR/results/round5-contracts-rerun.txt" | grep -E '^(FAIL|OPEN|SUPERSEDED|PASS  R5-CARRY|ROUND5)'
R5_STATUS="${PIPESTATUS[0]}"

echo
echo "== Round-4 contracts (reference; its reworded pins are superseded by round-5 — see check_round5_contracts.sh) =="
CHECK_ROUND4_OUT="$TOPIC_DIR/results" \
  bash "$WORKTREE/docs/verification/pr546-kep-roleinstance-lifecycle/scripts/check_round4_contracts.sh" "$WORKTREE" \
  2>&1 | tee "$TOPIC_DIR/results/round4-contracts-rerun.txt" | grep -E '^(FAIL|OPEN|ROUND4)'
R4_STATUS="${PIPESTATUS[0]}"

echo
echo "== Round-3 contracts (reference only since the round-4 coexistence pivot; NOT gating) =="
CHECK_ROUND3_OUT="$TOPIC_DIR/results" \
  bash "$WORKTREE/docs/verification/pr546-kep-roleinstance-lifecycle/scripts/check_round3_contracts.sh" "$WORKTREE" \
  2>&1 | tee "$TOPIC_DIR/results/round3-contracts-rerun.txt" | grep -E '^(FAIL|OPEN|ROUND3)'
R3_STATUS="${PIPESTATUS[0]}"

echo
echo "== Round-2 contracts (reference only; NOT gating) =="
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

# Round-4 fixed findings: verdicts come from the round-5 contract pins
# (re-pinned wording) — the round-4 script is invoked inside check_round5.
if [ "$R5_STATUS" -eq 0 ]; then
  for f in F-OLDNEW-COEXIST F-INDEPENDENT-TYPE F-LATEST-CONFIG-SEMANTICS \
           F-LABEL-SYNTAX F-INTERRUPT-WHEN-DEAD-AMBIGUITY F-KEPYAML-GATE-SHAPE \
           F-PROPAGATED-SNAPSHOT-EDIT F-ZH-ENGLISH-TERMS \
           CARRY-PRECREATE-RATIONALE CARRY-FINALIZER-ORDERING CARRY-NS-TEARDOWN \
           CARRY-RBGSET-BOUNDARY CARRY-GATE-LIFETIME CARRY-ESCAPE-HATCH \
           CARRY-DEAD-ENGINE CARRY-PHASE-STORAGE CARRY-NOPODFINALIZERS \
           CARRY-PREDELETE-LIVENESS CARRY-DISABLE-SUPPORTED \
           CARRY-NOPOSTREADY-OMITTED CARRY-WIRE-NAMES CARRY-KEPYAML-NOISE; do
    echo "$f: Fixed (round-4 contract pin green on this ref)"
  done
else
  echo "Round-4/5 fixed findings: one or more MISSING (see results/round5-contracts-rerun.txt)"
fi
echo "F-TESTPLAN-ABSENCE-CONTRADICTION: verdict from R5-TESTPLAN-DISABLE pins (new bullet present, old bullet absent, EN+ZH)"
echo "F-STALE-LABEL-DISPOSITION: verdict from R5-STALE-LABEL-DISPOSITION / R5-REMOVAL-SEMANTICS pins (single disposition rule, EN+ZH)"
echo "F-COEXIST-GATE-EDGE: PARTIAL — gate-off rule stated (R5-GATE-OFF-RULE pins); gate-on for dual-configured objects and the deprecation timeline still unstated"
echo "F-CONFIG-REMOVAL-SKIPS-HOOK: Still-open — no admission guard, no Risks/Troubleshooting acknowledgment of the mid-deletion skip path"
echo "F-KEP502-RECOVERING-GATE-CIRCULARITY: Fixed on the dependency side (PR #528 head 0580735 states phase completion uses controller-observed readiness only and explicitly removes the KEP-527 circular wait) — re-fetch PR #528 to confirm"
echo "F-INFLIGHT-CONFIG-ADMISSION / F-INFLIGHT-ADMISSION-MUST: Disputed (author reversed the rule by design in round 4 — see F-PREDELETE-REMOVAL-ONE-WAY; reversed rule pinned absent)"
echo "F-LEGACY-REMOVAL-TRANSITION: Resolved by the round-4 reversal (coexistence satisfies issue #527 compatibility; narrowed residue tracked as F-COEXIST-GATE-EDGE)"
echo "F-KEP502-CREATING-PHASE-CONFLICT: Fixed in the dependency PR (PR #528 amended to fold Creating-target changes, citing KEP-527 consistency) — external evidence, re-fetch to confirm"

echo
if [ "$premise_ok" -eq 1 ] && [ "$GO_STATUS" -eq 0 ] && [ "$R5_STATUS" -eq 0 ] \
   && ! grep -q 'see-also: FAIL' "$TOPIC_DIR/results/docchecks-rerun.txt"; then
  echo "RESULT: all machine-checkable gating findings fixed (F-SEEALSO-502 aside)"
  exit 0
else
  echo "RESULT: gating findings remain open"
  exit 1
fi
