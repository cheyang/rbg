#!/usr/bin/env bash
# Verify the KEP-543 doc's "existing behavior" claims against the real code.
# Usage: check_code_refs.sh <repo-root> [ref]   (default: HEAD)
# Each claim names a symbol/construct the KEP says exists today; the check
# greps the tree at <ref>. Polarity: contract — PASS means the doc's reuse
# claims are grounded; FAIL means the doc cites something that does not exist.
set -u
ROOT="${1:-$(pwd)}"
REF="${2:-HEAD}"

check() { # name, pattern, fileglob
  local name="$1" pat="$2" glob="$3"
  local hits
  hits=$(git -C "$ROOT" grep -l -E "$pat" "$REF" -- "$glob" 2>/dev/null | grep -v _test | head -1)
  if [ -n "$hits" ]; then
    echo "PASS $name ($hits)"
  else
    echo "FAIL $name: pattern '$pat' not found under $glob at $REF"
  fi
}

check "applyRBGSAReplicasOverride (RBG in-memory override path)" \
  "func \(r \*RoleBasedGroupReconciler\) applyRBGSAReplicasOverride" \
  "internal/controller/workloads/*.go"
check "handleCoordinationStrategies (RBG parameter calculation)" \
  "func \(r \*RoleBasedGroupReconciler\) handleCoordinationStrategies" \
  "internal/controller/workloads/*.go"
check "updateRoleReplicas (Adapter persistent write path)" \
  "func \(r \*RoleBasedGroupScalingAdapterReconciler\) updateRoleReplicas" \
  "internal/controller/workloads/*.go"
check "ScalingAdapter type in RBG API (enable/labels reuse claim)" \
  "type ScalingAdapter struct" \
  "api/workloads/v1alpha2/*.go"
check "AdapterScaleTargetRef with required Role today" \
  "type AdapterScaleTargetRef struct" \
  "api/workloads/v1alpha2/*.go"
check "GangSchedulingStrategy.MinReplicas (map, per-role minimums)" \
  "MinReplicas map\[string\]int32" \
  "api/workloads/v1alpha2/*.go"
check "Policy rollingUpdate maxSkew/partition/maxUnavailable exist" \
  "type RollingUpdateCoordinationStrategy struct" \
  "api/workloads/v1alpha2/*.go"
check "Progression default OrderScheduled" \
  "kubebuilder:default=OrderScheduled" \
  "api/workloads/v1alpha2/*.go"
check "Volcano subGroupPolicy support probe (CRD capability check)" \
  "supportsSubGroupPolicy" \
  "pkg/scheduler/volcano/*.go"
check "subGroup per-RoleInstance + matchLabelKeys=role-instance-name today" \
  "MatchLabelKeys: \[\]string\{constants.RoleInstanceNameLabelKey\}" \
  "pkg/scheduler/volcano/*.go"
check "raiseScalingTargetsToGangMinimum (non-Segment target raising)" \
  "raiseScalingTargetsToGangMinimum" \
  "internal/controller/workloads/*.go"
check "committed-skip-ordinals annotation key (dense-membership prerequisite)" \
  "committed-skip-ordinals" \
  "api/workloads/constants/*.go"
check "group-name label key (Segment selector reuse)" \
  "group-name" \
  "api/workloads/constants/*.go"
check "RIS surge formula: activeSurge = min(maxSurge, max(healthyOld-maxUnavailable,0))" \
  "t.activeSurge = min\(active, maxSurge\)" \
  "pkg/reconciler/roleinstanceset/statefulmode/*.go"
check "RIS execution budget: maxUnavailable + availableSurge" \
  "effectiveBudget = maxUnavailable \+ availableSurge" \
  "pkg/reconciler/roleinstanceset/statefulmode/*.go"
check "discovery ConfigMap built by RBG controller from spec roles" \
  "discovery.NewConfigBuilder" \
  "internal/controller/workloads/*.go"
