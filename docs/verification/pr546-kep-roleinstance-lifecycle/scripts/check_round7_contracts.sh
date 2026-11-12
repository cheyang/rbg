#!/usr/bin/env bash
# KEP-527 (PR #546) round-7 contract checks.
#
# Round 7 (delta c3ea6547..d038c23b, docs-only, 1 commit "docs: address
# round-6 review findings in KEP-527") responds to every open round-6 item:
#   - Foreground-deletion admission is REDESIGNED: the webhook no longer
#     resolves reachable RIs through informer caches at all; while
#     RoleInstanceLifecycleHooks is enabled, admission rejects EVERY
#     Foreground propagation at the RBGSet/RBG/RIS/RI deletion entries
#     (closes F-C4541237496's stale-cache-on-successful-lookup hole and
#     re-closes F-C4528291011 with a simpler, cache-free rule).
#   - KEP-502 (PR #528) MERGED upstream on 2026-11-12 (commit c2896cc0):
#     the KEP now says "now merged", states the five phase values
#     (verified against the merged KEP-502 phase table), and DISCLOSES that
#     the merged KEP-502 text still references the round-6-removed
#     markPodNotReady gate, gating implementation on a KEP-502 follow-up
#     amendment (closes F-KEP502-GATE-REFERENCE-DRIFT on the KEP-527 side).
#   - Marker-key record is added to the field-ownership enumeration, and
#     users / metadata sync / executors are barred from writing it
#     (closes F-MARKERKEY-RECORD-OWNERSHIP).
#   - "Mirrors are not authenticated" trust-model paragraph
#     (closes F-POD-MIRROR-SPOOF-WINDOW).
#   - Admission converse rule: owning RIS selectors may not use
#     controller-reserved keys (closes F-SELECTOR-RESERVED-KEY-REVERSE).
#   - Uniqueness check spans configuration generations: no signal-key
#     reassignment between hooks while any governed RI still records the key
#     (closes F-C4541237385).
#   - "Gate edges" paragraph: no retroactive validation, dual-configured
#     objects corrected before next update, gate-off controller behavior,
#     inPlaceUpdate deprecation contract (closes F-COEXIST-GATE-EDGE and
#     AC-KEP-REPLACEMENT-RULE).
#   - Rollback precondition strengthened with "no reserved preDelete
#     finalizer remaining" (body + test plan, EN+ZH).
#   - Risk-table row + Troubleshooting bullet acknowledge the mid-operation
#     hook-removal barrier skip and the one-way preDelete release
#     (closes F-CONFIG-REMOVAL-SKIPS-HOOK / F-PREDELETE-REMOVAL-ONE-WAY
#     acknowledgment residue).
#   - zh rollout-risk list drops the stale "Ready 循环等待" entry, restoring
#     zh/en parity (closes F-ZH-EN-ROLLOUT-RISK-PARITY).
#   - Scalability section adds the signal-label mirror fan-out cost and
#     scale-test coverage (closes F-SCALABILITY-POD-MIRROR-WRITES).
#   - Unit bullet gains the reserved `rbg.workloads.x-k8s.io/` prefix clause
#     (closes F-TESTPLAN-PREFIX-COVERAGE).
#
# HARNESS-UPDATE (disclosed): the round-7 edits reword/supersede 19 round-6
# and round-5 carry pins (foreground webhook design replaced; ownership list
# extended; rollback precondition strengthened; "can reach an RI" scope
# broadened to "gate enabled"). Their substance survives under the round-7
# wording and is re-pinned below. Round-6 FAILs are tolerated ONLY for those
# disclosed wordings; anything else red in check_round6_contracts.sh is a
# carry regression and fails this script.
#
# Pins are exact fixed-string literals extracted from the whitespace-flattened
# head text (hard-wrap positions baked in), matched with grep -F.
#
# Usage: bash check_round7_contracts.sh <repo-root-of-ref-under-test>
# Exit 0 iff every round-7 contract (and every non-superseded round-6 carry
# contract) holds.
set -u
ROOT="${1:?usage: check_round7_contracts.sh <repo-root>}"
KEP="$ROOT/keps/527-roleinstance-lifecycle"

OUT_DIR="${CHECK_ROUND7_OUT:-$(cd "$(dirname "$0")/../results" && pwd)}"
mkdir -p "$OUT_DIR"
OUT="$OUT_DIR/round7-contracts.txt"
: >"$OUT"

flatten() { tr '\n' ' ' <"$1" 2>/dev/null | tr -s ' '; }
EN="$(flatten "$KEP/README.md")"
ZH="$(flatten "$KEP/README.zh-CN.md")"

overall=0

