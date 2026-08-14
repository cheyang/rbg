#!/bin/bash
# L3 teardown: restore the sandbox cluster to its pre-experiment state (main build).
set -uo pipefail
cd /root/rbg424
RB=/root/rbg424-l3/rollback

echo "===== remove harness objects ====="
kubectl delete rbg pr424-f2 pr424-probe -n default --ignore-not-found 2>&1 | tail -3
sleep 20

echo "===== helm rollback to revision 3 (main build) ====="
helm rollback rbgs 3 -n rbg-system --wait --timeout 6m 2>&1 | tail -5
echo "helm rc=$?"

echo "===== restore the main CRDs ====="
for f in "$RB"/*.crd.main.yaml; do
  kubectl apply --server-side --force-conflicts -f "$f" 2>&1 | tail -1
done
sleep 10
kubectl get crd roleinstances.workloads.x-k8s.io -o json | python3 -c 'import json,sys; s=json.load(sys.stdin)["spec"]["versions"][0]["schema"]["openAPIV3Schema"]["properties"]["spec"]["properties"]; print("restartPolicy.type:", s.get("restartPolicy",{}).get("type")); print("restartPolicyConfig in schema:", "restartPolicyConfig" in s)'

echo "===== restore the 4 RoleInstances to the object shape main expects ====="
for ri in $(kubectl get roleinstances -n default -o name); do
  kubectl patch "$ri" -n default --type=merge \
    -p '{"spec":{"restartPolicy":{"type":"None","baseDelaySeconds":30,"maxDelaySeconds":600}}}' 2>&1 | tail -1
done
kubectl get roleinstances -A -o json | python3 -c '
import json,sys
for i in json.load(sys.stdin)["items"]:
    print(i["metadata"]["name"], "restartPolicy=", json.dumps(i["spec"].get("restartPolicy")))'

echo "===== the PR mutating webhook must be gone (failurePolicy=Fail would block all writes) ====="
kubectl get mutatingwebhookconfigurations 2>/dev/null | grep -i rbg || echo "none (good)"

echo "===== controller health ====="
kubectl -n rbg-system get deploy rbgs-controller-manager -o jsonpath='{.spec.template.spec.containers[0].image}{"\n"}'
kubectl -n rbg-system get pods --no-headers | awk '{print $1, $2, $3}'
sleep 40
for p in $(kubectl -n rbg-system get pods -o name | sed 's|pod/||'); do
  echo "$p decode errors = $(kubectl -n rbg-system logs "$p" --tail=3000 2>/dev/null | grep -c 'cannot unmarshal object into Go struct field RoleInstanceSpec')"
done
echo "===== original workload intact? ====="
kubectl get rbg -A 2>&1 | tail -5
kubectl get pods -n default --no-headers 2>/dev/null | grep nginx-cluster | awk '{print $1, $2, $3}'
echo ROLLBACK_DONE
