#!/usr/bin/env bash
# check-doc-contracts.sh — text-level contract checks for the KEP-531 design doc
# (PR #548). Round 4 (head 0c51bc0e): the env model was redesigned from
# author-precedence+normalization to verbatim positional (kubelet
# last-occurrence) semantics; strings re-pinned for it. New contract checks pin
# the round-4 fixes (DOC-ENV-POSITIONAL-MODEL, DOC-COMPAT-CERP-LIFECYCLE,
# DOC-GRADUATION-EVENTS, DOC-KEPYAML-SEEALSO, DOC-DOWNGRADE-WORDING,
# DOC-V1ALPHA1-CEL, DOC-PROFILE-EVOLUTION-TEST) and the round-4 findings
# (DOC-CAPABILITY-FORM-REGEX, DOC-DEPRECATED-PATH-MECHANISM,
# DOC-FINALIZER-REMOVAL-OVERCLAIM, DOC-WEBHOOK-ROLLBACK).
# The Go harness pins main's *code* behavior; this script pins the *document*
# findings: each check is a contract test over the KEP text at a given git ref.
# PASS = the criterion is met; FAIL = the finding reproduces.
#
# Usage: check-doc-contracts.sh [ref]   (default: HEAD; run from inside the repo)
set -uo pipefail
REF="${1:-HEAD}"
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
R="$TMP/README.md"; K="$TMP/kep.yaml"; F="$TMP/README.flat"
if git cat-file -e "$REF:keps/531-engine-runtime-profile-injection/README.md" 2>/dev/null; then
  git show "$REF:keps/531-engine-runtime-profile-injection/README.md" > "$R"
  git show "$REF:keps/531-engine-runtime-profile-injection/kep.yaml" > "$K"
  tr '\n' ' ' < "$R" | tr -s ' ' > "$F"   # prose phrases may wrap across lines
else
  echo "KEP absent at $REF (pre-PR base): all doc contracts vacuously PASS"
  exit 0
fi

pass=0; fail=0
chk() { # $1=id $2=desc $3=0-if-pass
  if [ "$3" -eq 0 ]; then printf 'PASS  %-28s %s\n' "$1" "$2"; pass=$((pass+1));
  else printf 'FAIL  %-28s %s\n' "$1" "$2"; fail=$((fail+1)); fi
}
has()  { grep -qF "$2" "$1"; }          # fixed-string, file $1
hasnt(){ ! grep -qF "$2" "$1"; }
hasE() { grep -qE "$2" "$1"; }
hasntE(){ ! grep -qE "$2" "$1"; }

# --- F-ENV-CONFLICT (round-1 blocker): one consistent env-duplicate model ---
if has "$F" 'legacy-only duplicates never error' \
   && hasntE "$F" 'spans both passes|spanning all three passes|across legacy merge and both patch passes' \
   && has "$F" 'Within one patch list' \
   && grep -qE 'duplicate.same.profile references|duplicate-reference case' "$F"; then c=0; else c=1; fi
chk DOC-CONSISTENCY 'single-scope env-duplicate rule; legacy merge unchecked; dup-reference byte-for-byte case in Test Plan' $c

# --- F-COVERAGE-UPDATESTRATEGY (round-1 major) ------------------------------
if has "$F" 'updateStrategy' && has "$F" 'Profile Mutability' && hasnt "$K" 'point by point'; then c=0; else c=1; fi
chk DOC-ISSUE-COVERAGE 'updateStrategy/profile-lifecycle dispositioned; kep.yaml overclaim gone' $c

# --- F-CAPS-MAXITEMS (round-1 major) ----------------------------------------
if hasntE "$F" 'roles=16|engineRuntimes=16|matching the v1alpha2 cap' && has "$F" 'no new caps'; then c=0; else c=1; fi
chk DOC-CAPS 'no false pre-existing-cap claims; published lists documented uncapped' $c

