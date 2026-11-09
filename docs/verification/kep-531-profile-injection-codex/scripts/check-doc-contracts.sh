#!/usr/bin/env bash
# check-doc-contracts.sh — text-level contract checks for the KEP-531 design doc
# (PR #548). Round 5 (head b4637b51): the immutability subsystem was redesigned
# again — the in-use finalizer, the alpha warning window and the
# --disable-profile-immutability escape hatch were REMOVED; enforcement is now a
# failurePolicy: Fail CERP webhook (update+DELETE) from the alpha, a controller
# watch, and persisted status bookkeeping (referenceCount / referencedGeneration
# / Frozen condition). Round-5 re-pins: DOC-DELETE-RACE, DOC-COMPAT-CERP-LIFECYCLE,
# DOC-PROFILE-EVOLUTION-TEST, DOC-CAPABILITY-FORM-REGEX (now passing),
# DOC-V1ALPHA1-CEL (tightened to the size>0 predicate), and
# DOC-FLAG-FINALIZER -> DOC-IMMUTABILITY-REDESIGN (removal consistency).
# New round-5 contracts pin the fixed round-4 findings (DOC-ROLE-INSERT-ORDER,
# DOC-PROFILE-DEFAULT-GEOMETRY, DOC-SNAPSHOT-ENV-SOURCE) and the new round-5
# findings (DOC-INJECTOR-CHAIN-ATTRIBUTION, DOC-NO-LIVE-ADOPTION,
# DOC-BOOKKEEPING-OLD-CRD, DOC-DRIFT-BASELINE-EDGE — all four FAIL at head).
# Round 4 (head 0c51bc0e): the env model was redesigned from
# author-precedence+normalization to verbatim positional (kubelet
# last-occurrence) semantics; strings re-pinned for it.
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

# --- F-FLAG-FINALIZER-TRANSITION (round-3 minor; superseded by the r5
# redesign — the flag AND the finalizer are gone). Replaced by the
# removal-consistency contract: no escape-hatch flag, no finalizer machinery,
# no alpha warning window may survive anywhere in the doc, and the redesigned
# pieces must be present.
if hasnt "$F" 'disable-profile-immutability' \
   && hasnt "$F" 'escape hatch' \
   && hasnt "$F" 'admission warning' \
   && has "$F" 'deliberately no in-use finalizer' \
   && has "$F" 'failurePolicy: Fail' \
   && has "$F" 'no warning window'; then c=0; else c=1; fi
chk DOC-IMMUTABILITY-REDESIGN 'r5 immutability redesign consistent: flag/finalizer/warning-window fully removed; fail-closed webhook stated' $c

# --- F-DELETE-FINALIZER-RACE (round-3 nit; FIXED at 0c51bc0e) ---------------
# Contract (strengthened r4; re-pinned r5 — Harness-update: the finalizer is
# gone, so the delete-side window is now reference-storage vs delete-admission):
# exhaustiveness claim gone AND the delete-side window named with its outcome.
if hasnt "$F" 'One race exists' \
   && has "$F" 'Two races of the same shape' \
   && hasE "$F" 'profile DELETE[^.]{0,300}before the[^.]{0,160}(finalizer|referencing role is stored)' \
   && has "$F" 'the dangling reference fails its next render'; then c=0; else c=1; fi
chk DOC-DELETE-RACE 'accepted-race inventory names the delete-side window and outcome; no exhaustiveness claim' $c

# --- F-CERP-LIFECYCLE-COMPAT-PLACEMENT (round-3 minor; FIXED at 0c51bc0e) ---
# Re-pinned r5 (Harness-update): the staging was removed; the Compatibility
# section must still carry the CERP lifecycle item and #2 must state how the
# guarantee is enforced (admission rejection, fail-closed).
if has "$F" 'The CERP lifecycle change applies to an empty set' \
   && has "$F" 'enforced from the alpha by admission rejection, fail-closed' \
   && has "$F" 'is still accepted'; then c=0; else c=1; fi
