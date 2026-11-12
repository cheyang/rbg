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
#     exclusion, precedence, or removal of the legacy protocol) between the
#     existing RIS spec.lifecycle and the proposed Role-level lifecycle.
#     Harness-update (round 3): the design switched from mutual exclusion to
#     wholesale removal of the legacy protocol; removal language now satisfies
#     the contract (nothing remains to coexist with).
if [ -f "$KEP_EN" ] && grep -qiE 'mutual(ly)? exclusive|mutual exclusion|deprecat|takes precedence|must not (both|be combined)|coexist|protocol is removed|replaces and removes' "$KEP_EN"; then
  ok CheckOldNewCoexistenceRule; else bad CheckOldNewCoexistenceRule; fi

# F5. Ready Gating must define the lifetime of a preDelete NotReady gate, not only
#     the Update/Recovery one.
#     SUPERSEDED round 6: the markNotReady gate / Ready Gating section was removed
#     wholesale; the finding is resolved by design removal. This pin now FAILS BY
#     DESIGN at head; the active contract is CheckReadyGatingRemoved (group 2e).
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

# F8 (F-INFLIGHT-CONFIG-ADMISSION; Harness-update, round 4). Rounds 2-3 pinned
#     the admission-rejection resolution ("Admission must reject lifecycle
#     changes while an affected RI has a non-empty hook-state"). Round 4
#     deliberately replaced that mechanism with declarative latest-config
#     semantics ("treats it as absent"). The underlying finding (in-flight
#     config change must have defined semantics) stays addressed; pin the new
#     resolution form + zh mirror + test-plan coverage.
if [ -f "$KEP_EN" ] && grep -q 'always evaluates the latest effective lifecycle configuration' "$KEP_EN" \
  && grep -q 'treats it as absent' "$KEP_EN" \
  && grep -q 'Removing an active hook uses absence semantics' "$KEP_EN" \
  && grep -q '声明式语义' "$KEP_ZH"; then
  ok CheckInflightSemanticsDefined; else bad CheckInflightSemanticsDefined; fi

# --- group 2b: round-3 checks (delta 71dd5e87..fb6c2b36) ---

# F9 (F-OLDNEW-ISOLATION / F-PROPAGATED-VS-LEGACY; Harness-update, round 5).
#     Round 3 pinned wholesale removal of the legacy protocol; round 4 reverted
#     to coexistence with admission-enforced mutual exclusion (the round-2
#     resolution form, strengthened). Round 5 reworded the zh Summary sentence
#     ("同一个 RIS 上二者互斥" -> "准入保证同一个 RIS 上不会同时激活二者");
#     the zh Non-Goal form is unchanged. Pin: mutual-exclusion rule + legacy field
#     retained + distinct new field paths + distinct state keys + zh mirrors
#     (Non-Goal form + new Summary form).
if [ -f "$KEP_EN" ] && grep -qiE 'mutual(ly)? exclusive' "$KEP_EN" \
  && grep -q 'legacy RIS field remains' "$KEP_EN" \
  && grep -q 'roleInstanceTemplate.lifecycle' "$KEP_EN" \
  && grep -q 'RoleInstance.spec.lifecycle' "$KEP_EN" \
  && grep -q 'distinct state keys' "$KEP_EN" \
  && grep -q '不允许同一个 RIS 同时配置旧协议和新协议' "$KEP_ZH" \
  && grep -q '不会同时激活二者' "$KEP_ZH"; then
  ok CheckLegacyCoexistenceSpecified; else bad CheckLegacyCoexistenceSpecified; fi

# F10 (F-FINALIZER-BOOTSTRAP-RACE). The reserved finalizer must be established
#     before the effective preDelete config is exposed, and enablement on an RI
#     that already has a deletionTimestamp must be rejected or deferred.
if [ -f "$KEP_EN" ] && grep -q 'adds the finalizer before exposing the effective' "$KEP_EN" \
  && grep -q 'already has a deletionTimestamp' "$KEP_EN"; then
  ok CheckFinalizerEstablishmentOrdering; else bad CheckFinalizerEstablishmentOrdering; fi

# F11 (F-NS-TEARDOWN-BYPASS). Namespace teardown must be explicitly scoped out
#     of the live-engine guarantee (EN and zh).
#     Harness-update (round 5): the EN sentence was re-wrapped across lines;
#     match wrap-tolerantly.
if [ -f "$KEP_EN" ] && tr '\n' ' ' < "$KEP_EN" | tr -s ' ' \
    | grep -q 'Namespace teardown is *outside the live-engine guarantee' \
  && grep -q 'namespace 删除不在存活引擎保证范围' "$KEP_ZH"; then
  ok CheckNamespaceTeardownScoped; else bad CheckNamespaceTeardownScoped; fi

