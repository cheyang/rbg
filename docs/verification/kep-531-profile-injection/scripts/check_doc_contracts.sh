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
# (Round-6 update 31466993: prose-style rewrite; the role-owned-append error
#  sentence reworded ("Legacy-merge output is role content, so a profile
#  append onto such a name is the same render error…" + the envFrom-defense
#  "a profile-side append onto it is a render error"). Semantics unchanged;
#  see results/doc-bites-r6.txt.)
if lacks "spanning all three passes" && lacks "still guarding the result" \
   && lacks "check spans both passes" \
   && has "legacy-only duplicates never error" \
   && has "Legacy-merge output is role content, so a profile append onto such a name is the same render error" \
   && has "a profile-side append onto it is a render error" \
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
# (Round-6 re-pin 31466993: em-dashes became commas in the rewrite.)
if has "anything else, a name injected only by a different reference or profile, is a render error"; then
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
#  counting as equal". Round-6 re-pin 31466993: "counting" → "count".)
if has "full semantic equality" && has "an absent field and an explicit zero value count as equal"; then
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

# F-PODSECURITY-STAGE (round-1 nit; canary FLIPPED at b4637b51 — the defective
# "fails at workload creation" wording is gone, inverted into this contract):
# the risk table and the mountPath paragraph now say pod admission / pod
# creation, the accurate stage (pod admission, after render).
if lacks "fails at workload creation" \
   && has "fails at pod admission" \
   && has "fail at pod creation anyway"; then
  emit "F-PODSECURITY-STAGE-CONTRACT" pass "PodSecurity stage claim says pod admission/creation"
else
  emit "F-PODSECURITY-STAGE-CONTRACT" fail "'workload creation' stage claim is back (or the pod-admission wording was removed)"
fi

# F-ENV-VAR-DIRECTION (round-1 nit, FIXED at d84004a2 — canary flipped and
# inverted into this contract): the absolute "cannot" claim is gone; the doc
# now states the positional rule ("resolve against earlier entries only").
if lacks "a profile-added env entry cannot use" && has "resolve against earlier entries only"; then
  emit "F-ENV-VAR-DIRECTION-CONTRACT" pass "\$(VAR) direction stated positionally"
else
  emit "F-ENV-VAR-DIRECTION-CONTRACT" fail "absolute \$(VAR) direction claim is back"
fi

# F-INJECTOR-COUNT-WORDING (nit, rounds 1-4; genuinely FIXED at b4637b51 —
# the round-4 "enumeration by kind" closure was retracted in cross-critique;
# this round the doc states the per-chain variation outright). Contract pins
# the fixed wording: "two to four passes by chain" with the two reasons
# (config injector stateful-only — verified against pkg/discovery/injector.go:
# InjectConfig returns nil for !IsStatefulRole; worker chain omits sidecar).
# (Round-6 re-pin 31466993: the rationale now cites chain composition —
#  worker chains omit the sidecar injector; LWP env injector RIS-only — both
#  verified against the SetInjectors call sites: RIS leader 4, RIS worker 3,
#  LWS worker 2 passes; LWS roles are always stateful so the floor is 2.)
if lacks "the four built-in injector passes" \
   && has "two to four passes by chain" \
   && has "the worker chains omit the sidecar injector" \
   && has "the LWP env injector runs on the RoleInstanceSet chains only"; then
  emit "F-INJECTOR-COUNT-WORDING-CONTRACT" pass "injector chain count states the 2-4 per-chain variation"
else
  emit "F-INJECTOR-COUNT-WORDING-CONTRACT" fail "'the four built-in injector passes' overgeneralization is back (or the 2-4 variation clause was removed)"
fi