required() { # required <id> <lang-var> <literal>
  local id="$1"; local var="$2"; local lit="$3"
  local content; eval "content=\"\$$var\""
  if printf '%s' "$content" | grep -Fq -- "$lit"; then
    echo "PASS  $id [$var] (required present: ${lit:0:70}...)" | tee -a "$OUT"
  else
    echo "FAIL  $id [$var] (required MISSING: $lit)" | tee -a "$OUT"
    overall=1
  fi
}

forbidden() { # forbidden <id> <lang-var> <literal>
  local id="$1"; local var="$2"; local lit="$3"
  local content; eval "content=\"\$$var\""
  if printf '%s' "$content" | grep -Fq -- "$lit"; then
    echo "FAIL  $id [$var] (forbidden PRESENT: $lit)" | tee -a "$OUT"
    overall=1
  else
    echo "PASS  $id [$var] (forbidden absent: $lit)" | tee -a "$OUT"
  fi
}

# --- disclosed round-7 supersessions over the round-6 pin set
SUPERSEDED_R7=(
  "The rollback precondition is an absent \`hook-state\` label together with an empty or absent marker-key record"
  "回滚前置条件是：\`hook-state\` label 不存在，且 marker key 记录为空或不存在"
  "Resolution is fail-closed: when the cache has not yet observed a newly created RI or a recently enabled \`preDelete\`"
  "解析采用 fail-closed 语义：缓存尚未观察到新建 RI 或刚启用的 preDelete 时"
  "webhook 无法证明删除安全，应拒绝或暂缓该请求"
  "A cache miss must not establish that deletion is safe"
  "cache miss 不能证明删除安全"
  "Foreground-deletion admission is fail-closed: requests that race RI creation or \`preDelete\` enablement are rejected or deferred while the informer cache is stale"
  "foreground 删除准入采用 fail-closed"
  "the controller owns phase, \`hook-state\`, the effective RI snapshot, the reserved finalizer, and the mirrored signal labels on member Pods; the executor only removes markers"
  "controller 拥有 phase、\`hook-state\`、RI 生效快照、保留 finalizer 以及成员 Pod 上的"
  "回滚前置条件是 \`hook-state\` label 不存在且记录为空"
  "The webhook intercepts those DELETE requests, checks \`DeleteOptions"
  "it resolves reachable RIs through owner references and cached informers"
  "webhook 拦截这些 DELETE 请求，检查 \`DeleteOptions"
  "通过 owner reference 和 informer 缓存解析可达 RI"
  "for RBGSet, RBG, RIS, or RI deletion requests that can reach an RI"
  "RBGSet、RBG、RIS 或 RI"
  "OPEN  F-SEEALSO-502"
  "R6-CARRY-REGRESSION"
)