# F12 (F-RBGSET-FOREGROUND-BOUNDARY). The foreground-delete admission boundary
#     must include RBGSet (EN and zh). UPDATED round 7 (Harness-update): the
#     rule became unconditional — while the gate is enabled, admission rejects
#     every Foreground propagation at RBGSet/RBG/RIS/RI entries; the pin now
#     tracks the new wording (substance strengthened, finding stays fixed).
if [ -f "$KEP_EN" ] && tr '\n' ' ' < "$KEP_EN" | tr -s ' ' | grep -q 'rejects every `Foreground` propagation for RBGSet, RBG, RIS, and RI' \
  && tr '\n' ' ' < "$KEP_ZH" | tr -s ' ' | grep -q '准入拒绝 RBGSet、RBG、RIS 和 RI 所有使用 `Foreground` 传播'; then
  ok CheckRBGSetForegroundBoundary; else bad CheckRBGSetForegroundBoundary; fi

# F13 (F-NOPOSTREADY-CLEANUP-CLARIFICATION). The postReady-omitted completion
#     path must be stated explicitly.
if [ -f "$KEP_EN" ] && grep -q 'If `postReady` is omitted' "$KEP_EN" \
  && grep -q 'clear `hook-state` without' "$KEP_EN"; then
  ok CheckPostReadyOmittedPath; else bad CheckPostReadyOmittedPath; fi

# F14 (F-DISABLE-SUPPORTED-CONTRADICTION). disable-supported must be consistent
#     with the named feature gate and the clarification sentence must exist.
if [ -f "$KEP_YAML" ] && grep -q 'disable-supported: true' "$KEP_YAML" \
  && grep -q 'disable-supported` metadata field describes feature-gate rollback' "$KEP_EN"; then
  ok CheckDisableSupportedClarified; else bad CheckDisableSupportedClarified; fi

# F15 (wire constants, new in round 3). The normative wire names must be pinned
#     and consistent between the README table and kep.yaml.
if [ -f "$KEP_EN" ] && grep -q 'rbg.workloads.x-k8s.io/role-instance-hook-state' "$KEP_EN" \
  && grep -q 'rbg.workloads.x-k8s.io/role-instance-pre-delete' "$KEP_EN" \
  && grep -q 'RoleInstanceLifecycleHooks' "$KEP_EN" \
  && grep -q 'RoleInstanceLifecycleHooks' "$KEP_YAML"; then
  ok CheckWireConstantsPinned; else bad CheckWireConstantsPinned; fi

# F16 (F-KEPYAML-GATE-SHAPE, new nit). kep.yaml feature-gates entries must use
#     the repo template's object form (- name: ...), as keps/NNNN-kep-template
#     shows; an empty list is conformant. Fails while the nit is open.
if [ -f "$KEP_YAML" ]; then
  entries=$(sed -n '/^feature-gates:/,/^[a-z]/p' "$KEP_YAML" | grep -c '^  - ' || true)
  named=$(sed -n '/^feature-gates:/,/^[a-z]/p' "$KEP_YAML" | grep -c '^  - name:' || true)
  if [ "$entries" = "$named" ]; then
    ok CheckKepYamlFeatureGateShape; else bad CheckKepYamlFeatureGateShape; fi
else
  bad CheckKepYamlFeatureGateShape
fi

# F17 (F-INTERRUPT-WHEN-DEAD-AMBIGUITY, new nit). The interrupt rule must not be
#     worded as conditional on a dead engine (the operation table is
#     unconditional). Fails while the ambiguous sentence stands.
if [ -f "$KEP_EN" ] && ! grep -q 'may interrupt Recovery when the previous engine is dead' "$KEP_EN"; then
  ok CheckInterruptConditionalityClear; else bad CheckInterruptConditionalityClear; fi

# --- group 2c: round-4 checks (delta fb6c2b36..43dab57b) ---

# F18 (F-PROPAGATED-SNAPSHOT-EDIT, nit; Harness-update, round 5). RBG-managed
#     RIS lifecycle snapshot ownership must be stated. Round 5 reworded the
#     clause ("must not be independently edited or combined with the legacy
#     protocol" -> "is not independently editable" + the stronger overwrite
#     rule "Direct edits ... are unsupported and are overwritten on the next
#     RBG reconcile"); the not-combined-with-legacy clause is now carried by
#     the user-configured clarification (any non-empty legacy spec.lifecycle
#     on an RBG-managed RIS is treated as user configuration -> admission
#     rejects the combination). Wrap-tolerant EN pin + zh mirror.
if [ -f "$KEP_EN" ] && tr '\n' ' ' < "$KEP_EN" | tr -s ' ' \
    | grep -q 'propagated from the Role and is *not independently editable' \
  && grep -q 'are unsupported and are overwritten' "$KEP_EN" \
  && grep -q '不支持直接修改' "$KEP_ZH"; then
  ok CheckSnapshotOwnershipStated; else bad CheckSnapshotOwnershipStated; fi

# F19 (F-ZH-ENGLISH-TERMS, nit). The zh-CN file must not embed the untranslated
#     English prose phrases from the configuration-hierarchy section.
if [ -f "$KEP_ZH" ] && ! grep -qE 'source of truth|desired snapshot' "$KEP_ZH"; then
  ok CheckZhTermsTranslated; else bad CheckZhTermsTranslated; fi

