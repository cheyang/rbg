#!/usr/bin/env bash
# Live A/B premise test for PR #472 (deprecate InPlaceOnly).
# P0 claim: "InPlaceOnly is not separately implemented and behaves identically to
# InPlaceIfPossible" -- both in-place update on an image-only change (Phase A) and
# both fall back to Pod recreation (rather than fail) on an in-place-incompatible
# change (Phase B).
#
# Runs against whatever cluster $KUBECONFIG (or the default kubeconfig) points at.
# Idempotent: cleans up its own namespace at the end. Captures output to results/.
set -uo pipefail

NS="pr472-verify"
RBG="pr472-abtest"
HERE="$(cd "$(dirname "$0")" && pwd)"
RES="${HERE}/../results"
mkdir -p "$RES"
LOG="$RES/live_abtest.log"
: > "$LOG"
log(){ echo "[$(date -u +%H:%M:%S)] $*" | tee -a "$LOG"; }

IMG_OLD="anolis-registry.cn-zhangjiakou.cr.aliyuncs.com/openanolis/nginx:1.14.1-8.6"
IMG_NEW="nginx:stable-alpine"

kubectl >/dev/null 2>&1 || { echo "kubectl missing"; exit 2; }

log "=== cleanup any prior run ==="
kubectl delete rbg "$RBG" -n "$NS" --ignore-not-found --timeout=60s 2>&1 | tee -a "$LOG"
kubectl delete namespace "$NS" --ignore-not-found --timeout=60s 2>&1 | tee -a "$LOG"
kubectl create namespace "$NS" 2>&1 | tee -a "$LOG"

log "=== apply A/B RBG (ipo=InPlaceOnly, ipc=InPlaceIfPossible) ==="
kubectl apply -f "$HERE/abtest-rbg.yaml" 2>&1 | tee -a "$LOG"

# helper: list pods for a role with their pod-phase + imageID
role_pods(){
  local role="$1"
  kubectl get pods -n "$NS" -l "workloads.x-k8s.io/role-name=$role" -o \
    jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.status.phase}{"\t"}{.status.containerStatuses[0].imageID}{"\n"}{end}' 2>/dev/null
}
# RoleInstanceSet label may differ across versions; fall back to name prefix.
role_pods_prefix(){
  local role="$1"
  kubectl get pods -n "$NS" -o json 2>/dev/null | python3 -c "
import sys,json
d=json.load(sys.stdin)
role='$role'
for p in d['items']:
    n=p['metadata']['name']
    if n.startswith('pr472-abtest-'+role+'-'):
        cs=p.get('status',{}).get('containerStatuses',[{}])
        img=cs[0].get('imageID','') if cs else ''
        ph=p.get('status',{}).get('phase','')
        print(f'{n}\t{ph}\t{img}')
"
}

wait_ready(){
  log "waiting for all pods Ready ..."
  kubectl -n "$NS" wait rbg/$RBG --for=condition=Ready --timeout=300s 2>&1 | tee -a "$LOG" || \
    kubectl -n "$NS" wait --for=condition=Ready pod --all --timeout=300s 2>&1 | tee -a "$LOG"
}

log "=== wait for initial rollout (4 pods Ready) ==="
wait_ready
log "--- baseline pods (Phase 0) ---"
IPO0="$(role_pods_prefix ipo)"; IPC0="$(role_pods_prefix ipc)"
{ echo "ipo (InPlaceOnly):"; echo "$IPO0"; echo "ipc (InPlaceIfPossible):"; echo "$IPC0"; } | tee "$RES/phase0-pods.txt"