# --- round-7 pins (exact fixed-string literals from whitespace-flattened head d038c23b; grep -F)
required R7-FOREGROUND-BLANKET EN "While \`RoleInstanceLifecycleHooks\` is enabled, admission rejects every \`Foreground\` propagation for RBGSet, RBG, RIS, and RI deletion requests"
required R7-FOREGROUND-NO-CACHE EN "does not depend on informer caches: neither a cache miss nor a successful lookup can prove that no reachable RI has \`preDelete\` in effect, because RI creation or hook enablement may have committed before its watch event arrived"
required R7-FOREGROUND-RETRY EN "The client may retry with background propagation"
required R7-FOREGROUND-ROLLOUT EN "While the feature gate is enabled, admission rejects \`Foreground\` propagation at every RBGSet, RBG, RIS, and RI deletion entry"
required R7-FOREGROUND-TESTBULLET EN "Foreground deletion is rejected for RBGSet, RBG, RIS, and RI while the feature gate is enabled, including requests that race RI creation or \`preDelete\` enablement"
required R7-KEP502-MERGED EN "The phase model is defined by KEP-502, now merged"
required R7-KEP502-DRIFT EN "The merged KEP-502 text still references the \`markPodNotReady\` lifecycle gate from an earlier revision of this proposal"
required R7-KEP502-AMENDMENT EN "a KEP-502 follow-up amendment must remove those references before implementation"
required R7-KEP502-PREIMPL EN "the follow-up amendment removing its stale \`markPodNotReady\` references"
required R7-OWNERSHIP-RECORD EN "the controller owns phase, \`hook-state\`, the effective RI snapshot, the marker-key record, the reserved finalizer, and the mirrored signal labels on member Pods"
required R7-OWNERSHIP-NONWRITERS EN "Users, metadata synchronization, and executors must not write the marker-key record"
required R7-MIRROR-UNAUTH EN "Mirrors are not authenticated. Any actor with Pod update permission can write or clear these keys until the next reconciliation re-syncs them, so the RI is the only authoritative source; safety-relevant consumers must verify the RI rather than trust a mirror alone"
required R7-RISK-REMOVAL-ROW EN "Active hook removed mid-operation | Barrier skipped; operator confirms"
required R7-TROUBLESHOOT-ONEWAY EN "The \`preDelete\` release is one-way — the finalizer cannot be re-added once deletion has begun — so operators must confirm the hook is no longer required before removing it"
required R7-SELECTOR-CONVERSE EN "Conversely, admission rejects an owning RIS selector that uses a controller-reserved key such as the hook-state key: the controller's own runtime writes would repeatedly match and unmatch the RI"
required R7-UNIQ-GENERATIONS EN "The uniqueness check also spans configuration generations: a signal key may not be reassigned from one hook to another while any RI governed by the configuration still records that key, so a retired executor cannot remove a marker that now belongs to a different hook, such as a pending \`postReady\` key reassigned to \`preDelete\`"
required R7-UNIQ-TESTBULLET EN "rejects reassigning a signal key to a different hook while any affected RI still records it, so a retired \`postReady\` execution cannot remove a \`preDelete\` marker"
required R7-ROLLBACK-BODY EN "The rollback precondition is an absent \`hook-state\` label, an empty or absent marker-key record, and no reserved \`preDelete\` finalizer remaining"
required R7-ROLLBACK-TEST EN "rollback precondition is an absent \`hook-state\` label, an empty record, and no reserved \`preDelete\` finalizer"
required R7-GATE-EDGES-DUAL EN "enabling the gate does not retroactively validate existing objects, but the mutual-exclusion rule applies to every subsequent write, so an object that already carries both the legacy and new configurations must be corrected before its next update"
required R7-GATE-EDGES-OFF EN "With the gate disabled, the controller neither writes nor matches new-protocol runtime labels; existing \`hook-state\`, markers, and the marker-key record remain for a compatible controller to finish"
required R7-INPLACEUPDATE-DEPRECATION EN "The legacy \`inPlaceUpdate\` hook remains supported for standalone RIS users until a deprecation release is announced"
required R7-ROLLOUT-RISK-EN EN "The main risks are repeated marker writes by the controller, removal of new markers by old executors, and indefinitely stalled deletion"
required R7-SCALABILITY-FANOUT EN "signal-label mirror fan-out (one RI write plus one write per member Pod per signal transition, re-checked on every reconciliation)"
required R7-SCALABILITY-TEST EN "mirror fan-out on multi-Pod instances"
required R7-PREFIX-TESTBULLET EN "rejects marker keys that collide with the controller state key or use the reserved \`rbg.workloads.x-k8s.io/\` prefix"
required R7-FOREGROUND-BLANKET ZH "开启期间，准入拒绝 RBGSet、RBG、RIS 和 RI 所有使用 \`Foreground\` 传播的删除请求"
required R7-FOREGROUND-NO-CACHE ZH "不依赖 informer 缓存：cache miss 和成功 lookup 都无法证明没有可达 RI 启用 preDelete，因为新建 RI 或刚启用 的 hook 可能已提交而 watch 事件尚未到达"
required R7-FOREGROUND-RETRY ZH "客户端可以改用 background 传播重试"
required R7-FOREGROUND-ROLLOUT ZH "RI 删除入口的 Foreground 传播一律被准入拒绝"
required R7-FOREGROUND-TESTBULLET ZH "feature gate 开启期间，RBGSet、RBG、RIS 和 RI 的 foreground 删除均被拒绝"
required R7-KEP502-MERGED ZH "phase 模型来自 KEP-502，现已合入"
required R7-KEP502-DRIFT ZH "已合入的 KEP-502 文本仍引用早期提案中的 \`markPodNotReady\` lifecycle 门控"
required R7-KEP502-AMENDMENT ZH "需要 KEP-502 后续 修订删除这些引用后才能实现"
required R7-KEP502-PREIMPL ZH "后续修订删除 其遗留的 \`markPodNotReady\` 引用"
required R7-OWNERSHIP-RECORD ZH "controller 拥有 phase、\`hook-state\`、RI 生效快照、marker key 记录、保留 finalizer 以及 成员 Pod 上的镜像信号标签"
required R7-OWNERSHIP-NONWRITERS ZH "用户、metadata 同步和 executor 都不得写 marker key 记录"
required R7-OWNERSHIP-METASYNC ZH "普通 RIS metadata 同步不得覆盖这些运行时标记和 marker key 记录"
required R7-MIRROR-UNAUTH ZH "镜像不可信。任何有 Pod 更新权限的对象都能在下一次 reconcile 同步前 写入或清除这些 key，因此 RI 是唯一权威来源；安全相关的消费者必须 回验 RI，不能只信镜像"
required R7-RISK-REMOVAL-ROW ZH "操作中删除 hook 配置 | 屏障被跳过；由运维确认删除时机"
required R7-TROUBLESHOOT-ONEWAY ZH "preDelete 的放行是单向的—— 删除开始后无法重新添加 finalizer——运维必须先确认该 hook 不再需要 再删除配置"
required R7-SELECTOR-CONVERSE ZH "反过来，准入也拒绝在所属 RIS selector 中使用 hook-state key 等 controller 保留 key"
required R7-UNIQ-GENERATIONS ZH "唯一性校验同样覆盖配置代际：只要受该配置管理的任一 RI 仍记录某个 信号 key，该 key 不得被重新分配给另一个 hook"
required R7-UNIQ-TESTBULLET ZH "拒绝在受影响 RI 仍记录某个信号 key 时将其重新分配给另一个 hook"
required R7-ROLLBACK-BODY ZH "回滚前置条件是：\`hook-state\` label 不存在、marker key 记录为空或不存在， 且没有保留的 preDelete finalizer 残留"
required R7-ROLLBACK-TEST ZH "回滚前置条件是 \`hook-state\` label 不存在、记录为空且没有保留的 preDelete finalizer"
required R7-GATE-EDGES-DUAL ZH "开启 gate 不会回溯校验存量对象，但互斥规则作用于后续每次 写入，已同时携带新旧配置的对象必须在下一次更新前修正"
required R7-GATE-EDGES-OFF ZH "gate 关闭期间， controller 不写入也不匹配新协议运行时 labels；存量 \`hook-state\`、 marker 和 marker key 记录保留给兼容 controller 收尾"
required R7-INPLACEUPDATE-DEPRECATION ZH "旧 \`inPlaceUpdate\` hook 在公布弃用版本前继续支持 standalone RIS 用户"
required R7-ROLLOUT-RISK ZH "主要风险是 controller 重复写入 marker、旧 executor 清除新 marker 和删除持续等待"
required R7-SCALABILITY-FANOUT ZH "信号标签镜像扇出（每次信号转换 1 次 RI 写入加每个成员 Pod 各 1 次写入，且每次 reconcile 重新核对）"
required R7-SCALABILITY-TEST ZH "多 Pod 实例的镜像 扇出"
required R7-PREFIX-TESTBULLET ZH "marker key 与 controller 状态 key 冲突或使用保留的 \`rbg.workloads.x-k8s.io/\` 前缀"
forbidden R7-NO-OLD-INFORMER-RESOLVE EN "resolves reachable RIs through owner references and cached informers"
forbidden R7-NO-OLD-FAILCLOSED EN "Resolution is fail-closed"
forbidden R7-NO-OLD-INFORMER-RESOLVE ZH "通过 owner reference 和 informer 缓存解析可达 RI"
forbidden R7-NO-OLD-FAILCLOSED ZH "fail-closed 语义"
forbidden R7-NO-READY-CIRCULAR-WAIT ZH "循环等待"