# F20 (F-MARKNOTREADY-NAME, round-4 form). SUPERSEDED round 6: markNotReady no
#     longer exists; this pin FAILS BY DESIGN at head. The active contract is
#     CheckSignalLabelsOnlyHookField (group 2e). Original round-4 comment: the
#     new protocol's field is
#     markNotReady on an independent hook type; the legacy wire name
#     markPodNotReady must survive only in legacy-context sentences (EN+zh),
#     and the new-protocol examples must use markNotReady.
naming_ok=1
if [ -f "$KEP_EN" ] && [ -f "$KEP_ZH" ]; then
  grep -q 'markNotReady: true' "$KEP_EN" || naming_ok=0
  grep -q 'markNotReady: true' "$KEP_ZH" || naming_ok=0
  while IFS= read -r hit; do
    ln="${hit%%:*}"
    sed -n "$(( ln > 2 ? ln - 2 : 1 )),$(( ln + 2 ))p" "$KEP_EN" | grep -qiE 'legacy' || naming_ok=0
  done < <(grep -n 'markPodNotReady' "$KEP_EN")
  while IFS= read -r hit; do
    ln="${hit%%:*}"
    sed -n "$(( ln > 2 ? ln - 2 : 1 )),$(( ln + 2 ))p" "$KEP_ZH" | grep -qE 'legacy|旧' || naming_ok=0
  done < <(grep -n 'markPodNotReady' "$KEP_ZH")
else
  naming_ok=0
fi
if [ "$naming_ok" -eq 1 ]; then ok CheckMarkNotReadyNaming; else bad CheckMarkNotReadyNaming; fi

# --- group 2d: round-5 checks (delta 43dab57b..6c15cc92) ---

# flat(): collapse newlines so pins survive the KEP's hard-wrapped prose.
flat() { tr '\n' ' ' < "$1" | tr -s ' '; }

# F21 (F-COEXIST-GATE-EDGE, round-4 question; gate-off half FIXED in round 5).
#     The KEP must state the gate-off behavior of the new protocol fields and
#     the legacy protocol's status while the gate is disabled (EN+zh).
if [ -f "$KEP_EN" ] && grep -q 'The gate defaults to false' "$KEP_EN" \
  && flat "$KEP_EN" | grep -q 'the new protocol fields are rejected and the legacy RIS lifecycle protocol retains its existing behavior' \
  && grep -q '默认关闭' "$KEP_ZH" && grep -q '拒绝新协议字段' "$KEP_ZH"; then
  ok CheckGateOffBehaviorStated; else bad CheckGateOffBehaviorStated; fi

# F22 (F-TESTPLAN-ABSENCE-CONTRADICTION, round-4 nit; FIXED in round 5). The
#     integration-test bullet must agree with the round-4 absence semantics:
#     new bullet present, stale round-3 bullet absent (EN+zh).
if [ -f "$KEP_EN" ] \
  && grep -q 'Hook disablement stops marker matching on the next reconciliation' "$KEP_EN" \
  && ! grep -q 'disablement during an unfinished operation does not skip' "$KEP_EN" \
  && grep -q '即使其他 lifecycle 操作尚未完成' "$KEP_ZH" \
  && ! grep -q '操作未完成时禁用不得跳过' "$KEP_ZH"; then
  ok CheckAbsenceTestplanConsistent; else bad CheckAbsenceTestplanConsistent; fi

# F23 (F-STALE-LABEL-DISPOSITION, round-4 nit; FIXED in round 5). Hook REMOVAL
#     now has a single disposition ("removes", both prose sites + test bullet),
#     while RECONFIGURATION (markerA->markerB) deliberately keeps the old
#     marker in place — the distinction the finding suggested (EN+zh).
if [ -f "$KEP_EN" ] && grep -q 'removes its stale runtime labels' "$KEP_EN" \
  && flat "$KEP_EN" | grep -q 'removes stale runtime labels and their Pod mirrors identified through the marker-key record' \
  && grep -q 'without migrating or removing the old marker' "$KEP_EN" \
  && grep -q '清理遗留 runtime labels，并按已放行' "$KEP_ZH" \
  && grep -q '按 marker key 记录清理遗留运行时 labels' "$KEP_ZH" \
  && grep -q '不迁移或清理旧 marker' "$KEP_ZH"; then
  ok CheckStaleLabelDispositionSingle; else bad CheckStaleLabelDispositionSingle; fi

# F24 (F-TESTPLAN-PREFIX-COVERAGE, NEW round-5 nit; contract FAILS while open).
#     Round 5 strengthened the design rule: handler keys are rejected when they
#     equal the exact hook-state key OR use the reserved rbg.workloads.x-k8s.io/
#     prefix. The unit-test bullet covering marker-key admission still tests
#     only the exact-key collision (EN :938-940, zh :696-698). Vacuous pass on
#     refs without the strengthened rule.
prefix_rule_present=0
if [ -f "$KEP_EN" ] && flat "$KEP_EN" | grep -q 'use the reserved .rbg.workloads.x-k8s.io/. prefix'; then
  prefix_rule_present=1
