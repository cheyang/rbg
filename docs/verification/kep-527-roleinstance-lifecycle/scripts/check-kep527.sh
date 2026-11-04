#!/usr/bin/env bash
# check-kep527.sh — claims/hygiene checks for the KEP-527 design-doc review (PR #546).
#
# The PR adds a documentation-only KEP (keps/527-roleinstance-lifecycle/). There is
# no executable production change, so the "unit layer" of this harness is a set of
# codebase-truth assertions: each check verifies that a claim the KEP makes about the
# EXISTING system is still true on the ref under test (grep-level), and each finding
# check asserts the KEP text has addressed the review finding. This is the docs-PR
# analogue of a unit test: it makes the review's evidence re-runnable.
#
# Output: one "<Name>\tpass|fail|skip" line per check on stdout.
# Contract polarity throughout: pass = the intended/correct state.
#
# Run from the repo root of the ref under test:  bash scripts/check-kep527.sh
set -uo pipefail

KEP_EN="keps/527-roleinstance-lifecycle/README.md"
KEP_ZH="keps/527-roleinstance-lifecycle/README.zh-CN.md"
KEP_YAML="keps/527-roleinstance-lifecycle/kep.yaml"

ok()   { printf '%s\tpass\n' "$1"; }
bad()  { printf '%s\tfail\n' "$1"; }
skip() { printf '%s\tskip\n' "$1"; }

# --- group 1: KEP claims about the EXISTING system (must stay true; a fail here
# --- means the codebase moved and the KEP/review basis needs a Harness-update) ---

# 1. RoleInstanceCustomReady condition exists (KEP "Ready Gating" reuses it).
if grep -q 'RoleInstanceCustomReady RoleInstanceConditionType = "RoleInstanceCustomReady"' \
    api/workloads/v1alpha2/roleinstance_types.go 2>/dev/null; then
  ok CheckCustomReadyConditionExists; else bad CheckCustomReadyConditionExists; fi

# 2. The readiness helper returns early when the readiness gate is not registered
#    (KEP: "otherwise, the existing readiness helper returns immediately").
if grep -q 'if !containsReadinessGate(instance, condType)' \
    pkg/inplace/instance/readiness/instance_readiness_utils.go 2>/dev/null; then
  ok CheckReadinessGateEarlyReturn; else bad CheckReadinessGateEarlyReturn; fi

# 3. RI spec carries readinessGates (KEP prerequisite for markNotReady).
if grep -q 'ReadinessGates \[\]RoleInstanceReadinessGate' \
    api/workloads/v1alpha2/roleinstance_types.go 2>/dev/null; then
  ok CheckRIReadinessGatesField; else bad CheckRIReadinessGatesField; fi

# 4. Existing user-facing RIS lifecycle with preDelete/inPlaceUpdate + labelsHandler
#    exists (basis of the old/new protocol coexistence finding F3).
if grep -q 'type RoleInstanceSetLifecycle struct' api/workloads/v1alpha2/roleinstanceset_types.go 2>/dev/null \
  && grep -q 'LabelsHandler     map\[string\]string' api/workloads/v1alpha2/roleinstanceset_types.go 2>/dev/null \
  && grep -q 'PreDelete \*RoleInstanceSetLifecycleHook' api/workloads/v1alpha2/roleinstanceset_types.go 2>/dev/null \
  && grep -q 'InPlaceUpdate \*RoleInstanceSetLifecycleHook' api/workloads/v1alpha2/roleinstanceset_types.go 2>/dev/null; then
  ok CheckRISLifecycleCoexists; else bad CheckRISLifecycleCoexists; fi

# 5. Old model is executor-registration based: controller waits while hook
#    labels/finalizers are present on the RI (KEP "Relationship to Existing
#    Mechanisms" description).
if grep -q 'func IsRoleInstanceHooked(' pkg/inplace/instance/lifecycle/lifecycle_utils.go 2>/dev/null \
  && grep -q 'func IsRoleInstanceAllHooked(' pkg/inplace/instance/lifecycle/lifecycle_utils.go 2>/dev/null; then
  ok CheckExecutorRegistrationModel; else bad CheckExecutorRegistrationModel; fi

