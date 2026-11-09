#!/usr/bin/env bash
# KEP-527 (PR #546) round-4 contract checks.
#
# Round 4 (delta fb6c2b36..43dab57b, docs-only, +219/-153) is a second design
# pivot: the round-3 REMOVAL of the legacy RoleInstanceSetSpec.lifecycle
# protocol is REVERSED back to coexistence with admission-enforced mutual
# exclusion, the new protocol now defines an INDEPENDENT hook type (wire names
# `labelsHandler` + `markNotReady`; legacy keeps `finalizersHandler` /
# `inPlaceUpdate` / `markPodNotReady`), and the round-2/3 in-flight
# config-change admission rejection is DELETED in favor of declarative
# "latest effective configuration / absence semantics".
#
# HARNESS-UPDATE (disclosed): the round-3 pins that pinned the removal wording
# (F-PROPAGATED-VS-LEGACY removal sentences), the markPodNotReady usage, and
# the in-flight admission 'must' are superseded by this pivot — they fail on
# head BY DESIGN because the author deleted the sentences they matched. They
# remain in check_round3_contracts.sh for reference and no longer gate. Their
# SUBSTANCE is re-pinned here under the round-4 wording:
#   - old/new boundary  -> mutual exclusion + distinct types/keys/controllers
#   - markPodNotReady   -> markNotReady (new type) vs markPodNotReady (legacy)
#   - in-flight guard   -> latest-effective-config + absence semantics
# The round-4 pins are differential: red on base 09accf63 and previous head
# fb6c2b36 (content absent), green on head 43dab57b (content present).
# Still-open F-SEEALSO-502 is reported as OPEN and does not gate the exit
# code.
#
# Matching is done against whitespace-flattened file content because the KEP
# hard-wraps paragraphs, so patterns may span physical lines.
#
# Usage: bash check_round4_contracts.sh <repo-root-of-ref-under-test>
# Exit 0 iff every fixed-finding contract holds.
set -u
ROOT="${1:?usage: check_round4_contracts.sh <repo-root>}"
KEP="$ROOT/keps/527-roleinstance-lifecycle"

OUT_DIR="${CHECK_ROUND4_OUT:-$(cd "$(dirname "$0")/../results" && pwd)}"
mkdir -p "$OUT_DIR"
OUT="$OUT_DIR/round4-contracts.txt"
: >"$OUT"

flatten() { tr '\n' ' ' <"$1" 2>/dev/null | tr -s ' '; }
EN="$(flatten "$KEP/README.md")"
ZH="$(flatten "$KEP/README.zh-CN.md")"
YAML="$(flatten "$KEP/kep.yaml")"

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

echo "== Round-4 contracts on $ROOT ==" | tee -a "$OUT"

# --- A. Round-4 pivot: coexistence + mutual exclusion (supersedes the
# --- round-3 removal wording for F-OLDNEW-ISOLATION / F-PROPAGATED-VS-LEGACY)

required F-OLDNEW-COEXIST EN 'The new protocol coexists with the legacy RoleInstanceSet lifecycle protocol, but the two are mutually exclusive on the same RIS'
required F-OLDNEW-COEXIST EN 'An admission webhook must reject a Role/RIS combination in which the new Role-level lifecycle and a legacy user-configured RIS lifecycle are both non-empty'
required F-OLDNEW-COEXIST EN 'the legacy protocol is executed by the RIS controller, while the new protocol is executed by the RI controller'
required F-OLDNEW-COEXIST EN 'The two runtime protocols must not be active on the same RIS; an admission webhook enforces that boundary'
required F-OLDNEW-COEXIST EN 'must not be independently edited or combined with the legacy protocol'
# the round-3 removal wording must not return (it would resurrect the
# issue #527 compatibility divergence that round 4 closes).
forbidden F-OLDNEW-COEXIST EN 'protocol is removed\. Admission rejects any use of that field'
forbidden F-OLDNEW-COEXIST EN 'neither writes nor interprets'
required F-OLDNEW-COEXIST ZH '新协议与旧 RoleInstanceSet lifecycle 协议共存，但同一个 RIS 上二者互斥'
required F-OLDNEW-COEXIST ZH '准入 webhook 必须拒绝新 Role-level lifecycle 与用户配置的旧 RIS lifecycle 同时非空'
required F-OLDNEW-COEXIST ZH '旧协议由 RIS controller 执行，新协议由 RI controller 执行'
required F-OLDNEW-COEXIST ZH '两套运行协议不得在同一 RIS 上同时生效，准入 webhook 强制该边界'
required F-OLDNEW-COEXIST ZH '不得独立修改，也不得与旧协议混用'
forbidden F-OLDNEW-COEXIST ZH '协议被移除。准入拒绝该字段的任何使用'