fi
if [ "$prefix_rule_present" -eq 0 ]; then
  ok CheckTestplanPrefixCoverage   # rule absent on this ref: nothing to cover
elif sed -n '/#### Unit Tests/,/#### Integration Tests/p' "$KEP_EN" | tr '\n' ' ' | tr -s ' ' \
    | grep -q 'controller state key.*prefix' \
  && sed -n '/#### 单元测试/,/#### 集成测试/p' "$KEP_ZH" | tr '\n' ' ' | tr -s ' ' \
    | grep -q 'controller 状态 key.*前缀'; then
  # UPDATED round 7 (Harness-update): the round-7 bullet lands the coverage in
  # the Unit Tests section; the old [^.]* pattern could not span the dots in
  # the qualified rbg.workloads.x-k8s.io/ name. Substance fixed at d038c23b.
  ok CheckTestplanPrefixCoverage; else bad CheckTestplanPrefixCoverage; fi

# F25 (F-CONFIG-REMOVAL-SKIPS-HOOK, round-4 question; still OPEN in round 5).
#     Contract FAILS while open: Risks or Troubleshooting must acknowledge that
#     removing/disabling a hook's CONFIGURATION skips the executor (and, for
#     preDelete mid-deletion, force-releases the barrier). Marker-level
#     break-glass guidance does not count. A pass is a change signal: a human
#     must then confirm the added wording actually covers the risk.
ack_en=1; ack_zh=1
if [ -f "$KEP_EN" ]; then
  { sed -n '/### Risks and Mitigations/,/## Design Details/p' "$KEP_EN"
    sed -n '/### Troubleshooting/,/## Implementation History/p' "$KEP_EN"
  } | grep -iE 'remov|disabl' | grep -i 'hook' | grep -qvi 'marker' || ack_en=0
fi
if [ -f "$KEP_ZH" ]; then
  { sed -n '/### 风险与缓解措施/,/### 挂载点与触发边界/p' "$KEP_ZH"
    sed -n '/### 故障排查/,/^## /p' "$KEP_ZH"
  } | grep -E '删除|禁用|移除' | grep 'hook' | grep -qv 'marker' || ack_zh=0
fi
if [ "$ack_en" -eq 1 ] && [ "$ack_zh" -eq 1 ]; then
  ok CheckRemovalRiskAcknowledged; else bad CheckRemovalRiskAcknowledged; fi

# F26 (round-5 content guard). preDelete executor must be independently
#     deployed and available before the application Pod is deleted (EN+zh).
if [ -f "$KEP_EN" ] && flat "$KEP_EN" | grep -q 'preDelete. executor must be independently deployed' \
  && grep -q '必须独立部署' "$KEP_ZH"; then
  ok CheckPreDeleteExecutorIndependent; else bad CheckPreDeleteExecutorIndependent; fi

# F27 (round-5 content guard). An RI that already has a deletionTimestamp never
#     exposes preDelete; that deletion continues without the hook (EN+zh).
if [ -f "$KEP_EN" ] && flat "$KEP_EN" | grep -q 'the hook is never exposed on that RI and its deletion continues without' \
  && grep -q '该 RI 不暴露 preDelete' "$KEP_ZH"; then
  ok CheckDeletingRINeverExposesHook; else bad CheckDeletingRINeverExposesHook; fi

# F28 (round-5 content guard). The strengthened marker-key admission rule:
#     reject keys equal to the exact hook-state key or using the reserved
#     rbg.workloads.x-k8s.io/ prefix (EN+zh).
if [ -f "$KEP_EN" ] && flat "$KEP_EN" | grep -q 'use the reserved .rbg.workloads.x-k8s.io/. prefix' \
  && flat "$KEP_ZH" | grep -q '使用保留的 .rbg.workloads.x-k8s.io/. 前缀'; then
  ok CheckReservedPrefixRejected; else bad CheckReservedPrefixRejected; fi

# F29 (round-5 content guard). Field ownership is explicit: users own the
#     authoritative configuration; the controller owns phase, hook-state, the
#     effective RI snapshot, and the reserved finalizer (EN+zh).
if [ -f "$KEP_EN" ] && flat "$KEP_EN" | grep -q 'users own the authoritative lifecycle configuration' \
  && grep -q '字段所有权明确' "$KEP_ZH"; then
  ok CheckFieldOwnershipExplicit; else bad CheckFieldOwnershipExplicit; fi

# F30 (round-5 content guard). The mutual-exclusion check defines
#     "user-configured" for the legacy field on an RBG-managed RIS: any
#     non-empty legacy spec.lifecycle the RBG controller does not own is
#     treated as user configuration (EN+zh).
if [ -f "$KEP_EN" ] && flat "$KEP_EN" | grep -q 'treated as user configuration' \
  && grep -q '视为用户配置' "$KEP_ZH"; then
  ok CheckUserConfiguredClarified; else bad CheckUserConfiguredClarified; fi

