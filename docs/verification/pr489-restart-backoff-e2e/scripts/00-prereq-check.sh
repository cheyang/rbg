#!/usr/bin/env bash
# Prereq check for the PR #489 live verification.
# Needs: kubectl + KUBECONFIG pointing at a cluster with rbgs CRDs (incl.
# leaderWorkerPattern.restartPolicyConfig) and a running rbgs controller whose
# restart/backoff logic matches the PR merge-base (test-only PR: controller
# code identical between base and head for the tested path).
set -euo pipefail
: "${KUBECONFIG:?set KUBECONFIG or have ~/.kube/config}"
kubectl cluster-info | head -1
kubectl get crd rolebasedgroups.workloads.x-k8s.io >/dev/null
kubectl get crd rolebasedgroups.workloads.x-k8s.io -o json \
  | jq -r '.spec.versions[] | select(.name=="v1alpha2") | .schema.openAPIV3Schema.properties.spec.properties.roles.items.properties.leaderWorkerPattern.properties.restartPolicyConfig.properties | keys[]' \
  | grep -x -e baseDelaySeconds -e maxDelaySeconds -e type
kubectl get pods -A -l control-plane=rbgs-controller --no-headers | head -2
echo "[prereq] OK"
