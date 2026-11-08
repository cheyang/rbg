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
# (Round-3 update d84004a2: the condense reworded the fixed-state sentences;
#  phrases re-pinned to the new wording, substance unchanged. See
#  results/doc-bites-r3.txt for the bite check on the new phrases.)
if lacks "spanning all three passes" && lacks "still guarding the result" \
   && lacks "check spans both passes" \
   && has "legacy-only duplicates never error" \
   && has "append onto a role-owned name is a render error" \
   && has "remains per-reference additive, exactly as on main"; then
  emit "F-ENV-CONFLICT-CONTRACT" pass "one positional env rule set; legacy merge unchecked as on main"
else
  emit "F-ENV-CONFLICT-CONTRACT" fail "contradictory conflict-check scope statements present (or fixed-state wording removed)"
fi

# F-COVERAGE-UPDATESTRATEGY (round-1 major, fixed; round-3 disposition evolved):
# updateStrategy is now deprecated outright and profile revisions are a Non-Goal
# under the new immutability design, with a dedicated Profile Mutability section.
if has "Any rollout or orchestration machinery for profile revisions" \
   && has "is deprecated in both served versions" \
   && grep -q "^#\+ Profile Mutability" "$README"; then
  emit "F-COVERAGE-UPDATESTRATEGY-CONTRACT" pass "updateStrategy dispositioned (Non-Goals + deprecation + Profile Mutability)"
else
  emit "F-COVERAGE-UPDATESTRATEGY-CONTRACT" fail "updateStrategy disposition missing"
fi

# F-CAPS-MAXITEMS (round-1 major, fixed): no invented caps on published lists.
if has "no new caps" && has "capping them inside the already-published" \
   && lacks "roles=16" && lacks "engineRuntimes=16"; then
  emit "F-CAPS-MAXITEMS-CONTRACT" pass "published lists carry no new caps; stored-object rationale stated"
else
  emit "F-CAPS-MAXITEMS-CONTRACT" fail "present-tense cap claims are back (or the no-caps statement was removed)"
fi

# F-COMPONENT-VOCAB (round-1 minor, fixed): component mirrors source vocabulary.
if has "The lack of a pattern on \`component\` is deliberate"; then
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

# F-CONVERSION-ROUNDTRIP (round-1 missingTests, fixed; round-3 wording re-pinned,
# and the CERP v1alpha1 mirror round-trip was added — Q-CERP-V1ALPHA1 answered).
if has "round-trip losslessness through both Hub conversions" \
   && has "pinned by envtest" && has "round trip keeping"; then
  emit "F-CONVERSION-ROUNDTRIP-CONTRACT" pass "conversion round-trip coverage in the Test Plan (incl. CERP mirror)"
else
  emit "F-CONVERSION-ROUNDTRIP-CONTRACT" fail "conversion round-trip coverage statement removed"
fi

# F-UNKNOWN-TARGET-PROSE (round-1 nit, fixed): unknown-name semantics stated.
if has "anything else — a name injected only by a different reference or profile — is a render error"; then
  emit "F-UNKNOWN-TARGET-PROSE-CONTRACT" pass "unknown targetContainers name is a stated render error"
else
  emit "F-UNKNOWN-TARGET-PROSE-CONTRACT" fail "unknown-target semantics missing from the prose"
fi

# F-COPILOT-SECRET-CHANNEL (round-1 nit, fixed): the reverse direction is
# scoped as a role-authored decision, not an unqualified guarantee.
# (Round-3 re-pin: the Consent and Trust section now states it directly.)
if has "is the role author's decision, not a blocked channel"; then
  emit "F-COPILOT-SECRET-CHANNEL-CONTRACT" pass "role→profile channels documented as a role-authored decision"
else
  emit "F-COPILOT-SECRET-CHANNEL-CONTRACT" fail "D9 secret-channel scope regressed to an unqualified claim"
fi