# --- group 2e: round-6 checks (delta 6c15cc92..c3ea6547) ---
# Round 6 is a design revision: labelsHandler renamed to signalLabels;
# markNotReady / the Ready Gating section removed wholesale (hooks never gate
# RI Ready); signal labels are mirrored from the RI onto member Pods; a durable
# marker-key record annotation was added; marker keys colliding with the owning
# RIS spec.selector are admission-rejected; the foreground-delete webhook
# resolution is fail-closed.

# F5' (supersedes CheckPreDeleteGateLifetime; F-PREDELETE-GATE-LIFETIME resolved
#     by design removal; also dissolves advisory F-C4528290945 — with no gate
#     there is no stateful-readiness-vs-gate divergence). Contract: markNotReady
#     absent (EN+zh) and the never-gate-Ready rule stated in both languages.
if [ -f "$KEP_EN" ] && ! grep -q 'markNotReady' "$KEP_EN" && ! grep -q 'markNotReady' "$KEP_ZH" \
  && flat "$KEP_EN" | grep -q 'no hook changes RI Ready' \
  && flat "$KEP_EN" | grep -q 'Hook markers never participate in RI Ready computation' \
  && grep -q 'hook marker 不参与 RI Ready 计算' "$KEP_ZH"; then
  ok CheckReadyGatingRemoved; else bad CheckReadyGatingRemoved; fi

# F20' (supersedes CheckMarkNotReadyNaming; F-MARKNOTREADY-NAME resolved by
#     design removal). Contract: signalLabels is the only hook field (EN+zh),
#     examples use signalLabels, and markPodNotReady survives only in
#     legacy-context sentences (EN+zh).
r6naming=1
if [ -f "$KEP_EN" ] && [ -f "$KEP_ZH" ]; then
  flat "$KEP_EN" | grep -q 'every hook contains only `signalLabels`' || r6naming=0
  grep -q '每个 hook 只包含' "$KEP_ZH" || r6naming=0
  grep -q '          signalLabels:' "$KEP_EN" || r6naming=0
  grep -q 'signalLabels' "$KEP_ZH" || r6naming=0
  while IFS= read -r hit; do
    ln="${hit%%:*}"
    sed -n "$(( ln > 2 ? ln - 2 : 1 )),$(( ln + 2 ))p" "$KEP_EN" | grep -qiE 'legacy|KEP-502|earlier revision|stale' || r6naming=0
  done < <(grep -n 'markPodNotReady' "$KEP_EN")
  while IFS= read -r hit; do
    ln="${hit%%:*}"
    # UPDATED round 7 (Harness-update): markPodNotReady may now also appear in
    # KEP-502-drift disclosure sentences (merged KEP-502's stale references),
    # in addition to legacy-context sentences. signalLabels remains the only
    # hook field; the mention class is what widened.
    sed -n "$(( ln > 2 ? ln - 2 : 1 )),$(( ln + 2 ))p" "$KEP_ZH" | grep -qE 'legacy|旧|KEP-502|遗留|早期' || r6naming=0
  done < <(grep -n 'markPodNotReady' "$KEP_ZH")
else
  r6naming=0
fi
if [ "$r6naming" -eq 1 ]; then ok CheckSignalLabelsOnlyHookField; else bad CheckSignalLabelsOnlyHookField; fi

# F31 (round-6 content guard). Signal Label Propagation to Pods section:
#     unidirectional RI->Pod mirroring, metadata-only (no Pod recreate),
#     hook-state excluded, template-label collision rejected (EN+zh).
if [ -f "$KEP_EN" ] && grep -q '### Signal Label Propagation to Pods' "$KEP_EN" \
  && flat "$KEP_EN" | grep -q 'Propagation is unidirectional from RI to Pod' \
  && flat "$KEP_EN" | grep -q 'metadata-only Pod update' \
  && flat "$KEP_EN" | grep -q 'Only `signalLabels` keys are mirrored' \
  && flat "$KEP_EN" | grep -q 'Declaring the same key in the Pod template labels is a configuration error' \
  && grep -q '### 信号标签向 Pod 传递' "$KEP_ZH" \
  && flat "$KEP_ZH" | grep -q '传递是单向的' \
  && flat "$KEP_ZH" | grep -q '仅修改 metadata' \
  && flat "$KEP_ZH" | grep -q '在 Pod template labels 中声明相同 key'; then
  ok CheckPodPropagationSpecified; else bad CheckPodPropagationSpecified; fi

