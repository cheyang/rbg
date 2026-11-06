#!/usr/bin/env bash
# KEP-527 (PR #546) round-3 contract checks.
#
# Round 3 (delta 71dd5e87..fb6c2b36, docs-only) is a design pivot: the legacy
# RoleInstanceSetSpec.lifecycle protocol is REPLACED/REMOVED instead of
# coexisting with mutual exclusion. Every round-2 open finding claimed fixed
# by this delta is pinned here as a required-string contract (red when the
# fix text is absent) in BOTH languages. The round-2 coexistence pins
# (F-OLDNEW-ISOLATION wording, 'Admission should reject', 'fully qualified
# label key under the prefix') are SUPERSEDED by this pivot: their substance
# is preserved and strengthened under new wording, which this script pins.
# Still-open F-SEEALSO-502 is reported as OPEN and does not gate the exit
# code.
#
# Matching is done against whitespace-flattened file content because the KEP
# hard-wraps paragraphs, so patterns may span physical lines.
#
# Usage: bash check_round3_contracts.sh <repo-root-of-ref-under-test>
# Exit 0 iff every fixed-finding contract holds.
set -u
ROOT="${1:?usage: check_round3_contracts.sh <repo-root>}"
KEP="$ROOT/keps/527-roleinstance-lifecycle"

OUT_DIR="${CHECK_ROUND3_OUT:-$(cd "$(dirname "$0")/../results" && pwd)}"
mkdir -p "$OUT_DIR"
OUT="$OUT_DIR/round3-contracts.txt"
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

echo "== Round-3 contracts on $ROOT ==" | tee -a "$OUT"

# --- Round-3 resolutions (new pins for findings fixed by this delta) ---

# F-SCOPE-PRECREATE: preCreate/postDelete exclusion rationale now in Non-Goals.
required F-SCOPE-PRECREATE EN 'Adding `preCreate` or `postDelete`\. `preCreate` has no established workload or executor to coordinate before Pod creation'
required F-SCOPE-PRECREATE EN 'concerns external cleanup after the live instance is gone and needs a separate protocol'
required F-SCOPE-PRECREATE ZH '不新增 preCreate 或 postDelete。preCreate 发生在 Pod 创建前，尚无已建立的'
required F-SCOPE-PRECREATE ZH 'workload 和 executor 可协调；postDelete 面向实例退出后的外部清理'

# F-PROPAGATED-VS-LEGACY (resolved by removal redesign): legacy protocol
# removed, admission rejects the field, distinct new entry points.
required F-PROPAGATED-VS-LEGACY EN 'The legacy `RoleInstanceSetSpec\.lifecycle` protocol is removed\. Admission rejects any use of that field'
required F-PROPAGATED-VS-LEGACY EN 'neither writes nor interprets'
required F-PROPAGATED-VS-LEGACY EN 'RoleBasedGroup\.spec\.roles\[\]\.lifecycle` is the user-facing source of truth'
required F-PROPAGATED-VS-LEGACY EN 'RoleInstanceSet\.spec\.roleInstanceTemplate\.lifecycle'
required F-PROPAGATED-VS-LEGACY ZH '旧 `RoleInstanceSetSpec\.lifecycle` 协议被移除。准入拒绝该字段的任何使用'
required F-PROPAGATED-VS-LEGACY ZH 'RoleInstanceSet\.spec\.roleInstanceTemplate\.lifecycle'
# the round-2 dual-protocol boundary language is superseded: no coexistence
# contract may survive the removal pivot.
forbidden F-PROPAGATED-VS-LEGACY EN 'The two runtime protocols must not be active on the same RIS'
forbidden F-PROPAGATED-VS-LEGACY ZH '同一 RI 上同时生效'

# F-FINALIZER-BOOTSTRAP-RACE: establishment pinned to creation/enablement;
# rejection once deletion has begun.
required F-FINALIZER-BOOTSTRAP-RACE EN 'New RIs are created with the reserved finalizer when their effective configuration includes `preDelete`'
required F-FINALIZER-BOOTSTRAP-RACE EN 'Enablement is rejected or deferred if the RI already has a deletionTimestamp'
required F-FINALIZER-BOOTSTRAP-RACE EN 'Kubernetes does not allow a new finalizer to be added after deletion has begun'
required F-FINALIZER-BOOTSTRAP-RACE ZH '新建 RI 在生效配置包含 preDelete 时，创建请求即带保留 finalizer'
required F-FINALIZER-BOOTSTRAP-RACE ZH '如果 RI 已有 deletionTimestamp'
required F-FINALIZER-BOOTSTRAP-RACE ZH 'Kubernetes 不允许删除开始后新增 finalizer'

# F-NS-TEARDOWN-BYPASS: namespace teardown scoped out of the guarantee.
required F-NS-TEARDOWN-BYPASS EN 'Namespace teardown is outside the live-engine guarantee'
required F-NS-TEARDOWN-BYPASS EN 'the namespace controller may delete member Pods directly'
required F-NS-TEARDOWN-BYPASS ZH 'namespace 删除不在存活引擎保证范围内'
required F-NS-TEARDOWN-BYPASS ZH 'namespace controller 可能直接删除'

# F-RBGSET-FOREGROUND-BOUNDARY: RBGSet included in the admission boundary.
required F-RBGSET-FOREGROUND-BOUNDARY EN 'for RBGSet, RBG, RIS, or RI deletion requests that can reach an RI'
required F-RBGSET-FOREGROUND-BOUNDARY EN 'for RBGSet, RBG, RIS, or RI deletion when it can reach'
required F-RBGSET-FOREGROUND-BOUNDARY ZH 'RBGSet、RBG、RIS 或 RI'
required F-RBGSET-FOREGROUND-BOUNDARY ZH '该准入范围包括 RBGSet、RBG、RIS 和 RI'

