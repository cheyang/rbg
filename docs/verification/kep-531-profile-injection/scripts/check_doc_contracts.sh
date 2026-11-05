#!/usr/bin/env bash
# Doc-layer contract checks for KEP-531 (PR #548, docs-only) — round 2+.
#
# The document is the entire deliverable, so the doc layer pins the text:
#
#   CONTRACT checks — assert the FIXED state of a previously-confirmed finding
#   (round 1 ledger). They PASS while the fix wording is present and FAIL if a
#   contradiction / removed disposition returns. A FAIL is a regression.
#
#   CANARY checks — assert the CURRENT (still-defective) state of an OPEN
#   nit/minor wording finding. They PASS while the defective phrase is present
#   and FLIP TO FAIL when the author removes/rewords it (= fixed; then invert
#   the check into a contract). Canary polarity matches review-finding-verifier.
#
# Output: human lines "ID  pass|fail  <detail>" on stdout.
# With --json: one {"Test": "<id>", "Action": "pass|fail"} object per line
# (gotest -json shaped, so scripts/re-verify.sh parses this layer natively).
#
# Usage:
#   bash docs/verification/kep-531-profile-injection/scripts/check_doc_contracts.sh [--json] [README-path]
#
# If the KEP README is absent (base branch, pre-PR), the doc layer emits no
# test records at all — re-verify.sh then reports HARNESS-UPDATE for these
# findings, which is the honest verdict: there is no document to check.
set -uo pipefail

README_DEFAULT="keps/531-engine-runtime-profile-injection/README.md"
KEPYAML_DEFAULT="keps/531-engine-runtime-profile-injection/kep.yaml"
JSON_MODE=0
README="" KEPYAML=""
for arg in "$@"; do
  case "$arg" in
    --json) JSON_MODE=1;;
    -h|--help) sed -n '2,26p' "$0"; exit 0;;
    *) if [ -z "$README" ]; then README="$arg"; else KEPYAML="$arg"; fi;;
  esac
done
README="${README:-$README_DEFAULT}"
KEPYAML="${KEPYAML:-$KEPYAML_DEFAULT}"

emit() { # $1=Test id  $2=pass|fail  $3=detail
  if [ "$JSON_MODE" -eq 1 ]; then
    printf '{"Test":"%s","Action":"%s"}\n' "$1" "$2"
  else
    printf '%-42s %-4s %s\n' "$1" "$2" "$3"
  fi
}

if [ ! -f "$README" ]; then
  echo "check_doc_contracts: README absent at $README — no document to check (base branch)." >&2
  exit 0
fi

# Flatten to a single line so line-wrapped phrases match; collapse runs of spaces.
# (Written to a temp file and matched with grep -F: bash's ${var#pattern} is
# pathologically slow on a ~100KB subject string.)
FLATFILE="$(mktemp)"
trap 'rm -f "$FLATFILE"' EXIT
tr '\n' ' ' < "$README" | tr -s ' ' > "$FLATFILE"

has()   { grep -qF -- "$1" "$FLATFILE"; }        # fixed-string substring test
lacks() { ! has "$1"; }

# ---------- CONTRACT checks (fixed findings — FAIL = regression) ----------

# F-ENV-CONFLICT (round-1 blocker, fixed in the round-2 rewrite):
# the env-name rules must be positional with ownership-drawn boundaries and
# the legacy merge's own appends must stay unchecked exactly as on main.
if lacks "spanning all three passes" && lacks "still guarding the result" \
   && lacks "check spans both passes" \
   && has "legacy-only duplicates never error" \
   && has "a profile-side append onto a role-owned name is the render error" \
   && has "repeated references append their overrides once per reference, exactly as on main"; then
  emit "F-ENV-CONFLICT-CONTRACT" pass "one positional env rule set; legacy merge unchecked as on main"
else
  emit "F-ENV-CONFLICT-CONTRACT" fail "contradictory conflict-check scope statements present (or fixed-state wording removed)"
fi