# --- B. Independent hook type (commit 43dab57b): wire names pinned, each
# --- name scoped to its own protocol, legacy retained and deprecated.

required F-INDEPENDENT-TYPE EN 'The new protocol defines an independent hook type: pre-operation hooks contain `labelsHandler` and `markNotReady`, while `postReady` contains only `labelsHandler`'
required F-INDEPENDENT-TYPE EN 'Pre-operation hook fields \| `labelsHandler`, `markNotReady` \|'
required F-INDEPENDENT-TYPE EN 'postReady field \| `labelsHandler` \|'
required F-INDEPENDENT-TYPE EN 'The legacy RIS protocol retains its existing `finalizersHandler` and `markPodNotReady` semantics'
required F-INDEPENDENT-TYPE EN 'its `inPlaceUpdate` hook is deprecated'
required F-INDEPENDENT-TYPE EN 'Legacy RIS field \| `RoleInstanceSet\.spec\.lifecycle` \|'
required F-INDEPENDENT-TYPE EN 'Legacy state key \| `rbg\.workloads\.x-k8s\.io/role-instance-lifecycle-state` \|'
# the new-protocol example must use the new field name only
forbidden F-INDEPENDENT-TYPE EN 'markPodNotReady: true'
required F-INDEPENDENT-TYPE ZH '新协议定义独立的 hook 类型'
required F-INDEPENDENT-TYPE ZH '前置 hooks 包含 `labelsHandler` 和 `markNotReady`'
required F-INDEPENDENT-TYPE ZH '前置 hook 字段 \| `labelsHandler`、`markNotReady` \|'
required F-INDEPENDENT-TYPE ZH '旧 RIS 协议继续保留 ?现有 `finalizersHandler` 和 `markPodNotReady` 语义'
required F-INDEPENDENT-TYPE ZH '其中 `inPlaceUpdate` 标记为 deprecated'
forbidden F-INDEPENDENT-TYPE ZH 'markPodNotReady: true'

# --- C. Latest-effective-config / absence semantics (commit df110cf0).
# --- This is the deliberate reversal of the round-2/3 in-flight admission
# --- guard: pin the new contract so it cannot silently flip back, and pin
# --- the reversed rule as absent. The safety analysis of this reversal is
# --- tracked as question F-PREDELETE-REMOVAL-ONE-WAY, not by this pin.

required F-LATEST-CONFIG-SEMANTICS EN 'The controller always evaluates the latest effective lifecycle configuration'
required F-LATEST-CONFIG-SEMANTICS EN 'takes effect on the next reconciliation without special in-flight handling'
required F-LATEST-CONFIG-SEMANTICS EN 'If `preDelete` is removed while deletion is in progress, the controller treats the hook as absent, performs no marker matching, and removes the reserved finalizer so deletion may continue'
required F-LATEST-CONFIG-SEMANTICS EN 'The change applies whether or not another lifecycle operation is in flight; no special active-hook state is retained'
forbidden F-LATEST-CONFIG-SEMANTICS EN 'Admission must reject lifecycle changes while an affected RI has a non-empty `hook-state`'
required F-LATEST-CONFIG-SEMANTICS ZH 'controller 始终按最新的生效 lifecycle 配置判断'
required F-LATEST-CONFIG-SEMANTICS ZH '不引入特殊的在途处理'
required F-LATEST-CONFIG-SEMANTICS ZH '如果删除过程中移除 preDelete'
required F-LATEST-CONFIG-SEMANTICS ZH '使删除可以继续'
forbidden F-LATEST-CONFIG-SEMANTICS ZH '准入必须在受影响 RI 的 `hook-state` 非空时拒绝修改'

