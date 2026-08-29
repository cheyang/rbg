#!/usr/bin/env bash
# live-scenario.sh — L3 layer for PR 439. Reproduces, against the LIVE cluster
# named by $KUBECONFIG, the restartPolicy-representation failure that the
# in-cluster (buggy) manager exhibits for a LeaderWorkerPattern RoleInstanceSet
# role. It is READ/CREATE-ONLY on a throwaway namespace and always tears down.
#
# What it proves (L3, against whatever manager is live):
#   - the live manager's stored RoleInstanceSet template.restartPolicy field,
#     and whether the RoleInstance CRD (which requires a STRING) accepts it.
#   On the in-cluster v0.8.0-0c00546d manager the stored value is an OBJECT and
#   RoleInstance creation is rejected (FailedCreate, 0 pods) — see
#   results/f1-live-reproduction.txt. The PR's string form (proven at L1) is
#   what the CRD requires.
#
# It does NOT deploy the PR manager: on a shared cluster a cluster-wide
# controller would reconcile non-test RBGs (e.g. default/nginx-cluster). Run it
# only to capture the live signal; cleanup is automatic.
#
# Usage:  KUBECONFIG=/path/to/kubeconfig bash live-scenario.sh
set -euo pipefail
: "${KUBECONFIG:?KUBECONFIG must be set}"
NS="pr439-verify-$$"   # unique-ish throwaway namespace
RBG="pr439-f2-identity"
MANIFEST="$(cd "$(dirname "$0")"/.. && pwd)/live/roleinstance-f2-identity.yaml"
trap 'echo "--- teardown ---"; kubectl delete namespace "$NS" --ignore-not-found >/dev/null 2>&1 || true' EXIT

kubectl create namespace "$NS" >/dev/null
# Drop any restartPolicy field (some live webhooks reject a string here) — F2
# does not depend on restart policy.
sed 's/^      restartPolicy:.*//' "$MANIFEST" | kubectl -n "$NS" apply -f - >/dev/null 2>&1 || true
kubectl -n "$NS" apply -f "$MANIFEST" >/dev/null 2>&1 || \
  sed 's/^      restartPolicy:.*//' "$MANIFEST" | kubectl -n "$NS" apply -f -

echo "namespace=$NS rbg=$RBG"
echo "give the live manager ~40s to reconcile the RoleInstanceSet"; sleep 40

echo "--- stored RoleInstanceSet template restartPolicy representation ---"
kubectl -n "$NS" get ris "$RBG-worker" -o json 2>/dev/null | python3 -c "
import json,sys
d=json.load(sys.stdin)
tpl=d['spec'].get('roleInstanceTemplate',{})
print('restartPolicy      =', json.dumps(tpl.get('restartPolicy')))
print('restartPolicyConfig=', json.dumps(tpl.get('restartPolicyConfig')))
" || echo "(RoleInstanceSet not yet created)"

echo "--- RoleInstance CRD expects spec.restartPolicy as a STRING ---"
kubectl get crd roleinstances.workloads.x-k8s.io -o json 2>/dev/null | python3 -c "
import json,sys
d=json.load(sys.stdin); v=d['spec']['versions'][0]
rp=v['schema']['openAPIV3Schema']['properties']['spec']['properties']['restartPolicy']
print('type=',rp.get('type'),'enum=',rp.get('enum'))
"

echo "--- FailedCreate events (object-in-string-field -> RoleInstance rejected) ---"
kubectl -n "$NS" get events --sort-by=.lastTimestamp 2>/dev/null | grep -E 'FailedCreate' | tail -2 || echo "(none)"

echo "--- pods (expect 0 on a manager that writes an object) ---"
kubectl -n "$NS" get pods -l app="$RBG" 2>/dev/null | tail -n +1