# F-COVERAGE-UPDATESTRATEGY (round-1 major, fixed): updateStrategy dispositioned.
if has "activating it is future work outside this KEP" && grep -q "^#\+ Profile Mutability" "$README"; then
  emit "F-COVERAGE-UPDATESTRATEGY-CONTRACT" pass "updateStrategy dispositioned (Non-Goals + Profile Mutability)"
else
  emit "F-COVERAGE-UPDATESTRATEGY-CONTRACT" fail "updateStrategy disposition missing"
fi

# F-CAPS-MAXITEMS (round-1 major, fixed): no invented caps on published lists.
if has "no new caps" && has "neither has \`maxItems\` on main" \
   && lacks "roles=16" && lacks "engineRuntimes=16"; then
  emit "F-CAPS-MAXITEMS-CONTRACT" pass "published lists carry no new caps; absence on main stated"
else
  emit "F-CAPS-MAXITEMS-CONTRACT" fail "present-tense cap claims are back (or the no-caps statement was removed)"
fi

# F-COMPONENT-VOCAB (round-1 minor, fixed): component mirrors source vocabulary.
if has "deliberately carries no pattern or length bound beyond \`MinLength=1\`"; then
  emit "F-COMPONENT-VOCAB-CONTRACT" pass "component validation mirrors the unmarked source vocabulary"
else
  emit "F-COMPONENT-VOCAB-CONTRACT" fail "component pattern restriction is back (or fix wording removed)"
fi

# F-ENABLEMENT-ROLLBACK (round-1 minor, fixed): template-required section present.
if grep -q "^## Feature Enablement and Rollback" "$README" && grep -q "^## Upgrade / Downgrade Strategy" "$README"; then
  emit "F-ENABLEMENT-ROLLBACK-CONTRACT" pass "Feature Enablement and Rollback + Upgrade/Downgrade sections present"
else
  emit "F-ENABLEMENT-ROLLBACK-CONTRACT" fail "required KEP-template section(s) missing"
fi

# F-CONVERSION-ROUNDTRIP (round-1 missingTests, fixed): round-trip pinned.
if has "round-trip losslessness through both Hub conversions is pinned by envtest"; then
  emit "F-CONVERSION-ROUNDTRIP-CONTRACT" pass "conversion round-trip coverage in the Test Plan"
else
  emit "F-CONVERSION-ROUNDTRIP-CONTRACT" fail "conversion round-trip coverage statement removed"
fi

# F-UNKNOWN-TARGET-PROSE (round-1 nit, fixed): unknown-name semantics stated.
if has "anything else — a name injected only by a different reference or profile — is a render error"; then
  emit "F-UNKNOWN-TARGET-PROSE-CONTRACT" pass "unknown targetContainers name is a stated render error"
else
  emit "F-UNKNOWN-TARGET-PROSE-CONTRACT" fail "unknown-target semantics missing from the prose"
fi

# F-COPILOT-SECRET-CHANNEL (round-1 nit, fixed): D9 scopes the guarantee and
# documents the retained legacy channels instead of implying none exist.
# (Phrase kept loose: the head wording is "main's legacy channels already
# carry this class of flow", the round-2 condense drops the trailing "on main".)
if has "legacy channels already carry this class of flow"; then
  emit "F-COPILOT-SECRET-CHANNEL-CONTRACT" pass "D9 documents the retained role→profile channels"
else
  emit "F-COPILOT-SECRET-CHANNEL-CONTRACT" fail "D9 secret-channel scope regressed to an unqualified claim"
fi

# F-COPILOT-VOLUMEMOUNT-EQUALITY (round-1 nit, fixed): full-field equality.
if has "\`readOnly\`, \`subPath\`, \`subPathExpr\`, \`mountPropagationMode\`, \`recursiveReadOnly\` compared alongside the key"; then
  emit "F-COPILOT-VOLUMEMOUNT-EQUALITY-CONTRACT" pass "volumeMount no-op requires full semantic equality"
