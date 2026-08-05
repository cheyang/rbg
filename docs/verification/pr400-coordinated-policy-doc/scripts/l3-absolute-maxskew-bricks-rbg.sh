#!/usr/bin/env bash
# L3 (live cluster) evidence for PR #400 finding F1.
#
# POLARITY: canary — records the CURRENT behavior. It PASSES while an absolute
# `scaling.maxSkew` breaks the whole RoleBasedGroup. If the implementation is
# changed to accept absolute values (or a webhook starts rejecting them at admit
# time), this FLIPS and must be inverted.
#
# Claim: doc zh:158 / en:165 advertise that `strategy.scaling.maxSkew` may be an
# absolute value such as `2`. The implementation only accepts percentage strings
# (pkg/coordination/coordinationscaling/scaler.go parsePercentage), and the error
# propagates out of Reconcile — so the RBG creates ZERO pods. The CRD does not
# reject it (x-kubernetes-int-or-string), so the user gets no feedback at apply
# time; the only clue is a FailedCalculateScaling event.
#
# Requires: $KUBECONFIG pointing at a cluster whose rbgs controller actually
# reconciles. Creates a scoped namespace and deletes it on exit.
set -uo pipefail

NS="${NS:-pr400-l3-$$}"
cleanup() { kubectl delete ns "$NS" --wait=false >/dev/null 2>&1 || true; }
trap cleanup EXIT

echo "=== L3: absolute scaling.maxSkew bricks the RoleBasedGroup ==="
echo "namespace : $NS (deleted on exit)"
echo "cluster   : $(kubectl version -o json 2>/dev/null | python3 -c 'import sys,json;print(json.load(sys.stdin)["serverVersion"]["gitVersion"])' 2>/dev/null)"
echo "controller: $(kubectl -n rbg-system get deploy rbgs-controller-manager -o jsonpath='{.spec.template.spec.containers[0].image}' 2>/dev/null)"
echo

kubectl create ns "$NS" --dry-run=client -o yaml | kubectl apply -f - >/dev/null

role() { # $1 = role name
cat <<EOF
  - name: $1
    replicas: 2
    standalonePattern:
      template:
        spec:
          containers:
          - name: x
            image: ${IMAGE:-registry-cn-hongkong.ack.aliyuncs.com/acs/busybox:v1.29.2}
            command: ["sh","-c","sleep 100000"]
EOF
}

{
  echo "apiVersion: workloads.x-k8s.io/v1alpha2"
  echo "kind: RoleBasedGroup"
  echo "metadata: {name: c, namespace: $NS}"
  echo "spec:"
  echo "  roles:"
  role prefill
  role decode
} > /tmp/$NS-rbg.yaml

cat > /tmp/$NS-cp.yaml <<EOF
apiVersion: workloads.x-k8s.io/v1alpha2
kind: CoordinatedPolicy
metadata: {name: c, namespace: $NS}
spec:
  policies:
  - name: p
    roles: [prefill, decode]
    strategy:
      scaling:
        maxSkew: 2          # <-- the absolute value the docs advertise
        progression: OrderReady
EOF

echo "--- 1. apply with maxSkew: 2 (absolute, as documented) ---"
kubectl apply -f /tmp/$NS-rbg.yaml -f /tmp/$NS-cp.yaml
echo "    (accepted by the apiserver: the CRD is x-kubernetes-int-or-string,"
echo "     so nothing warns the user here)"
echo
echo "--- 2. wait 50s, then observe ---"
sleep 50
echo -n "    RBG ready : "; kubectl -n "$NS" get rbg c --no-headers 2>&1 | awk '{print $2}'
echo -n "    pods      : "; kubectl -n "$NS" get pods --no-headers 2>/dev/null | wc -l
echo "    events    :"
kubectl -n "$NS" get events --sort-by=.lastTimestamp 2>/dev/null \
  | grep -iE "FailedCalculateScaling|percent" | tail -2 | sed 's/^/      /'

pods_broken=$(kubectl -n "$NS" get pods --no-headers 2>/dev/null | wc -l)

echo
echo "--- 3. change ONLY maxSkew to \"100%\" and re-observe ---"
kubectl -n "$NS" patch coordinatedpolicy c --type=json \
  -p '[{"op":"replace","path":"/spec/policies/0/strategy/scaling/maxSkew","value":"100%"}]' >/dev/null
sleep 60
echo -n "    RBG ready : "; kubectl -n "$NS" get rbg c --no-headers 2>&1 | awk '{print $2}'
echo -n "    pods      : "; kubectl -n "$NS" get pods --no-headers 2>/dev/null | wc -l
kubectl -n "$NS" get pods --no-headers 2>/dev/null | sed 's/^/      /'

pods_fixed=$(kubectl -n "$NS" get pods --no-headers 2>/dev/null | wc -l)

echo
echo "--- verdict ---"
if [ "$pods_broken" -eq 0 ] && [ "$pods_fixed" -gt 0 ]; then
  echo "CANARY HOLDS: absolute maxSkew -> $pods_broken pods; percentage -> $pods_fixed pods."
  echo "F1 confirmed on a live cluster: the documented absolute form bricks the RBG."
  exit 0
else
  echo "CANARY FLIPPED or inconclusive: broken=$pods_broken fixed=$pods_fixed."
  echo "If broken>0, absolute maxSkew now works -- invert this canary and fix the docs' status."
  exit 1
fi