# 6. Member Pods carry a controller ownerReference to the RI, so default GC
#    cascade-deletes Pods when the RI is deleted (basis of the preDelete finding F1).
if grep -q 'metav1.NewControllerRef(instance,' \
    pkg/reconciler/roleinstance/core/instance_core.go 2>/dev/null; then
  ok CheckPodOwnedByRI; else bad CheckPodOwnedByRI; fi

# 7. The RI controller owns no finalizer on the RI today (KEP must introduce one).
if ! grep -rqi 'finalizer' pkg/reconciler/roleinstance/instance_reconciler.go 2>/dev/null; then
  ok CheckRINoFinalizerToday; else bad CheckRINoFinalizerToday; fi

# 8. RI status has no phase field today (basis of the phase-persistence finding F2).
if ! grep -A40 'type RoleInstanceStatus struct' api/workloads/v1alpha2/roleinstance_types.go 2>/dev/null \
    | grep -q 'Phase'; then
  ok CheckRINoPhaseField; else bad CheckRINoPhaseField; fi

# 9. Admission webhook infrastructure exists (KEP relies on admission rejection of
#    empty handlers and illegal marker keys).
if [ -f api/workloads/v1alpha2/rolebasedgroup_webhook.go ]; then
  ok CheckAdmissionWebhookInfra; else bad CheckAdmissionWebhookInfra; fi

# 10. API group/version in the KEP example matches the codebase.
if grep -q 'Group: "workloads.x-k8s.io", Version: "v1alpha2"' \
    api/workloads/v1alpha2/groupversion_info.go 2>/dev/null; then
  ok CheckAPIGroupVersion; else bad CheckAPIGroupVersion; fi

# --- group 2: review findings against the KEP document itself
# --- (fail = finding reproduced on the ref under test; pass = addressed) ---

# F4a. kep.yaml must not carry the unrelated "Segment" disablement comment.
if [ -f "$KEP_YAML" ] && ! grep -qi 'segment' "$KEP_YAML"; then
  ok CheckKepYamlNoSegmentComment; else bad CheckKepYamlNoSegmentComment; fi

# F4b. kep.yaml authors must be filled in (not the TBD placeholder).
if [ -f "$KEP_YAML" ] && ! grep -q -- '- TBD' "$KEP_YAML"; then
  ok CheckKepYamlAuthorsSet; else bad CheckKepYamlAuthorsSet; fi

# F4c. every see-also entry must resolve to an existing keps/<dir>.
CheckSeeAlsoDirsExist() {
  [ -f "$KEP_YAML" ] || return 1
  local missing=0 p
  while IFS= read -r p; do
    p="${p#/}"; [ -z "$p" ] && continue
    [ -d "$p" ] || missing=1
  done < <(sed -n '/^see-also:/,/^[a-z-]*:/p' "$KEP_YAML" | grep -oE '/keps/[a-z0-9-]+' | sort -u)
  return $missing
}
if CheckSeeAlsoDirsExist; then ok CheckSeeAlsoDirsExist; else bad CheckSeeAlsoDirsExist; fi

# F1. The deletion design must specify how member Pods are protected from GC
#     cascade deletion (orphan/ownerRef handling or controller-ordered deletion),
#     not only a Pod finalizer. Heuristic proxy: the deletion section must mention
#     orphaning, ownerReference removal, or cascade prevention.
if [ -f "$KEP_EN" ] && sed -n '/### Deletion Interaction Flow/,/### Restart Recovery/p' "$KEP_EN" \
    | grep -qiE 'orphan|ownerreference|cascade'; then
  ok CheckDeletionCascadeSpecified; else bad CheckDeletionCascadeSpecified; fi