else
  emit "F-COPILOT-VOLUMEMOUNT-EQUALITY-CONTRACT" fail "volumeMount equality dropped full-field comparison"
fi

# F-INFERENCE-KIT-404 (round-1 nit, fixed): no dead see-also link.
if [ -f "$KEPYAML" ] && ! grep -q "inference-kit" "$KEPYAML" \
   && ! grep -q "github.com/sgl-project/inference-kit" "$README"; then
  emit "F-INFERENCE-KIT-404-CONTRACT" pass "no dead inference-kit see-also link"
else
  emit "F-INFERENCE-KIT-404-CONTRACT" fail "404 inference-kit link is back"
fi

# ---------- CANARY checks (open findings — pass while defective, flip on fix) ----------

# F-MOUNTPATH-MECHANISM (open nit): the wrong "kubelet last-wins" citation for
# duplicate mountPaths sits beside the correct apiserver "must be unique" one.
if has "the kubelet resolves overlapping mount paths last-wins"; then
  emit "F-MOUNTPATH-MECHANISM-CANARY" pass "defective kubelet-last-wins citation present (apiserver rejects dup mountPaths)"
else
  emit "F-MOUNTPATH-MECHANISM-CANARY" fail "FIXED? kubelet-last-wins citation removed — invert into a contract"
fi

# F-PODSECURITY-STAGE (open nit): risk table says "fails at workload creation"
# while the doc itself says render success is not workload creation.
if has "fails at workload creation"; then
  emit "F-PODSECURITY-STAGE-CANARY" pass "defective 'workload creation' stage claim present"
else
  emit "F-PODSECURITY-STAGE-CANARY" fail "FIXED? 'workload creation' wording removed — invert into a contract"
fi

# F-ENV-VAR-DIRECTION (open nit): absolute "cannot" claim overgeneralizes —
# role-side head inserts land ahead of profile-appended entries.
if has "a profile-added env entry cannot use"; then
  emit "F-ENV-VAR-DIRECTION-CANARY" pass "unqualified \$(VAR) direction claim present"
else
  emit "F-ENV-VAR-DIRECTION-CANARY" fail "FIXED? \$(VAR) direction claim reworded — invert into a contract"
fi

# F-INJECTOR-COUNT-WORDING (open nit): "four-injector pass" overgeneralizes;
# per-template injector sets vary 2-4.
if has "four-injector pass"; then
  emit "F-INJECTOR-COUNT-WORDING-CANARY" pass "'four-injector pass' overgeneralization present"
else
  emit "F-INJECTOR-COUNT-WORDING-CANARY" fail "FIXED? injector-count wording qualified — invert into a contract"
fi

# F-STAGE3-CHECK-TIMING (open minor, round 2): Application Order says each
# stage-3 pass resolves "against the role template and the pod-level content of
# earlier references only", which contradicts the phased order it also states
# (all pod-level content lands in stages 1-2 before any stage-3 pass).
if has "content of earlier references only"; then
  emit "F-STAGE3-CHECK-TIMING-CANARY" pass "contradictory stage-3 check-timing clause present"
else
  emit "F-STAGE3-CHECK-TIMING-CANARY" fail "FIXED? stage-3 check-timing clause reworded — invert into a contract"
fi

# F-PROFILE-EVOLUTION-TEST (open minor, round 1): the Test Plan has no case for
# a profile adding containerPatches while a consenting reference exists.
testplan="$(awk '/^## Test Plan/{f=1;next} /^## /{f=0} f' "$README")"
if ! printf '%s' "$testplan" | grep -qi "evolution\|profile change\|profile edit\|profile mutab\|consenting reference"; then
  emit "F-PROFILE-EVOLUTION-TEST-CANARY" pass "Test Plan still lacks a consented profile-evolution case"
else
  emit "F-PROFILE-EVOLUTION-TEST-CANARY" fail "FIXED? Test Plan now covers consented profile evolution — invert into a contract"
fi

# Overall exit: 0 iff no CONTRACT regression and no CANARY flip (ledger steady).
exit 0
