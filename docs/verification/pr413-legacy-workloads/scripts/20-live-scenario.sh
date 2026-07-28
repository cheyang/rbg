#!/usr/bin/env bash
# L3 scenario for PR#413: run the PR binary out-of-cluster as the controller
# ServiceAccount with --enable-legacy-workloads=false against a real cluster.
#
# Observes:
#   A) a mixed RBG (one legacy Deployment role + one RoleInstanceSet role)
#   B) an RBG whose legacy role was migrated away, leaving an orphaned Deployment
#
# Requires 10-live-setup.sh (fresh SA token). Cleans up its own namespace.
#
# HARD GUARDS -- earlier runs of this script produced vacuous results twice, so
# both failure modes now abort loudly instead of reporting a fake pass:
#   1. FIXTURE INVALID  : the legacy annotation did not persist (wrong key prefix)
#   2. CONTROLLER VOID  : the manager never reconciled (expired token / cache
#                         never synced), so "nothing happened" proves nothing
set -uo pipefail
: "${KUBECONFIG:=$HOME/.kube/config}"
export KUBECONFIG
WORK=${WORK:-/root/pr413-verify}
SA_KUBECONFIG="$WORK/sa-kubeconfig.yaml"
NS=pr413-verify
IMG=anolis-registry.cn-zhangjiakou.cr.aliyuncs.com/openanolis/nginx:1.14.1-8.6
WT_ANN=rbg.workloads.x-k8s.io/role-workload-type
GN_LBL=rbg.workloads.x-k8s.io/group-name
ROOT="$(git rev-parse --show-toplevel)"
RESULTS="$ROOT/docs/verification/pr413-legacy-workloads/results"
mkdir -p "$RESULTS"
RUNLOG="$WORK/controller-clean.log"

stop_controller() {
  [ -n "${PID:-}" ] || return 0
  kill -TERM "$PID" 2>/dev/null
  for _ in $(seq 1 10); do kill -0 "$PID" 2>/dev/null || return 0; sleep 1; done
  kill -9 "$PID" 2>/dev/null
}
cleanup() { stop_controller; kubectl delete ns "$NS" --wait=false >/dev/null 2>&1 || true; }
trap cleanup EXIT

echo "== reset namespace $NS =="
kubectl delete ns "$NS" --ignore-not-found --wait=true >/dev/null 2>&1 || true
kubectl create ns "$NS" >/dev/null

# --- A) mixed RBG: one legacy role + one modern role ------------------------
kubectl apply -f - >/dev/null <<YAML
apiVersion: workloads.x-k8s.io/v1alpha2
kind: RoleBasedGroup
metadata: {name: mixed, namespace: $NS}
spec:
  roles:
    - name: modern
      replicas: 1
      standalonePattern:
        template:
          spec: {containers: [{name: c, image: $IMG}]}
    - name: legacy
      replicas: 1
      annotations: {"$WT_ANN": "apps/v1/Deployment"}
      standalonePattern:
        template:
          spec: {containers: [{name: c, image: $IMG}]}
YAML
STORED=$(kubectl -n "$NS" get rbg mixed -o json \
  | python3 -c "import json,sys;print(next((r.get('annotations') or {}).get('$WT_ANN','') for r in json.load(sys.stdin)['spec']['roles'] if r['name']=='legacy'))")
if [ "$STORED" != "apps/v1/Deployment" ]; then
  echo "FIXTURE INVALID: legacy role workload-type did not persist (got '$STORED'); aborting." >&2
  exit 3
fi
echo "   RBG mixed: role legacy workload-type = $STORED (verified)"

# --- B) migrated RBG + orphaned legacy Deployment ---------------------------
kubectl apply -f - >/dev/null <<YAML
apiVersion: workloads.x-k8s.io/v1alpha2
kind: RoleBasedGroup
metadata: {name: migrated, namespace: $NS}
spec:
  roles:
    - name: worker
      replicas: 1
      standalonePattern:
        template:
          spec: {containers: [{name: c, image: $IMG}]}
YAML
RBG_UID=$(kubectl -n "$NS" get rbg migrated -o jsonpath='{.metadata.uid}')
kubectl apply -f - >/dev/null <<YAML
apiVersion: apps/v1
kind: Deployment
metadata:
  name: migrated-oldrole
  namespace: $NS
  labels: {"$GN_LBL": migrated}
  ownerReferences:
    - {apiVersion: workloads.x-k8s.io/v1alpha2, kind: RoleBasedGroup, name: migrated, uid: $RBG_UID, controller: true, blockOwnerDeletion: true}
spec:
  replicas: 1
  selector: {matchLabels: {app: migrated-oldrole}}
  template:
    metadata: {labels: {app: migrated-oldrole}}
    spec: {containers: [{name: c, image: $IMG}]}
YAML
echo "   RBG migrated: worker=RoleInstanceSet, plus orphaned Deployment migrated-oldrole"

