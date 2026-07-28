#!/usr/bin/env bash
# L3 / F4: the real enabled -> disabled migration.
#
# Design notes (two earlier fixture designs were invalid, do not regress):
#  * A hand-crafted ownerReference on the orphan Deployment is deleted by
#    Kubernetes GC within ~10s even with NO controller running, so the orphan
#    must be created by the controller itself to get a genuine ownerReference.
#  * RBAC is left at the FULL 18-rule ClusterRole throughout. With the reduced
#    role the SA cannot delete Deployments at all, so a surviving orphan would be
#    ambiguous (code skipped it vs. RBAC forbade it). Full RBAC isolates the code.
#
# Sequence: create a legacy role with the flag ON (controller creates the
# Deployment) -> remove that role from the RBG -> re-run with the flag in $MODE
# and see whether the now-orphaned Deployment is cleaned up.
#
#   MODE=disabled -> expect orphan to SURVIVE (F4)
#   MODE=enabled  -> control, expect orphan to be DELETED (proves the test bites)
set -uo pipefail
export KUBECONFIG=${KUBECONFIG:-$HOME/.kube/config}
W=/root/pr413-verify
MODE=${MODE:-disabled}
NS=pr413-f4-$MODE
IMG=anolis-registry.cn-zhangjiakou.cr.aliyuncs.com/openanolis/nginx:1.14.1-8.6
BASE=$W/baseline
cd "$W/repo"

# Full RBAC for the whole run.
kubectl replace -f "$BASE/baseline-clusterrole.yaml" >/dev/null 2>&1 || kubectl apply -f "$BASE/baseline-clusterrole.yaml" >/dev/null 2>&1
echo "ClusterRole rules: $(kubectl get clusterrole rbgs-controller-role -o json | python3 -c 'import json,sys;print(len(json.load(sys.stdin)["rules"]))') (full)"

kubectl delete ns $NS --ignore-not-found --wait=true >/dev/null 2>&1
kubectl create ns $NS >/dev/null

run_controller() {  # $1 = true|false, $2 = seconds
  KUBECONFIG=$W/sa-kubeconfig.yaml $W/probe-bin --enable-legacy-workloads=$1 \
    --enable-webhooks=none --metrics-bind-address=0 --health-probe-bind-address=0 \
    --zap-log-level=info >> $W/f4mig-$MODE.log 2>&1 &
  CPID=$!
  sleep "$2"
  kill -9 $CPID 2>/dev/null; wait $CPID 2>/dev/null
}

: > $W/f4mig-$MODE.log

# --- phase 1: flag ON, RBG with a legacy Deployment role ---------------------
cat <<Y | kubectl apply -f - >/dev/null
apiVersion: workloads.x-k8s.io/v1alpha2
kind: RoleBasedGroup
metadata: {name: app, namespace: $NS}
spec:
  roles:
  - name: keep
    replicas: 1
    standalonePattern:
      template:
        spec:
          containers: [{name: c, image: $IMG}]
  - name: old
    replicas: 1
    annotations: {rbg.workloads.x-k8s.io/role-workload-type: apps/v1/Deployment}
    standalonePattern:
      template:
        spec:
          containers: [{name: c, image: $IMG}]
Y
echo "phase1: running with legacy ENABLED so the controller creates the Deployment"
run_controller true 50
OWNED=$(kubectl -n $NS get deploy app-old -o jsonpath='{.metadata.ownerReferences[0].kind}/{.metadata.ownerReferences[0].name} uid={.metadata.ownerReferences[0].uid}' 2>/dev/null)
if [ -z "$OWNED" ]; then
  echo "FIXTURE INVALID: controller did not create Deployment app-old with the flag ON."
  grep -o '"error":"[^"]*"' $W/f4mig-$MODE.log | sort -u | head -3 | sed 's/^/  /'
  exit 3
fi
echo "  Deployment app-old created, ownerRef = $OWNED"

# --- phase 2: remove the legacy role -> its Deployment is now an orphan ------
kubectl -n $NS patch rbg app --type=json -p '[{"op":"remove","path":"/spec/roles/1"}]' >/dev/null
echo "phase2: removed role 'old' from the RBG; roles now = $(kubectl -n $NS get rbg app -o jsonpath='{range .spec.roles[*]}{.name} {end}')"
echo "  orphan present before phase3: $(kubectl -n $NS get deploy app-old -o name 2>/dev/null || echo NONE)"

# --- phase 3: re-run with the flag in $MODE ----------------------------------
FLAG=false; [ "$MODE" = enabled ] && FLAG=true
echo "phase3: running with --enable-legacy-workloads=$FLAG (full RBAC)"
run_controller $FLAG 60

echo "=== VERDICT (MODE=$MODE) ==="
if kubectl -n $NS get deploy app-old >/dev/null 2>&1; then
  echo "  app-old SURVIVED -> orphaned legacy Deployment leaked"
  kubectl -n $NS get deploy app-old --no-headers -o custom-columns=N:.metadata.name,R:.status.readyReplicas | sed 's/^/    /'
else
  echo "  app-old DELETED -> orphan cleanup ran"
fi
echo "  'delete deploy' log lines : $(grep -c 'delete deploy' $W/f4mig-$MODE.log)"
echo "  Forbidden                 : $(grep -ci forbidden $W/f4mig-$MODE.log)"
echo "  legacy reject lines       : $(grep -c 'not supported when legacy' $W/f4mig-$MODE.log)"
echo "  reconciles finished       : $(grep -c 'Finished reconciling' $W/f4mig-$MODE.log)"
echo "  surviving 'keep' role RIS : $(kubectl -n $NS get roleinstanceset -o name --no-headers 2>/dev/null | tr '\n' ' ')"
kubectl delete ns $NS --wait=false >/dev/null 2>&1
