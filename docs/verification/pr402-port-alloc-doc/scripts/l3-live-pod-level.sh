#!/usr/bin/env bash
# L3 (live cluster, real Pods) evidence for the PR #402 doc claims.
#
# Everything here was previously provable only at unit level, because the
# sandbox cluster could not start Pods (controller/CRD restartPolicy mismatch).
# After upgrading rbgs to v0.8.0-47cfe17 with the port allocator enabled, these
# are observable on real Pods.
#
# Requires:
#   - $KUBECONFIG pointing at a cluster whose rbgs controller reconciles
#   - the controller started with --enable-port-allocator=true
#     (helm: --set controller.features.portAllocator.enabled=true)
#
# POLARITY: contract for everything except where noted. These assert the shapes
# the doc promises, so they stay green while the doc is accurate.
set -uo pipefail

NS="${NS:-pr402-l3-$$}"
IMAGE="${IMAGE:-registry-cn-hongkong.ack.aliyuncs.com/acs/busybox:v1.29.2}"
cleanup() { kubectl delete ns "$NS" --wait=false >/dev/null 2>&1 || true; }
trap cleanup EXIT

echo "=== L3: port allocation & service discovery on real Pods ==="
echo "namespace : $NS (deleted on exit)"
echo "cluster   : $(kubectl version -o json 2>/dev/null | python3 -c 'import sys,json;print(json.load(sys.stdin)["serverVersion"]["gitVersion"])' 2>/dev/null)"
echo "controller: $(kubectl -n rbg-system get deploy rbgs-controller-manager -o jsonpath='{.spec.template.spec.containers[0].image}' 2>/dev/null)"
echo -n "port allocator flags: "
kubectl -n rbg-system get deploy rbgs-controller-manager -o jsonpath='{.spec.template.spec.containers[0].args}' 2>/dev/null \
  | tr ',' '\n' | grep -iE "port" | tr '\n' ' '; echo
echo

kubectl create ns "$NS" --dry-run=client -o yaml | kubectl apply -f - >/dev/null

# --- Part A: a simple standalone role -> env vars, ConfigMap, headless Service
cat > /tmp/$NS-a.yaml <<EOF
apiVersion: workloads.x-k8s.io/v1alpha2
kind: RoleBasedGroup
metadata: {name: a, namespace: $NS}
spec:
  roles:
  - name: prefill
    replicas: 2
    standalonePattern:
      template:
        spec:
          containers:
          - name: c
            image: $IMAGE
            command: ["sh","-c","sleep 100000"]
EOF

# --- Part B: customComponentsPattern -> port allocation + component discovery
cat > /tmp/$NS-b.yaml <<EOF
apiVersion: workloads.x-k8s.io/v1alpha2
kind: RoleBasedGroup
metadata: {name: pd, namespace: $NS}
spec:
  roles:
  - name: prefill
    replicas: 1
    customComponentsPattern:
      components:
      - name: leader
        size: 1
        annotations:
          rolebasedgroup.workloads.x-k8s.io/port-allocator: |
            {"allocations":[{"name":"leader-grpc","env":"LEADER_GRPC_PORT","scope":"PodScoped"}]}
        template:
          spec:
            containers:
            - name: c
              image: $IMAGE
              command: ["sh","-c","sleep 100000"]
      - name: worker
        size: 2
        annotations:
          rolebasedgroup.workloads.x-k8s.io/component-discovery: |
            {"addressRefs":[{"env":"LEADER_ADDR","component":"leader","index":0}]}
        template:
          spec:
            containers:
            - name: c
              image: $IMAGE
              command: ["sh","-c","sleep 100000"]
EOF

kubectl apply -f /tmp/$NS-a.yaml -f /tmp/$NS-b.yaml >/dev/null
echo "--- waiting for 5 pods Running (2 standalone + 3 components) ---"
for i in $(seq 1 60); do
  r=$(kubectl -n "$NS" get pods --no-headers 2>/dev/null | awk '$3=="Running"' | wc -l)
  [ "$r" -ge 5 ] && break
  sleep 4
done
kubectl -n "$NS" get pods --no-headers 2>/dev/null | sed 's/^/    /'
echo

