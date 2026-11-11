#!/usr/bin/env bash
# KEP-527 (PR #546) round-6 contract checks.
#
# Round 6 (delta 6c15cc92..c3ea6547, docs-only, +322/-256, 5 commits) is a
# REAL DESIGN CHANGE, not a wording pass:
#   - markNotReady / RI-Ready gating is REMOVED from the new protocol (commit
#     7018a526); every hook now contains only `signalLabels`, RoleInstanceCustomReady
#     and the "Ready Gating" section are gone, and "providing a readiness-gating
#     field or changing RI Ready" is an explicit Non-Goal.
#   - `labelsHandler` is renamed `signalLabels` (commit 0448e977).
#   - NEW: the controller mirrors signal-label keys onto member Pods
#     ("Signal Label Propagation to Pods"), unidirectional RI->Pod.
#   - NEW: marker-key record annotation rbg.workloads.x-k8s.io/role-instance-hook-marker-keys
#     (commits f7166771 + c3ea6547) — durable tracking of protocol-owned marker
#     keys, closing advisory C4528291076 at the design level.
#   - NEW admission safeguards: marker keys used by the owning RIS
#     spec.selector (matchLabels / matchExpressions, incl. DoesNotExist-only)
#     are rejected (closes advisory C4528290874), and signal label keys declared
#     in the Pod template labels are rejected.
#   - The foreground-deletion webhook resolution is now explicitly fail-closed
#     (closes advisory C4528291011).
#
# HARNESS-UPDATE (disclosed): the design change invalidates 14 round-5 pin
# wordings (independent-type field lists, gate-lifetime, label-syntax,
# stale-label disposition, zh disable bullet, zh reserved-prefix, field
# ownership EN+ZH). Their substance either survives under the round-6 wording
# (re-pinned below) or is deliberately REMOVED (markNotReady gate lifetime —
# retired; there is no readiness gate to have a lifetime). The round-5 FAILs
# below are tolerated ONLY for those disclosed wordings; anything else red in
# check_round5_contracts.sh is a carry regression and fails this script.
#
# New round-6 normative content is pinned differentially: expected red on
# previous head 6c15cc92 and base, green on head c3ea6547.
#
# Matching is done against whitespace-flattened file content because the KEP
# hard-wraps paragraphs, so patterns may span physical lines.
#
# Usage: bash check_round6_contracts.sh <repo-root-of-ref-under-test>
# Exit 0 iff every round-6 contract (and every non-superseded round-5 carry
# contract) holds.
set -u
ROOT="${1:?usage: check_round6_contracts.sh <repo-root>}"
KEP="$ROOT/keps/527-roleinstance-lifecycle"

OUT_DIR="${CHECK_ROUND6_OUT:-$(cd "$(dirname "$0")/../results" && pwd)}"
mkdir -p "$OUT_DIR"
OUT="$OUT_DIR/round6-contracts.txt"
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

# --- A. markNotReady / RI-Ready gating REMOVED (round-6 design decision).
# --- Replaces the retired CARRY-GATE-LIFETIME / F-INDEPENDENT-TYPE round-4/5
# --- pins: there is no readiness gate anymore, so "no hook changes RI Ready"
# --- is the new normative contract.

