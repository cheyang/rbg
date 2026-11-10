#!/usr/bin/env bash
# KEP-527 (PR #546) round-5 contract checks.
#
# Round 5 (delta 43dab57b..6c15cc92, docs-only, +276/-219, 8 wording commits)
# rewords the round-4 coexistence/absence-semantics contracts and fixes three
# of the five round-4 open items:
#   - F-TESTPLAN-ABSENCE-CONTRADICTION (nit): the stale integration-test
#     bullet is rewritten in BOTH languages to match the round-4 absence
#     semantics (the old "does not skip the marker" bullet is now forbidden).
#   - F-STALE-LABEL-DISPOSITION (nit): the removed-hook disposition is
#     unified to a single normative strength ("removes its stale runtime
#     labels, and follows the same transition as a released hook") in the
#     config paragraph, the normal-disable paragraph, and the unit-test
#     bullet, in both languages.
#   - F-COEXIST-GATE-EDGE (question): PARTIALLY fixed — the gate-off rule is
#     now stated ("The gate defaults to false. While it is disabled, the new
#     protocol fields are rejected..."); gate-on for existing dual-configured
#     objects and the legacy deprecation timeline remain unstated.
#   - F-CONFIG-REMOVAL-SKIPS-HOOK (question): still OPEN — no admission guard
#     and no Risks/Troubleshooting acknowledgment of the mid-deletion
#     preDelete-removal skip path; the ownership sentence "This is the direct
#     result of the user removing the hook" was deleted this round.
#   - F-SEEALSO-502 (minor): still OPEN (kep.yaml untouched this round;
#     PR #528 still OPEN).
#
# HARNESS-UPDATE (disclosed): the round-5 rewording changed the wording of 12
# round-4 pins (mutual exclusion, admission rejection, snapshot ownership,
# removal/mid-deletion semantics, enablement-during-deletion, zh terms).
# Their SUBSTANCE survives the rewording and is re-pinned here under the
# round-5 wording; the superseded round-4 pin wordings are allowed to fail in
# check_round4_contracts.sh (see the SUPERSEDED list at the bottom of this
# script — a round-4 failure outside that list is still a carry regression
# and fails this script).
#
# New round-5 normative content is pinned differentially: red on base
# 09accf63 and previous head 43dab57b, green on head 6c15cc92.
#
# Matching is done against whitespace-flattened file content because the KEP
# hard-wraps paragraphs, so patterns may span physical lines.
#
# Usage: bash check_round5_contracts.sh <repo-root-of-ref-under-test>
# Exit 0 iff every round-5 contract (and every non-superseded round-4 carry
# contract) holds.
set -u
ROOT="${1:?usage: check_round5_contracts.sh <repo-root>}"
KEP="$ROOT/keps/527-roleinstance-lifecycle"

OUT_DIR="${CHECK_ROUND5_OUT:-$(cd "$(dirname "$0")/../results" && pwd)}"
mkdir -p "$OUT_DIR"
OUT="$OUT_DIR/round5-contracts.txt"
: >"$OUT"

flatten() { tr '\n' ' ' <"$1" 2>/dev/null | tr -s ' '; }
EN="$(flatten "$KEP/README.md")"
ZH="$(flatten "$KEP/README.zh-CN.md")"

overall=0

required() { # required <finding> <lang-var-name> <pattern>
  local id="$1"; local var="$2"; local pat="$3"
  local content; eval "content=\"\$$var\""
  if printf '%s' "$content" | grep -Eq "$pat"; then
    echo "PASS  $id [$var] (required present: $pat)" | tee -a "$OUT"
  else
    echo "FAIL  $id [$var] (required MISSING: $pat)" | tee -a "$OUT"
    overall=1
  fi
}

forbidden() { # forbidden <finding> <lang-var-name> <pattern>
  local id="$1"; local var="$2"; local pat="$3"
  local content; eval "content=\"\$$var\""
  if printf '%s' "$content" | grep -Eq "$pat"; then
    echo "FAIL  $id [$var] (forbidden PRESENT: $pat)" | tee -a "$OUT"
    overall=1
  else
    echo "PASS  $id [$var] (forbidden absent: $pat)" | tee -a "$OUT"
  fi
}

