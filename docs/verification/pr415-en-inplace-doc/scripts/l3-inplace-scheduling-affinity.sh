#!/usr/bin/env bash
# L3 evidence for the in-place SCHEDULING claims of the PR #415 doc.
#
# Corrects an earlier invalid run of mine that put the annotation on the RBG's
# metadata.annotations. The doc puts it at spec.roles[].annotations (en guide:164,
# :272) together with rolloutStrategy.rollingUpdate.type: RecreatePod.
#
# The DECISIVE observable is whether the recreated Pod carries injected
# nodeAffinity -- not merely whether it lands on the same node. With replicas
# spread across a small cluster, "same node" is explainable by the scheduler
# spreading evenly, so node placement alone proves nothing.
#
# Doc claims under test (en guide:206-208 / :313-316):
#   Preferred -> preferredDuringScheduling with weight=100 toward the historical node
#   Required  -> requiredDuringScheduling toward the historical node
set -uo pipefail

NS="${NS:-l3s2}"
IMAGE="${IMAGE:-registry-cn-hongkong.ack.aliyuncs.com/acs/busybox:v1.29.2}"

kubectl create ns "$NS" --dry-run=client -o yaml | kubectl apply -f - >/dev/null 2>&1

mk() { # $1=name $2=Preferred|Required
cat <<EOF
apiVersion: workloads.x-k8s.io/v1alpha2
kind: RoleBasedGroup
metadata:
  name: $1
  namespace: $NS
spec:
  roles:
    - name: backend
      replicas: 2
      annotations:
        rbg.workloads.x-k8s.io/role-inplace-scheduling: "$2"
      rolloutStrategy:
        type: RollingUpdate
        rollingUpdate:
          type: RecreatePod
          maxUnavailable: 1
      standalonePattern:
        template:
          spec:
            containers:
              - name: c
                image: $IMAGE
                command: ["sh","-c","sleep 100000"]
                env:
                  - name: new_env
                    value: v1
EOF
}

for v in Preferred Required; do
  mk "$(echo "$v" | tr 'A-Z' 'a-z')" "$v" | kubectl apply -f - >/dev/null
done

echo "=== waiting for 4 pods Running ==="
for i in $(seq 1 60); do
  r=$(kubectl -n "$NS" get pods --no-headers 2>/dev/null | awk '$3=="Running"' | wc -l)
  [ "$r" -ge 4 ] && break
  sleep 3
done

dump_aff() { # $1 = pod
  kubectl -n "$NS" get pod "$1" -o json 2>/dev/null | python3 -c '
import sys, json
d = json.load(sys.stdin)
aff = (d["spec"].get("affinity") or {}).get("nodeAffinity")
print("      node:", d["spec"].get("nodeName"))
if not aff:
    print("      nodeAffinity: <NONE>")
    sys.exit()
req = aff.get("requiredDuringSchedulingIgnoredDuringExecution")
pref = aff.get("preferredDuringSchedulingIgnoredDuringExecution")
if req:
    for t in req.get("nodeSelectorTerms", []):
        for e in t.get("matchExpressions", []):
            print("      REQUIRED :", e["key"], e["operator"], e.get("values"))
if pref:
    for t in pref:
        for e in t.get("preference", {}).get("matchExpressions", []):
            print("      PREFERRED: weight=%s" % t.get("weight"), e["key"], e["operator"], e.get("values"))
'
}

snap() { kubectl -n "$NS" get pods -o custom-columns=NAME:.metadata.name,NODE:.spec.nodeName,UID:.metadata.uid --no-headers 2>/dev/null | sort; }

echo "=== BEFORE ==="
snap | tee /tmp/$NS-before.txt
echo "  affinity before the update (expected: none yet -- there is no history):"
for p in preferred-backend-0 required-backend-0; do echo "    $p"; dump_aff "$p"; done

echo
echo "=== trigger: env change (non-image) with type RecreatePod ==="
for n in preferred required; do
  kubectl -n "$NS" patch rbg "$n" --type=json \
    -p '[{"op":"replace","path":"/spec/roles/0/standalonePattern/template/spec/containers/0/env/0/value","value":"v2"}]' >/dev/null
done

echo "=== polling for the recreated pods, capturing affinity AS SOON AS it appears ==="
# Catch the injected affinity even if the pod is short-lived / rescheduled.
for i in $(seq 1 90); do
  hits=0
  for p in $(kubectl -n "$NS" get pods -o jsonpath='{.items[*].metadata.name}' 2>/dev/null); do
    has=$(kubectl -n "$NS" get pod "$p" -o jsonpath='{.spec.affinity.nodeAffinity}' 2>/dev/null)
    if [ -n "$has" ]; then
      echo "  t=+$((i*3))s  $p HAS nodeAffinity:"
      dump_aff "$p"
      hits=$((hits+1))
    fi
  done
  [ "$hits" -ge 2 ] && break
  sleep 3
done
[ "${hits:-0}" -eq 0 ] && echo "  ** no pod ever showed injected nodeAffinity during the whole rollout **"

echo
echo "=== settle, then AFTER ==="
sleep 45
snap | tee /tmp/$NS-after.txt
echo "  affinity on the settled pods:"
for p in $(kubectl -n "$NS" get pods -o jsonpath='{.items[*].metadata.name}' 2>/dev/null); do echo "    $p"; dump_aff "$p"; done

echo
echo "=== node preserved? recreated? ==="
join /tmp/$NS-before.txt /tmp/$NS-after.txt 2>/dev/null | while read -r name n1 u1 n2 u2; do
  nd="NODE-SAME"; [ "$n1" != "$n2" ] && nd="NODE-MOVED($n1 -> $n2)"
  ud="uid-same(NOT recreated)"; [ "$u1" != "$u2" ] && ud="uid-changed(recreated)"
  printf "  %-24s %-38s %s\n" "$name" "$nd" "$ud"
done

echo
echo "=== how many nodes are even schedulable? (needed to judge whether NODE-SAME is meaningful) ==="
kubectl get nodes --no-headers 2>/dev/null | wc -l | sed 's/^/  nodes: /'
echo "  NOTE: with replicas=2 on a 3-node cluster, NODE-SAME is weak evidence on its own."
echo "        The affinity injection above is the real signal."

echo
echo "=== env updated? ==="
kubectl -n "$NS" get pods -o jsonpath='{range .items[*]}{.metadata.name}{"="}{.spec.containers[0].env[?(@.name=="new_env")].value}{"\n"}{end}' 2>/dev/null | sort
echo
echo "(namespace $NS left in place for inspection; delete with: kubectl delete ns $NS)"