required R6-NO-READY-GATING EN 'The new protocol defines an independent hook type: every hook contains only `signalLabels`, and no hook changes RI Ready'
required R6-NO-READY-GATING ZH '新协议定义独立的 hook 类型：每个 hook 只包含 `signalLabels`'
required R6-NO-READY-GATING EN 'Hook markers never participate in RI Ready computation'
required R6-NO-READY-GATING ZH 'hook marker 不参与 RI Ready 计算'
required R6-NO-READY-GATING EN 'Providing a readiness-gating field or changing RI Ready; applications control their own traffic ingress'
required R6-NO-READY-GATING ZH '不提供 Ready 门控字段或修改 RI Ready'
required R6-NO-READY-GATING EN 'Hooks do not change RI Ready, Pod Ready, EndpointSlice, or an external router; applications explicitly integrate traffic control'
required R6-NO-READY-GATING EN 'Every hook contains only `signalLabels`, and no hook gates RI Ready'
# the removed mechanism must be fully gone in both languages (markNotReady is
# a NEW-protocol name; only the legacy markPodNotReady may remain, in legacy
# context). RoleInstanceCustomReady must not appear at all anymore.
forbidden R6-NO-READY-GATING EN 'markNotReady'
forbidden R6-NO-READY-GATING ZH 'markNotReady'
forbidden R6-NO-READY-GATING EN 'RoleInstanceCustomReady'
forbidden R6-NO-READY-GATING ZH 'RoleInstanceCustomReady'
# wire table: single field row; readiness-condition row replaced by marker-key record
required R6-NO-READY-GATING EN 'Hook fields \| `signalLabels`'
required R6-NO-READY-GATING ZH 'hook 字段 \| `signalLabels`'
# test plan carries the new contract
required R6-NO-READY-GATING EN 'Hook markers never participate in RI Ready computation; a pending `postReady` marker does not prevent entry into `Running`'
required R6-NO-READY-GATING EN 'The new API exposes only `signalLabels` on every hook'
required R6-NO-READY-GATING ZH '新 API 的每个 hook 只暴露 `signalLabels`'

# --- B. signalLabels field semantics (renamed from labelsHandler; substance
# --- of the round-4 F-LABEL-SYNTAX / F-INDEPENDENT-TYPE pins re-pinned).

required R6-SIGNALLABELS EN 'Marker keys and values are user-configured through `signalLabels`; they are not project-wide constants'
required R6-SIGNALLABELS ZH '由用户通过 `signalLabels` 配置，不是项目级常量'
required R6-SIGNALLABELS EN 'Every `signalLabels` key and value must satisfy Kubernetes label syntax'
required R6-SIGNALLABELS EN 'A configured hook with an omitted or empty `signalLabels` is invalid and must be rejected by admission'
required R6-SIGNALLABELS ZH '配置了 hook 但 `signalLabels` 缺省或为空属于配置错误，准入校验必须拒绝'
required R6-SIGNALLABELS EN 'The new protocol exposes only `signalLabels`'
required R6-SIGNALLABELS ZH '新协议只暴露 `signalLabels`'
# the old field name may appear only in the legacy-context comparison sentence
required R6-SIGNALLABELS EN 'labelsHandler.*but changes marker initialization ownership'
required R6-SIGNALLABELS ZH '沿用现有 RIS `labelsHandler` 的协作思路'

# --- C. Marker-key record (NEW round-6 mechanism; closes advisory
# --- C4528291076 at the design level: durable record of retired keys).