# --- AC-KEP531-MAIN-BEHAVIOR-ACCURACY (retained wording nits) ---------------
# Round 4: mountPath citation FIXED ('the apiserver rejects duplicate mount
# paths' + vendored validation.go:3046). F-PODSECURITY-STAGE still open
# ('fails at workload creation' survives in the risk table). Injector-count
# nit re-pinned to the round-4 wording: 'the four built-in injector passes'
# still asserts one fixed chain per component template, but RIS worker runs 3
# and LWS worker 2 (SetInjectors call sites at base); PASS requires either the
# flat claim gone or an explicit per-template variation note.
if has "$F" 'the apiserver rejects duplicate mount paths' \
   && hasnt "$F" 'kubelet resolves overlapping mount paths last-wins' \
   && hasnt "$F" 'fails at workload creation' \
   && { hasnt "$F" 'the four built-in injector passes' \
        || grep -qE 'injector (set|chain|passes)[^.]{0,80}(varies|vary|per (template|render path))|two to four|2–4' "$F"; } \
   && hasnt "$F" 'dependency would point backwards'; then c=0; else c=1; fi
chk DOC-MAIN-ACCURACY 'mechanism-citation slips fixed (mountPath apiserver / PodSecurity stage / injector-set variation / $(VAR) direction)' $c

# --- F-COMPONENT-VOCAB ------------------------------------------------------
if grep -qE 'carries no pattern or length bound|lack of a pattern on `component` is deliberate' "$F"; then c=0; else c=1; fi
chk DOC-COMPONENT-VOCAB 'component field documented as unvalidated (mirrors unmarked source vocabulary)' $c

# --- F-CONVERSION-ROUNDTRIP -------------------------------------------------
if has "$F" 'round-trip losslessness' && has "$F" 'round-trip conversion'; then c=0; else c=1; fi
chk DOC-CONVERSION 'v1alpha1<->v1alpha2 round-trip pinned (Version Serving + Graduation)' $c

# --- F-ENABLEMENT-ROLLBACK --------------------------------------------------
if hasE "$R" '^## Feature Enablement and Rollback'; then c=0; else c=1; fi
chk DOC-ENABLEMENT 'Feature Enablement and Rollback section present (KEP template, alpha)' $c

# --- F-UNKNOWN-TARGET-PROSE -------------------------------------------------
if has "$F" 'resolves against the role-declared names snapshotted before injection' \
   && grep -E 'anything else .{0,80}is a render error' "$F" >/dev/null; then c=0; else c=1; fi
chk DOC-UNKNOWN-TARGET 'unknown targetContainers name stated as render error in the resolution prose' $c

# --- F-COPILOT-VOLUMEMOUNT-EQUALITY -----------------------------------------
if has "$F" 'semantic equality'; then c=0; else c=1; fi
chk DOC-VMOUNT-EQUALITY 'volumeMount no-op requires full-field semantic equality' $c

# --- F-INFERENCE-KIT-404 -----------------------------------------------------
if hasnt "$K" 'inference-kit'; then c=0; else c=1; fi
chk DOC-NO-404-LINK 'kep.yaml carries no dead see-also link' $c

# --- F-STAGE3-LEGACY-VISIBILITY (round-2 major; FIXED at 0c51bc0e) ----------
# Contract (re-pinned r4 — Harness-update: the r3 sentence was replaced by the
# two-static-sources rule): role-owned env names must include the referencing
# role's own legacy engineRuntimes[].containers[].env names, and the
# 'earlier references only' visibility clause must be gone.
if has "$F" 'role-owned names come from two static sources' \
   && has "$F" 'legacy `engineRuntimes[].containers[].env` names' \
   && hasnt "$F" 'earlier references only'; then c=0; else c=1; fi
chk DOC-STAGE3-LEGACY "stage-3 ownership counts the role's own legacy-merge env names; 'earlier references only' gone" $c

# --- F-PROPAGATION-TRIGGER (round-2 minor; FIXED at d84004a2 by redesign) ---
if hasnt "$F" 'today only deliberate workload updates' \
   && has "$F" 'immutable while referenced'; then c=0; else c=1; fi
chk DOC-PROPAGATION-TRIGGER 'propagation settled: no inaccurate trigger claim; referenced profiles immutable' $c

# --- F-SSA-ENV-PREMISE (round-3 minor; FIXED at 0c51bc0e by removal) --------
# Contract: no false SSA duplicate-env-rejection premise (RIS template is
# schemaless, TestKEP531_S9) AND no normalization/author-precedence leftovers.
if hasnt "$F" 'keyed by name and rejects duplicates' \
   && hasnt "$F" 'duplicate-free' \
   && hasnt "$F" 'author precedence' && hasnt "$F" 'author-precedence'; then c=0; else c=1; fi
