#!/usr/bin/env bash
# L3 / F1 — does `tolerations` supplied via spec.roleTemplates + templateRef.patch
# reach the real Pod?
#
# PR #410 (yaml-rules.md:6, yaml-rules.md:570, SKILL.md:230) says no: "tolerations are
# silently dropped by RBG controller when going through templateRef". That claim is the
# sole justification for the skill's rule "if the GPU nodes have taints you MUST NOT use
# roleTemplates/templateRef".
#
# Polarity: CONTRACT (refutes the doc claim). Exit 0 == tolerations DID reach the Pod
# == PR #410's claim is false.
set -euo pipefail
NS="${VERIFY_NS:-rbg-verify-pr410}"
RBG=f1-templateref-tol
IMG="${VERIFY_IMAGE:-anolis-registry.cn-zhangjiakou.cr.aliyuncs.com/openanolis/nginx:1.14.1-8.6}"
TOL_KEY=verify.rbg.io/pr410

# Fresh start: drop the RBG and wait until its Pods are really gone, so the
# observation below can never be a stale Pod from an earlier run.
kubectl -n "$NS" delete rbg "$RBG" --ignore-not-found --wait=true >/dev/null 2>&1 || true
for _ in $(seq 1 30); do
  left=$(kubectl -n "$NS" get pods -l rbg.workloads.x-k8s.io/group-name="$RBG" --no-headers 2>/dev/null | wc -l)
  [ "$left" -eq 0 ] && break
  sleep 3
done

cat <<YAML | kubectl apply -n "$NS" -f -
apiVersion: workloads.x-k8s.io/v1alpha2
kind: RoleBasedGroup
metadata:
  name: $RBG
spec:
  roleTemplates:
    - name: engine-base
      template:
        spec:
          containers:
            - name: engine
              image: $IMG
              ports:
                - name: http
                  containerPort: 80
  roles:
    # role A: tolerations come from templateRef.patch only
    - name: prefill
      replicas: 1
      standalonePattern:
        templateRef:
          name: engine-base
          patch:
            spec:
              tolerations:
                - key: $TOL_KEY
                  operator: Equal
                  value: from-patch
                  effect: NoSchedule
              nodeSelector:
                kubernetes.io/os: linux
    # role B: empty patch, tolerations inherited from the shared roleTemplate
    - name: decode
      replicas: 1
      standalonePattern:
        templateRef:
          name: engine-base
          patch: {}
YAML

echo "--- waiting for pods (up to 120s) ---"
for _ in $(seq 1 24); do
  n=$(kubectl -n "$NS" get pods -l rbg.workloads.x-k8s.io/group-name="$RBG" \
        --no-headers 2>/dev/null | wc -l)
  [ "$n" -ge 2 ] && break
  sleep 5
done
kubectl -n "$NS" get pods -o wide 2>&1 | grep -E "NAME|$RBG" || true

echo
echo "--- rbg status ---"
kubectl -n "$NS" get rbg "$RBG" -o json | jq -c '{phase:.status.phase,roles:[.status.roleStatuses[]?|{n:.name,ready:.readyReplicas}]}'

echo
echo "--- OBSERVED: tolerations on each Pod (kubernetes-injected defaults filtered out) ---"
kubectl -n "$NS" get pods -l rbg.workloads.x-k8s.io/group-name="$RBG" -o json \
  | jq -r '.items[]|"\(.metadata.name)\t\([.spec.tolerations[]?|select(.key|startswith("node.kubernetes.io/")|not)]|tojson)"'

echo
echo "--- OBSERVED: nodeSelector on each Pod ---"
kubectl -n "$NS" get pods -l rbg.workloads.x-k8s.io/group-name="$RBG" -o json \
  | jq -r '.items[]|"\(.metadata.name)\t\(.spec.nodeSelector|tojson)"'

echo
prefill_tol=$(kubectl -n "$NS" get pods -l rbg.workloads.x-k8s.io/group-name=$RBG,rbg.workloads.x-k8s.io/role-name=prefill -o json \
  | jq "[.items[].spec.tolerations[]?|select(.key==\"$TOL_KEY\")]|length")
if [ "${prefill_tol:-0}" -ge 1 ]; then
  echo "RESULT F1: REFUTED — the toleration from templateRef.patch IS present on the Pod ($prefill_tol match)."
  exit 0
else
  echo "RESULT F1: CONFIRMED — no toleration with key $TOL_KEY on the Pod; PR #410's claim holds."
  exit 1
fi