required R6-MARKER-KEY-RECORD EN 'Marker-key record \| `rbg\.workloads\.x-k8s\.io/role-instance-hook-marker-keys`'
required R6-MARKER-KEY-RECORD ZH 'marker key 记录 \| `rbg\.workloads\.x-k8s\.io/role-instance-hook-marker-keys`'
required R6-MARKER-KEY-RECORD EN 'Marker-key Go constant \| `RoleInstanceHookMarkerKeysAnnotation`'
required R6-MARKER-KEY-RECORD ZH 'marker key 记录 Go 常量 \| `RoleInstanceHookMarkerKeysAnnotation`'
required R6-MARKER-KEY-RECORD EN 'records the marker keys owned by the protocol in the annotation `rbg.workloads.x-k8s\.io/role-instance-hook-marker-keys`, a comma-separated list'
required R6-MARKER-KEY-RECORD ZH '在 annotation `rbg\.workloads\.x-k8s\.io/role-instance-hook-marker-keys` 中记录协议拥有的'
required R6-MARKER-KEY-RECORD EN 'The record is updated in the same metadata write as the marker, and a key is removed only after the corresponding RI label and its Pod mirrors are confirmed absent'
required R6-MARKER-KEY-RECORD ZH '只有确认对应 RI label 及其 Pod 镜像均已不存在后才移除条目'
required R6-MARKER-KEY-RECORD EN 'After markerA changes to markerB, markerA stays recorded until its label and Pod mirrors are cleaned, so a restart can still distinguish protocol-owned stale labels from ordinary user labels'
required R6-MARKER-KEY-RECORD ZH 'markerA 在 label 和 Pod 镜像清理前保持记录'
required R6-MARKER-KEY-RECORD EN 'The rollback precondition is an absent `hook-state` label together with an empty or absent marker-key record'
required R6-MARKER-KEY-RECORD ZH '回滚前置条件是：`hook-state` label 不存在，且 marker key 记录为空或不存在'
# atomicity of the triple write (hook-state + marker + record)
required R6-MARKER-KEY-RECORD EN 'the controller sets `hook-state`, the corresponding marker, and its marker-key record in the same RI metadata write'
required R6-MARKER-KEY-RECORD EN '`hook-state`, the marker, and its marker-key record must be set in the same metadata write'
required R6-MARKER-KEY-RECORD ZH 'marker 与 marker key 记录同批写入'
# test plan
required R6-MARKER-KEY-RECORD EN 'The marker-key record is written with the marker, retained across restarts, and cleared only after the corresponding label and its Pod mirrors are removed'
required R6-MARKER-KEY-RECORD ZH 'marker key 记录与 marker 同批写入、跨重启保留，仅在对应 label 及其 Pod 镜像移除后清空'
required R6-MARKER-KEY-RECORD EN 'the old key remains recorded until its label and Pod mirrors are cleaned'
required R6-MARKER-KEY-RECORD ZH '旧 key 在 label 和 Pod 镜像清理前保持记录'

# --- D. Signal-label propagation to Pods (NEW round-6 mechanism).

required R6-POD-PROPAGATION EN 'the RI controller mirrors the corresponding RI label onto every member Pod'
required R6-POD-PROPAGATION ZH 'RI controller 都会把 RI 上对应的 label 镜像到每个成员 Pod'
required R6-POD-PROPAGATION EN 'Propagation is unidirectional from RI to Pod\. The RI is the only source of truth, and the Pod copies are controller-owned mirrors'
required R6-POD-PROPAGATION ZH '传递是单向的，只从 RI 到 Pod。RI 是唯一事实来源，Pod 上的副本由 controller 拥有'
required R6-POD-PROPAGATION EN 'Only `signalLabels` keys are mirrored; `hook-state` remains on the RI'
required R6-POD-PROPAGATION ZH '仅镜像 `signalLabels` 的 key；`hook-state` 只保留在 RI 上'
required R6-POD-PROPAGATION EN 'Executors still remove markers only on the RI; the removal reaches member Pods through the same propagation'
required R6-POD-PROPAGATION ZH 'executor 仍然只在 RI 上清除 marker；清除结果通过同一传递机制到达'
required R6-POD-PROPAGATION EN 'Propagation is a metadata-only Pod update\. It does not change the Pod template, create a new controller revision, or recreate Pods'
required R6-POD-PROPAGATION ZH '传递是仅修改 metadata 的 Pod 更新，不修改 Pod template、不生成新的 controller revision，也不重建 Pod'
required R6-POD-PROPAGATION EN 'The controller reconciles these keys on every RI reconciliation and batches the Pod updates it issues'
required R6-POD-PROPAGATION ZH 'controller 在每次 RI reconcile 时核对这些 key，并合并批量更新 Pod'
required R6-POD-PROPAGATION EN 'Signal label keys are controller-owned on member Pods\. Declaring the same key in the Pod template labels is a configuration error and is rejected by admission'
required R6-POD-PROPAGATION ZH '在 Pod template labels 中声明相同 key 属于配置错误，准入校验拒绝'
# test plan
required R6-POD-PROPAGATION EN 'Signal labels propagate from the RI to member Pods: present keys are copied, absent keys are removed, and value changes are synced; propagation is metadata-only and does not recreate Pods'
required R6-POD-PROPAGATION ZH '信号标签从 RI 传递到成员 Pod'
required R6-POD-PROPAGATION EN 'RI signal-label changes reach member Pods without revision changes, and marker removal on the RI removes the mirrored Pod labels'
required R6-POD-PROPAGATION ZH 'RI 信号标签变化在不修改 revision 的情况下到达成员 Pod'

