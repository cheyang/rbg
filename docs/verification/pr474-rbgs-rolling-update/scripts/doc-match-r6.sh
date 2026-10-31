#!/usr/bin/env bash
# Round 6 doc-match check: the PR body was rewritten again (fixes #519/#521,
# round-6 labels-only completion, orphan revision history). Verify every
# checkable claim the new body makes against the head tree (af9dd84b).
# Usage: bash scripts/doc-match-r6.sh <pr-body-file> <repo-rev>
set -u
BODY="$1"
REV="${2:-HEAD}"
REPO="$(git rev-parse --show-toplevel 2>/dev/null || echo .)"
fail=0
check() { # check <ok> <label> <detail>
  if [ "$1" = "0" ]; then echo "PASS  $2 — $3"; else echo "FAIL  $2 — $3"; fail=1; fi
}

TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
git -C "$REPO" show "$REV":api/workloads/v1alpha2/rolebasedgroupset_types.go > "$TMP/types.go" 2>/dev/null
git -C "$REPO" show "$REV":internal/controller/workloads/rolebasedgroupset_rolling.go > "$TMP/rolling.go" 2>/dev/null
git -C "$REPO" show "$REV":internal/controller/workloads/rolebasedgroupset_revision.go > "$TMP/revision.go" 2>/dev/null
git -C "$REPO" show "$REV":internal/controller/workloads/rolebasedgroup_controller.go > "$TMP/rbgctl.go" 2>/dev/null
git -C "$REPO" show "$REV":api/workloads/v1alpha2/rolebasedgroupset_admission.go > "$TMP/admission.go" 2>/dev/null
git -C "$REPO" show "$REV":api/workloads/constants/label.go > "$TMP/label.go" 2>/dev/null
git -C "$REPO" show "$REV":pkg/utils/revision_utils.go > "$TMP/revutils.go" 2>/dev/null

# --- API surface -------------------------------------------------------------
grep -q 'Recreate;InPlaceUpdate' "$TMP/types.go"; check $? "enum {Recreate, InPlaceUpdate}" "types.go"
grep -qi 'default=InPlaceUpdate' "$TMP/types.go"; check $? "kubebuilder default InPlaceUpdate" "types.go"
grep -qiE 'Paused' "$TMP/types.go"; [ $? -ne 0 ]; check $? "no Paused field/condition in types" "types.go"
grep -q 'There is no `paused` field or condition' "$BODY"; check $? "body states: no paused field/condition" "body"

for f in CurrentReplicas UpdatedReplicas UpdatedReadyReplicas ExpectedUpdatedReplicas CurrentRevision UpdateRevision; do
  grep -q "	$f " "$TMP/types.go"; check $? "status field $f exists" "types.go"
done
git -C "$REPO" show "$REV":api/workloads/v1alpha2/rolebasedgroup_types.go 2>/dev/null | grep -q 'ObservedRolloutRequest'
check $? "status.observedRolloutRequest (v1alpha2)" "rolebasedgroup_types.go v1alpha2"
git -C "$REPO" show "$REV":api/workloads/v1alpha1/rolebasedgroup_types.go 2>/dev/null | grep -q 'ObservedRolloutRequest'
check $? "status.observedRolloutRequest (v1alpha1)" "rolebasedgroup_types.go v1alpha1"

# --- admission (#521) --------------------------------------------------------
grep -q 'must be between 0 and replicas' "$TMP/admission.go"; [ $? -ne 0 ]
check $? "admission no longer bounds partition by replicas" "admission.go"
grep -q 'An integer may exceed `replicas`' "$BODY"; check $? "body: integer partition may exceed replicas" "body"
grep -q 'limits.partition = min(limits.partition, replicas)' "$TMP/rolling.go"
check $? "controller clamps the effective partition" "rolling.go"
grep -q 'Scaling below a stored partition therefore does not block subsequent object updates' "$BODY"
check $? "body documents the #521 fix" "body"

# --- budget resolution -------------------------------------------------------
grep -q 'resolve(strategy.MaxUnavailable, 1, false)' "$TMP/rolling.go"
check $? "maxUnavailable defaults to 1" "rolling.go"
grep -q 'limits.maxSurge == 0 && limits.maxUnavailable == 0' "$TMP/rolling.go"
check $? "both-zero fallback to minimum unavailable budget" "rolling.go"
grep -q 'if !limits.inPlaceUpdate {' "$TMP/rolling.go"
check $? "surge groups only under Recreate" "rolling.go"

# --- revisions ---------------------------------------------------------------
grep -q 'rbgs-%x' "$TMP/revision.go"; check $? "ControllerRevision named rbgs-<sha256[:24]>" "revision.go"
grep -q 'canonicalGroupSetTemplateJSON' "$TMP/revision.go"
check $? "revision data stored as canonical JSON" "revision.go"
grep -q 'groupset-revision' "$TMP/label.go"; check $? "groupset-revision label key" "constants"
grep -q 'DefaultRevisionHistoryLimit = 5' "$TMP/revutils.go"
check $? "DefaultRevisionHistoryLimit is 5 (body: 'currently 5')" "revision_utils.go"