chk DOC-SSA-ENV-KEYED 'false SSA premise and the normalization/author-precedence model are gone' $c

# --- DOC-ENV-POSITIONAL-MODEL (round-4 redesign pinned as regression contract)
if has "$F" 'standard Kubernetes semantics' \
   && has "$F" 'never reordered or deduplicated' \
   && has "$F" 'legal **only** at index `0`' \
   && has "$F" 'EngineRuntimeEnvCollision'; then c=0; else c=1; fi
chk DOC-ENV-POSITIONAL-MODEL 'positional env model stated: verbatim landing, index-0-only profile default on role-owned names, collision event' $c

# --- F-FLAG-FINALIZER-TRANSITION (round-3 minor; FIXED at 0c51bc0e) ---------
# Contract (tightened r4 — r3's regex matched the Test-Plan sentence only;
# require the normative removal semantics): the flag section must state the
# controller removes existing finalizers while the flag is set, and the Test
# Plan must pin the transition.
if hasnt "$F" 'disable-profile-immutability'; then c=0
elif has "$F" 'neither added nor kept' \
   && has "$F" 'actively removes any in-use finalizer' \
   && hasE "$F" 'removed once the flag is set'; then c=0; else c=1; fi
chk DOC-FLAG-FINALIZER 'escape-hatch flag: existing in-use finalizers actively removed; transition test planned' $c

# --- F-DELETE-FINALIZER-RACE (round-3 nit; FIXED at 0c51bc0e) ---------------
# Contract (strengthened r4): exhaustiveness claim gone AND the delete-side
# window named.
if hasnt "$F" 'One race exists' \
   && has "$F" 'Two races of the same shape' \
   && hasE "$F" 'profile DELETE[^.]{0,300}before the[^.]{0,160}finalizer'; then c=0; else c=1; fi
chk DOC-DELETE-RACE 'accepted-race inventory names the finalizer-add/delete window; no exhaustiveness claim' $c

# --- F-CERP-LIFECYCLE-COMPAT-PLACEMENT (round-3 minor; FIXED at 0c51bc0e) ---
if has "$F" 'The CERP lifecycle change is staged' \
   && has "$F" 'holds under enforcement'; then c=0; else c=1; fi
chk DOC-COMPAT-CERP-LIFECYCLE 'Compatibility section carries the CERP lifecycle item; #2 qualified for the alpha window' $c

# --- F-GRADUATION-EVENT-INVENTORY (round-2 nit; FIXED at 0c51bc0e) ----------
if has "$F" 'EngineRuntimeWrapShadowed' && has "$F" 'EngineRuntimeEnvCollision' \
   && grep -qE 'Warning events:[^]]*EngineRuntimeWrapShadowed' "$F"; then c=0; else c=1; fi
chk DOC-GRADUATION-EVENTS 'Graduation checklist inventories all named warning events incl. the shadowing and env-collision events' $c

# --- F-KEPYAML-SEEALSO-REMOVED (round-2 nit; FIXED at 0c51bc0e) -------------
if has "$K" 'issues/531'; then c=0; else c=1; fi
chk DOC-KEPYAML-SEEALSO 'kep.yaml see-also links issue #531' $c

# --- F-PROPAGATION-DOWNGRADE-WORDING (round-3 nit; FIXED at 0c51bc0e) -------
if hasnt "$F" 'only a deliberate workload update' \
   && has "$F" "the workload's own rollout"; then c=0; else c=1; fi
chk DOC-DOWNGRADE-WORDING "Downgrade framing: every reconcile re-renders; pods change only via the workload's own rollout" $c

# --- C-COPILOT-V1ALPHA1-CEL-RULE (round-3 advisory; ADDRESSED at 0c51bc0e) --
if has "$F" 'has(self.leaderWorkerSet)' && has "$F" 'v1alpha1 mirror'; then c=0; else c=1; fi
chk DOC-V1ALPHA1-CEL 'v1alpha1 CEL mirror carries the version-specific predicate (no standalonePattern in v1alpha1)' $c