# ---------------------------------------------------------------------------
# Round-6 carry: run the round-6 script and tolerate ONLY the disclosed
# round-7 supersessions (re-pinned above under the round-7 wording).
# ---------------------------------------------------------------------------
R6_OUT="$(mktemp)"
CHECK_ROUND6_OUT="$(mktemp -d)" bash "$(dirname "$0")/check_round6_contracts.sh" "$ROOT" >"$R6_OUT" 2>&1
r6_bad=0
while IFS= read -r line; do
  case "$line" in
    FAIL*|OPEN*)
      superseded=0
      for sp in "${SUPERSEDED_R7[@]}"; do
        if printf '%s' "$line" | grep -Fq -- "$sp"; then superseded=1; break; fi
      done
      if [ "$superseded" -eq 1 ]; then
        echo "SUPERSEDED (round-7 rewording/design change, re-pinned above): $line" | tee -a "$OUT"
      else
        echo "$line" | tee -a "$OUT"
        r6_bad=1
      fi
      ;;
  esac
done <"$R6_OUT"
# keep the full round-6 log for reference
cp "$R6_OUT" "$OUT_DIR/round6-contracts-inside-round7.txt"

if [ "$r6_bad" -eq 0 ]; then
  echo "PASS  R7-CARRY (all non-superseded round-6 pins green on this ref; round-7 supersessions disclosed above)" | tee -a "$OUT"
else
  echo "FAIL  R7-CARRY-REGRESSION (a round-6 pin that is NOT in the disclosed round-7 supersession list failed — see above and $OUT_DIR/round6-contracts-inside-round7.txt)" | tee -a "$OUT"
  overall=1
fi

echo "ROUND7 CONTRACTS: $([ "$overall" -eq 0 ] && echo 'all green' || echo 'one or more contracts missing')" | tee -a "$OUT"
exit "$overall"
