#!/usr/bin/env bash
# check-doc-contracts.sh — text-level contract checks for the KEP-531 design doc
# (PR #548). Round 3: strings updated for the d84004a2 condensation, three
# checks added (DOC-SSA-ENV-KEYED, DOC-FLAG-FINALIZER, DOC-DELETE-RACE). The Go harness pins main's *code* behavior; this script pins the
# *document* findings: each check is a contract test over the KEP text at a
# given git ref. PASS = the criterion is met; FAIL = the finding reproduces.
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
# String drift at d84004a2 (harness-update, r3): 'no conflict check to carve
# out' -> 'legacy-only duplicates never error'; Test Plan 'duplicate
# same-profile references' -> 'the duplicate-reference case'.
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

# --- AC-KEP531-MAIN-BEHAVIOR-ACCURACY (4 retained wording nits) -------------
if hasnt "$F" 'kubelet resolves overlapping mount paths last-wins' \
   && hasnt "$F" 'fails at workload creation' \
   && hasnt "$F" 'four-injector pass' \
   && hasnt "$F" 'dependency would point backwards'; then c=0; else c=1; fi
chk DOC-MAIN-ACCURACY 'mechanism-citation slips fixed (mountPath / PodSecurity stage / injector counts / $(VAR) direction)' $c

# --- F-COMPONENT-VOCAB ------------------------------------------------------
# String drift at d84004a2 (harness-update, r3): 'carries no pattern or length
# bound' -> 'The lack of a pattern on `component` is deliberate'.
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

# --- F-STAGE3-LEGACY-VISIBILITY (round-2 major, OPEN) ------------------------
# Contract: the stage-3 role-owned ownership check must count the referencing
# role's OWN legacy-merge output (engineRuntimes[].containers env names) as
# role-owned; otherwise a profile-side /env/- append silently overrides the
# role's legacy override, contradicting Compatibility #3 ("keeps its exact
# outcome"). PASS iff the visibility sentence names the legacy merge.
if grep -E 'role-owned checks against[^.]*legacy' "$F" >/dev/null; then c=0; else c=1; fi
chk DOC-STAGE3-LEGACY "stage-3 ownership-check visibility includes the reference's own legacy-merge env names" $c

# --- F-PROPAGATION-TRIGGER (round-2 minor; FIXED at d84004a2 by redesign) ---
# Contract (updated, r3): the inaccurate 'today only deliberate workload
# updates' sentence is gone AND the profile-change question is settled
# structurally — a referenced profile is immutable, so there is no silent
# trigger set left to state.
if hasnt "$F" 'today only deliberate workload updates' \
   && has "$F" 'immutable while referenced'; then c=0; else c=1; fi
chk DOC-PROPAGATION-TRIGGER 'propagation settled: no inaccurate trigger claim; referenced profiles immutable' $c

# --- F-SSA-DUPLICATE-ENV-PREMISE (round-3 minor, OPEN) -----------------------
# Contract: the KEP must not claim the RIS server-side-apply path keys env by
# name / rejects duplicates — the RIS CRD's roleInstanceTemplate is schemaless
# (x-kubernetes-preserve-unknown-fields), pinned by
# TestKEP531_S9_RISTemplateSchemalessSoSSACannotRejectDuplicateEnv.
if hasnt "$F" 'keyed by name and rejects duplicates'; then c=0; else c=1; fi
chk DOC-SSA-ENV-KEYED 'no false SSA duplicate-env-rejection premise (RIS template is schemaless)' $c

# --- F-FLAG-FINALIZER-TRANSITION (round-3 minor, OPEN) -----------------------
# Contract: when --disable-profile-immutability exists, the doc states what
# happens to EXISTING in-use finalizers while the flag is set (they must be
# removed/lifted, else a profile adopted before the flag can never be
# deleted). Vacuous PASS where the flag does not exist (prev heads).
if hasnt "$F" 'disable-profile-immutability'; then c=0
elif grep -qE 'finalizer[^.]{0,200}(removed|removal|cleared|lifted|dropped)[^.]{0,200}(flag|disable-profile-immutability)|(flag|disable-profile-immutability)[^.]{0,200}finalizer[^.]{0,200}(removed|removal|cleared|lifted|dropped)' "$F"; then c=0; else c=1; fi
chk DOC-FLAG-FINALIZER 'escape-hatch flag: existing in-use finalizers are removed while the flag is set' $c

# --- F-DELETE-FINALIZER-RACE (round-3 nit, OPEN) ------------------------------
# Contract: the accepted-race inventory must not claim exhaustiveness ('One
# race exists') while the reference-add/finalizer-add/delete window is
# unstated. PASS if the claim is gone or a delete-side window is acknowledged.
if hasnt "$F" 'One race exists'; then c=0
elif grep -qE 'delet[a-z]+[^.]{0,200}(before|prior to|ahead of)[^.]{0,160}finalizer|finalizer[^.]{0,160}(not yet|has not (yet )?been|before it (is )?(added|installed))|before the (in-use )?finalizer' "$F"; then c=0; else c=1; fi
chk DOC-DELETE-RACE 'accepted-race inventory covers the finalizer-add/delete window (or drops the exhaustiveness claim)' $c

echo "----"; echo "pass=$pass fail=$fail"
[ "$fail" -eq 0 ]
