#!/bin/bash
# L3 phase 3: the working order (CRDs first), recovery, and the live F2 test.
set -uo pipefail
cd /root/rbg424
OUT=/root/rbg424-l3/results; mkdir -p "$OUT"

echo "===== restore the object phase 2 damaged ====="
kubectl patch roleinstance nginx-cluster-backend-0 -n default --type=merge \
  -p '{"spec":{"restartPolicy":{"type":"None","baseDelaySeconds":30,"maxDelaySeconds":600}}}' 2>&1 | tail -2

echo
echo "===== apply the PR 424 CRDs (what the crd-upgrade Job would do) ====="
kubectl apply --server-side --force-conflicts -f config/crd/bases/ 2>&1 | tail -12
sleep 10
kubectl get crd roleinstances.workloads.x-k8s.io -o json | python3 -c 'import json,sys; s=json.load(sys.stdin)["spec"]["versions"][0]["schema"]["openAPIV3Schema"]["properties"]["spec"]["properties"]; print("restartPolicy.type:", s.get("restartPolicy",{}).get("type")); print("restartPolicyConfig in schema:", "restartPolicyConfig" in s)'

echo
echo "===== what is SERVED for the existing objects now ====="
kubectl get roleinstances -A -o json | python3 -c '
import json,sys
for i in json.load(sys.stdin)["items"]:
    print(i["metadata"]["name"], "restartPolicy=", json.dumps(i["spec"].get("restartPolicy")))'

echo
echo "===== migrate all RoleInstances with the merge-patch recipe ====="
for ri in $(kubectl get roleinstances -n default -o name); do
  kubectl patch "$ri" -n default --type=merge \
    -p '{"spec":{"restartPolicy":null,"restartPolicyConfig":{"type":"None"}}}' 2>&1 | tail -1
done
kubectl get roleinstances -n default -o json | python3 -c '
import json,sys
for i in json.load(sys.stdin)["items"]:
    print(i["metadata"]["name"], "restartPolicy=", json.dumps(i["spec"].get("restartPolicy")),
          "restartPolicyConfig=", json.dumps(i["spec"].get("restartPolicyConfig")))'

echo
echo "===== restart the controller and confirm the informer recovers ====="
kubectl -n rbg-system rollout restart deploy rbgs-controller-manager >/dev/null 2>&1
kubectl -n rbg-system rollout status deploy rbgs-controller-manager --timeout=5m 2>&1 | tail -2
sleep 40
DEAD=0
for p in $(kubectl -n rbg-system get pods -o name | sed 's|pod/||'); do
  n=$(kubectl -n rbg-system logs "$p" --tail=3000 2>/dev/null | grep -c "cannot unmarshal object into Go struct field RoleInstanceSpec")
  echo "$p: RoleInstance decode errors = $n"
  DEAD=$((DEAD+n))
done
echo "TOTAL decode errors after migration = $DEAD"

echo
echo "===== LIVE F2: v0.7.0-shaped RBG, then kubectl edit the deprecated field ====="
cat <<'YAML' | kubectl apply -f - 2>&1 | tail -2
apiVersion: workloads.x-k8s.io/v1alpha2
kind: RoleBasedGroup
metadata:
  name: pr424-f2
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
sleep 15
echo "--- as stored after admission (new defaulting webhook) ---"
kubectl get rbg pr424-f2 -n default -o json | python3 -c '
import json,sys
r=json.load(sys.stdin)["spec"]["roles"][0]["leaderWorkerPattern"]
print("restartPolicy      =", json.dumps(r.get("restartPolicy")))
print("restartPolicyConfig=", json.dumps(r.get("restartPolicyConfig")))'

echo "--- operator flips the deprecated field to None (what kubectl edit sends) ---"
kubectl patch rbg pr424-f2 -n default --type=json \
  -p '[{"op":"replace","path":"/spec/roles/0/leaderWorkerPattern/restartPolicy","value":"None"}]' 2>&1 | tail -1
sleep 10
kubectl get rbg pr424-f2 -n default -o json | python3 -c '
import json,sys
r=json.load(sys.stdin)["spec"]["roles"][0]["leaderWorkerPattern"]
print("restartPolicy      =", json.dumps(r.get("restartPolicy")), " <- operator asked for None")
print("restartPolicyConfig=", json.dumps(r.get("restartPolicyConfig")), " <- what the controller obeys")'

echo "--- and what actually reached the RoleInstance ---"
sleep 30
kubectl get roleinstance -n default -o json | python3 -c '
import json,sys
for i in json.load(sys.stdin)["items"]:
    if i["metadata"]["name"].startswith("pr424-f2"):
        s=i["spec"]
        print(i["metadata"]["name"], "restartPolicy=", json.dumps(s.get("restartPolicy")),
              "restartPolicyConfig=", json.dumps(s.get("restartPolicyConfig")))'
echo L3_PHASE3_DONE