# --- D. New normative rule: labelsHandler key/value label-syntax validation.

required F-LABEL-SYNTAX EN 'Every `labelsHandler` key and value must satisfy Kubernetes label syntax'
required F-LABEL-SYNTAX EN 'Admission rejects invalid marker keys or values before configuration is accepted'
required F-LABEL-SYNTAX EN 'The controller does not truncate, rewrite, or drop a configured marker to make it writable'
required F-LABEL-SYNTAX ZH '都必须满足 Kubernetes label 语法'
required F-LABEL-SYNTAX ZH '准入在配置被接受前拒绝非法 marker key 或'
required F-LABEL-SYNTAX ZH 'controller 不会为了可写而截断、改写或丢弃配置的 marker'

# --- E. Round-4 nit fixes

# F-INTERRUPT-WHEN-DEAD-AMBIGUITY: reworded this round.
required F-INTERRUPT-WHEN-DEAD-AMBIGUITY EN 'Update may interrupt Recovery; the previous engine may then be dead'
forbidden F-INTERRUPT-WHEN-DEAD-AMBIGUITY EN 'Update may interrupt Recovery when the previous engine is dead'
required F-INTERRUPT-WHEN-DEAD-AMBIGUITY ZH 'Update 可以打断 Recovery；此时旧 engine 可能已经失效'

# F-KEPYAML-GATE-SHAPE: object form per the repo template.
required F-KEPYAML-GATE-SHAPE YAML 'feature-gates: - name: RoleInstanceLifecycleHooks'
forbidden F-KEPYAML-GATE-SHAPE YAML 'feature-gates: - RoleInstanceLifecycleHooks'

# F-PROPAGATED-SNAPSHOT-EDIT: ownership rule restored with the coexistence
# pivot.
required F-PROPAGATED-SNAPSHOT-EDIT EN 'must not be independently edited or combined with the legacy protocol'
required F-PROPAGATED-SNAPSHOT-EDIT ZH '不得独立修改，也不得与旧协议混用'

# F-ZH-ENGLISH-TERMS: translated this round.
required F-ZH-ENGLISH-TERMS ZH '唯一事实来源'
required F-ZH-ENGLISH-TERMS ZH '期望快照'
forbidden F-ZH-ENGLISH-TERMS ZH 'source of truth'
forbidden F-ZH-ENGLISH-TERMS ZH 'desired snapshot'

# --- F. Carry substance that must survive the round-4 rewrite (spot pins
# --- carried from the round-3 gating set, re-verified green on head).