# --- E. Admission safeguards (NEW round-6 rule; closes advisory C4528290874).

required R6-ADMISSION-SAFEGUARDS EN 'Admission also rejects a marker key used by the owning RIS `spec\.selector`, whether in `matchLabels` or in any `matchExpressions` entry, even when the key is absent from template labels'
required R6-ADMISSION-SAFEGUARDS ZH '准入还拒绝与所属 RIS `spec\.selector` 相关的 marker key'
required R6-ADMISSION-SAFEGUARDS EN 'the stateful RIS instance-reference logic then releases the RI.s owner reference'
required R6-ADMISSION-SAFEGUARDS ZH 'stateful RIS 的实例引用管理随后会释放该 RI 的'
# test plan
required R6-ADMISSION-SAFEGUARDS EN 'Admission rejects marker keys used by the owning RIS `spec\.selector` in `matchLabels` or `matchExpressions`, including keys selected only by `DoesNotExist`'
required R6-ADMISSION-SAFEGUARDS ZH '准入拒绝所属 RIS `spec\.selector` 在 `matchLabels` 或 `matchExpressions`'
required R6-ADMISSION-SAFEGUARDS EN 'Admission rejects signal label keys that are declared in the Pod template labels'
required R6-ADMISSION-SAFEGUARDS ZH '准入拒绝在 Pod template labels 中声明的信号标签 key'

# --- F. Fail-closed foreground-deletion webhook (NEW round-6 rule; closes
# --- advisory C4528291011).

required R6-FAIL-CLOSED-WEBHOOK EN 'Resolution is fail-closed: when the cache has not yet observed a newly created RI or a recently enabled `preDelete`, the webhook cannot prove the deletion is safe and rejects or defers the request'
required R6-FAIL-CLOSED-WEBHOOK ZH '解析采用 fail-closed 语义：缓存尚未观察到新建 RI 或刚启用的 preDelete 时'
required R6-FAIL-CLOSED-WEBHOOK ZH 'webhook 无法证明删除安全，应拒绝或暂缓该请求'
required R6-FAIL-CLOSED-WEBHOOK EN 'A cache miss must not establish that deletion is safe'
required R6-FAIL-CLOSED-WEBHOOK ZH 'cache miss 不能证明删除安全'
# test plan (integration)
required R6-FAIL-CLOSED-WEBHOOK EN 'Foreground-deletion admission is fail-closed: requests that race RI creation or `preDelete` enablement are rejected or deferred while the informer cache is stale'
required R6-FAIL-CLOSED-WEBHOOK ZH 'foreground 删除准入采用 fail-closed'

# --- G. Absence / disable semantics under the round-6 wording (round-5
# --- R5-STALE-LABEL-DISPOSITION / R5-TESTPLAN-DISABLE substance re-pinned,
# --- now including Pod mirrors via the marker-key record).

