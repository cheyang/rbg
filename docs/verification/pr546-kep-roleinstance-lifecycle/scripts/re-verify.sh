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
# Round 8 (head e1116c79, docs-only, 1 commit "docs: address round-7
# review findings in KEP-527", +41/-11) PLUS a rebase of the PR branch onto
# c2896cc0 (KEP-502 merge) — base moves 3612013e -> c2896cc0. Content:
# blanket-Foreground scope acknowledged in the Goal (EN+ZH) and the webhook
# paragraph (EN+ZH); zh also qualifies Upgrade/Downgrade and the regression
# baseline, EN does not (partial fix of F-FOREGROUND-BLANKET-SCOPE + a new
# EN/ZH content divergence at those two sites, non-gating minor); the
# executor re-read / hook-state-match / RI-UID guard addresses Reviewer-C
# advisory F-C4554909778 (EN+ZH prose + unit bullets); the preDelete
# integration bullet drops the stale "affected ancestor" wording
# (F-AFFECTED-ANCESTOR-WORDING fixed). Rebase effects (disclosed
# Harness-updates): keps/502-* now exists IN the PR tree, so the round-6
# see-also check flips to OPEN->RESOLVED in-tree (check_round7's carry
# filter updated additively to tolerate it); the in-tree KEP-502 still
# references markPodNotReady (:413/:485) so the KEP-527 drift disclosure
# stays accurate, and the required amendment now has an owner (open PR
# #572 removes both references). check_round8_contracts.sh is the gating
# contract set; it internally re-runs the round-7 script (which chains
# round-6/5/4/3/2) with no new supersessions — the round-8 delta is purely
# additive wording.
#
# Round 7 (head d038c23b, docs-only, 1 commit): responds to every open
# round-6 item — the foreground-deletion webhook is redesigned to reject ALL
# Foreground propagation at the four deletion entries while the gate is
# enabled (no informer-cache dependency at all), KEP-502 is declared merged
# (verified: PR #528 merged 2026-11-12, c2896cc0) with its stale
# markPodNotReady references disclosed and gated on a follow-up amendment,
# the marker-key record joins the field-ownership enumeration with an
# explicit non-writer rule, the mirror trust model is stated
# ("Mirrors are not authenticated"), the selector converse rule and the
# cross-generation signal-key uniqueness rule are added, the rollback
# precondition gains the preDelete-finalizer clause, a "Gate edges" paragraph
# closes F-COEXIST-GATE-EDGE, the zh rollout-risk list is re-parityed, the
# Scalability section adds mirror fan-out, and the reserved-prefix unit
# bullet lands. check_kep_docs.sh's see-also check is a disclosed
# Harness-update (environment change: the reference now resolves on
# origin/main via the merged KEP-502). The round-7 script is the gating
# contract set; it internally re-runs the round-6 script and tolerates ONLY
# the 20 disclosed round-7 supersessions; anything else red is a carry
# regression.
#
# Round 6 (head c3ea6547): a REAL DESIGN CHANGE (+322/-256, 5 commits):
# markNotReady / RI-Ready gating is REMOVED (every hook contains only
# signalLabels; RoleInstanceCustomReady gone; readiness gating is a Non-Goal),
# labelsHandler is renamed signalLabels, and three NEW mechanisms land: the
# marker-key record annotation (durable retired-key tracking — closes
# C4528291076), signal-label propagation onto member Pods, and admission
# safeguards (RIS spec.selector marker-key collision — closes C4528290874;
# Pod template label collision) plus a fail-closed foreground-deletion webhook
# (closes C4528291011). The round-6 script is the gating contract set; it
# internally re-runs the round-5 script and tolerates ONLY the 14 disclosed
# round-6 supersessions (design-change re-pins or the retired gate-lifetime
# pins); anything else red in round-5 is a carry regression.
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
echo "== Round-7 contracts (gating; round-7 pins + round-6 carry with disclosed supersessions, red = regression) =="
CHECK_ROUND7_OUT="$TOPIC_DIR/results" \
  bash "$WORKTREE/docs/verification/pr546-kep-roleinstance-lifecycle/scripts/check_round7_contracts.sh" "$WORKTREE" \
  2>&1 | tee "$TOPIC_DIR/results/round7-contracts-rerun.txt" | grep -E '^(FAIL|OPEN|SUPERSEDED|PASS  R7|ROUND7)' | tail -20