# F-INFLIGHT-ADMISSION-MUST: normative strength aligned to 'must'.
required F-INFLIGHT-ADMISSION-MUST EN 'Admission must reject lifecycle changes while an affected RI has a non-empty `hook-state`'
forbidden F-INFLIGHT-ADMISSION-MUST EN 'Admission should reject lifecycle changes'
required F-INFLIGHT-ADMISSION-MUST ZH '准入必须在受影响 RI 的 `hook-state` 非空时拒绝修改 lifecycle'
forbidden F-INFLIGHT-ADMISSION-MUST ZH '准入应在受影响'

# F-DISABLE-SUPPORTED-CONTRADICTION: gate named, boolean reconciled,
# semantics clarified.
required F-DISABLE-SUPPORTED-CONTRADICTION YAML 'feature-gates: -? ?RoleInstanceLifecycleHooks|RoleInstanceLifecycleHooks'
required F-DISABLE-SUPPORTED-CONTRADICTION YAML 'disable-supported: true'
required F-DISABLE-SUPPORTED-CONTRADICTION EN 'The `disable-supported` metadata field describes feature-gate rollback, not stable-boundary removal of individual hook configuration'

# F-NOPOSTREADY-CLEANUP-CLARIFICATION: completion path for the omitted-hook
# case, in prose and in both diagrams.
required F-NOPOSTREADY-CLEANUP-CLARIFICATION EN 'If `postReady` is omitted, enter `Running`'
required F-NOPOSTREADY-CLEANUP-CLARIFICATION EN 'else postReady is omitted C->>R: Set phase=Running, lift gate, and clear state'
required F-NOPOSTREADY-CLEANUP-CLARIFICATION EN 'When `postReady` is omitted'
required F-NOPOSTREADY-CLEANUP-CLARIFICATION ZH '未配置 postReady 时，controller 直接进入'
required F-NOPOSTREADY-CLEANUP-CLARIFICATION ZH 'else 未配置 postReady'
required F-NOPOSTREADY-CLEANUP-CLARIFICATION ZH '未配置 postReady 时，Update 或 Recovery 达到工作负载 Ready'

# --- Wire Names and Constants (new normative section, both languages) ---
required F-WIRE-NAMES EN 'rbg\.workloads\.x-k8s\.io/role-instance-hook-state'
required F-WIRE-NAMES EN 'rbg\.workloads\.x-k8s\.io/role-instance-pre-delete'
required F-WIRE-NAMES EN 'RoleInstanceLifecycleHookStateKey'
required F-WIRE-NAMES EN 'RoleInstancePreDeleteFinalizer'
required F-WIRE-NAMES EN 'must not reuse the existing `rbg\.workloads\.x-k8s\.io/role-instance-lifecycle-state` key'
required F-WIRE-NAMES ZH 'rbg\.workloads\.x-k8s\.io/role-instance-hook-state'
required F-WIRE-NAMES ZH 'rbg\.workloads\.x-k8s\.io/role-instance-pre-delete'
required F-WIRE-NAMES ZH 'RoleInstanceLifecycleHookStateKey'
required F-WIRE-NAMES ZH '不得复用现有的 `rbg\.workloads\.x-k8s\.io/role-instance-lifecycle-state`'

# lifecycle-state -> hook-state rename complete (round-3 residue of
# F-LIFECYCLE-STATE-PREFIX, strengthened to the exact key).
forbidden F-WIRE-NAMES EN 'lifecycle-state='
forbidden F-WIRE-NAMES ZH 'lifecycle-state='
forbidden F-WIRE-NAMES EN 'Write lifecycle-state'
required F-WIRE-NAMES EN 'Write hook-state=PreUpdate'
required F-WIRE-NAMES ZH '设置 `hook-state`'

# --- Round-2 substance that the pivot must not regress (spot pins) ---
required CARRY-PREDELETE-LIVENESS EN 'background GC does not delete dependent member Pods'
required CARRY-PREDELETE-LIVENESS ZH '保留的 RI finalizer 使 owner 对象继续存在'
required CARRY-GATE-LIFETIME EN 'remains until the RI object is deleted'
required CARRY-GATE-LIFETIME ZH '持续到 RI 对象删除'
required CARRY-ESCAPE-HATCH EN 'break-glass'
required CARRY-ESCAPE-HATCH ZH 'break-glass'
required CARRY-DEAD-ENGINE EN 'must not assume that a live engine is'
required CARRY-DEAD-ENGINE ZH '不能假设必然存在存活 engine'
required CARRY-PHASE-STORAGE EN 'KEP-527 cannot become implementable until the KEP-502'
required CARRY-PHASE-STORAGE ZH 'KEP-502'
required CARRY-PHASE-STORAGE EN 'persisted in `RoleInstance\.status\.phase` as `Creating`, `Running`,'
required CARRY-NOPODFINALIZERS EN 'No member Pod requires a lifecycle finalizer|No per-Pod finalizer is used'
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
  echo "ROUND3 CONTRACTS: all fixed-finding contracts hold" | tee -a "$OUT"
else
  echo "ROUND3 CONTRACTS: one or more fixed-finding contracts missing" | tee -a "$OUT"
fi
exit "$overall"