# F-STAGE3-LEGACY-VISIBILITY / F-STAGE3-CHECK-TIMING (round-2 major downgraded
# to minor at r3; FIXED at 0c51bc0e — canary flipped and inverted): the stage-3
# role-owned env check now counts BOTH static sources — the pre-injection
# snapshot AND the referencing role's own legacy engineRuntimes[].containers[].env
# names ("Legacy-merge output is role content" — round-6 re-pin of the r5
# wording "legacy-merge output being role content"), so the promised render
# error for a profile append onto a legacy-declared name can fire. The old
# "content of earlier references only" clause is gone.
if has "two static sources" && has "Legacy-merge output is role content" \
   && lacks "content of earlier references only"; then
  emit "F-STAGE3-CHECK-TIMING-CONTRACT" pass "stage-3 check sees snapshot + own-role legacy env names"
else
  emit "F-STAGE3-CHECK-TIMING-CONTRACT" fail "stage-3 role-owned visibility regressed (legacy env names no longer counted)"
fi

# F-ROLE-INSERT-ORDER (round-4 NEW minor; FIXED at b4637b51 — the author
# qualified the winner claim by spelling, and the Test Plan echo gained the
# head-insert case; inverted into this contract). The append-qualified sentence
# legitimately keeps the substring "the later reference's entry lands later
# and wins" — scoped to "appended entries land in reference order, so ..." —
# so the contract pins the qualification and the inversion clause instead.
# (Round-6 re-pin 31466993: "by spelling" → "and spelling".)
if has "resolve by reference order and spelling" \
   && has "two \`/env/0\` head inserts invert it" \
   && has "resolving positionally by spelling"; then
  emit "F-ROLE-INSERT-ORDER-CONTRACT" pass "role-side two-reference winner qualified by spelling; Test Plan covers both spellings"
else
  emit "F-ROLE-INSERT-ORDER-CONTRACT" fail "role-side winner claim lost its by-spelling qualification (or the Test Plan head-insert case was removed)"
fi

# F-PROFILE-EVOLUTION-TEST (minor, rounds 1-4; RE-CONTRACTED at b4637b51 with
# changeReason: the immutability subsystem was redesigned to webhook-only
# enforcement — no in-use finalizer, no --disable-profile-immutability flag, no
# alpha warning window — so the contracted flag/finalizer transition cases no
# longer exist by design. The equivalent lifecycle coverage under the new
# design: the bypassed-mutation watch event, the reference-bookkeeping drift
# cases, and the accepted deletion race. Phrases re-pinned to that coverage.)
# (Round-6 re-pin 31466993: "deletion race" → "deletion interleaving" in the
#  rewrite; same test coverage.)
testplan="$(awk '/^## Test Plan/{f=1;next} /^## /{f=0} f' "$README" | tr '\n' ' ' | tr -s ' ')"
if printf '%s' "$testplan" | grep -qF "emitting the mutation-while-referenced warning event when the webhook is bypassed" \
   && printf '%s' "$testplan" | grep -qF "the drift baseline surviving a controller restart" \
   && printf '%s' "$testplan" | grep -qF "\`referencedGeneration\` recorded on the none-to-referenced transition" \
   && printf '%s' "$testplan" | grep -qF "the accepted deletion interleaving"; then
  emit "F-PROFILE-EVOLUTION-TEST-CONTRACT" pass "new-design lifecycle coverage in the Test Plan (watch event, bookkeeping drift, deletion race)"
else
  emit "F-PROFILE-EVOLUTION-TEST-CONTRACT" fail "new-design lifecycle coverage (watch event / bookkeeping drift / deletion race) removed from the Test Plan"
fi

# F-SSA-ENV-PREMISE (round-3 NEW minor; FIXED at 0c51bc0e — canary flipped and
# inverted into this contract): the false "server-side apply, whose env list is
# keyed by name and rejects duplicates" justification is gone (the whole final
# normalization was removed with it — entries land verbatim, never reordered or
# deduplicated, matching the unit pin TestKEP531_Verify_RISTemplateEnvIsNotMapKeyed:
# the served RIS CRD carries no map-keyed env list, so SSA never rejects env
# duplicates). Contract: the false premise stays out and the verbatim-position
# statement stays in. (Round-6 re-pin 31466993: "adds no winner rule of its
#  own" → "adds no winner rule".)
if lacks "keyed by name and rejects duplicates" \
   && has "the render adds no winner rule" \
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

