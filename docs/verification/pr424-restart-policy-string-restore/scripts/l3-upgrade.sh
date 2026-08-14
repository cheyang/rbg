#!/bin/bash
# L3 live layer, phase 1: upgrade the real cluster from main to the PR 424 build and
# observe whether the 4 pre-existing object-shaped RoleInstances strand the controller.
set -uo pipefail
cd /root/rbg424
OUT=/root/rbg424-l3/results
mkdir -p "$OUT"

echo "===== BEFORE: stored restartPolicy shapes ====="
kubectl get roleinstances -A -o json | python3 -c '
import json,sys
for i in json.load(sys.stdin)["items"]:
    print(i["metadata"]["namespace"], i["metadata"]["name"], "->", json.dumps(i["spec"].get("restartPolicy")))
' | tee "$OUT/before-shapes.txt"

echo "===== BEFORE: controller healthy? ====="
kubectl -n rbg-system get pods -o wide --no-headers | tee "$OUT/before-pods.txt"

echo "===== helm upgrade to PR 424 build ====="
helm upgrade rbgs deploy/helm/rbgs -n rbg-system \
  --set controller.image.repository=rolebasedgroup/rbgs-controller \
  --set controller.image.tag=pr424-843fd2d9 \
  --set crdUpgrade.enabled=false \
  --wait --timeout 6m 2>&1 | tail -20
echo "helm rc=$?"

echo "===== CRD now says restartPolicy is: ====="
kubectl get crd roleinstances.workloads.x-k8s.io -o json \
 | python3 -c 'import json,sys; s=json.load(sys.stdin)["spec"]["versions"][0]["schema"]["openAPIV3Schema"]["properties"]["spec"]["properties"]; print("restartPolicy:", json.dumps(s.get("restartPolicy",{}).get("type"))); print("restartPolicyConfig present:", "restartPolicyConfig" in s)' | tee "$OUT/after-crd.txt"

echo "===== stored shape as SERVED after the CRD upgrade ====="
kubectl get roleinstances -A -o json | python3 -c '
import json,sys
for i in json.load(sys.stdin)["items"]:
    print(i["metadata"]["namespace"], i["metadata"]["name"], "->", json.dumps(i["spec"].get("restartPolicy")))
' | tee "$OUT/after-shapes.txt"

sleep 45
echo "===== controller pods ====="
kubectl -n rbg-system get pods -o wide --no-headers | tee "$OUT/after-pods.txt"

echo "===== controller logs: list/decode/cache failures ====="
for p in $(kubectl -n rbg-system get pods -o name | sed 's|pod/||'); do
  echo "--- $p ---"
  kubectl -n rbg-system logs "$p" --tail=4000 2>/dev/null \
    | grep -iE "restartPolicy|cannot unmarshal|failed to list|Failed to watch|timed out waiting for cache|RoleInstance" \
    | tail -25
done | tee "$OUT/after-logs.txt"

echo L3_PHASE1_DONE
