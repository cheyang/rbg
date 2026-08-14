#!/bin/bash
set -uo pipefail
RB=/root/rbg424-l3/rollback

echo "===== which CRDs are still on the PR schema? ====="
for c in roleinstances roleinstancesets rolebasedgroups rolebasedgroupsets; do
  echo -n "$c: "
  kubectl get crd $c.workloads.x-k8s.io -o json | python3 -c '
import json,sys
def walk(o):
    if isinstance(o,dict):
        if "restartPolicyConfig" in o.get("properties",{}): return True
        return any(walk(v) for v in o.values())
    if isinstance(o,list): return any(walk(v) for v in o)
    return False
print("PR schema" if walk(json.load(sys.stdin)["spec"]) else "main schema")'
done

echo "===== force-restore the main CRDs ====="
for f in "$RB"/*.crd.main.yaml; do
  n=$(basename "$f" .crd.main.yaml)
  python3 - "$f" <<'PY' > /tmp/clean.yaml
import sys,re
raw=open(sys.argv[1]).read()
# drop server-populated fields so a plain apply is accepted
import yaml
d=yaml.safe_load(raw)
d.get("metadata",{}).pop("managedFields",None)
d.get("metadata",{}).pop("resourceVersion",None)
d.get("metadata",{}).pop("uid",None)
d.get("metadata",{}).pop("creationTimestamp",None)
d.get("metadata",{}).pop("generation",None)
d.pop("status",None)
yaml.safe_dump(d,sys.stdout,default_flow_style=False)
PY
  echo -n "$n: "
  kubectl apply --server-side --force-conflicts -f /tmp/clean.yaml 2>&1 | tail -1
done
sleep 15

echo "===== verify ====="
for c in roleinstances roleinstancesets rolebasedgroups rolebasedgroupsets; do
  echo -n "$c: "
  kubectl get crd $c.workloads.x-k8s.io -o json | python3 -c '
import json,sys
def walk(o):
    if isinstance(o,dict):
        if "restartPolicyConfig" in o.get("properties",{}): return True
        return any(walk(v) for v in o.values())
    if isinstance(o,list): return any(walk(v) for v in o)
    return False
print("PR schema" if walk(json.load(sys.stdin)["spec"]) else "main schema")'
done

echo "===== now restore the RoleInstance restartPolicy objects ====="
for ri in $(kubectl get roleinstances -n default -o name); do
  kubectl patch "$ri" -n default --type=merge \
    -p '{"spec":{"restartPolicy":{"type":"None","baseDelaySeconds":30,"maxDelaySeconds":600}}}' 2>&1 | tail -1
done
kubectl get roleinstances -A -o json | python3 -c '
import json,sys
for i in json.load(sys.stdin)["items"]:
    print(i["metadata"]["name"], "restartPolicy=", json.dumps(i["spec"].get("restartPolicy")),
          "restartPolicyConfig=", json.dumps(i["spec"].get("restartPolicyConfig")))'

echo "===== final health ====="
kubectl -n rbg-system get deploy rbgs-controller-manager -o jsonpath='{.spec.template.spec.containers[0].image}{"\n"}'
kubectl get rbg -A 2>&1 | tail -3
sleep 30
for p in $(kubectl -n rbg-system get pods -o name | sed 's|pod/||'); do
  echo "$p decode errors = $(kubectl -n rbg-system logs "$p" --tail=3000 2>/dev/null | grep -c 'cannot unmarshal')"
done
kubectl get mutatingwebhookconfigurations 2>/dev/null | grep -i rbg || echo "no rbg mutating webhook (good)"
echo ROLLBACK2_DONE