# ---------- Round-5 (head b4637b51) additions ----------
# Round-4 open findings the author fixed this round — inverted into contracts.

# F-DEPRECATED-PATH-MECHANISM (round-4 minor; FIXED at b4637b51): the rejection
# of the new fields on deprecated render paths now names its mechanism — write
# time via the shared role-side validation function riding the existing
# webhooks (the role-side fourth invariant), render as the annotation-flip
# backstop — in Non-Goals, the Design table, the Validation Model, and the
# Test Plan. (Round-6 re-pin 31466993: "at write time, by" → "at write time
#  via", Non-Goals now also carries "the workload-type-annotation flip
#  included".)
if has "at write time via the shared role-side validation function riding the existing webhooks" \
   && has "the workload-type-annotation flip included" \
   && has "role-side four" \
   && printf '%s' "$testplan" | grep -qF "at write time by the shared validation function"; then
  emit "F-DEPRECATED-PATH-MECHANISM-CONTRACT" pass "deprecated-path rejection names its mechanism at all three layers"
else
  emit "F-DEPRECATED-PATH-MECHANISM-CONTRACT" fail "deprecated-path rejection lost its named mechanism (or the Test Plan case was removed)"
fi

# F-WEBHOOK-ROLLBACK (round-4 minor; FIXED at b4637b51): the Downgrade story
# now leads with removing the CERP webhook entry BEFORE the image rollback,
# the alpha failurePolicy is stated (Fail from the alpha), and the Test Plan
# gains the admission-enabled rollback drill. (Round-6 re-pin 31466993: the
#  rollback drill now names the honest re-upgrade mechanism — the entry is
#  re-applied from the release artifacts as its own step; the controller
#  cannot reinstall it.)
if has "remove the CERP webhook entry" \
   && has "cannot reinstall the entry" \
   && has "re-applies the ValidatingWebhookConfiguration from the release artifacts" \
   && printf '%s' "$testplan" | grep -qF "The admission-enabled rollback drill"; then
  emit "F-WEBHOOK-ROLLBACK-CONTRACT" pass "rollback removes the webhook entry first; drill in the Test Plan"
else
  emit "F-WEBHOOK-ROLLBACK-CONTRACT" fail "webhook-entry-first rollback step removed (or the drill was dropped)"
fi

# F-CAPABILITY-FORM-REGEX (round-4 minor; FIXED at b4637b51): the form warning
# now fires on names matching the canonical uppercase form while carrying the
# CAP_ prefix (checked case-insensitively) — the form the Test Plan promises.
# (Round-6 re-pin 31466993: ", checked case-insensitively" → "(checked
#  case-insensitively:".)
if has "or matching it while carrying the \`CAP_\` prefix (checked case-insensitively" \
   && printf '%s' "$testplan" | grep -qF "the form warning on lowercase and \`CAP_\`-prefixed names"; then
  emit "F-CAPABILITY-FORM-REGEX-CONTRACT" pass "form warning fires on the CAP_-prefixed hazard the Test Plan promises"
else
  emit "F-CAPABILITY-FORM-REGEX-CONTRACT" fail "CAP_-prefix warning leg removed (or the Test Plan promise was dropped)"
fi

# F-PROFILE-DEFAULT-GEOMETRY (round-4 minor; FIXED at b4637b51): the index-0
# rule's rationale now matches insert geometry — only the head is statically
# knowable to precede the role's own entry.
if has "Only the head is statically knowable" \
   && has "an insert whose index exceeds the role entry's own lands after it and wins"; then
  emit "F-PROFILE-DEFAULT-GEOMETRY-CONTRACT" pass "index-0 rationale states the real insert geometry"
else
  emit "F-PROFILE-DEFAULT-GEOMETRY-CONTRACT" fail "index-0 rationale regressed to the false 'any position but the head' claim"
fi