required R6-REMOVAL-SEMANTICS EN 'If an active hook is removed, the controller treats it as absent, stops matching its marker, removes its stale runtime labels, and follows the same transition as a released hook; for example, `PreUpdate` advances to `Updating`'
required R6-REMOVAL-SEMANTICS ZH '如果删除 active hook，controller 视其'
required R6-REMOVAL-SEMANTICS ZH '不为它执行 marker 匹配，清理遗留 runtime labels'
required R6-REMOVAL-SEMANTICS EN 'removes stale runtime labels and their Pod mirrors identified through the marker-key record, and, for `preDelete`, removes the reserved RI finalizer'
required R6-REMOVAL-SEMANTICS ZH '按 marker key 记录清理遗留运行时 labels 及其 Pod 镜像'
required R6-REMOVAL-SEMANTICS EN 'If `preDelete` is removed while deletion is in progress, removing the finalizer lets deletion continue'
required R6-REMOVAL-SEMANTICS ZH '如果删除过程中移除 preDelete，清理 finalizer 后删除可以继续'
required R6-REMOVAL-SEMANTICS EN 'Hook disablement stops marker matching on the next reconciliation and cleans stale runtime labels and the reserved RI finalizer, even when another lifecycle operation is unfinished'
required R6-REMOVAL-SEMANTICS ZH '清理遗留运行时 labels、其 Pod 镜像和 RI 上的保留 finalizer'

# --- H. Field ownership under the round-6 wording (round-5 R5-FIELD-OWNERSHIP
# --- substance re-pinned, extended with the mirrored signal labels).

required R6-FIELD-OWNERSHIP EN 'the controller owns phase, `hook-state`, the effective RI snapshot, the reserved finalizer, and the mirrored signal labels on member Pods; the executor only removes markers'
required R6-FIELD-OWNERSHIP ZH 'controller 拥有 phase、`hook-state`、RI 生效快照、保留 finalizer 以及成员 Pod 上的'
required R6-FIELD-OWNERSHIP ZH '镜像信号标签；executor 只负责清除 marker'

# --- Reserved-prefix rule under the round-6 wording (round-5
# --- R5-RESERVED-PREFIX zh substance re-pinned).

required R6-RESERVED-PREFIX EN 'Signal label keys must also be rejected when they equal the exact hook-state key or use the reserved `rbg\.workloads\.x-k8s\.io/` prefix'
required R6-RESERVED-PREFIX ZH '也不能等于固定的 hook-state key 或使用保留的 `rbg\.workloads\.x-k8s\.io/` 前缀'

# --- I. Rollback precondition in the downgrade section mentions Pod mirrors
# --- through the marker-key record (round-6 extension of F-PREDELETE-LIVENESS
# --- downgrade hygiene).

required R6-ROLLBACK-PRECONDITION EN 'has no `hook-state` label, remaining marker, or Pod mirror \(verified through the marker-key record\)'
required R6-ROLLBACK-PRECONDITION ZH '回滚前置条件是 `hook-state` label 不存在且记录为空'

# --- J. Round-5 carry contracts: re-run the round-5 script and accept ONLY
# --- the disclosed round-6 supersessions as failures. Anything else red is
# --- a carry regression and fails this script.

R5_OUT="$(mktemp)"
CHECK_ROUND5_OUT="$(mktemp -d)" bash "$(dirname "$0")/check_round5_contracts.sh" "$ROOT" >"$R5_OUT" 2>&1
r5_status=$?

# Superseded round-5 pins (wording replaced or mechanism removed by the
# round-6 design change; substance re-pinned in sections A-I above or — for
# the gate-lifetime pair — retired with the markNotReady mechanism itself).
SUPERSEDED_FAILS=0
superseded() { # grep -F against the FAIL line content
  if grep -Fq "MISSING: $1" "$R5_OUT"; then
    echo "SUPERSEDED (round-5 wording, re-pinned above or retired): $1" | tee -a "$OUT"
    SUPERSEDED_FAILS=$((SUPERSEDED_FAILS + 1))
  fi
}
superseded '清理遗留运行时 labels 和 RI 上的保留 finalizer'
superseded '清理协议拥有的遗留运行时 labels；对 preDelete 还要在正常配置收敛中'
superseded 'the controller stops matching its markers, removes stale runtime labels owned by the protocol, and, for `preDelete`, removes the reserved RI finalizer'
superseded 'handler key 也不能等于固定的 hook-state key 或使用保留的 `rbg\.workloads\.x-k8s\.io/` 前缀'
superseded 'the controller owns phase, `hook-state`, the effective RI snapshot, and the reserved finalizer; the executor only removes markers'
superseded 'controller 拥有 phase、`hook-state`、RI 生效快照和保留 finalizer；executor 只负责清除 marker'
superseded 'Every `labelsHandler` key and value must satisfy Kubernetes label syntax'
superseded 'The new protocol defines an independent hook type: pre-operation hooks contain `labelsHandler` and `markNotReady`, while `postReady` contains only `labelsHandler`'
superseded 'Pre-operation hook fields \| `labelsHandler`, `markNotReady` \|'
superseded 'postReady field \| `labelsHandler` \|'
superseded '前置 hooks 包含 `labelsHandler` 和 `markNotReady`'
superseded '前置 hook 字段 \| `labelsHandler`、`markNotReady` \|'
superseded 'remains until the RI object is deleted'
superseded '持续到 RI 对象删除'