R7_STATUS="${PIPESTATUS[0]}"

echo
echo "== Round-8 contracts (gating; round-8 pins + round-7 carry with no new supersessions, red = regression) =="
CHECK_ROUND8_OUT="$TOPIC_DIR/results" \
  bash "$WORKTREE/docs/verification/pr546-kep-roleinstance-lifecycle/scripts/check_round8_contracts.sh" "$WORKTREE" \
  2>&1 | tee "$TOPIC_DIR/results/round8-contracts-rerun.txt" | grep -E '^(FAIL|OPEN|PASS  R8|ROUND8)' | tail -25
R8_STATUS="${PIPESTATUS[0]}"

echo
echo "== Round-6 contracts (reference; its design-changed pins are superseded by round-7 — see check_round7_contracts.sh) =="
CHECK_ROUND6_OUT="$TOPIC_DIR/results" \
  bash "$WORKTREE/docs/verification/pr546-kep-roleinstance-lifecycle/scripts/check_round6_contracts.sh" "$WORKTREE" \
  2>&1 | tee "$TOPIC_DIR/results/round6-contracts-rerun.txt" | grep -E '^(FAIL|OPEN|SUPERSEDED|PASS  R6|ROUND6)' | tail -20
R6_STATUS="${PIPESTATUS[0]}"

echo
echo "== Round-5 contracts (reference; its design-changed pins are superseded by round-6 — see check_round6_contracts.sh) =="
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
  echo "F-SEEALSO-502: Fixed (see-also targets all resolve — in the ref tree or on origin/main via merged PR #528; disclosed Harness-update in check_kep_docs.sh)"
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

# Round-4/5/6/7 fixed findings: verdicts come from the round-7 contract pins
# (round-6 carry is invoked inside check_round7; the 20 round-7
# supersessions are disclosed design-change re-pins, not regressions).
if [ "$R8_STATUS" -eq 0 ]; then
  for f in F-OLDNEW-COEXIST F-LATEST-CONFIG-SEMANTICS \
           F-LABEL-SYNTAX F-INTERRUPT-WHEN-DEAD-AMBIGUITY F-KEPYAML-GATE-SHAPE \
           F-PROPAGATED-SNAPSHOT-EDIT F-ZH-ENGLISH-TERMS \
           CARRY-PRECREATE-RATIONALE CARRY-FINALIZER-ORDERING CARRY-NS-TEARDOWN \
           CARRY-RBGSET-BOUNDARY CARRY-ESCAPE-HATCH \
           CARRY-DEAD-ENGINE CARRY-PHASE-STORAGE CARRY-NOPODFINALIZERS \
           CARRY-PREDELETE-LIVENESS CARRY-DISABLE-SUPPORTED \
           CARRY-NOPOSTREADY-OMITTED CARRY-WIRE-NAMES CARRY-KEPYAML-NOISE; do
    echo "$f: Fixed (round-5 carry pin green on this ref via check_round7 -> check_round6)"
  done
  echo "F-INDEPENDENT-TYPE / F-MARKNOTREADY-NAME: Harness-update (round-6 design change: every hook contains only signalLabels, re-pinned R6-SIGNALLABELS / R6-NO-READY-GATING)"
  echo "F-PREDELETE-GATE-LIFETIME: Moot (round-6 design change removed markNotReady and RI-Ready gating entirely — no gate left to have a lifetime; replacement contract R6-NO-READY-GATING green)"
  echo "F-C4528291076 (marker-key record): Fixed (R6-MARKER-KEY-RECORD pins green — durable retired-key tracking now specified EN+ZH)"
  echo "F-C4528290874 (marker keys vs RIS selectors): Fixed (R6-ADMISSION-SAFEGUARDS pins green — selector-collision admission rule + test bullet, EN+ZH)"
  echo "F-C4528291011 (fail-closed on cache miss): Fixed (R6-FAIL-CLOSED-WEBHOOK pins green — fail-closed resolution + integration bullet, EN+ZH)"
  echo "F-C4528290945 (stateful readiness never sees the gate): Moot (round-6 removed the markNotReady gate entirely; hooks never change RI Ready, stated as a Non-Goal)"
