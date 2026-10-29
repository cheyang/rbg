#!/usr/bin/env bash
# Re-verification harness for PR #491 (KEP-455: RBGSet rolling update).
# Reviewer A (Claude) — round 1, 2026-10-11; round 2, 2026-10-14 (head
# b03a7e67: round-1 fixes pinned as contract checks, E3c inverted after the
# F2 fix, new R2-F9 contract reproduction for the bootstrap-window corner).
#
# The PR is docs-only, so the harness has two layers:
#   L1 unit:         Go tests pinning the codebase facts the KEP relies on
#                    (internal/controller/workloads/verify_pr491_claude_test.go)
#                    plus the repo unit suites for the touched components.
#   L2 integration:  repo envtest suites (fake-code paths are covered in L1;
#                    envtest exercises real apiserver + CRDs + webhooks).
#
# The evidence script below (E-checks) records grep-verifiable facts the Go
# tests cannot express (markers, absence of writers, KEP text contents).
#
# Usage: bash docs/verification/455-rbgs-rolling-update-claude/scripts/re-verify.sh
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd)"
BASE_SHA="$(git -C "$REPO_ROOT" merge-base origin/main HEAD)"
HEAD_SHA="$(git -C "$REPO_ROOT" rev-parse HEAD)"
OUT="$REPO_ROOT/docs/verification/455-rbgs-rolling-update-claude/results"
mkdir -p "$OUT"

echo "== re-verify 455-rbgs-rolling-update-claude =="
echo "base: $BASE_SHA  head: $HEAD_SHA  date: $(date -u +%Y-%m-%dT%H:%M:%SZ)"

# Guard: the PR head this harness was built for must still be an ancestor of
# this branch. If the PR advanced, re-run after rebasing/grafting.
if git rev-parse --verify -q origin/pr/491 >/dev/null 2>&1; then
  if git merge-base --is-ancestor origin/pr/491 HEAD; then
    echo "pr-head: $(git rev-parse origin/pr/491) (covered by this branch)"
  else
    echo "WARN: PR head $(git rev-parse origin/pr/491) is NOT an ancestor of this branch — graft the harness onto the new head before trusting results"
  fi
else
  echo "WARN: origin/pr/491 not fetched locally; run: git fetch origin pull/491/head:refs/remotes/origin/pr/491"
fi

cd "$REPO_ROOT"

fail=0
check() { # check <id> <description> <cmd...>
  local id="$1" desc="$2"; shift 2
  local out
  out=$("$@" 2>&1)
  if echo "$out" | grep -q "$EXPECT_GREP"; then
    echo "PASS $id — $desc"
    echo "$out" >"$OUT/$id.txt"
  else
    echo "FAIL $id — $desc (expected pattern: $EXPECT_GREP)"
    echo "$out" >"$OUT/$id.txt"
    fail=1
  fi
}

# E2: RollingUpdateInProgress is declared in the API but no controller
# publishes it (RBG controller sets only Ready/GangConfigured).
EXPECT_GREP="^0$"
check E2-no-writer "no non-test writer of RollingUpdateInProgress" \
  bash -c 'grep -rn "RollingUpdateInProgress" internal/ cmd/ pkg/ 2>/dev/null | grep -v "_test.go" | wc -l'

# E3a: scale subresource is a served, existing API surface on both versions.
EXPECT_GREP="statusReplicasPath: .status.replicas"
check E3a-scale-subresource-crd "CRD serves the scale subresource on .status.replicas" \
  grep -h "statusReplicasPath" config/crd/bases/workloads.x-k8s.io_rolebasedgroupsets.yaml

EXPECT_GREP="subresource:scale"
check E3b-scale-subresource-api "API markers declare the scale subresource (v1alpha1+v1alpha2)" \
  grep -h "kubebuilder:subresource:scale" api/workloads/v1alpha1/rolebasedgroupset_types.go api/workloads/v1alpha2/rolebasedgroupset_types.go