# --- orphan adoption ---------------------------------------------------------
grep -q 'adoptGroupSetOrphans' "$TMP/rolling.go"; check $? "rolling path adopts orphan children" "rolling.go"
grep -q 'SetControllerReference(set, revision, r.scheme)' "$TMP/revision.go"
check $? "orphan ControllerRevisions adopted via owner-ref patch" "revision.go"
grep -q 'MergeFromWithOptimisticLock' "$TMP/revision.go"
check $? "owner-reference patches use optimistic locking (body claim)" "revision.go"
grep -q 'client.Preconditions{UID: &revision.UID, ResourceVersion: &revision.ResourceVersion}' "$TMP/revision.go"
check $? "pruning uses UID/resourceVersion delete preconditions (body claim)" "revision.go"
grep -q 'fresh.UID != set.UID' "$TMP/revision.go"
check $? "live parent UID/deletion check before adoption (body claim)" "revision.go"
grep -q 'CreationTimestamp.Before' "$TMP/revision.go"
check $? "creation time is the primary retention ordering (body claim)" "revision.go"

# --- surge sizing (#519) -----------------------------------------------------
grep -q 'floor := max(0, replicas-limits.maxUnavailable)' "$TMP/rolling.go"
check $? "body formula: floor = max(0, replicas - maxUnavailable)" "rolling.go"
grep -q 'headroom := max(0, accounting.readyBase-floor)' "$TMP/rolling.go"
check $? "body formula: headroom = max(0, readyBase - floor)" "rolling.go"
grep -q 'needed := min(limits.maxSurge, max(0, accounting.servingOutdated-headroom))' "$TMP/rolling.go"
check $? "body formula: surge = min(maxSurge, max(0, servingOutdated - headroom))" "rolling.go"
grep -q 'is sized from \*\*live availability\*\*' "$BODY"
check $? "body documents live-budget surge sizing (#519)" "body"

# --- rollout pacing ----------------------------------------------------------
grep -q 'left > right' "$TMP/rolling.go"; check $? "outdated children processed in descending ordinal order" "rolling.go"
grep -q 'DeletePropagationForeground' "$TMP/rolling.go"
check $? "Recreate uses foreground deletion with preconditions" "rolling.go"
grep -q 'groupSetOnlyReplicasChanged' "$TMP/rolling.go"
check $? "replica-only changes stay in place under either strategy" "rolling.go"
grep -q 'requestGroupSetChildRollout' "$TMP/rolling.go"
check $? "budgeted writes carry the rollout-request token" "rolling.go"

# --- completion barrier ------------------------------------------------------
grep -q 'child.Status.ObservedRolloutRequest != request' "$TMP/rolling.go"
check $? "groupSetChildReady requires the request echo" "rolling.go"
grep -q 'rolling.ObservedGeneration >= child.Generation' "$TMP/rolling.go"
check $? "groupSetChildReady requires a False rollout condition caught up" "rolling.go"
git -C "$REPO" show "$REV":internal/controller/workloads/rolebasedgroup_rollout_status.go 2>/dev/null | grep -q 'func (r \*RoleBasedGroupReconciler) finalizeRolloutStatus' 
check $? "finalizeRolloutStatus exists (body: completion barrier)" "rbgctl.go"

# --- requeue scope -----------------------------------------------------------
grep -q 'if request != "" && request != rbg.Status.ObservedRolloutRequest' "$TMP/rbgctl.go"
check $? "RBG requeues only while a request is unacknowledged (body claim)" "rbgctl.go"
grep -q 'if changed || adopted {' "$TMP/rolling.go"
check $? "set requeues only after changing/adopting children (body claim)" "rolling.go"
grep -q 'incompatibleGangConfigRequeue = 5 \* time.Minute' "$TMP/rbgctl.go"
check $? "5-minute incompatible-gang recovery path retained (body claim)" "rbgctl.go"

# --- group annotations / upgrade note (F16) ----------------------------------
grep -q 'func AddGroupAnnotationsToRoleHashes' "$TMP/revutils.go"
check $? "AddGroupAnnotationsToRoleHashes folds group annotations into role hashes" "revision_utils.go"
grep -q 'does not imply Pod replacement' "$BODY"
check $? "body states a label refresh alone does not replace Pods (F16)" "body"

# --- body-listed test names must exist if any --------------------------------
missing=0
for t in $(grep -oE 'Test[A-Za-z0-9_]+' "$BODY" | sort -u); do
  git -C "$REPO" grep -l "func ${t}(" "$REV" >/dev/null 2>&1 || { echo "      missing test: $t"; missing=1; }
done
check "$missing" "all body-listed test names exist (round-6 body lists none)" "grep at rev"

[ "$fail" = "0" ] && echo "DOC-MATCH: body matches shipped API" || echo "DOC-MATCH: body still diverges"
exit "$fail"