chk DOC-COMPAT-CERP-LIFECYCLE 'Compatibility carries the CERP lifecycle item; #2 states the enforcement; updateStrategy accepted' $c

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
# Tightened r5 (F-V1ALPHA1-CEL-EMPTY-COMPONENTS fixed): the predicate must be
# the size>0 form — has() is true for a present-but-empty list.
if has "$F" '!(has(self.components) && size(self.components) > 0)' \
   && has "$F" 'v1alpha1 mirror'; then c=0; else c=1; fi
chk DOC-V1ALPHA1-CEL 'v1alpha1 CEL mirror carries the size>0 predicate (empty-components edge handled)' $c

# --- F-PROFILE-EVOLUTION-TEST (round-1 minor; FIXED at 0c51bc0e) ------------
# Re-pinned r5 (Harness-update): with rejection-from-alpha there is no admitted
# edit to land; profile-evolution safety is structural. Contract: the Test Plan
# pins the webhook rejection of referenced-profile spec changes (and the
# metadata-only admission), and the revision workflow end-to-end.
if has "$F" 'the webhook rejecting a spec change to a referenced' \
   && has "$F" 'admitting a metadata-only change' \
   && has "$F" 'the revision workflow end-to-end'; then c=0; else c=1; fi
chk DOC-PROFILE-EVOLUTION-TEST 'profile evolution pinned structurally: rejection of referenced edits + revision workflow envtest' $c

# --- F-CAPABILITY-FORM-REGEX (round-4 minor; FIXED at b4637b51) --------------
# Contract (re-pinned r5): the form-warning rule must actually fire on the
# CAP_-prefixed form the Test Plan promises to warn on. Fixed by the added
# clause: a name matching the canonical form while carrying the CAP_ prefix
# also warns. PASS when the rule covers the prefixed form (or the promise is
# dropped).
if has "$F" 'or matching it while carrying the `CAP_` prefix'; then c=0
elif has "$F" 'form warning on lowercase and `CAP_`-prefixed names' \
   && has "$F" 'canonical uppercase form `^[A-Z]+(_[A-Z]+)*$`'; then c=1; else c=0; fi
chk DOC-CAPABILITY-FORM-REGEX 'capability form-warning fires on the CAP_-prefixed form the Test Plan promises' $c

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

# --- F-FINALIZER-REMOVAL-OVERCLAIM (round-4 nit; FIXED at b4637b51 by
# removal — the finalizer and its freshness sentence are gone with the r5
# redesign; the check now guards against reintroduction of the overclaim).
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


# --- F-ROLE-INSERT-ORDER (round-4 minor; FIXED at b4637b51) ------------------
# Contract: the role-side two-reference winner rule is qualified by spelling
# (append: later reference wins; two head inserts: inverted, earlier wins) and
# the Test Plan echo carries the same split.
if has "$F" 'resolve by reference order, by spelling' \
   && has "$F" 'pushes the earlier reference' \
   && has "$F" 'two `/env/0` head inserts leaving the earlier'; then c=0; else c=1; fi
chk DOC-ROLE-INSERT-ORDER 'role-side two-reference winner rule qualified by spelling (grammar + Test Plan)' $c

# --- F-PROFILE-DEFAULT-GEOMETRY (round-4 minor; FIXED at b4637b51) -----------
# Contract: the index-0-only profile-default rule's rationale matches insert
# geometry — the false 'any position but the head lands after the role entry'
# claim is gone, replaced by the statically-knowable-head reasoning.
if hasnt "$F" 'any position but the head lands' \
   && has "$F" 'Only the head is statically knowable' \
   && has "$F" 'lands after it and wins by'; then c=0; else c=1; fi
chk DOC-PROFILE-DEFAULT-GEOMETRY 'index-0-only default rule rationale matches insert geometry' $c

# --- F-SNAPSHOT-ENV-SOURCE (round-4 nit; FIXED at b4637b51) ------------------
# Contract: the stage-3 role-owned sources name the role template's env entries
# read off the snapshotted containers (the snapshot holds container names).
if has "$F" 'the role template' \
   && grep -qE "the role template's own env entries" "$F" \
   && has "$F" 'the snapshot holds container names'; then c=0; else c=1; fi
chk DOC-SNAPSHOT-ENV-SOURCE 'stage-3 sources: role template env entries read off the snapshotted containers' $c