echo "== Round-5 contracts on $ROOT ==" | tee -a "$OUT"

# --- A. Coexistence / mutual exclusion under the round-5 wording (replaces
# --- the reworded F-OLDNEW-COEXIST / F-PROPAGATED-VS-LEGACY round-4 pins).

required R5-OLDNEW-COEXIST EN 'The new protocol coexists with the legacy RoleInstanceSet lifecycle protocol at the API level, but admission prevents both from being active on the same RIS'
required R5-OLDNEW-COEXIST EN 'Admission rejects a Role/RIS combination in which the new lifecycle protocol and a legacy user-configured RIS lifecycle are both non-empty'
required R5-OLDNEW-COEXIST EN 'the legacy protocol is executed by the RIS controller, while the new protocol is executed by the RI controller'
required R5-OLDNEW-COEXIST EN 'The two runtime protocols must not be active on the same RIS; an admission webhook enforces that boundary'
# new round-5 clarification: what counts as user-configured for the check
required R5-OLDNEW-COEXIST EN 'any non-empty legacy `spec\.lifecycle` on an RBG-managed RIS is treated as user configuration'
# the round-3 removal wording must not return
forbidden R5-OLDNEW-COEXIST EN 'protocol is removed\. Admission rejects any use of that field'
required R5-OLDNEW-COEXIST ZH '新协议与旧 RoleInstanceSet lifecycle 协议在 API 层共存'
required R5-OLDNEW-COEXIST ZH '准入保证同一个 RIS 上不会同时激活二者'
required R5-OLDNEW-COEXIST ZH '准入拒绝新 lifecycle 协议 与用户配置的旧 RIS lifecycle 同时非空'
required R5-OLDNEW-COEXIST ZH '旧协议由 RIS controller 执行，新协议由 RI controller 执行'
required R5-OLDNEW-COEXIST ZH '两套运行协议不得在同一 RIS 上同时生效，准入 webhook 强制该边界'
required R5-OLDNEW-COEXIST ZH 'RBG 管理的 RIS 上旧 `spec\.lifecycle` 非空即视为用户配置'
forbidden R5-OLDNEW-COEXIST ZH '协议被移除。准入拒绝该字段的任何使用'

# --- B. Snapshot ownership, round-5 form (replaces the reworded
# --- F-PROPAGATED-SNAPSHOT-EDIT round-4 pin; substance strengthened).

required R5-SNAPSHOT-OWNERSHIP EN 'Direct edits to that snapshot on an RBG-owned RIS are unsupported and are overwritten on the next RBG reconcile'
required R5-SNAPSHOT-OWNERSHIP ZH '直接修改 RBG 所管理 RIS 上的该快照不受支持，并会在下一次 RBG reconcile 被覆盖'
# configuration hierarchy now names BOTH authorities
required R5-SNAPSHOT-OWNERSHIP EN 'For a standalone RIS, `roleInstanceTemplate\.lifecycle` is the user-owned desired configuration'
required R5-SNAPSHOT-OWNERSHIP ZH '对 standalone RIS，`roleInstanceTemplate\.lifecycle` 是用户拥有的期望配置'

# --- C. Absence / removal semantics, round-5 form (replaces the reworded
# --- F-LATEST-CONFIG-SEMANTICS round-4 pins; adds the phase-advance rule).

required R5-REMOVAL-SEMANTICS EN 'The controller always evaluates the latest effective lifecycle configuration'
required R5-REMOVAL-SEMANTICS EN 'If an active hook is removed, the controller treats it as absent, stops matching its marker, removes its stale runtime labels, and follows the same transition as a released hook; for example, `PreUpdate` advances to `Updating`'
required R5-REMOVAL-SEMANTICS EN 'If `preDelete` is removed while deletion is in progress, removing the finalizer lets deletion continue'
required R5-REMOVAL-SEMANTICS EN 'The change applies whether or not another lifecycle operation is in flight; no special active-hook state is retained'
required R5-REMOVAL-SEMANTICS ZH 'controller 始终按最新的生效 lifecycle 配置判断'
required R5-REMOVAL-SEMANTICS ZH '如果删除 active hook，controller 视其'
required R5-REMOVAL-SEMANTICS ZH '不为它执行 marker 匹配，清理遗留 runtime labels，并按已放行'
required R5-REMOVAL-SEMANTICS ZH 'hook 的转换继续'
required R5-REMOVAL-SEMANTICS ZH '如果删除过程中移除 preDelete，清理 finalizer 后删除可以继续'
# reversed round-2/3 in-flight admission guard stays absent
forbidden R5-REMOVAL-SEMANTICS EN 'Admission must reject lifecycle changes while an affected RI has a non-empty `hook-state`'
forbidden R5-REMOVAL-SEMANTICS ZH '准入必须在受影响 RI 的 `hook-state` 非空时拒绝修改 lifecycle'