# F2. The KEP must state where the top-level phase is persisted (label or status
#     field); restart recovery claims to reconstruct progress from it.
if [ -f "$KEP_EN" ] && grep -qiE 'phase (is )?(stored|persisted|recorded) (in|as|on)|phase (label|field)|status\.phase|phase.*status field' "$KEP_EN"; then
  ok CheckPhaseStorageSpecified; else bad CheckPhaseStorageSpecified; fi

# F3. The KEP must state a concrete coexistence rule (deprecation, mutual
#     exclusion, or precedence) between the existing RIS spec.lifecycle and the
#     proposed Role-level lifecycle.
if [ -f "$KEP_EN" ] && grep -qiE 'mutual(ly)? exclusive|mutual exclusion|deprecat|takes precedence|must not (both|be combined)|coexist' "$KEP_EN"; then
  ok CheckOldNewCoexistenceRule; else bad CheckOldNewCoexistenceRule; fi

# F5. Ready Gating must define the lifetime of a preDelete NotReady gate, not only
#     the Update/Recovery one.
if [ -f "$KEP_EN" ] && sed -n '/### Ready Gating/,/### Hook Executor Service/p' "$KEP_EN" \
    | grep -qiE 'preDelete.*(gate|NotReady).*(until|remain|lift|delet)|gate.*preDelete.*(until|remain|lift)'; then
  ok CheckPreDeleteGateLifetime; else bad CheckPreDeleteGateLifetime; fi


# F6 (F-SCOPE-PRECREATE). The KEP must state the rationale for dropping preCreate
#     (and postDelete) versus issue #527's five-hook scope, in Motivation/Non-Goals
#     (not only an unjustified Drawbacks admission).
if [ -f "$KEP_EN" ] && sed -n '/^## Motivation/,/^## Proposal/p' "$KEP_EN" \
    | grep -qiE 'pre-?create'; then
  ok CheckPreCreateRationaleStated; else bad CheckPreCreateRationaleStated; fi

# F7 (F-ZH-RIS-RI-PARITY, nit). The zh-CN coexistence sentence must scope the
#     mutual-exclusion rule to the RIS, matching the English text ("same RIS").
if [ -f "$KEP_ZH" ] && ! grep -q '在同一 RI 上同时生效' "$KEP_ZH"; then
  ok CheckEnZhCoexistenceScopeParity; else bad CheckEnZhCoexistenceScopeParity; fi

# F8 (F-INFLIGHT-ADMISSION-MUST, nit). The in-flight lifecycle-change admission
#     rule must use normative "must" like the sibling admission rules and the
#     test plan, not "should".
if [ -f "$KEP_EN" ] && grep -q 'Admission must reject lifecycle changes' "$KEP_EN"; then
  ok CheckInflightAdmissionMust; else bad CheckInflightAdmissionMust; fi

# --- group 3: document hygiene (regression guards for future revisions) ---

# H1. markdownlint clean on both docs (the PR's own verification claim).
if command -v npx >/dev/null 2>&1; then
  if npx --yes markdownlint-cli2 "$KEP_EN" "$KEP_ZH" 2>&1 | grep -q 'Summary: 0 issues'; then
    ok CheckMarkdownlint; else bad CheckMarkdownlint; fi
else
  skip CheckMarkdownlint
fi

# H2. EN and zh-CN keep the same section structure (###-level headers count).
if [ -f "$KEP_EN" ] && [ -f "$KEP_ZH" ]; then
  en=$(grep -c '^### ' "$KEP_EN"); zh=$(grep -c '^### ' "$KEP_ZH")
  en2=$(grep -c '^## ' "$KEP_EN"); zh2=$(grep -c '^## ' "$KEP_ZH")
  if [ "$en" = "$zh" ] && [ "$en2" = "$zh2" ]; then
    ok CheckEnZhSectionParity; else bad CheckEnZhSectionParity; fi
else
  bad CheckEnZhSectionParity
fi

# H3. no trailing whitespace or tab-indentation errors in the KEP files.
if ! grep -nE ' +$' "$KEP_EN" "$KEP_ZH" "$KEP_YAML" >/dev/null 2>&1; then
  ok CheckWhitespace; else bad CheckWhitespace; fi