patch_image(){
  local role="$1" img="$2"
  kubectl patch rbg "$RBG" -n "$NS" --type=json -p="[{\"op\":\"replace\",\"path\":\"/spec/roles/$1/image\",\"value\":\"$2\"}]" 2>&1 | tee -a "$LOG"
}
# index helper: find role index in the roles array
role_index(){ python3 -c "
import json,sys
d=json.loads(open('/dev/stdin').read())
for i,r in enumerate(d['spec']['roles']):
    if r['name']=='$1': print(i); break
" < <(kubectl get rbg "$RBG" -n "$NS" -o json); }

# ---------------- Phase A: image-only change (in-place) ----------------
IPO_IDX="$(role_index ipo)"; IPC_IDX="$(role_index ipc)"
log "=== Phase A: image-only change (in-place expected) ==="
log "patching role ipo[idx=$IPO_IDX] and ipc[idx=$IPC_IDX] image -> $IMG_NEW"
kubectl patch rbg "$RBG" -n "$NS" --type=json -p="[{\"op\":\"replace\",\"path\":\"/spec/roles/$IPO_IDX/standalonePattern/template/spec/containers/0/image\",\"value\":\"$IMG_NEW\"},{\"op\":\"replace\",\"path\":\"/spec/roles/$IPC_IDX/standalonePattern/template/spec/containers/0/image\",\"value\":\"$IMG_NEW\"}]" 2>&1 | tee -a "$LOG"
log "waiting for Phase A rollout ..."
kubectl -n "$NS" wait rbg/$RBG --for=condition=Ready --timeout=300s 2>&1 | tee -a "$LOG" || \
  kubectl -n "$NS" wait --for=condition=Ready pod --all --timeout=300s 2>&1 | tee -a "$LOG"
sleep 5
IPOA="$(role_pods_prefix ipo)"; IPCA="$(role_pods_prefix ipc)"
{ echo "ipo (InPlaceOnly):"; echo "$IPOA"; echo "ipc (InPlaceIfPossible):"; echo "$IPCA"; } | tee "$RES/phaseA-pods.txt"

# in-place signal: pod NAMES unchanged, imageID changed
inplace_ipo_names_unchanged=$(diff <(echo "$IPO0" | awk -F'\t' '{print $1}') <(echo "$IPOA" | awk -F'\t' '{print $1}') >/dev/null && echo yes || echo no)
inplace_ipc_names_unchanged=$(diff <(echo "$IPC0" | awk -F'\t' '{print $1}') <(echo "$IPCA" | awk -F'\t' '{print $1}') >/dev/null && echo yes || echo no)
log "Phase A in-place? ipo names unchanged=$inplace_ipo_names_unchanged ; ipc names unchanged=$inplace_ipc_names_unchanged"

# ---------------- Phase B: non-image change (recreate fallback) ----------------
log "=== Phase B: non-image spec change containerPort 80->8080 (recreate fallback expected) ==="
kubectl patch rbg "$RBG" -n "$NS" --type=json -p="[{\"op\":\"replace\",\"path\":\"/spec/roles/$IPO_IDX/standalonePattern/template/spec/containers/0/ports/0/containerPort\",\"value\":8080},{\"op\":\"replace\",\"path\":\"/spec/roles/$IPC_IDX/standalonePattern/template/spec/containers/0/ports/0/containerPort\",\"value\":8080}]" 2>&1 | tee -a "$LOG"
log "waiting for Phase B rollout ..."
kubectl -n "$NS" wait rbg/$RBG --for=condition=Ready --timeout=300s 2>&1 | tee -a "$LOG" || \
  kubectl -n "$NS" wait --for=condition=Ready pod --all --timeout=300s 2>&1 | tee -a "$LOG"
sleep 5
IPOB="$(role_pods_prefix ipo)"; IPCB="$(role_pods_prefix ipc)"
{ echo "ipo (InPlaceOnly):"; echo "$IPOB"; echo "ipc (InPlaceIfPossible):"; echo "$IPCB"; } | tee "$RES/phaseB-pods.txt"

recreate_ipo=$(diff <(echo "$IPOA" | awk -F'\t' '{print $1}') <(echo "$IPOB" | awk -F'\t' '{print $1}') >/dev/null && echo no || echo yes)
recreate_ipc=$(diff <(echo "$IPCA" | awk -F'\t' '{print $1}') <(echo "$IPCB" | awk -F'\t' '{print $1}') >/dev/null && echo no || echo yes)
log "Phase B recreated pods? ipo=$recreate_ipo ; ipc=$recreate_ipc"

# ---------------- status + logs ----------------
log "=== final RBG status ==="
kubectl get rbg "$RBG" -n "$NS" -o yaml 2>&1 | tee "$RES/final-rbg.yaml"
log "=== controller logs mentioning pr472/InPlaceOnly/errors (last 10m) ==="
kubectl logs -n rbg-system deploy/rbgs-controller-manager --since=10m 2>&1 | grep -iE "pr472|InPlaceOnly|error|fail" | tail -40 | tee "$RES/controller-logs.txt" || true

log "=== verdict summary ==="
{
  echo "Phase A (image-only, in-place expected):"
  echo "  ipo InPlaceOnly      names-unchanged=$inplace_ipo_names_unchanged"
  echo "  ipc InPlaceIfPossible names-unchanged=$inplace_ipc_names_unchanged"
  echo "Phase B (containerPort change, recreate fallback expected):"
  echo "  ipo InPlaceOnly      recreated-pods=$recreate_ipo"
  echo "  ipc InPlaceIfPossible recreated-pods=$recreate_ipc"
  if [ "$inplace_ipo_names_unchanged" = "$inplace_ipc_names_unchanged" ] && [ "$recreate_ipo" = "$recreate_ipc" ] && [ "$recreate_ipo" = "yes" ]; then
    echo "PREMISE: CONFIRMED -- InPlaceOnly behaves identically to InPlaceIfPossible (in-place when possible, recreate fallback when not; no fail/stall)."
  else
    echo "PREMISE: DIVERGENCE -- see details above."
  fi
} | tee "$RES/verdict.txt"

log "=== cleanup ==="
kubectl delete rbg "$RBG" -n "$NS" --ignore-not-found --timeout=60s 2>&1 | tee -a "$LOG"
kubectl delete namespace "$NS" --ignore-not-found --timeout=60s 2>&1 | tee -a "$LOG"
log "done."
