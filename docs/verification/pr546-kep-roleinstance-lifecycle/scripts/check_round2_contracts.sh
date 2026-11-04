#!/usr/bin/env bash
# KEP-527 (PR #546) round-2 contract checks.
# Each round-1 finding that the head claims fixed is pinned here as a
# required-string contract (red when the fix text is absent) or a
# forbidden-string contract (red when the removed text reappears), in BOTH
# languages. Still-open F-SEEALSO-502 is pinned as a contract that is red
# while the dangling see-also reference remains.
#
# Matching is done against whitespace-flattened file content because the KEP
# hard-wraps paragraphs, so patterns may span physical lines.
#
# Usage: bash check_round2_contracts.sh <repo-root-of-ref-under-test>
# Exit 0 iff every fixed-finding contract holds (F-SEEALSO-502 is reported
# separately as OPEN and does not affect the exit code).
set -u
ROOT="${1:?usage: check_round2_contracts.sh <repo-root>}"
KEP="$ROOT/keps/527-roleinstance-lifecycle"
EN_RAW="$KEP/README.md"
ZH_RAW="$KEP/README.zh-CN.md"
YAML_RAW="$KEP/kep.yaml"

OUT_DIR="${CHECK_ROUND2_OUT:-$(cd "$(dirname "$0")/../results" && pwd)}"
mkdir -p "$OUT_DIR"
OUT="$OUT_DIR/round2-contracts.txt"
: >"$OUT"

flatten() { tr '\n' ' ' <"$1" 2>/dev/null | tr -s ' '; }
EN="$(flatten "$EN_RAW")"
ZH="$(flatten "$ZH_RAW")"
YAML="$(flatten "$YAML_RAW")"

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

echo "== Round-2 contracts on $ROOT ==" | tee -a "$OUT"

# F-OLDNEW-ISOLATION (round-1 major): coexistence/mutual-exclusion rule stated,
# 'sole definition point' scoped to the new protocol only.
required F-OLDNEW-ISOLATION EN 'sole user-facing definition point for the new four-hook protocol'
required F-OLDNEW-ISOLATION EN 'Alpha admission must reject a Role/RIS combination'
required F-OLDNEW-ISOLATION EN 'hook is deprecated'
required F-OLDNEW-ISOLATION EN 'must be empty; admission rejects a non-empty value there'
required F-OLDNEW-ISOLATION ZH '同时非空'
required F-OLDNEW-ISOLATION ZH '标记为 deprecated'
forbidden F-OLDNEW-ISOLATION EN 'sole user-facing definition point\.$'

# F-PREDELETE-LIVENESS: the real background-GC invariant + foreground rejection.
required F-PREDELETE-LIVENESS EN 'background GC does not delete dependent member Pods'
required F-PREDELETE-LIVENESS EN '[Ff]oreground cascading deletion must be rejected by admission'
required F-PREDELETE-LIVENESS EN 'reserved RI finalizer keeps the owner object present'
required F-PREDELETE-LIVENESS ZH '准入必须拒绝'
required F-PREDELETE-LIVENESS ZH '保留的 RI finalizer 使 owner 对象继续存在'
# the old, factually wrong mechanism (per-Pod finalizer injection) must be gone
forbidden F-PREDELETE-LIVENESS EN 'inject an RBG-owned `preDelete` finalizer into each member Pod'

# F-CREATING-REPLACE-FINALIZER: per-Pod finalizers removed entirely.
required F-CREATING-REPLACE-FINALIZER EN 'No member Pod requires a lifecycle finalizer|No per-Pod finalizer is used'
required F-CREATING-REPLACE-FINALIZER ZH '不使用逐 Pod finalizer|成员 Pod 不需要 lifecycle finalizer'
forbidden F-CREATING-REPLACE-FINALIZER EN 'removes the old Pod.s finalizer'
forbidden F-CREATING-REPLACE-FINALIZER ZH '移除旧 Pod 的 finalizer'

