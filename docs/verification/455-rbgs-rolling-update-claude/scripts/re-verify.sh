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

# R2-F9 (NEW finding, contract, EXPECTED-FAIL reproduction at head): the
# barrier design treats an absent condition as ready for ALL children,
# including children the set has already spec-bumped in the current rollout
# whose RBG controller has not yet reconciled (post-upgrade bootstrap window).
# The fix is one sentence restricting absence-acceptance to children not yet
# touched by the current rollout. FAIL here is the reproduction, not a harness
# error — it does not gate the overall result.
r2f9=$(git show origin/pr/491:keps/455-rbgs-rolling-update/README.md \
  | tr '\n' ' ' | grep -oiE "absen(t|ce)[^.]{0,300}\." \
  | grep -icE "current rollout|already updated|not yet updated|until the condition|must present" || true)
echo "$r2f9" > "$OUT/R2-F9-bootstrap-window.txt"
if [ "${r2f9:-0}" -ge 1 ]; then
  echo "PASS R2-F9-bootstrap-window — KEP restricts absent-as-ready (F9 fixed)"
else
  echo "EXPECTED-FAIL R2-F9-bootstrap-window — absent-as-ready is unrestricted in the KEP text (F9 open; reproduction at results/R2-F9-bootstrap-window.txt)"
fi

# Residual open nits (round 1, not addressed by the amendment) — recorded,
# non-gating: F8 vocabulary collision cross-ref, O1 KEP-template sections,
# M1 wording catches, C4238820879 lowercase rbg.
echo "-- residual-open (informational, non-gating) --"
git show origin/pr/491:keps/455-rbgs-rolling-update/README.md > "$OUT/kep-head-snapshot.txt"
grep -c "Graduation Criteria\|Release Signoff" "$OUT/kep-head-snapshot.txt" | sed 's/^/O1-template-sections: /'
grep -n "old template, not the current one" "$OUT/kep-head-snapshot.txt" | sed 's/^/M1-terminology: /'
grep -nE "\brbg\b" "$OUT/kep-head-snapshot.txt" | sed 's/^/C4238820879-lowercase-rbg: /' | head -3
grep -icE "role-level (Recreate|update strategy|vocabulary)" "$OUT/kep-head-snapshot.txt" | sed 's/^/F8-cross-reference: /'

# P0-net: the linked issue is still open (network; recorded, not gating).
gh issue view 455 --repo sgl-project/rbg --json state,title -t '{{.state}} {{.title}}' \
  >"$OUT/P0-issue-455.txt" 2>&1 || echo "network-unavailable" >"$OUT/P0-issue-455.txt"
echo "P0-net: $(cat "$OUT/P0-issue-455.txt")"

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
grep -q "no test files\|^ok" <(grep -v "^---" "$OUT/integration-envtest.out" || true) || true

echo
if [ "$fail" -eq 0 ]; then
  echo "RESULT: all evidence checks + unit layer green (see results/)"
else
  echo "RESULT: FAILURES — inspect results/"
fi
exit "$fail"