# F32 (round-6 content guard; also the fix evidence for advisory
#     F-C4528291076, retired marker keys now durably tracked). The marker-key
#     record: wire name + Go constant pinned, written in the same metadata
#     write as the marker, a key removed only after the RI label and its Pod
#     mirrors are confirmed absent, and the record feeds the rollback
#     precondition (EN+zh).
if [ -f "$KEP_EN" ] && grep -q 'rbg.workloads.x-k8s.io/role-instance-hook-marker-keys' "$KEP_EN" \
  && grep -q 'RoleInstanceHookMarkerKeysAnnotation' "$KEP_EN" \
  && flat "$KEP_EN" | grep -q 'records the marker keys owned by the protocol in the annotation' \
  && flat "$KEP_EN" | grep -q 'updated in the same metadata write as the marker' \
  && flat "$KEP_EN" | grep -q 'a key is removed only after the corresponding RI label and its Pod mirrors are confirmed absent' \
  && grep -q 'role-instance-hook-marker-keys' "$KEP_ZH" \
  && grep -q 'marker key 记录' "$KEP_ZH"; then
  ok CheckMarkerKeyRecordPinned; else bad CheckMarkerKeyRecordPinned; fi

# F33 (fix evidence for advisory F-C4528290874). Admission rejects marker keys
#     used by the owning RIS spec.selector (matchLabels or any matchExpressions
#     entry, incl. DoesNotExist-only), with the owner-reference-release
#     rationale (EN+zh).
if [ -f "$KEP_EN" ] && flat "$KEP_EN" | grep -q 'Admission also rejects a marker key used by the owning RIS `spec.selector`' \
  && flat "$KEP_EN" | grep -q 'whether in `matchLabels` or in any `matchExpressions` entry' \
  && flat "$KEP_EN" | grep -q 'releases the RI.s owner reference' \
  && grep -q '准入还拒绝与所属 RIS `spec.selector` 相关的 marker key' "$KEP_ZH" \
  && grep -q 'owner reference' "$KEP_ZH"; then
  ok CheckSelectorAdmissionStated; else bad CheckSelectorAdmissionStated; fi

# F34 (fix evidence for advisory F-C4528291011). Foreground-deletion webhook
#     resolution is fail-closed on cache miss (EN+zh).
#     SUPERSEDED round 7: the fail-closed cache-miss rule was replaced by an
#     unconditional rejection design (disclosed mechanism replacement in
#     d038c23b — strictly stronger: resolution no longer consults informer
#     caches at all). This pin now FAILS BY DESIGN at head; the active
#     contract is CheckForegroundRejectUnconditional (group 2f).
if [ -f "$KEP_EN" ] && flat "$KEP_EN" | grep -q 'Resolution is fail-closed' \
  && flat "$KEP_EN" | grep -q 'cache miss must not establish that deletion is safe' \
  && grep -q '解析采用 fail-closed 语义' "$KEP_ZH" \
  && grep -q 'cache miss 不能证明删除安全' "$KEP_ZH"; then
  ok CheckFailClosedCacheMiss; else bad CheckFailClosedCacheMiss; fi

# F35 (F-ZH-EN-ROLLOUT-RISK-PARITY, NEW round-6 minor; contract FAILS while
#     open). Round 6 dropped "circular waits involving Ready" from the EN
#     rollout-risk list when Ready gating was removed; the zh list still
#     contains "Ready 循环等待". Pass requires parity restored (either side
#     may change; a flip needs a human re-read).
if [ -f "$KEP_EN" ] && [ -f "$KEP_ZH" ]; then
  en_has=0; zh_has=0
  grep -q 'circular wait' "$KEP_EN" && en_has=1
  grep -q 'Ready 循环等待' "$KEP_ZH" && zh_has=1
  if [ "$en_has" -eq "$zh_has" ]; then
    ok CheckZhEnRolloutRiskParity; else bad CheckZhEnRolloutRiskParity; fi
else
  ok CheckZhEnRolloutRiskParity   # no KEP on this ref: vacuous pass
fi

# F36 (F-MARKERKEY-RECORD-OWNERSHIP, NEW round-6 minor; contract FAILS while
#     open). The field-ownership enumeration must cover the marker-key record
#     annotation (controller-owned / edit-protected), in both languages: the
#     rollback precondition and stale-label attribution depend on its
#     integrity, but only markers are protected today.
if [ -f "$KEP_EN" ] && grep -q 'role-instance-hook-marker-keys' "$KEP_EN"; then
  own_en=1; own_zh=1
  awk '/^Field ownership is explicit/{f=1} f{print} f&&NF==0{exit}' "$KEP_EN" \
    | grep -q 'marker-key record' || own_en=0
  awk '/^字段所有权明确/{f=1} f{print} f&&NF==0{exit}' "$KEP_ZH" \
    | grep -q 'marker key 记录' || own_zh=0
  if [ "$own_en" -eq 1 ] && [ "$own_zh" -eq 1 ]; then
    ok CheckMarkerKeyRecordOwnership; else bad CheckMarkerKeyRecordOwnership; fi
else
  ok CheckMarkerKeyRecordOwnership   # record absent on this ref: vacuous pass
fi