# --- D. Enablement-during-deletion, round-5 form (replaces the reworded
# --- CARRY-FINALIZER-ORDERING pin; substance unchanged: no finalizer after
# --- deletion begins, so the hook is never exposed).

required R5-FINALIZER-ORDERING EN 'New RIs are created with the reserved finalizer when their effective configuration includes `preDelete`'
required R5-FINALIZER-ORDERING EN 'the controller adds the finalizer before exposing the effective `preDelete` configuration'
required R5-FINALIZER-ORDERING EN 'If the RI already has a deletionTimestamp, the controller cannot add the finalizer, so the hook is never exposed on that RI and its deletion continues without `preDelete`'
required R5-FINALIZER-ORDERING EN 'Kubernetes does not allow a new finalizer to be added after deletion has begun'
required R5-FINALIZER-ORDERING ZH '如果 RI 已有 deletionTimestamp'
required R5-FINALIZER-ORDERING ZH 'controller 无法补充 finalizer，因此该 RI 不暴露 preDelete'
required R5-FINALIZER-ORDERING ZH 'Kubernetes 不允许删除开始后新增 finalizer'

# --- E. F-TESTPLAN-ABSENCE-CONTRADICTION fix (nit closed this round): the
# --- integration-test bullet now agrees with the absence semantics, and the
# --- stale round-3 bullet is gone in both languages.

required R5-TESTPLAN-DISABLE EN 'Hook disablement stops marker matching on the next reconciliation and cleans stale runtime labels and the reserved RI finalizer, even when another lifecycle operation is unfinished'
required R5-TESTPLAN-DISABLE ZH '下一次 reconcile 起停止 marker 匹配'
required R5-TESTPLAN-DISABLE ZH '清理遗留运行时 labels 和 RI 上的保留 finalizer'
required R5-TESTPLAN-DISABLE ZH '即使其他 lifecycle 操作尚未完成'
forbidden R5-TESTPLAN-DISABLE EN 'disablement during an unfinished operation does not skip the marker'
forbidden R5-TESTPLAN-DISABLE ZH '操作未完成时禁用不得跳过 marker'
# the unit-test bullet must still state the same semantics (round-4 pin, carry)
required R5-TESTPLAN-DISABLE EN 'Removing an active hook uses absence semantics: the controller performs no marker matching for it, removes its stale runtime labels, and follows the released-hook transition'
required R5-TESTPLAN-DISABLE ZH '删除 active hook 后按“不存在”语义处理：controller 不为它执行 marker 匹配，清理遗留 runtime labels，并按已放行 hook 的转换继续'

# --- F. F-STALE-LABEL-DISPOSITION fix (nit closed this round): one
# --- disposition rule for a removed hook's runtime labels, both languages,
# --- matching the normal-disable paragraph.

required R5-STALE-LABEL-DISPOSITION EN 'the controller stops matching its markers, removes stale runtime labels owned by the protocol, and, for `preDelete`, removes the reserved RI finalizer'
required R5-STALE-LABEL-DISPOSITION ZH 'controller 停止匹配其 marker'
required R5-STALE-LABEL-DISPOSITION ZH '清理协议拥有的遗留运行时 labels；对 preDelete 还要在正常配置收敛中'
required R5-STALE-LABEL-DISPOSITION ZH '清理 RI 上的保留 finalizer'

# --- G. F-COEXIST-GATE-EDGE partial fix: gate-off rule for the new protocol
# --- fields, both languages (gate-on for dual-configured objects and the
# --- deprecation timeline remain open — see the findings ledger).