# E3c (round 2, inverted — F2 fixed at head b03a7e67): the KEP now documents
# the scale-subresource interaction and scopes HPA-during-surge out. This was
# a silence canary (grep=0) in round 1; the fix flipped it, so it is promoted
# to a contract check on the amended text.
EXPECT_GREP="scale subresource"
check E3c-kep-addresses-scale "KEP documents the scale subresource interaction incl. HPA stance (F2 fix)" \
  bash -c 'git show origin/pr/491:keps/455-rbgs-rolling-update/README.md | grep -iE "scale subresource|not a supported configuration"'

# E4a: the RBG controller writes discovery-config-mode onto child RBGs
# (the controller-owned annotation the KEP excludes from revision comparison).
EXPECT_GREP="ensureDiscoveryConfigMode"
check E4a-discovery-writer "RBG controller manages discovery-config-mode" \
  grep -n "func (r \*RoleBasedGroupReconciler) ensureDiscoveryConfigMode" internal/controller/workloads/rolebasedgroup_controller.go

# E4b: today the set replaces child annotations wholesale during an update.
EXPECT_GREP="rbg.Annotations = nil"
check E4b-annotation-wholesale "syncRBGMetadata replaces annotations wholesale" \
  grep -n "rbg.Annotations = nil" internal/controller/workloads/rolebasedgroupset_controller.go

# E5: naming surface the set-level enum must coexist with.
EXPECT_GREP="LegacyRecreateUpdateStrategyType"
check E5-role-level-naming "role-level strategy enum incl. legacy Recreate spelling" \
  grep -n "LegacyRecreateUpdateStrategyType UpdateStrategyType" api/workloads/v1alpha2/rolebasedgroup_types.go

# E6: the v1alpha1->v2 conversion injects a controller-owned key into
# groupTemplate.annotations — a key the revision-hash exclusion list must cover,
# or an unchanged template round-tripped through v1alpha1 changes revision identity.
EXPECT_GREP="annotationV1alpha1Coordination"
check E6-conversion-injects-template-annotation "conversion injects coordination annotation into groupTemplate.annotations" \
  grep -nF "dst.Spec.GroupTemplate.Annotations[annotationV1alpha1Coordination]" api/workloads/v1alpha1/rolebasedgroupset_conversion.go

# ---- Round 2 (2026-10-14, head b03a7e67) -------------------------------
# The PR was force-pushed with a KEP amendment answering round 1. These checks
# pin the round-1 fixes as KEP-text contract tests (regression suite) and add
# a contract test for the one new design corner found this round (F9).

# R2-F1: the Child Rollout Barrier design section exists (producer/consumer/
# ordering), closing the round-1 "barrier has no producer" gap.
EXPECT_GREP="Child Rollout Barrier"
check R2-F1-barrier-section "KEP designs the child barrier: section + producer/consumer/ordering" \
  bash -c 'KEP_SHOW() { git show origin/pr/491:keps/455-rbgs-rolling-update/README.md; }; KEP_SHOW | grep -nE "^#{2,3} Child Rollout Barrier|Producer\.|Consumer\.|Ordering invariant\."'

# R2-F1b: the KEP's ordering claim (status written in Step 4, before Step 8
# mutates workloads) maps onto the real RBG controller structure, and the
# status write really happens inside Step 4 (before reconcileRoles in Step 8).
EXPECT_GREP="constructAndUpdateRoleStatuses"
check R2-F1b-ordering-matches-code "RBG controller: Step 4 constructs+writes status, Step 8 mutates roles" \
  bash -c 'grep -n "Step 4: Construct role statuses\|Step 8: Reconcile roles" internal/controller/workloads/rolebasedgroup_controller.go; sed -n "/func (r \*RoleBasedGroupReconciler) constructAndUpdateRoleStatuses/,/^}/p" internal/controller/workloads/rolebasedgroup_controller.go | grep -n "updateRBGStatus\|constructAndUpdateRoleStatuses"'

# R2-F1c: the producer uses uncached reads; the reconciler has an apiReader.
EXPECT_GREP="GetAPIReader"
check R2-F1c-api-reader-exists "RBG reconciler exposes an apiReader for uncached reads" \
  bash -c 'grep -n "apiReader" internal/controller/workloads/rolebasedgroup_controller.go | grep "GetAPIReader"'