# F37 (F-SCALABILITY-POD-MIRROR, NEW round-6 nit; contract FAILS while open).
#     The Scalability section must account for the Pod-mirror fan-out write
#     cost (1 RI write + N Pod writes per signal transition, re-synced every
#     RI reconcile) — the dominant new write cost for multi-Pod RIs (EN+zh).
if [ -f "$KEP_EN" ] && grep -q '### Signal Label Propagation to Pods' "$KEP_EN"; then
  if sed -n '/### Scalability/,/### Troubleshooting/p' "$KEP_EN" | grep -qiE 'mirror|propagat' \
    && sed -n '/### 可扩展性/,/### 故障排查/p' "$KEP_ZH" | grep -q '镜像'; then
    ok CheckScalabilityPodMirrorCost; else bad CheckScalabilityPodMirrorCost; fi
else
  ok CheckScalabilityPodMirrorCost   # no propagation on this ref: vacuous pass
fi

# F38 (F-ROLLBACK-PRECONDITION-SHORTHAND, NEW round-6 nit; contract FAILS while
#     open). The marker-key-record paragraph states "The rollback precondition
#     is an absent hook-state label together with an empty or absent marker-key
#     record" and reads as complete, but omits the reserved-finalizer condition
#     stated in Upgrade/Downgrade. Pass requires the sentence to carry (or
#     reference) the finalizer condition; a scoping reword instead is a
#     Harness-update (human re-read on flip).
if [ -f "$KEP_EN" ] && flat "$KEP_EN" | grep -q 'The rollback precondition is an absent'; then
  if flat "$KEP_EN" | grep -oE 'The rollback precondition is an absent[^.]*\.' | grep -qi 'finalizer' \
    && flat "$KEP_ZH" | grep -oE '回滚前置条件是[^。]*。' | grep -q 'finalizer'; then
    ok CheckRollbackPreconditionComplete; else bad CheckRollbackPreconditionComplete; fi
else
  ok CheckRollbackPreconditionComplete   # no shorthand sentence on this ref: vacuous pass
fi

# F39 (F-WEBHOOK-DEFER-WORDING, NEW round-6 nit; contract FAILS while open).
#     The fail-closed rule says the webhook "rejects or defers the request";
#     an admission webhook cannot defer — denial is the only fail-closed
#     mechanism (clients retry). Pass when the mechanics are stated as
#     rejection-only (EN+zh).
if [ -f "$KEP_EN" ] && flat "$KEP_EN" | grep -q 'Resolution is fail-closed'; then
  if ! flat "$KEP_EN" | grep -q 'rejects or defers the request' \
    && ! grep -q '拒绝或暂缓' "$KEP_ZH"; then
    ok CheckWebhookDeferralNotClaimed; else bad CheckWebhookDeferralNotClaimed; fi
else
  ok CheckWebhookDeferralNotClaimed   # no fail-closed rule on this ref: vacuous pass
fi

# --- group 2f: round-7 checks (delta c3ea6547..d038c23b) ---

# F34' (round-7 form of the F34 contract). While the gate is enabled,
#     admission rejects every Foreground propagation at RBGSet/RBG/RIS/RI
#     deletion entries, with no informer-cache dependence, and the test plan
#     states the unconditional rule (EN+zh).
if [ -f "$KEP_EN" ] && flat "$KEP_EN" | grep -q 'rejects every `Foreground` propagation for RBGSet, RBG, RIS, and RI' \
  && flat "$KEP_EN" | grep -q 'does not depend on informer caches' \
  && flat "$KEP_EN" | grep -q 'Foreground deletion is rejected for RBGSet, RBG, RIS, and RI while the feature gate is enabled' \
  && flat "$KEP_ZH" | grep -q '不依赖 informer 缓存' \
  && flat "$KEP_ZH" | grep -q 'RBGSet、RBG、RIS 和 RI 的 foreground 删除均被拒绝'; then
  ok CheckForegroundRejectUnconditional; else bad CheckForegroundRejectUnconditional; fi

# F40 (fix evidence for F-COEXIST-GATE-EDGE, closed round 7). Gate edges
#     stated (EN+zh): gate-on does not retroactively validate existing objects
#     but mutual exclusion applies to subsequent writes (dual-configured
#     objects must be corrected before their next update); gate-off leaves
#     runtime state for a compatible controller; legacy inPlaceUpdate remains
#     supported until a deprecation release is announced.
if [ -f "$KEP_EN" ] && flat "$KEP_EN" | grep -q 'Gate edges:' \
  && flat "$KEP_EN" | grep -q 'must be corrected before its next update' \
  && flat "$KEP_EN" | grep -q 'neither writes nor matches new-protocol runtime labels' \
  && flat "$KEP_EN" | grep -q 'until a deprecation release is announced' \
  && flat "$KEP_ZH" | grep -q '不会回溯校验存量对象' \
  && flat "$KEP_ZH" | grep -q '在下一次更新前修正' \
  && flat "$KEP_ZH" | grep -q '公布弃用版本前继续支持'; then
  ok CheckGateEdgesStated; else bad CheckGateEdgesStated; fi