# --- run the controller under test ------------------------------------------
# Guard: the binary is built from the working tree, so a dirty tree (e.g. a
# harness-bites patch applied concurrently) would silently test the wrong code.
DIRTY=$(git diff --name-only)
if [ -n "$DIRTY" ]; then
  echo "TREE DIRTY -- refusing to run; the binary would not be the code under review:" >&2
  echo "$DIRTY" | sed 's/^/  M /' >&2
  exit 5
fi
echo "   tree clean; building from $(git rev-parse --short HEAD)"

go build -o "$WORK/rbgs-under-test" ./cmd/rbgs || { echo "build failed" >&2; exit 2; }
echo "== controller: --enable-legacy-workloads=false, identity=rbgs-controller-sa =="
echo "   binary sha256: $(sha256sum "$WORK/rbgs-under-test" | cut -c1-16)"
KUBECONFIG="$SA_KUBECONFIG" "$WORK/rbgs-under-test" \
  --enable-legacy-workloads=false --enable-webhooks=none \
  --metrics-bind-address=0 --health-probe-bind-address=0 --zap-log-level=info \
  > "$RUNLOG" 2>&1 &
PID=$!

# Liveness guard: the manager must actually reconcile. The 'migrated' RBG has no
# legacy role, so a healthy controller must give it a RoleInstanceSet.
echo -n "   waiting for the controller to prove it reconciles"
RECONCILED=no
for _ in $(seq 1 40); do
  sleep 3; echo -n "."
  if kubectl -n "$NS" get roleinstanceset migrated-worker >/dev/null 2>&1; then RECONCILED=yes; break; fi
  kill -0 "$PID" 2>/dev/null || break
done
echo
if [ "$RECONCILED" != yes ]; then
  echo "CONTROLLER VOID: no RoleInstanceSet for the legacy-free RBG after ~120s." >&2
  echo "  The manager never reconciled, so nothing below would prove anything." >&2
  echo "  Most common causes (both harness-side): expired SA token -> 'Unauthorized'," >&2
  echo "  or CRDs older than the PR's Go types -> 'cannot unmarshal'. Top errors:" >&2
  grep -o '"error":"[^"]*"' "$RUNLOG" 2>/dev/null | sort | uniq -c | sort -rn | head -4 | sed 's/^/    /' >&2
  exit 4
fi
echo "   controller is reconciling (roleinstanceset/migrated-worker exists)"
sleep 45   # let the legacy paths settle
stop_controller

echo
echo "===== OBSERVATIONS ====="
echo "-- A) mixed RBG: is the healthy 'modern' role reconciled despite the legacy role? --"
echo "   RoleInstanceSets:"; kubectl -n "$NS" get roleinstanceset --no-headers -o custom-columns=NAME:.metadata.name 2>/dev/null | sed 's/^/     /'
echo -n "   mixed-modern RoleInstanceSet present: "
kubectl -n "$NS" get roleinstanceset mixed-modern >/dev/null 2>&1 && echo "YES" || echo "NO  <-- healthy role starved by the legacy role (F3b)"
echo "   RBG mixed roleStatuses: $(kubectl -n "$NS" get rbg mixed -o jsonpath='{.status.roleStatuses}' 2>/dev/null)"
echo "   RBG mixed conditions : $(kubectl -n "$NS" get rbg mixed -o jsonpath='{range .status.conditions[*]}{.type}={.status}({.reason}) {end}' 2>/dev/null)"
echo "   events:"; kubectl -n "$NS" get events --field-selector involvedObject.name=mixed \
  -o custom-columns=REASON:.reason,MSG:.message --no-headers 2>/dev/null | sort -u | head -4 | sed 's/^/     /'
echo -n "   Deployment created for the legacy role: "
kubectl -n "$NS" get deploy mixed-legacy >/dev/null 2>&1 && echo "YES (unexpected -- reject did not hold)" || echo "no (reject held)"

echo
echo "-- B) migrated RBG: is the orphaned legacy Deployment cleaned up? --"
if kubectl -n "$NS" get deploy migrated-oldrole >/dev/null 2>&1; then
  echo "     migrated-oldrole STILL PRESENT -> F4 reproduced (orphan leaked)"
  kubectl -n "$NS" get deploy migrated-oldrole --no-headers \
    -o custom-columns=NAME:.metadata.name,READY:.status.readyReplicas,OWNER:.metadata.ownerReferences[0].name | sed 's/^/     /'
else
  echo "     migrated-oldrole deleted -> cleanup ran (F4 not reproduced live)"
fi

echo
echo "-- controller log: legacy / RBAC signal --"
grep -oE "legacy workload type [^\"]*|Forbidden[^\"]*" "$RUNLOG" | sort -u | head -6 | sed 's/^/   /'
echo "   watch errors: $(grep -c 'Failed to watch' "$RUNLOG" 2>/dev/null)"
cp "$RUNLOG" "$RESULTS/l3-controller.log" 2>/dev/null || true