required CARRY-PRECREATE-RATIONALE EN 'Adding `preCreate` or `postDelete`\. `preCreate` has no established workload or executor to coordinate before Pod creation'
required CARRY-PRECREATE-RATIONALE ZH '不新增 preCreate 或 postDelete'
required CARRY-FINALIZER-ORDERING EN 'New RIs are created with the reserved finalizer when their effective configuration includes `preDelete`'
required CARRY-FINALIZER-ORDERING EN 'Enablement is rejected or deferred if the RI already has a deletionTimestamp'
required CARRY-FINALIZER-ORDERING EN 'Kubernetes does not allow a new finalizer to be added after deletion has begun'
required CARRY-FINALIZER-ORDERING ZH '如果 RI 已有 deletionTimestamp'
required CARRY-FINALIZER-ORDERING ZH 'Kubernetes 不允许删除开始后新增 finalizer'
required CARRY-NS-TEARDOWN EN 'Namespace teardown is outside the live-engine guarantee'
required CARRY-NS-TEARDOWN ZH 'namespace 删除不在存活引擎保证范围内'
required CARRY-RBGSET-BOUNDARY EN 'for RBGSet, RBG, RIS, or RI deletion requests that can reach an RI'
required CARRY-RBGSET-BOUNDARY ZH 'RBGSet、RBG、RIS 或 RI'
required CARRY-GATE-LIFETIME EN 'remains until the RI object is deleted'
required CARRY-GATE-LIFETIME ZH '持续到 RI 对象删除'
required CARRY-ESCAPE-HATCH EN 'break-glass'
required CARRY-ESCAPE-HATCH ZH 'break-glass'
required CARRY-DEAD-ENGINE EN 'must not assume that a live engine is'
required CARRY-DEAD-ENGINE ZH '不能假设必然存在存活 engine'
required CARRY-PHASE-STORAGE EN 'persisted in `RoleInstance\.status\.phase` as `Creating`, `Running`,'
required CARRY-PHASE-STORAGE EN 'KEP-527 cannot become implementable until the KEP-502'
required CARRY-PHASE-STORAGE ZH 'KEP-502'
required CARRY-NOPODFINALIZERS EN 'No per-Pod finalizer is used|No member Pod requires a lifecycle finalizer'
required CARRY-PREDELETE-LIVENESS EN 'background GC does not delete dependent member Pods'
required CARRY-PREDELETE-LIVENESS ZH '保留的 RI finalizer 使 owner 对象继续存在'
required CARRY-DISABLE-SUPPORTED YAML 'disable-supported: true'
required CARRY-DISABLE-SUPPORTED EN 'The `disable-supported` metadata field describes feature-gate rollback, not stable-boundary removal of individual hook configuration'
required CARRY-NOPOSTREADY-OMITTED EN 'If `postReady` is omitted, enter `Running`'
required CARRY-NOPOSTREADY-OMITTED ZH '未配置 postReady 时，controller 直接进入'
required CARRY-WIRE-NAMES EN 'rbg\.workloads\.x-k8s\.io/role-instance-hook-state'
required CARRY-WIRE-NAMES EN 'rbg\.workloads\.x-k8s\.io/role-instance-pre-delete'
required CARRY-WIRE-NAMES EN 'must not reuse the existing `rbg\.workloads\.x-k8s\.io/role-instance-lifecycle-state` key'
required CARRY-WIRE-NAMES ZH '不得复用现有的 `rbg\.workloads\.x-k8s\.io/role-instance-lifecycle-state`'
forbidden CARRY-WIRE-NAMES EN 'lifecycle-state='
forbidden CARRY-WIRE-NAMES ZH 'lifecycle-state='
forbidden CARRY-KEPYAML-NOISE YAML 'Segment is data-driven'
forbidden CARRY-KEPYAML-NOISE YAML 'TBD'

# F-SEEALSO-502 (still OPEN — reported, does not gate the exit code).
if [ -e "$ROOT/keps/502-workload-status-model" ]; then
  echo "PASS  F-SEEALSO-502 (target /keps/502-workload-status-model now exists in repo)" | tee -a "$OUT"
else
  echo "OPEN  F-SEEALSO-502 (see-also target /keps/502-workload-status-model still absent from repo)" | tee -a "$OUT"
fi

echo | tee -a "$OUT"
if [ "$overall" -eq 0 ]; then
  echo "ROUND4 CONTRACTS: all fixed-finding contracts hold" | tee -a "$OUT"
else
  echo "ROUND4 CONTRACTS: one or more fixed-finding contracts missing" | tee -a "$OUT"
fi
exit "$overall"
