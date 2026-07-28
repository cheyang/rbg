#!/usr/bin/env bash
# L3 teardown — removes ONLY what this harness created: the scoped namespace.
# Nothing cluster-wide (CRDs, the controller, other namespaces) is touched.
set -uo pipefail
NS="${VERIFY_NS:-rbg-verify-pr410}"
echo "deleting scoped namespace $NS (and everything in it)"
kubectl delete rbg --all -n "$NS" --ignore-not-found --timeout=120s 2>&1 | tail -3
kubectl delete namespace "$NS" --ignore-not-found --timeout=180s 2>&1 | tail -2
echo "remaining rbgs in $NS: $(kubectl get rbg -n "$NS" --no-headers 2>&1 | head -1)"