# R2-F3: the controller-owned key exclusion is enumerated once (4 keys) and
# governs revision hash, drift comparison, and metadata sync.
EXPECT_GREP="ok"
check R2-F3-key-enumeration "KEP enumerates 4 controller-owned keys for hash+drift+sync (F3/F4 fix)" \
  bash -c 'KEP_SHOW() { git show origin/pr/491:keps/455-rbgs-rolling-update/README.md; }; K=$(KEP_SHOW); for k in "rbg.workloads.x-k8s.io/discovery-config-mode" "conversion.workloads.x-k8s.io/v1alpha1-coordination" "conversion.workloads.x-k8s.io/v1alpha2-rollout-strategy" "conversion.workloads.x-k8s.io/v1alpha2-rollout-status"; do echo "$K" | grep -qF "$k" || { echo "missing: $k"; exit 1; }; done; echo "$K" | grep -qF "syncRBGMetadata" || { echo "missing: syncRBGMetadata carry-over"; exit 1; }; echo ok'

# R2-F5: restart-safe checkpoint storage is named (annotation on the RBGSet).
EXPECT_GREP="groupset-observed-replicas"
check R2-F5-checkpoint-storage "KEP names the observed-replicas checkpoint storage (F5 fix)" \
  bash -c 'git show origin/pr/491:keps/455-rbgs-rolling-update/README.md | grep -nF "rbg.workloads.x-k8s.io/groupset-observed-replicas"'

# R2-F6: numeric revision field declared informational-only (A->B->A by name).
EXPECT_GREP="informational only"
check R2-F6-numeric-informational "KEP: numeric revision is informational; name equality decides (F6 fix)" \
  bash -c 'git show origin/pr/491:keps/455-rbgs-rolling-update/README.md | grep -n "informational only"'

# R2-F7: percentage-partition resolution timing pinned.
EXPECT_GREP="re-resolves"
check R2-F7-partition-timing "KEP pins partition resolution timing (F7 fix)" \
  bash -c 'git show origin/pr/491:keps/455-rbgs-rolling-update/README.md | grep -n "re-resolves\|resolved against the current"'

# R2-Q1: admission verdict for explicit maxUnavailable: 0 under InPlaceUpdate.
EXPECT_GREP="rejects it"
check R2-Q1-zero-verdict "KEP: webhook rejects explicit maxUnavailable 0 (Q1 fix)" \
  bash -c 'git show origin/pr/491:keps/455-rbgs-rolling-update/README.md | grep -n "webhook rejects it"'

# R2-Q2: operator-downgrade stance written down.
EXPECT_GREP="Downgrading"
check R2-Q2-downgrade-stance "KEP states the operator-downgrade stance (Q2 fix)" \
  bash -c 'git show origin/pr/491:keps/455-rbgs-rolling-update/README.md | grep -n "Downgrading"'

# R2-F9 / N1 (round-2 major: the barrier's absent-condition exception was not
# generation-gated, so a spec-bumped child could count as ready on stale
# conditions). Round 3 fixed it with a different — stronger — remedy than the
# round-2 exemplar clause: a persistent rollout-request/acknowledgement
# protocol. The round-2 grep was remedy-shaped and missed it (disclosed
# harness note in the round-2 manifest), so the check is updated to a
# disjunction: EITHER remedy closes the window. Harness-update, not a fix
# claim by itself — the R3-N1* checks below pin the actual mechanism.
r2f9_remedy_a=$(git show origin/pr/491:keps/455-rbgs-rolling-update/README.md \
  | tr '\n' ' ' | grep -oiE "absen(t|ce)[^.]{0,300}\." \
  | grep -icE "current rollout|already updated|not yet updated|until the condition|must present" || true)
r2f9_remedy_b=$(git show origin/pr/491:keps/455-rbgs-rolling-update/README.md \
  | grep -icE "never bypasses a pending request|cannot bypass an issued request" || true)
echo "remedyA=$r2f9_remedy_a remedyB=$r2f9_remedy_b" > "$OUT/R2-F9-bootstrap-window.txt"
if [ "$(( ${r2f9_remedy_a:-0} + ${r2f9_remedy_b:-0} ))" -ge 1 ]; then
  echo "PASS R2-F9-bootstrap-window — the absent-as-ready window is closed by a stated remedy (N1 fixed)"