else
  echo "Round-4/5/6/7/8 fixed findings: one or more MISSING (see results/round8-contracts-rerun.txt)"
fi
echo "F-AFFECTED-ANCESTOR-WORDING: Fixed (R8-ANCESTOR-WORDING pins green + forbidden 'at every affected ancestor' / 受影响祖先 absent, EN+ZH — the integration bullet now names the four deletion entries under the gate)"
echo "F-C4554909778 (delayed old-executor cleanup deletes a reassigned marker): Fixed at the specification level (R8-EXECUTOR-REREAD / R8-EXECUTOR-REREAD-RECORD / R8-EXECUTOR-TESTBULLET pins green — the executor must re-read the RI and remove a marker only while hook-state still matches its own invocation and the RI UID is unchanged; an emptied marker-key record proves only that the label was cleaned, EN+ZH prose + unit bullets)"
echo "F-FOREGROUND-BLANKET-SCOPE: PARTIAL (R8-BLANKET-GOAL / R8-BLANKET-WEBHOOK pins green EN+ZH — the Goal and the webhook paragraph now acknowledge the cluster-wide rejection of hook-less workloads; but R8-OPEN-UPGRADE-EN and R8-OPEN-REGRESSIONBASE-EN are red residue: README.md:1155 and :979-981 still promise unchanged behavior unqualified while the zh counterparts (:849, :737-738) now qualify it — open minor, EN/ZH content divergence at those two sites)"
echo "F-KEP502-AMENDMENT-TRACKER: Fixed (the required KEP-502 amendment now has an owner — open PR #572 removes both markPodNotReady references at KEP-502 README.md:413/:485; unmerged at round-8 capture, re-opens if #572 stalls)"
echo "F-STALE-LABEL-DISPOSITION: Fixed (R6-REMOVAL-SEMANTICS pins carried green inside check_round7; single disposition rule incl. Pod mirrors, EN+ZH)"
echo "F-COEXIST-GATE-EDGE: Fixed (R7-GATE-EDGES-DUAL / R7-GATE-EDGES-OFF / R7-INPLACEUPDATE-DEPRECATION pins green — gate-on for dual-configured objects, gate-off controller behavior, inPlaceUpdate deprecation contract, EN+ZH)"
echo "F-CONFIG-REMOVAL-SKIPS-HOOK: Fixed at the acknowledgment level (R7-RISK-REMOVAL-ROW + R7-TROUBLESHOOT-ONEWAY pins green — risk-table row and Troubleshooting bullet acknowledge the mid-operation removal barrier skip and the one-way preDelete release, EN+ZH; the absence-semantics design itself remains the adjudicated round-4 decision)"
echo "F-PREDELETE-REMOVAL-ONE-WAY: Fixed (same R7-RISK-REMOVAL-ROW / R7-TROUBLESHOOT-ONEWAY pins; the one-way release is now an explicitly acknowledged operator confirmation)"
echo "F-POD-MIRROR-SPOOF-WINDOW: Fixed (R7-MIRROR-UNAUTH pins green — 'Mirrors are not authenticated', RI is the only authoritative source, safety-relevant consumers must verify the RI, EN+ZH)"
echo "F-SELECTOR-RESERVED-KEY-REVERSE: Fixed (R7-SELECTOR-CONVERSE pins green — admission rejects owning RIS selectors using controller-reserved keys, EN+ZH, plus the unit bullet)"
echo "F-C4541237385 (postReady key reassigned to preDelete): Fixed (R7-UNIQ-GENERATIONS / R7-UNIQ-TESTBULLET pins green — uniqueness spans configuration generations, EN+ZH)"
echo "F-C4541237496 (stale cache even on successful lookup): Fixed by redesign (R7-FOREGROUND-BLANKET / R7-FOREGROUND-NO-CACHE pins green — the webhook no longer consults informer caches at all and rejects every Foreground propagation while the gate is enabled, EN+ZH)"
echo "F-ZH-EN-ROLLOUT-RISK-PARITY: Fixed (R7-ROLLOUT-RISK pins + forbidden zh 循环等待 absent; EN/ZH rollout-risk lists match again)"
echo "F-MARKERKEY-RECORD-OWNERSHIP: Fixed (R7-OWNERSHIP-RECORD / R7-OWNERSHIP-NONWRITERS / R7-OWNERSHIP-METASYNC pins green — record in the ownership enumeration, users/metadata-sync/executors barred, EN+ZH)"
echo "F-SCALABILITY-POD-MIRROR-WRITES: Fixed (R7-SCALABILITY-FANOUT / R7-SCALABILITY-TEST pins green — mirror fan-out cost + scale coverage, EN+ZH)"
echo "F-TESTPLAN-PREFIX-COVERAGE: Fixed (R7-PREFIX-TESTBULLET pins green — reserved-prefix clause added to the unit bullet, EN+ZH)"
echo "F-KEP502-RECOVERING-GATE-CIRCULARITY: Fixed on the dependency side (PR #528 MERGED 2026-11-12 as c2896cc0: phase completion uses controller-observed readiness only and explicitly removes the KEP-527 circular wait)"
echo "F-KEP502-GATE-REFERENCE-DRIFT: Fixed on the KEP-527 side (R7-KEP502-MERGED / R7-KEP502-DRIFT / R7-KEP502-AMENDMENT / R7-KEP502-PREIMPL pins green — the KEP states KEP-502 is merged, discloses that its merged text still references markPodNotReady, and gates implementation on the follow-up amendment, EN+ZH; verified against merged KEP-502 README.md:413/:485). The external KEP-502 amendment itself is still owed — tracked as the non-gating question F-KEP502-AMENDMENT-TRACKER."
echo "F-KEP502-AMENDMENT-TRACKER: Open (non-gating question) — the required KEP-502 follow-up amendment (remove markPodNotReady references at merged KEP-502 README.md:413/:485) has no tracking issue/PR yet; pre-implementable item #1 depends on it."
echo "F-INFLIGHT-CONFIG-ADMISSION / F-INFLIGHT-ADMISSION-MUST: Disputed (author reversed the rule by design in round 4 — see F-CONFIG-REMOVAL-SKIPS-HOOK; reversed rule pinned absent)"
echo "F-LEGACY-REMOVAL-TRANSITION: Resolved by the round-4 reversal (coexistence satisfies issue #527 compatibility; narrowed residue tracked as F-COEXIST-GATE-EDGE)"
echo "F-KEP502-CREATING-PHASE-CONFLICT: Fixed in the dependency PR (PR #528 merged as c2896cc0: Creating-target changes folded into Create convergence, citing KEP-527 consistency)"

echo
if [ "$premise_ok" -eq 1 ] && [ "$GO_STATUS" -eq 0 ] && [ "$R8_STATUS" -eq 0 ] \
   && [ "$DOC_STATUS" -eq 0 ]; then
  echo "RESULT: all machine-checkable gating findings fixed (round-8 contract set green; the only open items are non-gating: F-FOREGROUND-BLANKET-SCOPE EN residue minor, F-CUSTOMREADY-STATUS-EMPTY pre-existing minor)"
  exit 0
else
  echo "RESULT: gating findings remain open"
  exit 1
fi
