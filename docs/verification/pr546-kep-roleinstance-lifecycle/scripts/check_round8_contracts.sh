#!/usr/bin/env bash
# KEP-527 (PR #546) round-8 contract checks.
#
# Round 8 (delta d038c23b..e1116c79 — one commit "docs: address round-7
# review findings in KEP-527", +41/-11, the two READMEs only) PLUS a REBASE
# of the PR branch onto c2896cc0 (the KEP-502 merge), which moves the PR
# base from 3612013e to c2896cc0. The rebase itself changes no KEP-527
# text; its environment effects are verified separately (see below).
#
# Content delta:
#   - The blanket Foreground rejection's coverage of hook-less workloads is
#     acknowledged: the Goal is qualified (EN+ZH) and the webhook paragraph
#     states the cluster-wide scope and the gate-off restoration (EN+ZH) —
#     PARTIAL fix of F-FOREGROUND-BLANKET-SCOPE. The zh README additionally
#     qualifies the Upgrade/Downgrade strategy (:849) and the regression
#     baseline (:737-738); the EN README does NOT (README.md:1155 /
#     :979-981 still promise unchanged behavior unqualified) — that is the
#     open residue AND a new EN/ZH content divergence at exactly those two
#     sites (tracked as the narrowed F-FOREGROUND-BLANKET-SCOPE).
#   - The executor must re-read the RI and remove a marker only while
#     `hook-state` still matches its own invocation and the RI UID is
#     unchanged; an emptied marker-key record proves only that the label
#     was cleaned, not that a superseded execution has stopped
#     (EN :349-356 / ZH :255-263 prose + unit bullets EN :1017-1020 /
#     ZH :763-765) — addresses Reviewer-C advisory F-C4554909778 at the
#     specification level.
#   - The preDelete integration bullet drops the deleted design's "at
#     every affected ancestor" / "所有受影响祖先" wording (EN+ZH) —
#     closes F-AFFECTED-ANCESTOR-WORDING.
#
# REBASE effects (environment changes, disclosed — Harness-update, never
# proof of a fix):
#   - keps/502-workload-status-model now exists IN the PR tree, so the
#     kep.yaml see-also reference resolves in-tree (check_round6 now prints
#     "OPEN->RESOLVED F-SEEALSO-502"); check_round7's carry filter is
#     updated (additively) to tolerate that resolved line.
#   - The in-tree KEP-502 README still references markPodNotReady at
#     :413/:485 (verified at head e1116c79), so the KEP-527 drift
#     disclosure remains accurate; the required amendment now has an owner
#     — open PR #572 removes both references (verified via gh pr diff).
#   - All premise code-claim Go pins re-run green on the new base through
#     the rebased head (results/gotest-rerun.txt).
#
# Pins are exact fixed-string literals extracted from the whitespace-flattened
# head text (hard-wrap positions baked in), matched with grep -F.
#
# Usage: bash check_round8_contracts.sh <repo-root-of-ref-under-test>
# Exit 0 iff every round-8 contract (and every non-superseded round-7 carry
# contract) holds. OPEN lines document non-gating residue and do not fail.
set -u
ROOT="${1:?usage: check_round8_contracts.sh <repo-root>}"
KEP="$ROOT/keps/527-roleinstance-lifecycle"

OUT_DIR="${CHECK_ROUND8_OUT:-$(cd "$(dirname "$0")/../results" && pwd)}"
mkdir -p "$OUT_DIR"
OUT="$OUT_DIR/round8-contracts.txt"
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

open() { # open <id> <lang-var> <literal> — non-gating residue documentation
  local id="$1"; local var="$2"; local lit="$3"
  local content; eval "content=\"\$$var\""
  if printf '%s' "$content" | grep -Fq -- "$lit"; then
    echo "OPEN  $id [$var] (residue present: $lit)" | tee -a "$OUT"
  else
    echo "PASS  $id [$var] (residue absent: $lit)" | tee -a "$OUT"
  fi
}