# every remaining round-5 FAIL is a genuine carry regression
carry_failures="$(grep -E '^FAIL' "$R5_OUT" | grep -vF 'FAIL  R5-CARRY-REGRESSION' | grep -vFf <(printf '%s\n' \
  'MISSING: 清理遗留运行时 labels 和 RI 上的保留 finalizer' \
  'MISSING: 清理协议拥有的遗留运行时 labels；对 preDelete 还要在正常配置收敛中' \
  'MISSING: the controller stops matching its markers, removes stale runtime labels owned by the protocol, and, for `preDelete`, removes the reserved RI finalizer' \
  'MISSING: handler key 也不能等于固定的 hook-state key 或使用保留的 `rbg\.workloads\.x-k8s\.io/` 前缀' \
  'MISSING: the controller owns phase, `hook-state`, the effective RI snapshot, and the reserved finalizer; the executor only removes markers' \
  'MISSING: controller 拥有 phase、`hook-state`、RI 生效快照和保留 finalizer；executor 只负责清除 marker' \
  'MISSING: Every `labelsHandler` key and value must satisfy Kubernetes label syntax' \
  'MISSING: The new protocol defines an independent hook type: pre-operation hooks contain `labelsHandler` and `markNotReady`, while `postReady` contains only `labelsHandler`' \
  'MISSING: Pre-operation hook fields \| `labelsHandler`, `markNotReady` \|' \
  'MISSING: postReady field \| `labelsHandler` \|' \
  'MISSING: 前置 hooks 包含 `labelsHandler` 和 `markNotReady`' \
  'MISSING: 前置 hook 字段 \| `labelsHandler`、`markNotReady` \|' \
  'MISSING: remains until the RI object is deleted' \
  'MISSING: 持续到 RI 对象删除' \
) || true)"
if [ -n "$carry_failures" ]; then
  echo "$carry_failures" | tee -a "$OUT"
  echo "FAIL  R6-CARRY-REGRESSION (a round-5 pin that is NOT in the disclosed round-6 supersession list failed — see above)" | tee -a "$OUT"
  overall=1
else
  echo "PASS  R6-CARRY (all non-superseded round-5 carry pins green on this ref; superseded wordings: $SUPERSEDED_FAILS)" | tee -a "$OUT"
fi

# F-SEEALSO-502 (still OPEN — reported, does not gate the exit code).
if git -C "$ROOT" rev-parse --verify HEAD >/dev/null 2>&1; then
  if git -C "$ROOT" ls-tree -d HEAD keps/ | grep -q '502-'; then
    echo "OPEN->RESOLVED F-SEEALSO-502 (keps/502-* exists in this tree)" | tee -a "$OUT"
  else
    echo "OPEN  F-SEEALSO-502 (keps/502-workload-status-model still absent from this tree)" | tee -a "$OUT"
  fi
fi

if [ "$overall" -eq 0 ]; then
  echo "ROUND6 CONTRACTS: all round-6 pins green on this ref" | tee -a "$OUT"
  exit 0
else
  echo "ROUND6 CONTRACTS: one or more round-6 contracts missing" | tee -a "$OUT"
  exit 1
fi