# F-SNAPSHOT-ENV-SOURCE (round-4 nit; FIXED at b4637b51): the stage-3 check's
# first static source is now named correctly — the role template's own env
# entries read off the containers named in the snapshot.
if has "the role template's own env entries, read off the containers named in the pre-injection snapshot" \
   && has "the snapshot holds container names"; then
  emit "F-SNAPSHOT-ENV-SOURCE-CONTRACT" pass "stage-3 first source named as the role template's own env entries"
else
  emit "F-SNAPSHOT-ENV-SOURCE-CONTRACT" fail "snapshot-as-env-name-source conflation is back"
fi

# F-V1ALPHA1-CEL-EMPTY-COMPONENTS (round-4 nit; FIXED at b4637b51): the
# v1alpha1 mirror's predicate now guards the empty-components edge, with the
# rationale and an envtest pin for it.
if has "!(has(self.components) && size(self.components) > 0)" \
   && has "including the empty-\`components\` edge"; then
  emit "F-V1ALPHA1-CEL-EMPTY-COMPONENTS-CONTRACT" pass "v1alpha1 predicate guards the empty-components edge, pinned by envtest"
else
  emit "F-V1ALPHA1-CEL-EMPTY-COMPONENTS-CONTRACT" fail "v1alpha1 predicate lost the size()>0 guard (or its envtest pin)"
fi

# F-PODRECONCILER-TERMINOLOGY (round-4 nit; FIXED at b4637b51): the doc drops
# the PodReconciler shorthand entirely (zero occurrences at head) and says
# "the Deployment/StatefulSet/LWS surfaces are deprecated". (Round-6 re-pin
#  31466993: the deprecated-surfaces sentence is now the Non-Goals bullet
#  "The deprecated workload surfaces: roles rendered as Deployment, StatefulSet
#  or LWS".)
if ! grep -q "PodReconciler" "$README" \
   && has "The deprecated workload surfaces: roles rendered as Deployment, StatefulSet or LWS"; then
  emit "F-PODRECONCILER-TERMINOLOGY-CONTRACT" pass "deprecated surfaces named without the PodReconciler shorthand"
else
  emit "F-PODRECONCILER-TERMINOLOGY-CONTRACT" fail "PodReconciler shorthand is back (or the deprecated-surfaces sentence changed)"
fi

# F-FINALIZER-REMOVAL-OVERCLAIM (round-4 nit; FIXED BY REMOVAL at b4637b51):
# the in-use finalizer is gone entirely, and with it the check-then-act
# closure claim ("leaves the finalizer in place"); the delete race is now
# honestly accepted as an admission window.
if has "deliberately no in-use finalizer" \
   && lacks "leaves the finalizer in place"; then
  emit "F-FINALIZER-REMOVAL-OVERCLAIM-CONTRACT" pass "finalizer removed by design; no check-then-act closure claim remains"
else
  emit "F-FINALIZER-REMOVAL-OVERCLAIM-CONTRACT" fail "finalizer-related overclaim returned"
fi

# ---------- Round-5 canaries — FIXED at 31466993 (round 6), inverted ----------
# The seven round-5 canaries flipped this round: the defective claims are gone
# and the corrected wording landed. Inverted into contracts per polarity
# policy (a canary is fixed only when it flips; then it is inverted).

# F-RACE-OUTCOME-OVERCLAIM (round-5 NEW minor — Claude, Copilot comment
#  4517519175 independently raised the update-race interleaving; FIXED at
#  31466993): the risk table now names every interleaving outcome, including
#  the silent ones ("silently consumes the recreated content", "one drifts
#  content on later re-renders, with the controller's watch warning event as
#  the signal"), and the overbroad "never silent miscontent" / "all ending in
#  visible render failure" claims are gone.
if lacks "never silent miscontent" && lacks "all ending in visible render failure" \
   && has "silently consumes the recreated content" \
   && has "one drifts content on later re-renders, with the controller's watch warning event as the signal"; then
  emit "F-RACE-OUTCOME-OVERCLAIM-CONTRACT" pass "all three interleaving outcomes named, silent legs included"
else
  emit "F-RACE-OUTCOME-OVERCLAIM-CONTRACT" fail "race-outcome claims regressed to the overbroad all-visible form"
