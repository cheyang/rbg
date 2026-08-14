#!/bin/bash
# L3 phase 2: (a) is the controller functionally dead? (b) can an operator migrate
# the objects BEFORE the CRD is upgraded, as the PR's upgrade note instructs?
set -uo pipefail
cd /root/rbg424
OUT=/root/rbg424-l3/results; mkdir -p "$OUT"

echo "===== (a) controller readiness vs. actual function ====="
kubectl -n rbg-system get pods --no-headers | awk '{print $1, $2, $3}'
kubectl -n rbg-system get deploy rbgs-controller-manager -o jsonpath='{.spec.template.spec.containers[0].image}{"\n"}'

echo "--- create a fresh RBG and see whether anything reconciles ---"
cat <<'YAML' | kubectl apply -f - 2>&1 | tail -3
apiVersion: workloads.x-k8s.io/v1alpha2
kind: RoleBasedGroup
metadata:
  name: pr424-probe
  namespace: default
spec:
  roles:
  - name: worker
    replicas: 1
    leaderWorkerPattern:
      size: 2
      restartPolicy: RecreateRoleInstanceOnPodRestart
      template:
        spec:
          containers:
          - name: main
            image: nginx:latest
YAML
sleep 60
echo "--- RBG status after 60s ---"
kubectl get rbg pr424-probe -n default -o json | python3 -c 'import json,sys; d=json.load(sys.stdin); print("status:", json.dumps(d.get("status",{}))[:400])'
echo "--- RoleInstanceSets / RoleInstances created for it? ---"
kubectl get roleinstanceset,roleinstance -n default 2>&1 | grep -c pr424-probe || echo "0 (nothing reconciled)"
echo "--- did the deprecated field get materialized by the new defaulting webhook? ---"
kubectl get rbg pr424-probe -n default -o json | python3 -c '
import json,sys
r=json.load(sys.stdin)["spec"]["roles"][0]["leaderWorkerPattern"]
print("restartPolicy:", json.dumps(r.get("restartPolicy")))
print("restartPolicyConfig:", json.dumps(r.get("restartPolicyConfig")))'

echo
echo "===== (b) the ordering trap: migrate BEFORE the CRD upgrade, as the doc says ====="
echo "--- CRD currently served for spec.restartPolicy ---"
kubectl get crd roleinstances.workloads.x-k8s.io -o json | python3 -c 'import json,sys; s=json.load(sys.stdin)["spec"]["versions"][0]["schema"]["openAPIV3Schema"]["properties"]["spec"]["properties"]; print("restartPolicy.type:", s.get("restartPolicy",{}).get("type")); print("restartPolicyConfig in schema:", "restartPolicyConfig" in s)'
echo "--- attempt the documented migration on a real RoleInstance ---"
kubectl patch roleinstance nginx-cluster-backend-0 -n default --type=merge \
  -p '{"spec":{"restartPolicy":null,"restartPolicyConfig":{"type":"None"}}}' 2>&1 | tail -3
echo "--- what actually landed? ---"
kubectl get roleinstance nginx-cluster-backend-0 -n default -o json | python3 -c '
import json,sys
s=json.load(sys.stdin)["spec"]
print("restartPolicy:", json.dumps(s.get("restartPolicy")))
print("restartPolicyConfig:", json.dumps(s.get("restartPolicyConfig")))'
echo L3_PHASE2_DONE
