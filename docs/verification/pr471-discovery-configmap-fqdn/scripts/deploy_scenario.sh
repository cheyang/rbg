#!/usr/bin/env bash
# Deploy the 3-pattern RBG used by the live probe (TestLiveProbe_ConfigMapVsPods).
# Usage: KUBECONFIG=/path/to/kubeconfig bash docs/verification/pr471-discovery-configmap-fqdn/scripts/deploy_scenario.sh [apply|delete]
# Idempotent. Pod naming is identical between the base and PR-head controller (the PR only edits
# config_builder.go), so deploying against the running base controller yields the real pod names that
# the PR-head ConfigBuilder is cross-checked against.
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ACTION="${1:-apply}"
NS="${RBG_PROBE_NS:-rbg-verify-pr471}"
RBG="${RBG_PROBE_RBG:-discovery-cm-test}"
IMG="registry.cn-hangzhou.aliyuncs.com/acs-sample/nginx:latest"

case "$ACTION" in
  delete)
    kubectl delete namespace "$NS" --ignore-not-found
    exit 0
    ;;
esac

cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: Namespace
metadata:
  name: $NS
---
apiVersion: workloads.x-k8s.io/v1alpha2
kind: RoleBasedGroup
metadata:
  name: $RBG
  namespace: $NS
  labels:
    workloads.x-k8s.io/group-name: $RBG
spec:
  roles:
  - name: router
    replicas: 2
    rolloutStrategy: {type: RollingUpdate}
    standalonePattern:
      template:
        metadata: {name: nginx}
        spec:
          containers: [{name: nginx, image: $IMG, imagePullPolicy: IfNotPresent}]
          terminationGracePeriodSeconds: 1
  - name: prefill
    replicas: 1
    rolloutStrategy: {type: RollingUpdate}
    leaderWorkerPattern:
      size: 2
      template: {metadata: {name: nginx}, spec: {containers: [{name: nginx, image: $IMG, imagePullPolicy: IfNotPresent}], terminationGracePeriodSeconds: 1}}
      leaderTemplatePatch: {metadata: {labels: {role: leader}}}
      workerTemplatePatch: {metadata: {labels: {role: worker}}}
  - name: decode
    replicas: 1
    rolloutStrategy: {type: RollingUpdate}
    customComponentsPattern:
      components:
      - {name: leader, size: 1, template: {metadata: {name: nginx}, spec: {containers: [{name: nginx, image: $IMG, imagePullPolicy: IfNotPresent}], terminationGracePeriodSeconds: 1}}}
      - {name: worker, size: 1, template: {metadata: {name: nginx}, spec: {containers: [{name: nginx, image: $IMG, imagePullPolicy: IfNotPresent}], terminationGracePeriodSeconds: 1}}}
EOF

echo ">> waiting for 6 ready pods in $NS..."
for i in $(seq 1 40); do
  rdy=$(kubectl -n "$NS" get pods --no-headers 2>/dev/null | grep -c '1/1' || true)
  [ "$rdy" -ge 6 ] && break
  sleep 6
done
kubectl -n "$NS" get pods -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.spec.hostname}{"\t"}{.spec.subdomain}{"\n"}{end}' 2>/dev/null | sort