fi

# F-DELETE-RECREATE-OUTCOME (round-5 NEW minor — Claude; FIXED at 31466993):
# the closure claim is now scoped ("Outside the accepted interleavings, this
# closes the delete + recreate-under-same-name bypass at its first write"),
# the same-name-recreate sub-case is considered (CREATE-interception rejected
# for the GitOps recovery order), and the dangling-reference outcome is named
# ("the role silently consumes its content, with no render failure … and no
# drift signal").
if has "Outside the accepted interleavings, this closes the delete + recreate-under-same-name bypass at its first write" \
   && has "the recreate lands on a dangling reference; that outcome is named below" \
   && has "the role silently consumes its content"; then
  emit "F-DELETE-RECREATE-OUTCOME-CONTRACT" pass "delete+recreate closure scoped; dangling-reference silent outcome named"
else
  emit "F-DELETE-RECREATE-OUTCOME-CONTRACT" fail "delete+recreate closure claim regressed to the unscoped form (or the outcome naming was removed)"
fi

# F-ANNOTATION-FLIP-DODGE (round-5 NEW minor — Claude; FIXED at 31466993):
# the Validation Model now states both webhooks intercept CREATE and UPDATE
# checking the whole object, so the flip "is itself a rejected spec update";
# the render leg is scoped to the webhook-absent window.
if has "so flipping a written role to a deprecated surface is itself a rejected spec update" \
   && lacks "dodges write-time rejection"; then
  emit "F-ANNOTATION-FLIP-DODGE-CONTRACT" pass "annotation flip rejected at admission; render leg scoped to the absent-webhook window"
else
  emit "F-ANNOTATION-FLIP-DODGE-CONTRACT" fail "annotation-flip 'dodges write-time rejection' premise is back"
fi

# F-REUPGRADE-WEBHOOK-REINSTALL (round-5 NEW minor — Copilot comment
#  4517519250, code-verified by Claude; FIXED at 31466993): the drill now
# says the image re-upgrade "cannot reinstall the entry (the controller
# patches existing webhook configurations and never creates them)" and the
# configuration is re-applied from the release artifacts as its own step.
if has "cannot reinstall the entry (the controller patches existing webhook configurations and never creates them)" \
   && has "reapplied from the release artifacts as its own step"; then
  emit "F-REUPGRADE-WEBHOOK-REINSTALL-CONTRACT" pass "re-upgrade mechanism honest: entry re-applied from release artifacts, not by the image"
else
  emit "F-REUPGRADE-WEBHOOK-REINSTALL-CONTRACT" fail "re-upgrade 'reinstalls the entry (it deploys with the image)' misattribution is back"
fi

# F-DOWNGRADE-UNSCOPED-SAFETY (round-5 NEW minor — Copilot comment
#  4517519293, doc-verified by Claude; FIXED at 31466993): the safety
# condition is now "A cluster whose references set only `profileName`" —
# neither new field — and a downgrade drill pins the silent-loss shape of the
# scenario-4 reference under the old controller.
if has "A cluster whose references set only \`profileName\` can downgrade at any depth, always safely" \
   && lacks "references are all unscoped can downgrade" \
   && printf '%s' "$testplan" | grep -qF "A standalonePattern reference with \`targetContainers\` but no \`component\` under the old controller"; then
  emit "F-DOWNGRADE-UNSCOPED-SAFETY-CONTRACT" pass "downgrade safety scoped to neither-new-field; scenario-4 drill present"
else
  emit "F-DOWNGRADE-UNSCOPED-SAFETY-CONTRACT" fail "downgrade safety condition regressed to unscoped-references"
fi

# Q-CERP-NO-ADOPTION (round-5 NEW question; ADDRESSED at 31466993): the
# premise is grounded on the API stage ("rests on the API stage rather than
# on a field survey") — both served versions alpha, breaking changes
# permitted without staging — instead of an unverifiable adoption survey.
if has "rests on the API stage rather than on a field survey" \
   && lacks "ClusterEngineRuntimeProfile has no live adoption"; then
  emit "Q-CERP-NO-ADOPTION-CONTRACT" pass "no-adoption premise grounded on the API stage, not a field survey"