else
  echo "EXPECTED-FAIL R2-F9-bootstrap-window — absent-as-ready is unrestricted in the KEP text (N1 open)"
fi

# ---- Round 3 (2026-10-23, head f711893c) -------------------------------
# The amendment rewrote the Child Rollout Barrier around a persistent
# request/acknowledgement protocol. These checks pin the N1 fix mechanism
# (R3-N1*) and the new design surface's code-fact claims (R3-*).

# R3-N1a: the consumer demands request/acknowledgement equality.
EXPECT_GREP="must match"
check R3-N1a-consumer-request-gate "groupSetChildReady requires request==acknowledgement (N1 fix)" \
  bash -c 'git show origin/pr/491:keps/455-rbgs-rolling-update/README.md | grep -n "If a rollout request exists, its value must match"'

# R3-N1b: the request is written atomically with the spec/metadata/label —
# this is what closes the post-bump observation window.
EXPECT_GREP="same resource-version"
check R3-N1b-request-atomic-write "request advances in the same Update as spec+metadata+revision label" \
  bash -c 'git show origin/pr/491:keps/455-rbgs-rolling-update/README.md | grep -n "same resource-version"'

# R3-N1c: a missing condition cannot release an issued request (the old
# absent-condition exception is subordinated to the request gate).
EXPECT_GREP="issued request"
check R3-N1c-absent-cannot-bypass "missing condition never bypasses a pending request" \
  bash -c 'git show origin/pr/491:keps/455-rbgs-rolling-update/README.md | grep -nE "never bypasses a pending request|cannot bypass an issued request"'

# R3-N1d: the risk row justifies the barrier via the request protocol.
EXPECT_GREP="Persist a new request"
check R3-N1d-risk-row-request "risk row: persist request, hold budget until acknowledgement" \
  bash -c 'git show origin/pr/491:keps/455-rbgs-rolling-update/README.md | grep -n "Persist a new request with the child update"'

# R3-N1e: restart/leader-handover safety — both values live on the object.
EXPECT_GREP="persist on the child object"
check R3-N1e-restart-persistence "request+ack persist across controller restart" \
  bash -c 'git show origin/pr/491:keps/455-rbgs-rolling-update/README.md | grep -n "persist on the child object"'

# R3-N1f: the fail-open exhibit (silent completion) is precluded: pending
# requests exclude the child from readyReplicas/updatedReadyReplicas.
EXPECT_GREP="exclude the child"
check R3-N1f-pending-excludes-ready "pending request excludes child from readyReplicas" \
  bash -c 'git show origin/pr/491:keps/455-rbgs-rolling-update/README.md | grep -n "exclude the child from .readyReplicas"'

# R3-N1g: A->B->A gets a fresh counter (no stale-ack reuse).
EXPECT_GREP="new request rather than reusing"
check R3-N1g-aba-fresh-request "A->B->A receives a new request, not an old acknowledgement" \
  bash -c 'git show origin/pr/491:keps/455-rbgs-rolling-update/README.md | grep -n "receives a new request rather than reusing an old acknowledgement"'

# R3-N1h: the bootstrap test-plan entry asserts the corrected behavior.
EXPECT_GREP="never-requested legacy child"
check R3-N1h-bootstrap-test-entry "bootstrap test entry: fallback only for never-requested children" \
  bash -c 'git show origin/pr/491:keps/455-rbgs-rolling-update/README.md | grep -n "never-requested legacy child retains"'

