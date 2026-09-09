#!/usr/bin/env bash
# 10-live-ab.sh — live A/B verification on a real ACK cluster (k8s v1.36.1).
#
# Requires: KUBECONFIG pointing at a cluster running a pre-enum release (v0.8.0),
# so the RBG CRD has no enum on rollingUpdate.type and the controller copies Recreate
# verbatim into the RoleInstanceSet.
#
# What it does:
#   1. Snapshot + patch the enum onto roleinstancesets CRD (post-#436 state); restore on exit.
#   2. PREMISE (base): create a Recreate RBG -> v0.8.0 controller leaves RIS absent (422).
#   3. FIX (PR, webhooks=none): run the PR binary -> RIS created with RecreatePod.
#   4. Restore the CRD, the controller replicas, and delete the test namespace.
#
# Idempotent; honors $KUBECONFIG. No cluster-wide destructive actions beyond the
# reversible enum patch on the one CRD (snapshotted).
set -euo pipefail
: "${KUBECONFIG:?KUBECONFIG must point at the ACK test cluster}"
NS="pr463-test"
RESULTS="$(cd "$(dirname "${BASH_SOURCE[0]}")/../results" && pwd)"
RIS_CRD="roleinstancesets.workloads.x-k8s.io"
SNAPSHOT="$RESULTS/ris-crd-live-snapshot.yaml"
RBG="$RESULTS/rbg-recreate.yaml"
PR_BIN="$RESULTS/rbgs-manager"        # build: go build -o $PR_BIN ./cmd/rbgs (PR branch)

cleanup() {
  echo "[teardown] restoring cluster..."
  kubectl patch crd "$RIS_CRD" --type=json \
    -p '[{"op":"remove","path":"/spec/versions/0/schema/openAPIV3Schema/properties/spec/properties/updateStrategy/properties/type/enum"}]' 2>/dev/null || true
  kubectl -n rbg-system scale deploy rbgs-controller-manager --replicas=1 2>/dev/null || true
  kubectl delete ns "$NS" --ignore-not-found 2>/dev/null || true
  pkill -f "$PR_BIN" 2>/dev/null || true
}
trap cleanup EXIT

echo "[1] snapshot RIS CRD + patch enum onto v1alpha2 updateStrategy.type"
kubectl get crd "$RIS_CRD" -o yaml > "$SNAPSHOT"
kubectl patch crd "$RIS_CRD" --type=json -p '[{"op":"add","path":"/spec/versions/0/schema/openAPIV3Schema/properties/spec/properties/updateStrategy/properties/type/enum","value":["RecreatePod","InPlaceIfPossible","InPlaceOnly"]}]'

echo "[2] PREMISE (base v0.8.0): Recreate RBG -> RIS absent + 422"
kubectl create namespace "$NS" --dry-run=client -o yaml | kubectl apply -f -
kubectl apply -f "$RBG"
kubectl -n rbg-system wait --for=condition=Ready pod -l control-plane=rbgs-controller --timeout=90s
sleep 12
echo "  RIS present (expect NO)? : $(kubectl -n "$NS" get roleinstanceset pr463-rbg-recreate-worker -o name 2>&1 || true)"
kubectl -n rbg-system logs -l control-plane=rbgs-controller --tail=50 2>&1 | grep -i "Unsupported value" | tail -2

echo "[3] FIX (PR binary, webhooks=none): RIS created with RecreatePod"
kubectl -n rbg-system scale deploy rbgs-controller-manager --replicas=0
"$PR_BIN" -kubeconfig="$KUBECONFIG" -enable-webhooks=none \
  -metrics-bind-address=:0 -health-probe-bind-address=:0 \
  -enable-port-allocator=false -scheduler-name=scheduler-plugins &
PR_PID=$!
sleep 20
echo "  RIS type (expect RecreatePod): $(kubectl -n "$NS" get roleinstanceset pr463-rbg-recreate-worker -o jsonpath='{.spec.updateStrategy.type}' 2>&1)"
kill "$PR_PID" 2>/dev/null || true
echo "[done] see README.md observed-vs-expected table"