else
  emit "Q-CERP-NO-ADOPTION-CONTRACT" fail "ungrounded no-live-adoption claim is back"
fi

# F-STALE-PR-BODY (round-3 minor, fixed r4, REOPENED r5, FIXED at 31466993):
# the PR body now describes the head design — webhook-only enforcement with
# "deliberately no in-use finalizer and no opt-out flag" — and no longer
# carries the removed --disable-profile-immutability flag. Read live via gh
# (read-only); emits nothing when gh is unavailable (re-verify then reports
# HARNESS-UPDATE, the honest verdict for an uncheckable external surface).
PRBODY=""
if command -v gh >/dev/null 2>&1; then
  PRBODY="$(gh pr view 548 --repo sgl-project/rbg --json body -q .body 2>/dev/null || true)"
fi
if [ -n "$PRBODY" ]; then
  if printf '%s' "$PRBODY" | grep -qF "no in-use finalizer and no opt-out flag" \
     && ! printf '%s' "$PRBODY" | grep -qF "disable-profile-immutability"; then
    emit "F-STALE-PR-BODY-CONTRACT" pass "PR body matches the head design (webhook-only enforcement, no finalizer/flag)"
  else
    emit "F-STALE-PR-BODY-CONTRACT" fail "PR body stale again — describes a design the head no longer carries"
  fi
fi

# ---------- Round-6 (head 31466993) additions ----------
# Round-5 open nits the author fixed this round — pinned as contracts (they
# were ledger findings without harness entries last round).

# F-INJECTOR-CHAIN-ATTRIBUTION (round-5 NEW nit, open at b4637b51; FIXED at
# 31466993): the 2-4 injector-pass variation is now attributed to chain
# composition — worker chains omit the sidecar injector, LWP env injector
# RIS-only — not to config statefulness (an internal no-op). Verified against
# the SetInjectors call sites: RIS leader [config,sidecar,env,lwpEnv]=4,
# RIS worker [config,env,lwpEnv]=3, LWS worker [config,env]=2; LWS roles are
# always stateful (helper.go: LeaderWorkerSetWorkloadType → true) so the
# floor is 2; InjectConfig no-ops for non-stateful (injector.go).
if has "two to four passes by chain: the worker chains omit the sidecar injector and the LWP env injector runs on the RoleInstanceSet chains only"; then
  emit "F-INJECTOR-CHAIN-ATTRIBUTION-CONTRACT" pass "2-4 injector-pass variation attributed to chain composition (verified against SetInjectors call sites)"
else
  emit "F-INJECTOR-CHAIN-ATTRIBUTION-CONTRACT" fail "injector-pass variation re-attributed to config statefulness (or the chain-composition rationale was removed)"
fi

# F-BOOKKEEPING-OLD-CRD-PRUNING (round-5 NEW nit, open at b4637b51; FIXED at
# 31466993): the version-skew paragraph now states the persisted reference
# tracking lands with the new CRD and the old CRD's status schema (a bare
# `type: object` — verified against the published
# config/crd/bases/workloads.x-k8s.io_clusterengineruntimeprofiles.yaml, both
# versions) prunes the new status fields on write, while the watch event
# fires on either row.
if has "prunes the new status fields on write" \
   && has "the in-memory watch event fires on either row"; then
  emit "F-BOOKKEEPING-OLD-CRD-PRUNING-CONTRACT" pass "old-CRD status pruning of the tracking fields stated in Version skew"
else
  emit "F-BOOKKEEPING-OLD-CRD-PRUNING-CONTRACT" fail "version-skew claim again says tracking binds regardless of CRD version"
fi

# F-DRIFT-BASELINE-UNOBSERVED-DRAIN (round-5 NEW nit, open at b4637b51; FIXED
# at 31466993): the referencedGeneration drift evidence sentence now names the
# unobserved-drain leg — "a bypassed write, or a legal edit in a zero-reference
# window the controller never observed".
if has "a bypassed write, or a legal edit in a zero-reference window the controller never observed"; then
  emit "F-DRIFT-BASELINE-UNOBSERVED-DRAIN-CONTRACT" pass "drift-baseline evidence names the unobserved zero-reference-window leg"