# --- F-INJECTOR-CHAIN-ATTRIBUTION (round-5 NEW nit; OPEN) --------------------
# Contract: the clause explaining the 2-4 built-in-injector pass variation must
# name real membership drivers. At head it says 'the config injector runs for
# stateful roles only and the worker chain omits the sidecar injector' — but
# config is a MEMBER of every chain (default set [config, sidecar, commonEnv]
# at pod_reconciler.go:96-98; its stateful-only aspect is an internal no-op,
# injector.go IsStatefulRole), and the second real driver — lwpEnv present only
# on the two RIS chains — is unmentioned, so the stated rules mispredict the
# LWS-leader/default chains (3 actual, 4 predicted). PASS when the variation
# note names lwpEnv's RIS-only membership (or drops the false attribution).
if hasnt "$F" 'two to four passes by chain'; then c=0
elif grep -qE 'two to four passes by chain[^.]{0,200}(LWP|lwp)[^.]{0,120}(RIS|RoleInstanceSet)|(LWP|lwp)[^.]{0,120}(RIS|RoleInstanceSet)[^.]{0,200}two to four' "$F"; then c=0
elif hasnt "$F" 'the config injector runs for stateful roles only'; then c=0; else c=1; fi
chk DOC-INJECTOR-CHAIN-ATTRIBUTION 'injector pass-count variation attributed to real membership drivers (lwpEnv RIS-only), not config statefulness' $c

# --- F-NO-LIVE-ADOPTION-CLAIM (round-5 NEW minor; OPEN) ----------------------
# Contract: the reject-from-alpha/no-warning-window/no-opt-out staging decision
# must rest on a verifiable or principled justification, not the unverifiable
# empirical claim 'ClusterEngineRuntimeProfile has no live adoption' (CERP has
# shipped in stable tags v0.7.0 and v0.8.0; profileName references are live and
# consumed by main's renderer — premise tests C-*). PASS when every occurrence
# is qualified ('no known adoption' + contingency) or replaced by the alpha-API
# policy justification.
if has "$F" 'has no live adoption' || has "$F" 'zero adoption'; then c=1; else c=0; fi
chk DOC-NO-LIVE-ADOPTION 'reject-from-alpha justification is principled/verifiable, not an unverifiable zero-adoption claim' $c

# --- F-BOOKKEEPING-OLD-CRD-PRUNING (round-5 NEW nit; OPEN) -------------------
# Contract: the version-skew claim must not say the reference bookkeeping binds
# 'regardless of CRD version'. The base CERP CRD's status schema is
# 'type: object' with no properties and no x-kubernetes-preserve-unknown-fields
# (config/crd/bases/...clusterengineruntimeprofiles.yaml), so on the
# new-controller/old-CRD row the new status fields (and the REFERENCES printer
# column) are pruned on write — enforcement is unaffected (status is never
# authoritative). PASS when the claim is scoped to the admission or the
# bookkeeping's CRD dependency is stated.
SKEW="$TMP/skew.flat"
awk '/^\*\*Version skew\.\*\*/{f=1} f&&/^## Feature Enablement/{f=0} f' "$R" | tr '\n' ' ' > "$SKEW"
if has "$SKEW" 'the reference bookkeeping ship' && has "$SKEW" 'regardless of CRD version'; then c=1; else c=0; fi
chk DOC-BOOKKEEPING-OLD-CRD 'version-skew claim scoped: bookkeeping needs the new CRD (old status schema prunes); only admission is CRD-independent' $c

# --- F-DRIFT-BASELINE-UNOBSERVED-DRAIN (round-5 NEW nit; OPEN) ---------------
# Contract: the referencedGeneration drift claim must not call the baseline
# mismatch 'precise evidence that the spec changed while referenced, with the
# webhook bypassed' — a legal edit admitted during a genuine zero-reference
# window the controller never observed (remove -> edit -> re-add across one
# observation gap, e.g. a controller restart) flips Frozen to
# SpecChangedWhileReferenced with no bypass. PASS when the sentence admits the
# unobserved-drain case.
if has "$F" 'is precise evidence that the spec changed while referenced'; then c=1; else c=0; fi
chk DOC-DRIFT-BASELINE-EDGE 'referencedGeneration drift claim admits the unobserved-drain false-positive window' $c

echo "----"; echo "pass=$pass fail=$fail"
[ "$fail" -eq 0 ]
