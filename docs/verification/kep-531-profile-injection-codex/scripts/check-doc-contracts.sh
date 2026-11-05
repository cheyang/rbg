#!/usr/bin/env bash
# check-doc-contracts.sh — text-level contract checks for the KEP-531 design doc
# (PR #548). The Go harness pins main's *code* behavior; this script pins the
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
if has "$F" 'no conflict check to carve out' \
   && hasntE "$F" 'spans both passes|spanning all three passes|across legacy merge and both patch passes' \
   && has "$F" 'Within one patch list' \
   && has "$F" 'duplicate same-profile references'; then c=0; else c=1; fi
chk DOC-CONSISTENCY 'single-scope env-duplicate rule; legacy merge unchecked; dup-reference byte-for-byte case in Test Plan' $c

# --- F-COVERAGE-UPDATESTRATEGY (round-1 major) ------------------------------
if has "$F" 'updateStrategy' && has "$F" 'Profile lifecycle' && hasnt "$K" 'point by point'; then c=0; else c=1; fi
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
if has "$F" 'carries no pattern or length bound'; then c=0; else c=1; fi
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

# --- F-PROPAGATION-TRIGGER (round-2 minor, OPEN) -----------------------------
# Contract: Profile Mutability states profile changes land at the next
# reconcile of any cause (spec change, owned-child event, controller
# restart) — not "only deliberate workload updates" — and names the
# consent-evolution caveat for consenting references.
if has "$F" 'today only deliberate workload updates'; then c=1; else c=0; fi
chk DOC-PROPAGATION-TRIGGER 'profile-change propagation trigger stated accurately (any reconcile, not only deliberate updates)' $c

echo "----"; echo "pass=$pass fail=$fail"
[ "$fail" -eq 0 ]