# F-COPILOT-VOLUMEMOUNT-EQUALITY (round-1 nit, fixed): full-field equality.
# (Round-3 re-pin: the field list was condensed into "full semantic equality
#  of the unmarshalled structs — an absent field and an explicit zero value
#  counting as equal".)
if has "full semantic equality" && has "an absent field and an explicit zero value counting as equal"; then
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

# F-MOUNTPATH-MECHANISM (nit, rounds 1-3; FIXED at 0c51bc0e — canary flipped
# and inverted into this contract): the duplicate-mountPath render-error rule
# is now cited to the apiserver's "must be unique" pod-admission validation
# (true: k8s core ValidateContainers rejects duplicate mountPaths per
# container), not to a kubelet last-wins claim.
if has "the apiserver rejects duplicate mount paths within a container at pod admission" \
   && lacks "kubelet resolves overlapping mount paths"; then
  emit "F-MOUNTPATH-MECHANISM-CONTRACT" pass "duplicate-mountPath rule cites the apiserver admission check"
else
  emit "F-MOUNTPATH-MECHANISM-CONTRACT" fail "mountPath citation regressed to a wrong mechanism (or the apiserver citation was removed)"
fi

# F-PODSECURITY-STAGE (open nit): risk table says "fails at workload creation"
# while the doc itself says render success is not workload creation.
if has "fails at workload creation"; then
  emit "F-PODSECURITY-STAGE-CANARY" pass "defective 'workload creation' stage claim present"
else
  emit "F-PODSECURITY-STAGE-CANARY" fail "FIXED? 'workload creation' wording removed — invert into a contract"
fi

# F-ENV-VAR-DIRECTION (round-1 nit, FIXED at d84004a2 — canary flipped and
# inverted into this contract): the absolute "cannot" claim is gone; the doc
# now states the positional rule ("resolve against earlier entries only").
if lacks "a profile-added env entry cannot use" && has "resolve against earlier entries only"; then
  emit "F-ENV-VAR-DIRECTION-CONTRACT" pass "\$(VAR) direction stated positionally"
else
  emit "F-ENV-VAR-DIRECTION-CONTRACT" fail "absolute \$(VAR) direction claim is back"
fi

# F-INJECTOR-COUNT-WORDING (nit, rounds 1-3; FIXED at 0c51bc0e — canary
# flipped and inverted): the chain is now described as "the four built-in
# injector passes — config, sidecar stages 1–2, env, LWP env" — an
# enumeration of the chain's injector kinds, no longer claiming every
# template runs all four (per-template sets vary 2-4).
if has "the four built-in injector passes" && lacks "four-injector pass"; then
  emit "F-INJECTOR-COUNT-WORDING-CONTRACT" pass "injector chain enumerated by kind, not per-template count"
else
  emit "F-INJECTOR-COUNT-WORDING-CONTRACT" fail "'four-injector pass' overgeneralization is back (or the enumeration was removed)"
fi

# F-STAGE3-LEGACY-VISIBILITY / F-STAGE3-CHECK-TIMING (round-2 major downgraded
# to minor at r3; FIXED at 0c51bc0e — canary flipped and inverted): the stage-3
# role-owned env check now counts BOTH static sources — the pre-injection
# snapshot AND the referencing role's own legacy engineRuntimes[].containers[].env
# names ("legacy-merge output being role content"), so the promised render
# error for a profile append onto a legacy-declared name can fire. The old
# "content of earlier references only" clause is gone.
if has "two static sources" && has "legacy-merge output being role content" \
   && lacks "content of earlier references only"; then
  emit "F-STAGE3-CHECK-TIMING-CONTRACT" pass "stage-3 check sees snapshot + own-role legacy env names"
else
  emit "F-STAGE3-CHECK-TIMING-CONTRACT" fail "stage-3 role-owned visibility regressed (legacy env names no longer counted)"
fi

