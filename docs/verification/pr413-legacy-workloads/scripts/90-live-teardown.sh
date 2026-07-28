#!/usr/bin/env bash
# L3 teardown: restore the cluster to the state captured before verification.
# Idempotent; safe to run twice or after a half-failed setup. ALWAYS run this.
set -uo pipefail
: "${KUBECONFIG:=$HOME/.kube/config}"
export KUBECONFIG
NS=rbg-system
BASE=${BASE:-/root/pr413-verify/baseline}
STRIP='
import yaml,sys
d=yaml.safe_load(open(sys.argv[1]))
m=d.setdefault("metadata",{})
for k in ["resourceVersion","uid","creationTimestamp","generation","managedFields","selfLink","annotations"]:
    m.pop(k,None)
yaml.safe_dump(d,open(sys.argv[2],"w"),default_flow_style=False)
print(len(d["rules"]))
'

echo "== deleting verification namespaces =="
for n in pr413-verify pr413-probe pr413-who pr413-decide pr413-f4 pr413-gc pr413-f4-disabled pr413-f4-enabled; do
  kubectl delete ns "$n" --ignore-not-found --wait=false >/dev/null 2>&1
done
echo "   requested deletion of pr413-* namespaces"

echo "== restoring baseline ClusterRole =="
# 'kubectl replace' fails on a snapshot that still carries resourceVersion/uid,
# and that failure is easy to miss: it silently leaves the controller running
# with the REDUCED role. Strip server-owned metadata, server-side apply, verify.
if [ -f "$BASE/baseline-clusterrole.yaml" ]; then
  WANT=$(python3 -c "$STRIP" "$BASE/baseline-clusterrole.yaml" /tmp/cr-restore.yaml)
  kubectl apply --server-side --force-conflicts -f /tmp/cr-restore.yaml >/dev/null 2>&1
  GOT=$(kubectl get clusterrole rbgs-controller-role -o json \
        | python3 -c 'import json,sys;print(len(json.load(sys.stdin)["rules"]))' 2>/dev/null)
  if [ "$WANT" = "$GOT" ]; then
    echo "   restored: $GOT rules (matches baseline)"
  else
    echo "   ERROR: baseline has $WANT rules, cluster has $GOT -- controller may lack legacy RBAC" >&2
  fi
else
  echo "   WARN: no baseline ClusterRole snapshot at $BASE" >&2
fi

echo "== restoring controller replicas =="
WANT_REPL=$(awk '/^spec:/{s=1} s&&/^  replicas:/{print $2;exit}' "$BASE/baseline-deploy.yaml" 2>/dev/null)
WANT_REPL=${WANT_REPL:-2}
kubectl -n "$NS" scale deploy/rbgs-controller-manager --replicas="$WANT_REPL" >/dev/null 2>&1 || true
kubectl -n "$NS" rollout status deploy/rbgs-controller-manager --timeout=180s 2>&1 | tail -1 | sed 's/^/   /'

echo "== final state =="
kubectl -n "$NS" get deploy rbgs-controller-manager --no-headers \
  -o custom-columns=NAME:.metadata.name,READY:.status.readyReplicas,DESIRED:.spec.replicas | sed 's/^/   /'
for r in deployments statefulsets; do
  printf "   SA can create/delete %-13s %s / %s\n" "$r" \
    "$(kubectl auth can-i create $r --as=system:serviceaccount:$NS:rbgs-controller-sa 2>/dev/null)" \
    "$(kubectl auth can-i delete $r --as=system:serviceaccount:$NS:rbgs-controller-sa 2>/dev/null)"
done
echo
echo "NOTE: the cluster CRDs were upgraded to this PR's config/crd/bases during"
echo "      verification and are NOT reverted by this script."