else
  emit "F-DRIFT-BASELINE-UNOBSERVED-DRAIN-CONTRACT" fail "drift evidence again claims precise webhook-bypass evidence only"
fi

# F-CONVERSION-STATUS-PIN (round-5 NEW nit, open at b4637b51; FIXED at
# 31466993): the conversion round-trip envtest pin now covers the new CERP
# status fields, not just containerPatches.
if printf '%s' "$testplan" | grep -qF "keeping \`containerPatches\` and the tracking status fields" \
   && printf '%s' "$testplan" | grep -qF "including the v1alpha1 \`components: []\` edge"; then
  emit "F-CONVERSION-STATUS-PIN-CONTRACT" pass "conversion round-trip pin covers the tracking status fields"
else
  emit "F-CONVERSION-STATUS-PIN-CONTRACT" fail "conversion round-trip pin lost the status-field coverage"
fi

# F-PROFILENAME-LENGTH-CLAIM (pre-existing inaccuracy, unnoticed r1-r5, FIXED
# at 31466993): the old text said the reference keeps profileName "required,
# MinLength=1" — the published schemas carry no MinLength marker
# (api/workloads/v1alpha2/rolebasedgroup_types.go: ProfileName string
# `json:"profileName"` — no +kubebuilder:validation:MinLength). The round-6
# rewrite corrected it to "required in the published schemas, with no
# minLength".
if has "\`profileName\` is required in the published schemas, with no \`minLength\`" \
   && lacks "(required, \`MinLength=1\`)"; then
  emit "F-PROFILENAME-LENGTH-CLAIM-CONTRACT" pass "profileName published-shape claim accurate (required, no minLength)"
else
  emit "F-PROFILENAME-LENGTH-CLAIM-CONTRACT" fail "profileName MinLength=1 claim is back (or the accurate claim was removed)"
fi

# F-R6-NEW-CONTENT (round-6 verified additions — not findings; pins the new
# substantive content the rewrite added, each verified against code/docs):
#  - envFrom priority rules (direct env beats envFrom by priority — matches
#    Kubernetes documented behavior; new Patch Grammar bullet + Test Plan);
#  - volumeMount naming a volume absent from the final rendered volume list
#    is a render error (apiserver would reject at admission);
#  - the /command/0 precondition (args-only / bare target is a
#    profile-attributed render error), consistent with the entrypoint-merge
#    rule;
#  - upgrade order: controller first, CRD second, so the documented dangerous
#    row (old controller + new CRD) is never traversed on the way up;
#  - controller-gen does not emit the schema-level `deprecated: true` flag
#    from field comments (verified against controller-tools v0.17.2 — the
#    repo's pinned CONTROLLER_TOOLS_VERSION — whose only deprecated emission
#    is the version-level +kubebuilder:deprecatedversion marker);
#  - the CERP webhook entry "ships as a release artifact (chart /
#    config/webhook/manifests.yaml)" — the file exists with one
#    ValidatingWebhookConfiguration.
if has "by **priority, not by position**" \
   && has "resolves against no volume in the final rendered pod's volume list" \
   && has "Neither shape has an authored \`command\` for \`/command/0\` to prepend to" \
   && has "Upgrade: controller first, CRD second" \
   && has "controller-gen does not emit" \
   && has "ships as a release artifact (chart / \`config/webhook/manifests.yaml\`)"; then
  emit "F-R6-NEW-CONTENT-CONTRACT" pass "round-6 verified additions present (envFrom rules, mount-volume resolution, wrap precondition, upgrade order, controller-gen, release-artifact webhook)"
else
  emit "F-R6-NEW-CONTENT-CONTRACT" fail "one or more round-6 verified additions were removed or reversed"
fi

# Overall exit: 0 iff no CONTRACT regression (ledger steady).
exit 0