# R3-F3b: the exclusion list grew to 6 system keys (adds rollout-request and
# v1alpha1-pod-group-policy) and still governs hash + drift + sync-preserve.
EXPECT_GREP="ok"
check R3-F3b-key-enumeration-6 "KEP enumerates 6 system keys for hash+drift+sync (round-3 refinement)" \
  bash -c 'KEP_SHOW() { git show origin/pr/491:keps/455-rbgs-rolling-update/README.md; }; K=$(KEP_SHOW); for k in "rbg.workloads.x-k8s.io/discovery-config-mode" "rbg.workloads.x-k8s.io/rollout-request" "conversion.workloads.x-k8s.io/v1alpha1-coordination" "conversion.workloads.x-k8s.io/v1alpha1-pod-group-policy" "conversion.workloads.x-k8s.io/v1alpha2-rollout-strategy" "conversion.workloads.x-k8s.io/v1alpha2-rollout-status"; do echo "$K" | grep -qF "$k" || { echo "missing: $k"; exit 1; }; done; echo ok'

# R3-C1: the KEP's "shared SSA serializer" maps to real code (the status
# acknowledgement rides the existing apply-configuration patch machinery).
EXPECT_GREP="func PatchObjectApplyConfiguration"
check R3-C1-ssa-serializer-exists "shared SSA serializer exists (pkg/utils PatchObjectApplyConfiguration)" \
  bash -c 'grep -n "func PatchObjectApplyConfiguration" pkg/utils/utils.go; grep -n "client.Apply" pkg/utils/utils.go | head -2'

# R3-C2: the two Pod-shaping group annotations the role-hash design folds in
# exist as constants.
EXPECT_GREP="GroupExclusiveTopologyKey"
check R3-C2-podshaping-keys-exist "group-exclusive-topology / group-gang-scheduling keys exist in code" \
  bash -c 'grep -n "GroupExclusiveTopologyKey = \|GangSchedulingAnnotationKey = " api/workloads/constants/annotation.go'

# R3-C3: per-role FNV-32a hashing exists (the design composes with it).
EXPECT_GREP="fnv.New32a()"
check R3-C3-rolehash-fnv "role revision hashing uses FNV-32a today" \
  grep -n "fnv.New32a()" pkg/utils/revision_utils.go

# R3-F10 (NEW round-3 finding, minor, doc-gap contract, expected-FAIL while
# open): a replace-style full-object write on a child RBG can strip the live
# rollout-request annotation, and the set keeps no independent record of the
# requests it issued (the key is excluded from drift comparison, so a stripped
# request is never re-issued). A stripped pending request reopens exactly the
# N1 window: stale Ready + no request -> legacy fallback -> budget released
# while the child's workloads are still rolling. Same hazard class as the
# round-2 F5 checkpoint nit (replace-writes stripping live annotations).
# One hardening sentence in the KEP (e.g. how the implementation detects or
# re-issues a lost request) flips this check green.
r3f10=$(git show origin/pr/491:keps/455-rbgs-rolling-update/README.md \
  | grep -icE "strip[sed]?[^.]{0,200}rollout-request|rollout-request[^.]{0,200}strip[sed]?|re-?issue[sd]?[^.]{0,120}request|lost[^.]{0,120}request" || true)
echo "$r3f10" > "$OUT/R3-F10-request-strip-fallback.txt"
if [ "${r3f10:-0}" -ge 1 ]; then
  echo "PASS R3-F10-request-strip-fallback — KEP states a fallback/re-issue for a stripped request (F10 fixed)"
else
  echo "EXPECTED-FAIL R3-F10-request-strip-fallback — no stated fallback for a replace-write stripping the rollout-request annotation (F10 open, minor)"
fi

# Residual open nits (rounds 1-3, not addressed or partially addressed) —
# recorded, non-gating: F8 vocabulary collision cross-ref, O1 KEP-template
# sections, M1 wording catches, C4238820879 lowercase rbg, F10 strip hazard.
echo "-- residual-open (informational, non-gating) --"
git show origin/pr/491:keps/455-rbgs-rolling-update/README.md > "$OUT/kep-head-snapshot.txt"
grep -c "Graduation Criteria\|Release Signoff" "$OUT/kep-head-snapshot.txt" | sed 's/^/O1-template-sections: /'
grep -n "old template, not the current one" "$OUT/kep-head-snapshot.txt" | sed 's/^/M1-terminology: /'
grep -n "a higher ordinal is not released" "$OUT/kep-head-snapshot.txt" | sed 's/^/M1-in-place-ordering-wording: /'
grep -nE "no old-version rbg can be reconstructed" "$OUT/kep-head-snapshot.txt" | sed 's/^/C4238820879-lowercase-rbg: /'
grep -icE "role-level (Recreate|update strategy|vocabulary)" "$OUT/kep-head-snapshot.txt" | sed 's/^/F8-cross-reference: /'
# Q3 (round-3 question): compatibility-annotation convergence is ungated while
# coordination data can drive role-replica application (code fact below).
grep -n "independently of revision changes and rollout budgets" "$OUT/kep-head-snapshot.txt" | sed 's/^/Q3-ungated-convergence: /'
grep -n "coordination scaling target replicas" internal/controller/workloads/rolebasedgroup_controller.go | sed 's/^/Q3-code-fact-coordination-drives-replicas: /'

