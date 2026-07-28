#!/usr/bin/env bash
# L3 live-layer setup for PR #410 verification. Scoped + idempotent.
# Honors $KUBECONFIG. Creates ONLY the namespace below; touches nothing cluster-wide.
set -euo pipefail
NS="${VERIFY_NS:-rbg-verify-pr410}"

kubectl get crd rolebasedgroups.workloads.x-k8s.io >/dev/null || {
  echo "FATAL: RBG CRD not installed on this cluster"; exit 1; }

kubectl create namespace "$NS" --dry-run=client -o yaml | kubectl apply -f -

echo "--- environment fingerprint ---"
kubectl version --short 2>/dev/null | head -2 || kubectl version -o yaml | head -6
echo "controller image: $(kubectl get deploy -A -l '' -o json \
  | jq -r '.items[]|select(.metadata.name=="rbgs-controller-manager")|.spec.template.spec.containers[0].image' | head -1)"
echo "controller ns:    $(kubectl get deploy -A -o json \
  | jq -r '.items[]|select(.metadata.name=="rbgs-controller-manager")|.metadata.namespace' | head -1)"
echo "crd versions:     $(kubectl get crd rolebasedgroups.workloads.x-k8s.io -o jsonpath='{.spec.versions[*].name}')"
echo "node taints:      $(kubectl get nodes -o json | jq -c '[.items[]|{n:.metadata.name,t:((.spec.taints//[])|map(.key))}]')"
echo "namespace:        $NS"