required R5-GATE-OFF-RULE EN 'The gate defaults to false\. While it is disabled, the new protocol fields are rejected and the legacy RIS lifecycle protocol retains its existing behavior'
required R5-GATE-OFF-RULE ZH '该 gate 默认关闭'
required R5-GATE-OFF-RULE ZH '关闭期间拒绝新协议字段，旧 RIS lifecycle 协议保持现有行为'
required R5-GATE-OFF-RULE EN 'Disabling or re-enabling the feature leaves in-flight markers and unfinished phases intact'
required R5-GATE-OFF-RULE ZH '关闭或重新启用功能时，执行中的 marker 和未完成阶段保持不变'

# --- H. New round-5 normative content (differential pins; none of this
# --- exists on base or the round-4 head).

# foreground-delete admission mechanism
required R5-FOREGROUND-WEBHOOK EN 'The webhook intercepts those DELETE requests, checks `DeleteOptions\.propagationPolicy`, and rejects `Foreground`'
required R5-FOREGROUND-WEBHOOK EN 'it resolves reachable RIs through owner references and cached informers'
required R5-FOREGROUND-WEBHOOK ZH 'webhook 拦截这些 DELETE 请求，检查 `DeleteOptions\.propagationPolicy`，并拒绝 `Foreground`'
required R5-FOREGROUND-WEBHOOK ZH '通过 owner reference 和 informer 缓存解析可达 RI'
# handler keys: reserved prefix rejected (stronger than exact-key equality)
required R5-RESERVED-PREFIX EN 'rejected when they equal the exact hook-state key or use the reserved `rbg\.workloads\.x-k8s\.io/` prefix'
required R5-RESERVED-PREFIX ZH 'handler key 也不能等于固定的 hook-state key 或使用保留的 `rbg\.workloads\.x-k8s\.io/` 前缀'
# explicit field ownership
required R5-FIELD-OWNERSHIP EN 'the controller owns phase, `hook-state`, the effective RI snapshot, and the reserved finalizer; the executor only removes markers'
required R5-FIELD-OWNERSHIP ZH 'controller 拥有 phase、`hook-state`、RI 生效快照和保留 finalizer；executor 只负责清除 marker'
# hook-state value overlap clarification
required R5-HOOKSTATE-OVERLAP EN '`Updating`, `Recovering`, and `Terminating` appear in both fields because the hook-state records which operation is in progress; it is cleared once no operation or background hook remains'
required R5-HOOKSTATE-OVERLAP ZH '在两个字段中同名，是因为 hook-state 记录当前操作'
required R5-HOOKSTATE-OVERLAP ZH '没有进行中的操作或后台 hook 时清空'
# multi-marker waiting rule retained after the shared-marker-section rewrite
required R5-MULTI-MARKER EN 'Multiple labels on a pre-operation hook represent multiple coordination conditions; the controller proceeds only after none of them match'
required R5-MULTI-MARKER ZH '前置 hooks 的多个 marker 只要仍有一个匹配，controller 就保持等待'
# preDelete executor must be independently deployed
required R5-PREDELETE-EXECUTOR EN 'The `preDelete` executor must be independently deployed and available before the application Pod is deleted'
required R5-PREDELETE-EXECUTOR ZH '的执行者必须独立部署'
required R5-PREDELETE-EXECUTOR ZH '并在业务 Pod 删除前可用'

# --- I. F-ZH-ENGLISH-TERMS under the round-5 wording (the zh file now uses
# --- 用户拥有的期望配置; the translated 快照 term and the absence of the
# --- English phrases are what matter).

required R5-ZH-TERMS ZH '期望快照'
forbidden R5-ZH-TERMS ZH 'source of truth'
forbidden R5-ZH-TERMS ZH 'desired snapshot'

# --- J. Round-4 carry contracts: re-run the round-4 script and accept ONLY
# --- the disclosed superseded pin wordings as failures. Anything else red
# --- is a carry regression and fails this script.

R4_OUT="$(mktemp)"
CHECK_ROUND4_OUT="$(mktemp -d)" bash "$(dirname "$0")/check_round4_contracts.sh" "$ROOT" >"$R4_OUT" 2>&1
r4_status=$?