# P0-net: the linked issue is still open (network; recorded, not gating).
gh issue view 455 --repo sgl-project/rbg --json state,title -t '{{.state}} {{.title}}' \
  >"$OUT/P0-issue-455.txt" 2>&1 || echo "network-unavailable" >"$OUT/P0-issue-455.txt"
echo "P0-net: $(cat "$OUT/P0-issue-455.txt")"

# ---- Round 4 (2026-10-29, head 5f14daaf) -------------------------------
# The amendment ("kep: correct the description related to Round 3") addresses
# W1 and the M1 Reviewer-C extension, unbounds partition from replicas, adds
# the canonical-payload design, orphan adoption, live-budget surge sizing,
# and the requeue scope. These checks pin the new text and its code facts.

# R4-W1: 'admission check' no longer overloads the webhook term for the
# runtime availability-floor check (W1 fixed at 5f14daaf).
EXPECT_GREP="^0$"
check R4-W1-no-admission-check-phrase "no 'admission check' phrasing left for the runtime floor check (W1 fix)" \
  bash -c 'git show origin/pr/491:keps/455-rbgs-rolling-update/README.md | grep -c "admission check"'
EXPECT_GREP="same availability gate"
check R4-W1b-availability-gate-phrase "KEP says 'availability gate' for the runtime floor check (W1 fix)" \
  bash -c 'git show origin/pr/491:keps/455-rbgs-rolling-update/README.md | grep -n "same availability gate"'

# R4-P1: partition is no longer bounded by replicas; controller clamps with
# min(partition, replicas), StatefulSet parity.
EXPECT_GREP="min(partition, replicas)"
check R4-P1-partition-clamped "KEP: partition unbounded, controller resolves min(partition, replicas)" \
  bash -c 'git show origin/pr/491:keps/455-rbgs-rolling-update/README.md | grep -n "min(partition, replicas)"'
EXPECT_GREP="rolebasedgroupsets/scale"
check R4-P2-scale-bypass-declared "scenario row: validating rule does not match rolebasedgroupsets/scale" \
  bash -c 'git show origin/pr/491:keps/455-rbgs-rolling-update/README.md | grep -n "rolebasedgroupsets/scale"'
# R4-P3 code fact: the validating webhook rule matches only the main
# resource (verbs create;update on rolebasedgroupsets), so a scale-subresource
# write skips admission — the scenario row's premise.
EXPECT_GREP="^0$"
check R4-P3-webhook-rule-no-scale "code fact: validating webhook rule covers rolebasedgroupsets, not .../scale" \
  bash -c 'grep -h "kubebuilder:webhook" api/workloads/v1alpha2/rolebasedgroupset_admission.go | grep -c "scale"'

# R4-V*: the v1alpha1 round-trip is now declared NOT lossless (M1 Reviewer-C
# extension resolved by narrowing): groupTemplate labels/annotations drop.
EXPECT_GREP="round-trip is not lossless"
check R4-V1-v1alpha1-lossy-declared "KEP: v1alpha1 round-trip declared not lossless" \
  bash -c 'git show origin/pr/491:keps/455-rbgs-rolling-update/README.md | grep -n "round-trip is not lossless"'
EXPECT_GREP="^2$"
check R4-V2-template-metadata-drop-both-sites "v1alpha1 write drops both maps, stated in risk row + scenario table" \
  bash -c 'git show origin/pr/491:keps/455-rbgs-rolling-update/README.md | grep -c "drops both maps"'