# --- F-PROFILE-EVOLUTION-TEST (round-1 minor; FIXED at 0c51bc0e) ------------
# Contract: the landing semantics of an admitted profile edit is stated and
# pinned by a planned envtest (delivered via the alpha-warning-window case —
# same render-read mechanism as the disable-flag path).
if has "$F" "alpha warning window's semantics" \
   && has "$F" 'subsequent renders reading the changed content'; then c=0; else c=1; fi
chk DOC-PROFILE-EVOLUTION-TEST 'profile-edit landing semantics stated and pinned by a planned envtest' $c

# --- F-CAPABILITY-FORM-REGEX (round-4 NEW minor; OPEN) -----------------------
# Contract: the form-warning rule must actually fire on the CAP_-prefixed form
# the Test Plan promises to warn on. The specified regex ^[A-Z]+(_[A-Z]+)*$
# ACCEPTS CAP_SYS_ADMIN (verified: echo CAP_SYS_ADMIN | grep -E), so the
# warning can never fire for the prefixed form while the Test Plan
# (:1168-1170) promises 'the form warning on lowercase and CAP_-prefixed
# names'. PASS when either side of the contradiction is fixed.
if has "$F" 'form warning on lowercase and `CAP_`-prefixed names' \
   && has "$F" 'canonical uppercase form `^[A-Z]+(_[A-Z]+)*$`'; then c=1; else c=0; fi
chk DOC-CAPABILITY-FORM-REGEX 'capability form-warning regex must exclude the CAP_ prefix the Test Plan promises to warn on' $c

# --- F-DEPRECATED-PATH-MECHANISM (round-4 NEW minor; OPEN) -------------------
# Contract: the promised rejection of the new fields on deprecated render
# paths (Non-Goals, Design Details, Test Plan) must name its mechanism/home
# (render error, webhook, or CEL on the workload-type annotation). Workload
# type is annotation-driven with RoleInstanceSet default
# (rolebasedgroup_types.go GetWorkloadType), so a write-time rule is
# expressible — but the doc never says which gate fires.
if hasnt "$F" 'rejected there rather than given meaning'; then c=0
elif grep -qE 'rejected there rather than given meaning[^.]{0,200}(render error|webhook|CEL|write[- ]time)' "$F" \
   || grep -qE '(render error|webhook|CEL|write[- ]time)[^.]{0,200}rejected there rather than given meaning' "$F"; then c=0; else c=1; fi
chk DOC-DEPRECATED-PATH-MECHANISM 'deprecated-path rejection of the new fields names its enforcement mechanism' $c

# --- F-FINALIZER-REMOVAL-OVERCLAIM (round-4 NEW nit; OPEN) -------------------
# Contract: the finalizer-removal freshness sentence must not claim the
# uncached confirm closes the check-then-act window (an RBG stored after the
# lookup, before the removal, races it; the residual is the doc's own
# accepted race #2 — dangling reference, loud render failure).
if hasnt "$F" 'leaves the finalizer in place'; then c=0
elif grep -qE 'leaves the finalizer in place[^.]{0,200}(race|window|accepted)' "$F"; then c=0; else c=1; fi
chk DOC-FINALIZER-REMOVAL-OVERCLAIM 'finalizer-removal freshness: uncached confirm narrows, not closes, the reference-create race' $c

# --- F-WEBHOOK-ROLLBACK (round-4 NEW minor; OPEN) -----------------------------
# Contract: the Downgrade path must account for the retained CERP webhook
# configuration on a controller-image-only rollback (with failurePolicy: Fail
# the stranded configuration rejects CERP updates/deletes — including the
# manual finalizer removal the same paragraph prescribes). Proxy pin: the
# Downgrade section must mention the webhook configuration's fate.
DSEC="$TMP/downgrade.flat"
awk '/^\*\*Downgrade\.\*\*/{f=1} f&&/^A cluster whose references/{f=0} f' "$R" | tr '\n' ' ' > "$DSEC"
if grep -qiE 'webhook' "$DSEC"; then c=0; else c=1; fi
chk DOC-WEBHOOK-ROLLBACK 'Downgrade path addresses the retained CERP webhook configuration (failurePolicy deadlock)' $c

echo "----"; echo "pass=$pass fail=$fail"
[ "$fail" -eq 0 ]
