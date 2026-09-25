#!/usr/bin/env bash
# PR474 live verification — group-template propagation behavior of one controller
# binary against the real cluster.
#
# Usage: live-rollout.sh <head|base>
#   base = pre-PR binary (fc201cdf): un-paced, every outdated child updated in one pass
#   head = PR binary (5bfa08b7) with rolloutStrategy{Recreate,maxUnavailable=1}:
#          one child deleted+rebuilt at a time, ready groups never below 1
#
# The in-cluster controller is scaled to 0 and the RBG validating webhook is
# relaxed to Ignore for the window (restored by live-restore.sh). The binary
# under test is the only reconciler. Timeline is captured at 1s cadence:
# per-child UID / Ready / template-marker, so pacing and identity changes are
# visible second by second.
set -uo pipefail
LABEL="${1:?usage: live-rollout.sh <head|base>}"
WORK=/root/work/rbg-pr474
NS=pr474-verify
BIN="$WORK/bin/rbgs-$LABEL"
LOG="$WORK/results/${LABEL}-controller.log"
TL="$WORK/results/${LABEL}-timeline.log"
[ -x "$BIN" ] || { echo "missing binary $BIN"; exit 1; }
: > "$TL"
step() { echo "[$(date +%H:%M:%S)] $*" | tee -a "$TL"; }

snapshot() {
  kubectl get rolebasedgroups -n "$NS" -o json 2>/dev/null | python3 -c '
import json, sys
try:
    d = json.load(sys.stdin)
except Exception:
    print("snapshot-unavailable"); sys.exit(0)
ready_groups = 0
parts = []
for rbg in sorted(d.get("items", []), key=lambda x: x["metadata"]["name"]):
    name = rbg["metadata"]["name"]
    uid = rbg["metadata"]["uid"][:8]
    conds = {c["type"]: c["status"] for c in rbg.get("status", {}).get("conditions", [])}
    ready = conds.get("Ready", "?")
    marker = "new" if "RBGSET_ROLLOUT_MARKER" in json.dumps(rbg.get("spec", {})) else "old"
    deleting = "del" if rbg["metadata"].get("deletionTimestamp") else "   "
    if ready == "True" and not rbg["metadata"].get("deletionTimestamp"):
        ready_groups += 1
    parts.append("%s(%s,%s,%s,%s)" % (name, uid, ready, marker, deleting))
print("readyGroups=%d  %s" % (ready_groups, " ".join(parts)))
'
}

step "=== live-rollout.sh label=$LABEL ==="
kubectl -n rbg-system scale deploy rbgs-controller-manager --replicas=0 >/dev/null
# Relax every rbgs admission webhook for the test window: the webhook server is
# scaled down with the controller, and the policies would otherwise fail closed.
python3 - <<'PY2'
import json, subprocess
for kind in ["validatingwebhookconfiguration", "mutatingwebhookconfiguration"]:
    out = subprocess.run(
        ["kubectl", "get", kind, "-o", "name"], stdout=subprocess.PIPE, universal_newlines=True,
    ).stdout.split()
    for name in out:
        if "rbgs" not in name:
            continue
        obj = json.loads(subprocess.run(
            ["kubectl", "get", kind, name.split("/")[-1], "-o", "json"], stdout=subprocess.PIPE, universal_newlines=True,
        ).stdout)
        patch = [{"op": "replace", "path": "/webhooks/%d/failurePolicy" % i, "value": "Ignore"}
                 for i in range(len(obj.get("webhooks", [])))]
        if patch:
            subprocess.run(["kubectl", "patch", kind, name.split("/")[-1], "--type=json",
                            "-p", json.dumps(patch)], stdout=subprocess.PIPE)
PY2
step "in-cluster controller scaled to 0; rbgs admission webhooks relaxed to Ignore"

pkill -f 'bin/rbgs-' 2>/dev/null || true
sleep 1
setsid nohup "$BIN" --enable-webhooks none --metrics-bind-address=0 --health-probe-bind-address=:18081 \
  > "$LOG" 2>&1 < /dev/null &
sleep 8
grep -m1 -E "Starting workers|Starting Controller" "$LOG" >/dev/null && step "controller up: $(grep -m1 'Starting workers' "$LOG" | cut -c1-160)" || step "WARN: controller startup not confirmed"

kubectl -n "$NS" delete rolebasedgroupset rollout-demo --ignore-not-found --wait=true >/dev/null 2>&1 || true
kubectl -n "$NS" delete rolebasedgroups --all --wait=true >/dev/null 2>&1 || true
kubectl apply -f "$WORK/scripts/manifests/rbgs-rollout-demo-$LABEL.yaml" | tee -a "$TL"

step "waiting for both children Ready"
for i in $(seq 1 150); do
  S=$(snapshot)
  case "$S" in *readyGroups=2*) step "both ready after ${i}s: $S"; break;; esac
  [ "$i" = 150 ] && step "TIMEOUT waiting for readiness: $S"
  sleep 2
done

step "initial UIDs:"
kubectl get rolebasedgroups -n "$NS" -o jsonpath='{range .items[*]}{.metadata.name}{" uid="}{.metadata.uid}{"\n"}{end}' | tee -a "$TL"

step "TRIGGER: add RBGSET_ROLLOUT_MARKER env to the group template"
kubectl patch rolebasedgroupset rollout-demo -n "$NS" --type=json \
  -p='[{"op":"add","path":"/spec/groupTemplate/spec/roles/0/standalonePattern/template/spec/containers/0/env","value":[{"name":"RBGSET_ROLLOUT_MARKER","value":"rolled"}]}]' | tee -a "$TL"

# Fast forensic loop: capture each child's rv/gen/marker at ~200ms cadence,
# so any in-place spec write is pinned between the reconciler's log lines.
( for f in $(seq 1 400); do
    kubectl get rolebasedgroups -n "$NS" -o json 2>/dev/null | python3 -c '
import json,sys,time
try: d=json.load(sys.stdin)
except Exception: sys.exit(0)
for i in sorted(d.get("items",[]), key=lambda x: x["metadata"]["name"]):
    m=i["metadata"]
    marker = "new" if "RBGSET_ROLLOUT_MARKER" in json.dumps(i.get("spec",{})) else "old"
    print("%.2f %s uid=%s rv=%s gen=%s %s delTS=%s" % (time.time(), m["name"], m["uid"][:8], m["resourceVersion"], m.get("generation"), marker, str(m.get("deletionTimestamp","-"))[11:19]))
'
    sleep 0.2
  done > "$WORK/results/${LABEL}-fastcap.log" 2>/dev/null ) &

for i in $(seq 1 120); do
  echo "t+${i}s  $(snapshot)" | tee -a "$TL"
  case "$i" in
    1|2|3|5|10|20|40|80|120)
      kubectl get rolebasedgroups -n "$NS" -o json > "$WORK/results/${LABEL}-snap-t${i}s.json" 2>/dev/null || true
      ;;
  esac
  sleep 1
done


step "final UIDs:"
kubectl get rolebasedgroups -n "$NS" -o jsonpath='{range .items[*]}{.metadata.name}{" uid="}{.metadata.uid}{"\n"}{end}' | tee -a "$TL"
step "controller signals:"
grep -E "Recreating outdated|Updating existing RoleBasedGroups|Scaling up|budget" "$LOG" | tail -12 | tee -a "$TL"

pkill -f 'bin/rbgs-' 2>/dev/null || true
kubectl -n "$NS" delete rolebasedgroupset rollout-demo --ignore-not-found --wait=false >/dev/null 2>&1 || true
step "=== done $LABEL ==="