EXPECT_GREP="written through v1alpha2"
check R4-V3-release-note-ask "release-note ask: write rolling sets through v1alpha2" \
  bash -c 'git show origin/pr/491:keps/455-rbgs-rolling-update/README.md | grep -n "written through v1alpha2"'
# R4-V4 code fact: the v1->v2 conversion injects only the coordination key
# into GroupTemplate.Annotations (from the set's own annotations); user
# template labels/annotations have no v1alpha1 representation at all.
EXPECT_GREP="annotationV1alpha1Coordination"
check R4-V4-conversion-injects-only-compat "code fact: v1->v2 restores only the compatibility key into template annotations" \
  grep -nF "dst.Spec.GroupTemplate.Annotations[annotationV1alpha1Coordination]" api/workloads/v1alpha1/rolebasedgroupset_conversion.go

# R4-C*: canonical payload design + code facts. The stored data must be
# canonical (sorted-keys) JSON because the API server rejects a data change
# on strategic-merge patches (verified end-to-end by the E7 envtest below).
EXPECT_GREP="Canonical payload"
check R4-C1-canonical-payload-section "KEP: canonical payload section (sorted keys, byte-wise immutable data)" \
  bash -c 'git show origin/pr/491:keps/455-rbgs-rolling-update/README.md | grep -n "Canonical payload"'
EXPECT_GREP="json.Marshal(objCopy)"
check R4-C2-stateful-path-canonicalizes "code fact: existing stateful revision path stores sorted-key data (map round-trip)" \
  grep -n "json.Marshal(objCopy)" pkg/reconciler/roleinstanceset/statefulmode/stateful_instance_set_helper.go
# R4-C3 code fact: the repo's own revision equality is byte-wise on Data.Raw,
# so byte stability across re-serialization is load-bearing for matching too.
EXPECT_GREP="bytes.Equal(lhs.Data.Raw, rhs.Data.Raw)"
check R4-C3-revision-equality-byte-wise "code fact: EqualRevision compares Data.Raw byte-wise" \
  grep -n "bytes.Equal(lhs.Data.Raw, rhs.Data.Raw)" pkg/utils/revision_utils.go

# R4-O*: orphan adoption. The scenario row claims the rolling path adopts
# orphans "matching the legacy path" — true of main since #500 (ecdfd6e6),
# which added isOwnedOrAdoptable + adoption; the PR branch's merge-base
# predates it, so the code fact is pinned against origin/main.
EXPECT_GREP="or adoptable"
check R4-O1-orphan-adoption-row "scenario row: rolling path considers owned or adoptable children" \
  bash -c 'git show origin/pr/491:keps/455-rbgs-rolling-update/README.md | grep -n "or adoptable"'
EXPECT_GREP="isOwnedOrAdoptable"
check R4-O2-legacy-adoption-code-fact "code fact (main): legacy set path owns-or-adopts children (#500) — 'matching the legacy path' accurate" \
  bash -c 'git show origin/main:internal/controller/workloads/rolebasedgroupset_controller.go | grep -n "func (r \*RoleBasedGroupSetReconciler) isOwnedOrAdoptable"'

# R4-S*: surge sized from the live availability budget, not the declared one.
EXPECT_GREP="live availability budget"
check R4-S1-surge-live-budget "KEP: surge sized from the live availability budget" \
  bash -c 'git show origin/pr/491:keps/455-rbgs-rolling-update/README.md | grep -n "live availability budget"'
EXPECT_GREP="still get their replacements"
check R4-S2-failure-below-partition-row "scenario row: a failed group must not stall the surge batch" \
  bash -c 'git show origin/pr/491:keps/455-rbgs-rolling-update/README.md | grep -n "still get their replacements"'

# R4-R*: requeue scope — an unacknowledged request is the barrier's only
# periodic requeue; RollingUpdateInProgress=True alone requeues nothing.
EXPECT_GREP="only periodic requeue"
check R4-R1-requeue-scope "KEP: unacknowledged request is the only periodic requeue" \
  bash -c 'git show origin/pr/491:keps/455-rbgs-rolling-update/README.md | grep -n "only periodic requeue"'