# --- round-8 pins (exact fixed-string literals from flattened head e1116c79)
# F-FOREGROUND-BLANKET-SCOPE: acknowledged sites (EN+ZH)
required R8-BLANKET-GOAL EN "hooks, except that Foreground deletion is rejected cluster-wide while the feature gate is enabled"
required R8-BLANKET-GOAL ZH "未配置 lifecycle hooks 的负载保持现有行为，唯一例外是 feature gate 开启期间集群级拒绝 Foreground 删除"
required R8-BLANKET-WEBHOOK EN "This rejection is cluster-wide while the gate is enabled: it also applies to workloads that configure no hooks, which otherwise keep their existing behavior and regain Foreground deletion when the gate is disabled"
required R8-BLANKET-WEBHOOK ZH "该拒绝在 gate 开启期间是集群级的：同样作用于未配置 hooks 的 负载，这些负载其余行为不变，gate 关闭后恢复 Foreground 删除"
# F-FOREGROUND-BLANKET-SCOPE: zh-only qualified sites (the divergence source)
required R8-UPGRADE-EXCEPTION ZH "唯一例外是 feature gate 开启期间 Foreground 删除被拒绝"
required R8-REGRESSIONBASE-NOTE ZH "并注明 gate 开启期间对这类负载的唯一变化是 Foreground 删除被拒绝"
# F-FOREGROUND-BLANKET-SCOPE: EN residue (open, non-gating)
open R8-OPEN-UPGRADE-EN EN "Workloads with no new hooks configured retain their existing behavior"
open R8-OPEN-REGRESSIONBASE-EN EN "Establish regression baselines for create, update, component scaling, Recovery, Ready, and deletion when lifecycle hooks are not configured"
# F-C4554909778 (Reviewer-C advisory): executor re-read guard (EN+ZH prose + unit bullets)
required R8-EXECUTOR-REREAD EN "the executor re-reads the RI and removes the marker only when the current \`hook-state\` still matches the hook it executed and the RI UID is unchanged"
required R8-EXECUTOR-REREAD ZH "只有当前 \`hook-state\` 仍匹配自己执行的 hook 且 RI UID 未变化时才执行清除"
required R8-EXECUTOR-REREAD-RECORD EN "An emptied marker-key record proves only that the label was cleaned, not that a superseded execution has stopped"
required R8-EXECUTOR-REREAD-RECORD ZH "marker key 记录清空只能 证明 label 已清理，不能证明被取代的执行已停止"
required R8-EXECUTOR-TESTBULLET EN "An executor removes a marker only while \`hook-state\` still matches its own invocation and the RI UID is unchanged; after a key is reassigned and its record cleaned, a delayed \`postReady\` cleanup cannot remove a \`preDelete\` marker"
required R8-EXECUTOR-TESTBULLET ZH "executor 只在 \`hook-state\` 仍匹配自身调用且 RI UID 未变化时清除 marker； key 被重新分配且记录清空后，迟到的 postReady 清理无法删除 preDelete marker"
# F-AFFECTED-ANCESTOR-WORDING: replacement wording + forbidden stale wording
required R8-ANCESTOR-WORDING EN "admission rejects foreground deletion at every RBGSet, RBG, RIS, and RI entry while the feature gate is enabled"
required R8-ANCESTOR-WORDING ZH "gate 开启期间准入 拒绝 RBGSet、RBG、RIS 和 RI 所有删除入口的 foreground 删除"
forbidden R8-NO-AFFECTED-ANCESTOR EN "at every affected ancestor"
forbidden R8-NO-AFFECTED-ANCESTOR ZH "受影响祖先"
# rebase effect: KEP-502 in-tree — the drift-disclosure claim stays accurate
# against the in-tree KEP-502 text (markPodNotReady refs at :413/:485).
if grep -q 'markPodNotReady' "$ROOT/keps/502-workload-status-model/README.md" 2>/dev/null; then
  echo "PASS  R8-KEP502-INTREE-DRIFT (in-tree KEP-502 still references markPodNotReady — the KEP-527 disclosure is accurate)" | tee -a "$OUT"
else
  echo "FAIL  R8-KEP502-INTREE-DRIFT (in-tree KEP-502 no longer references markPodNotReady — the KEP-527 drift disclosure is now STALE and must be re-checked against PR #572)" | tee -a "$OUT"
  overall=1
fi

# ---------------------------------------------------------------------------
# Round-7 carry: run the round-7 script and tolerate NOTHING new (no
# round-8 supersessions — the round-8 delta is purely additive wording).
# The round-7 script itself (after its disclosed additive fix) tolerates the
# see-also OPEN->RESOLVED line caused by the rebase bringing KEP-502 in-tree.
# ---------------------------------------------------------------------------
R7_OUT="$(mktemp)"
CHECK_ROUND7_OUT="$(mktemp -d)" bash "$(dirname "$0")/check_round7_contracts.sh" "$ROOT" >"$R7_OUT" 2>&1
r7_status=$?
if [ "$r7_status" -eq 0 ]; then
  echo "PASS  R8-CARRY (all round-7 pins green on this ref)" | tee -a "$OUT"
else
  echo "$R7_OUT" | grep -E '^(FAIL|OPEN)' | tee -a "$OUT"
  echo "FAIL  R8-CARRY-REGRESSION (a round-7 pin failed on this ref — see above)" | tee -a "$OUT"
  overall=1
fi
# keep the full round-7 log for reference
cp "$R7_OUT" "$OUT_DIR/round7-contracts-inside-round8.txt"

echo "ROUND8 CONTRACTS: $([ "$overall" -eq 0 ] && echo 'all green' || echo 'one or more contracts missing')" | tee -a "$OUT"
exit "$overall"