# F-PHASE-STORAGE: phase storage + values + executor observability + KEP-502 dependency.
required F-PHASE-STORAGE EN 'persisted in `RoleInstance\.status\.phase`'
required F-PHASE-STORAGE EN 'KEP-527 cannot become implementable until the KEP-502'
required F-PHASE-STORAGE ZH 'RoleInstance\.status\.phase'
required F-PHASE-STORAGE ZH 'KEP-502 的 schema 与持久化规则被接受前'

# F-PREDELETE-GATE-LIFETIME: gate lifetime for preDelete stated.
required F-PREDELETE-GATE-LIFETIME EN 'remains until the RI object is deleted'
required F-PREDELETE-GATE-LIFETIME ZH '持续到 RI 对象删除'

# F-ESCAPE-HATCH: supported break-glass emergency procedure defined.
required F-ESCAPE-HATCH EN 'break-glass'
required F-ESCAPE-HATCH ZH 'break-glass'

# F-DEAD-ENGINE-PREUPDATE: dead-engine guidance for preUpdate.
required F-DEAD-ENGINE-PREUPDATE EN 'must not assume that a live engine is available'
required F-DEAD-ENGINE-PREUPDATE ZH '不能假设必然存在存活 engine'

# F-INFLIGHT-CONFIG-ADMISSION: admission rejects in-flight lifecycle mutation.
required F-INFLIGHT-CONFIG-ADMISSION EN 'Admission should reject lifecycle changes while an affected RI has a non-empty `lifecycle-state`'
required F-INFLIGHT-CONFIG-ADMISSION ZH '非空时拒绝修改 lifecycle'

# F-MARKNOTREADY-NAME: aligned to the existing sibling wire name.
required F-MARKNOTREADY-NAME EN 'markPodNotReady'
forbidden F-MARKNOTREADY-NAME EN 'markNotReady'
required F-MARKNOTREADY-NAME ZH 'markPodNotReady'
forbidden F-MARKNOTREADY-NAME ZH 'markNotReady'

# F-READYBLOCK-NAME: postReady marker renamed away from -block.
required F-READYBLOCK-NAME EN 'ready-notify'
forbidden F-READYBLOCK-NAME EN 'ready-block'
required F-READYBLOCK-NAME ZH 'ready-notify'
forbidden F-READYBLOCK-NAME ZH 'ready-block'

# F-LIFECYCLE-STATE-PREFIX: fully qualified key required, legacy key must not be reused.
required F-LIFECYCLE-STATE-PREFIX EN 'must use a fully qualified label key'
required F-LIFECYCLE-STATE-PREFIX EN 'must not reuse the existing `rbg\.workloads\.x-k8s\.io/role-instance-lifecycle-state` key'
required F-LIFECYCLE-STATE-PREFIX ZH '不得复用现有的'

# F-KEPYAML-NOISE: stray Segment comment removed; author named; no TBD.
forbidden F-KEPYAML-NOISE YAML 'Segment is data-driven'
forbidden F-KEPYAML-NOISE YAML 'TBD'
required F-KEPYAML-NOISE YAML 'disable-supported'

# F-SEEALSO-502 (still OPEN — reported, does not gate the exit code).
if [ -e "$ROOT/keps/502-workload-status-model" ]; then
  echo "PASS  F-SEEALSO-502 (target /keps/502-workload-status-model now exists in repo)" | tee -a "$OUT"
else
  echo "OPEN  F-SEEALSO-502 (see-also target /keps/502-workload-status-model still absent from repo)" | tee -a "$OUT"
fi

echo | tee -a "$OUT"
if [ "$overall" -eq 0 ]; then
  echo "ROUND2 CONTRACTS: all fixed-finding contracts hold" | tee -a "$OUT"
else
  echo "ROUND2 CONTRACTS: one or more fixed-finding contracts regressed" | tee -a "$OUT"
fi
exit "$overall"