# F41 (fix evidence for advisory F-C4541237385, closed round 7). Signal-key
#     uniqueness spans configuration generations: no reassignment to another
#     hook while any governed RI still records the key (EN+zh).
if [ -f "$KEP_EN" ] && flat "$KEP_EN" | grep -q 'may not be reassigned from one hook to another while any RI' \
  && flat "$KEP_ZH" | grep -q '不得被重新分配给另一个 hook'; then
  ok CheckCrossGenerationKeyUniqueness; else bad CheckCrossGenerationKeyUniqueness; fi

# F42 (fix evidence for F-POD-MIRROR-SPOOF-WINDOW, closed round 7). Mirrors
#     are not authenticated; the RI is the only authoritative source and
#     safety-relevant consumers must verify the RI (EN+zh).
if [ -f "$KEP_EN" ] && flat "$KEP_EN" | grep -q 'Mirrors are not authenticated' \
  && flat "$KEP_EN" | grep -q 'verify the RI rather than trust a mirror' \
  && flat "$KEP_ZH" | grep -q '镜像不可信' \
  && flat "$KEP_ZH" | grep -q '回验 RI'; then
  ok CheckMirrorNotAuthenticated; else bad CheckMirrorNotAuthenticated; fi

# F43 (fix evidence for F-SELECTOR-RESERVED-KEY-REVERSE, closed round 7).
#     Admission also rejects an owning RIS selector that uses a
#     controller-reserved key (the reverse direction), EN+zh.
if [ -f "$KEP_EN" ] && flat "$KEP_EN" | grep -q 'admission rejects an owning RIS selector that uses a controller-reserved key' \
  && flat "$KEP_ZH" | grep -q '准入也拒绝在所属 RIS selector 中使用'; then
  ok CheckSelectorReservedKeyReverse; else bad CheckSelectorReservedKeyReverse; fi

# F44 (fix evidence for F-KEP502-GATE-REFERENCE-DRIFT, closed round 7). The
#     KEP states KEP-502 is now merged, discloses that the merged text still
#     references the removed markPodNotReady gate, and requires a KEP-502
#     follow-up amendment before implementation (EN+zh).
if [ -f "$KEP_EN" ] && flat "$KEP_EN" | grep -q 'KEP-502, now merged' \
  && flat "$KEP_EN" | grep -q 'still references the `markPodNotReady`' \
  && flat "$KEP_EN" | grep -q 'follow-up amendment must remove' \
  && flat "$KEP_ZH" | grep -q '现已合入' \
  && flat "$KEP_ZH" | grep -q '仍引用早期提案中的 `markPodNotReady`' \
  && flat "$KEP_ZH" | grep -q '后续修订删除'; then
  ok CheckKep502DriftDisclosed; else bad CheckKep502DriftDisclosed; fi

# F45 (F-AFFECTED-ANCESTOR-WORDING, NEW round-7 nit; contract FAILS while
#     open). The preDelete integration-test bullet still says admission
#     rejects foreground deletion "at every affected ancestor" (EN+zh) — a
#     reachability term whose definition was removed with the informer-based
#     resolution; under the unconditional rule nothing computes affectedness.
#     Pass when the bullet is aligned with the unconditional rule.
if [ -f "$KEP_EN" ] && grep -q 'Foreground deletion is rejected for RBGSet' "$KEP_EN"; then
  if ! grep -q 'affected ancestor' "$KEP_EN" && ! grep -q '受影响祖先' "$KEP_ZH"; then
    ok CheckAffectedAncestorRetired; else bad CheckAffectedAncestorRetired; fi
else
  ok CheckAffectedAncestorRetired   # pre-redesign ref: vacuous pass
fi

# F46 (F-FOREGROUND-SCOPE-BROADENED, NEW round-7 question; contract FAILS
#     while open). The unconditional foreground rejection also covers
#     RBGSet/RBG/RIS/RI objects that configure no hooks at all while the gate
#     is enabled (gate defaults false; clients retry with background). The
#     KEP never acknowledges that broadened blast radius. Pass when an
#     acknowledgment sentence exists (EN or zh); a flip needs a human re-read.
if [ -f "$KEP_EN" ] && grep -q 'rejects every `Foreground`' "$KEP_EN"; then
  if flat "$KEP_EN" | grep -qiE 'regardless of whether[^.]*(hook|preDelete)|even (when|if)[^.]*no[^.]*(hook|preDelete)[^.]*configured|whether or not[^.]*(hook|preDelete)[^.]*configured' \
    || flat "$KEP_ZH" | grep -qE '无论[^，。]*(是否|有无)[^，。]*(hook|preDelete)|即使[^，。]*未配置[^，。]*(hook|preDelete)'; then
    ok CheckForegroundScopeAcknowledged; else bad CheckForegroundScopeAcknowledged; fi
else
  ok CheckForegroundScopeAcknowledged   # rule absent on this ref: vacuous pass
fi

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