# Superseded round-4 pins (wording replaced by the round-5 rewrite; substance
# re-pinned in sections A-I above). A round-4 FAIL is tolerated only if its
# pattern matches one of these fixed strings.
SUPERSeded_FAILS=0
superseded() { # grep -F against the FAIL line pattern
  if grep -Fq "MISSING: $1" "$R4_OUT"; then
    echo "SUPERSEDED (round-4 wording, re-pinned above): $1" | tee -a "$OUT"
    SUPERSeded_FAILS=$((SUPERSeded_FAILS + 1))
  fi
}
superseded 'The new protocol coexists with the legacy RoleInstanceSet lifecycle protocol, but the two are mutually exclusive on the same RIS'
superseded 'An admission webhook must reject a Role/RIS combination in which the new Role-level lifecycle and a legacy user-configured RIS lifecycle are both non-empty'
superseded 'must not be independently edited or combined with the legacy protocol'
superseded '新协议与旧 RoleInstanceSet lifecycle 协议共存，但同一个 RIS 上二者互斥'
superseded '准入 webhook 必须拒绝新 Role-level lifecycle 与用户配置的旧 RIS lifecycle 同时非空'
superseded '不得独立修改，也不得与旧协议混用'
superseded 'If `preDelete` is removed while deletion is in progress, the controller treats the hook as absent, performs no marker matching, and removes the reserved finalizer so deletion may continue'
superseded '使删除可以继续'
superseded '唯一事实来源'
superseded 'Enablement is rejected or deferred if the RI already has a deletionTimestamp'

# every remaining round-4 FAIL is a genuine carry regression
carry_failures="$(grep -E '^FAIL' "$R4_OUT" | grep -vFf <(printf '%s\n' \
  'MISSING: The new protocol coexists with the legacy RoleInstanceSet lifecycle protocol, but the two are mutually exclusive on the same RIS' \
  'MISSING: An admission webhook must reject a Role/RIS combination in which the new Role-level lifecycle and a legacy user-configured RIS lifecycle are both non-empty' \
  'MISSING: must not be independently edited or combined with the legacy protocol' \
  'MISSING: 新协议与旧 RoleInstanceSet lifecycle 协议共存，但同一个 RIS 上二者互斥' \
  'MISSING: 准入 webhook 必须拒绝新 Role-level lifecycle 与用户配置的旧 RIS lifecycle 同时非空' \
  'MISSING: 不得独立修改，也不得与旧协议混用' \
  'MISSING: If `preDelete` is removed while deletion is in progress, the controller treats the hook as absent, performs no marker matching, and removes the reserved finalizer so deletion may continue' \
  'MISSING: 使删除可以继续' \
  'MISSING: 唯一事实来源' \
  'MISSING: Enablement is rejected or deferred if the RI already has a deletionTimestamp' \
) || true)"
if [ -n "$carry_failures" ]; then
  echo "$carry_failures" | tee -a "$OUT"
  echo "FAIL  R5-CARRY-REGRESSION (a round-4 pin that is NOT in the disclosed supersession list failed — see above)" | tee -a "$OUT"
  overall=1
else
  echo "PASS  R5-CARRY (all non-superseded round-4 carry pins green on this ref; superseded wordings: $SUPERSeded_FAILS)" | tee -a "$OUT"
fi

# F-SEEALSO-502 (still OPEN — reported, does not gate the exit code).
if [ -e "$ROOT/keps/502-workload-status-model" ]; then
  echo "PASS  F-SEEALSO-502 (target /keps/502-workload-status-model now exists in repo)" | tee -a "$OUT"
else
  echo "OPEN  F-SEEALSO-502 (see-also target /keps/502-workload-status-model still absent from repo)" | tee -a "$OUT"
fi

echo | tee -a "$OUT"
if [ "$overall" -eq 0 ]; then
  echo "ROUND5 CONTRACTS: all round-5 and carry contracts hold" | tee -a "$OUT"
else
  echo "ROUND5 CONTRACTS: one or more contracts missing" | tee -a "$OUT"
fi
exit "$overall"