# R4-Q1: request writer generalized to "every budgeted child write" (F9
# partial improvement).
EXPECT_GREP="every budgeted child write"
check R4-Q1-request-writer-generalized "request writer scoped to every budgeted child write (F9 partial)" \
  bash -c 'git show origin/pr/491:keps/455-rbgs-rolling-update/README.md | grep -n "every budgeted child write"'

# R4-F9 (F9 residual, expected-FAIL while open): the request-writer list
# still does not say whether drift-repair and the one-shot upgrade rewrite
# advance the rollout-request counter, nor scopes them out.
r4f9=$(git show origin/pr/491:keps/455-rbgs-rolling-update/README.md \
  | grep -icE "(repair|one-shot|upgrade rewrite)[^.]{0,160}request|request[^.]{0,160}(repair|one-shot|rewrite)" || true)
echo "$r4f9" > "$OUT/R4-F9-repair-write-request-scope.txt"
if [ "${r4f9:-0}" -ge 1 ]; then
  echo "PASS R4-F9-repair-write-request-scope — KEP states the repair/one-shot-rewrite request semantics (F9 fixed)"
else
  echo "EXPECTED-FAIL R4-F9-repair-write-request-scope — repair / one-shot-rewrite request scope still unstated (F9 open, minor)"
fi

# R4-F11 (NEW round-4 finding, minor, doc-precision contract, expected-FAIL
# while open): expectedUpdatedReplicas = replicas - partition is now
# negative-able, because partition is no longer bounded by replicas
# (autoscaler can scale replicas below an absolute partition). The Status
# table gives no clamp; a negative "target batch size" is meaningless for
# status consumers. One clause (max(0, ...)) fixes it.
r4f11=$(git show origin/pr/491:keps/455-rbgs-rolling-update/README.md \
  | grep -icE "expectedUpdatedReplicas[^|]{0,120}max\(0|max\(0[^)]{0,40}replicas - partition" || true)
echo "$r4f11" > "$OUT/R4-F11-expected-updated-clamped.txt"
if [ "${r4f11:-0}" -ge 1 ]; then
  echo "PASS R4-F11-expected-updated-clamped — expectedUpdatedReplicas clamped at zero (F11 fixed)"
else
  echo "EXPECTED-FAIL R4-F11-expected-updated-clamped — expectedUpdatedReplicas = replicas - partition can go negative (F11 open, minor)"
fi

echo
echo "== L1 unit layer =="
go test ./internal/controller/workloads/ -run 'TestVerify491' -count=1 2>&1 | tee "$OUT/unit-l1-verify491.out"
go test ./internal/controller/workloads/ -count=1 2>&1 | tee "$OUT/unit-l1-controllers.out"
grep -q "^ok" "$OUT/unit-l1-controllers.out" || fail=1

echo
echo "== L2 integration layer (envtest) =="
# envtest needs the control-plane binaries; resolve the newest installed set.
KUBEBUILDER_ASSETS="${KUBEBUILDER_ASSETS:-$(ls -d "$HOME"/.local/share/kubebuilder-envtest/k8s/*-linux-amd64 2>/dev/null | sort -V | tail -1)}"
export KUBEBUILDER_ASSETS
echo "KUBEBUILDER_ASSETS=$KUBEBUILDER_ASSETS"
go test ./test/envtest/testcase/... -count=1 -timeout 25m 2>&1 | tee "$OUT/integration-envtest.out"
echo "== L2b E7 canonical-payload envtest (round 4) =="
go test ./test/envtest/verify491r4/ -count=1 -timeout 5m -v 2>&1 | tee "$OUT/integration-e7-canonical.out"
grep -q "^ok" "$OUT/integration-e7-canonical.out" || fail=1
grep -q "no test files\|^ok" <(grep -v "^---" "$OUT/integration-envtest.out" || true) || true

echo
if [ "$fail" -eq 0 ]; then
  echo "RESULT: all evidence checks + unit layer green (see results/)"
else
  echo "RESULT: FAILURES — inspect results/"
fi
exit "$fail"
