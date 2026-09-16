#!/usr/bin/env bash
# Live scenario for PR #470 verification.
# Drives a Stateful RoleInstanceSet rollout where rev1 pods carry a failing
# readinessProbe (-> RoleInstances unhealthy) and rev2 removes the probe.
# Captures the controller's requeue behavior so HEAD (requeue -> resume) can be
# distinguished from BASE (no requeue -> stall).
#
# Usage: bash live-scenario.sh <controller_log> <label>
# Depends on a running rbgs controller (head or base) reconciling the cluster.
set -u
LOG="${1:-/tmp/rbgs-verify/head.log}"
LABEL="${2:-run}"
NS="pr470-verify"
OUT="/tmp/rbgs-verify/timeline-${LABEL}.log"
: > "$OUT"

ts() { date +%H:%M:%S; }

log() { echo "[$(ts)] $*" | tee -a "$OUT"; }

status_line() {
  kubectl -n "$NS" get ris ris-stall -o \
    jsonpath='replicas={.status.replicas} ready={.status.readyReplicas} updated={.status.updatedReplicas} currentRev={.status.currentRevision} updateRev={.status.updateRevision}' 2>/dev/null
}

# Clean slate
kubectl -n "$NS" delete ris ris-stall --ignore-not-found >/dev/null 2>&1
kubectl -n "$NS" delete roleinstances --all >/dev/null 2>&1
sleep 3

# Truncate controller log marker so we can grep cleanly after this point.
MARKER="LIVE-SCENARIO-START-${LABEL}-$(date +%s)"
echo "$MARKER" >> "$LOG"

log "apply rev1 (failing readinessProbe)"
kubectl apply -f /tmp/rbgs-verify/ris-rev1.yaml >/dev/null 2>&1

# Wait until BOTH RoleInstances report RoleInstanceReady=False (freshly unhealthy).
READYCOND='{.status.conditions[?(@.type=="RoleInstanceReady")].status}'
for i in $(seq 1 40); do
  r0=$(kubectl -n "$NS" get roleinstance ris-stall-0 -o jsonpath="$READYCOND" 2>/dev/null)
  r1=$(kubectl -n "$NS" get roleinstance ris-stall-1 -o jsonpath="$READYCOND" 2>/dev/null)
  if [ "$r0" = "False" ] && [ "$r1" = "False" ]; then
    log "both RIs RoleInstanceReady=False (freshly unhealthy) at poll #$i — applying rev2 NOW"
    break
  fi
  sleep 0.5
done

kubectl apply -f /tmp/rbgs-verify/ris-rev2.yaml >/dev/null 2>&1
T_UPDATE=$(date +%s)
log "rev2 applied at $T_UPDATE"

# Poll status for ~35s, capturing rollout progress + requeue log lines.
for i in $(seq 1 35); do
  sleep 1
  log "t+${i}s  $(status_line)"
done

log "=== controller signal lines (budget exhausted / RequeueAfter / Finished syncing) ==="
awk "/$MARKER/{f=1} f" "$LOG" \
  | grep -E "Rolling update budget exhausted|Finished syncing InstanceSet|Progress rolling update|Updating instance|Stably|free" \
  | grep -v "panic" | tail -40 | tee -a "$OUT"

log "=== final RI / pod state ==="
kubectl -n "$NS" get roleinstances -o wide 2>&1 | tee -a "$OUT"
kubectl -n "$NS" get pods -o wide 2>&1 | tee -a "$OUT"