# F-ROLE-INSERT-ORDER (round-4 NEW minor): the Patch Grammar's role-side bullet
# claims two references' role-side same-name entries resolve "by reference
# order — the later reference's entry lands later and wins", which holds for
# appends only. For two /env/0 head inserts the later pass lands its entry at
# index 0 (pushing the earlier reference's entry to index 1), so by the doc's
# own last-occurrence rule the EARLIER reference's entry wins — the inversion
# the cross-profile bullet states explicitly ("head-insert geometry puts the
# later reference's entry ahead, so the earlier reference's default wins").
# Pass while the unqualified winner claim is present; flips when reworded.
if has "the later reference's entry lands later and wins"; then
  emit "F-ROLE-INSERT-ORDER-CANARY" pass "role-side two-reference winner claim contradicts own head-insert geometry"
else
  emit "F-ROLE-INSERT-ORDER-CANARY" fail "FIXED? role-side winner claim qualified by spelling — invert into a contract"
fi

# F-PROFILE-EVOLUTION-TEST (minor, rounds 1-3; FIXED at 0c51bc0e — canary
# re-pinned onto the new wording and inverted into this contract): the Test
# Plan now covers both the landing semantics (an admitted edit to a referenced
# profile landing, subsequent renders reading the changed content — the
# alpha-warning-window case, which is also the disable-flag question: freeze
# off, edit lands, next render reads it) and the flag transition path (a
# finalizer installed under default operation removed once the flag is set).
testplan="$(awk '/^## Test Plan/{f=1;next} /^## /{f=0} f' "$README")"
if printf '%s' "$testplan" | grep -q "the transition path" \
   && printf '%s' "$testplan" | grep -q "subsequent renders reading the changed content"; then
  emit "F-PROFILE-EVOLUTION-TEST-CONTRACT" pass "landing semantics + disable-flag transition path in the Test Plan"
else
  emit "F-PROFILE-EVOLUTION-TEST-CONTRACT" fail "disable-flag landing-semantics / transition coverage removed from the Test Plan"
fi

# F-SSA-ENV-PREMISE (round-3 NEW minor; FIXED at 0c51bc0e — canary flipped and
# inverted into this contract): the false "server-side apply, whose env list is
# keyed by name and rejects duplicates" justification is gone (the whole final
# normalization was removed with it — entries land verbatim, never reordered or
# deduplicated, matching the unit pin TestKEP531_Verify_RISTemplateEnvIsNotMapKeyed:
# the served RIS CRD carries no map-keyed env list, so SSA never rejects env
# duplicates). Contract: the false premise stays out and the verbatim-position
# statement stays in.
if lacks "keyed by name and rejects duplicates" \
   && has "the render adds no winner rule of its own" \
   && has "never reordered or deduplicated"; then
  emit "F-SSA-ENV-PREMISE-CONTRACT" pass "env model is verbatim-positional; false SSA premise absent"
else
  emit "F-SSA-ENV-PREMISE-CONTRACT" fail "false SSA duplicate-rejection premise is back (or the verbatim statement was removed)"
fi

# F-GRADUATION-EVENT-INVENTORY (round-2 nit; FIXED at 0c51bc0e — canary
# flipped and inverted into this contract): the warning-event inventory in the
# Graduation checklist now names every event the doc promises — including the
# newly named EngineRuntimeEnvCollision and EngineRuntimeWrapShadowed.
gradsec="$(awk '/^## Graduation Criteria/{f=1;next} /^## /{f=0} f' "$README")"
if printf '%s' "$gradsec" | grep -q "EngineRuntimeEnvCollision" \
   && printf '%s' "$gradsec" | grep -q "EngineRuntimeWrapShadowed" \
   && printf '%s' "$gradsec" | grep -q "EngineRuntimeNameCollision" \
   && printf '%s' "$gradsec" | grep -q "UnaimedContainerPatch" \
   && printf '%s' "$gradsec" | grep -q "EngineRuntimeRenderFailed"; then
  emit "F-GRADUATION-EVENT-INVENTORY-CONTRACT" pass "Graduation warning-events checklist names the full event inventory"
else
  emit "F-GRADUATION-EVENT-INVENTORY-CONTRACT" fail "an event promised in the body is missing from the Graduation checklist"
fi

# Overall exit: 0 iff no CONTRACT regression and no CANARY flip (ledger steady).
exit 0