echo "--- 1. injected RBG_* env vars on a standaloneP attern pod (doc zh:91-118) ---"
echo "    The doc splits these into 4 sub-tables by applicability; a standalone"
echo "    role is NOT LeaderWorkerPattern, so the 3 RBG_LWP_* vars must be ABSENT."
kubectl -n "$NS" get pod a-prefill-0 -o json 2>/dev/null > /tmp/$NS-p.json
python3 - "/tmp/$NS-p.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
envs = d["spec"]["containers"][0].get("env", [])
rbg = [e for e in envs if e["name"].startswith("RBG_")]
print("    count:", len(rbg))
for e in sorted(rbg, key=lambda x: x["name"]):
    v = e.get("value")
    if v is None:
        fr = e.get("valueFrom", {}).get("fieldRef", {})
        v = "<fieldRef %s>" % fr.get("fieldPath")
    print("      %-26s = %s" % (e["name"], v))
lwp = [e["name"] for e in rbg if e["name"].startswith("RBG_LWP_")]
print("    RBG_LWP_* present:", lwp or "none (expected for standalonePattern)")
print("    hostname/subdomain:", d["spec"].get("hostname"), "/", d["spec"].get("subdomain"))
mounts = [(m["name"], m["mountPath"], m.get("readOnly")) for m in d["spec"]["containers"][0].get("volumeMounts", [])]
print("    volumeMounts:", [m for m in mounts if "rbg" in m[0]])
PY
echo

echo "--- 2. headless Service naming + attributes (doc zh:32-38) ---"
kubectl -n "$NS" get svc -o custom-columns=NAME:.metadata.name,CLUSTERIP:.spec.clusterIP,PUBNOTREADY:.spec.publishNotReadyAddresses --no-headers 2>/dev/null | sed 's/^/    /'
echo

echo "--- 3. ConfigMap /etc/rbg/config.yaml -- ACTUAL key order (doc F4) ---"
echo "    The guide prints size->roles and size->instances; sigs.k8s.io/yaml"
echo "    emits alphabetically, so expect name->roles->size and instances->size."
kubectl -n "$NS" get cm a -o jsonpath='{.data.config\.yaml}' 2>/dev/null | sed 's/^/    /'
echo

echo "--- 4. allocated port + component-discovery FQDN (doc F3) ---"
echo "    The doc truncates LEADER_ADDR with an ellipsis that swallows"
echo "    '-leader-0' and its separating dot. The real value must contain it."
for p in $(kubectl -n "$NS" get pods -o jsonpath='{.items[*].metadata.name}' 2>/dev/null | tr ' ' '\n' | grep '^pd-'); do
  vals=$(kubectl -n "$NS" get pod "$p" -o jsonpath='{range .spec.containers[0].env[*]}{.name}={.value}{"\n"}{end}' 2>/dev/null \
          | grep -E "LEADER_GRPC_PORT|LEADER_ADDR")
  [ -n "$vals" ] && echo "    $p:" && echo "$vals" | sed 's/^/      /'
done
echo

port=$(kubectl -n "$NS" get pod pd-prefill-0-leader-0 -o jsonpath='{.spec.containers[0].env[?(@.name=="LEADER_GRPC_PORT")].value}' 2>/dev/null)
addr=$(kubectl -n "$NS" get pod pd-prefill-0-worker-0 -o jsonpath='{.spec.containers[0].env[?(@.name=="LEADER_ADDR")].value}' 2>/dev/null)

echo "--- verdict ---"
rc=0
if [ -n "$port" ] && [ "$port" -ge 30000 ] && [ "$port" -le 34999 ]; then
  echo "  OK   allocated port $port is inside the documented 30000-34999 range"
else
  echo "  FAIL allocated port '$port' outside documented range"; rc=1
fi
case "$addr" in
  *-leader-0.*.svc.cluster.local)
    echo "  OK   LEADER_ADDR is a full FQDN containing '-leader-0.': $addr" ;;
  *)
    echo "  FAIL LEADER_ADDR unexpected shape: '$addr'"; rc=1 ;;
esac
exit $rc
