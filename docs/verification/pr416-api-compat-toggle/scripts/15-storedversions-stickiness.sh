#!/usr/bin/env bash
# Why examples/preflight.yaml defaults checkStoredVersions to false.
#
# The claim under test: status.storedVersions is STICKY. Kubernetes appends to it
# when the storage version changes, but never removes an entry on its own -- not even
# once every object has been rewritten in the new version. Only an explicit patch by
# an operator shrinks it.
#
# If that is true, gating an upgrade on "storedVersions contains v1alpha1" would
# refuse clusters that are in fact fully migrated, which is why it must not be the
# default.
#
# SAFETY: uses a throwaway CRD in its own group (pr416test.example.com) that cannot
# collide with anything in the cluster. It is additive, never touches the rbgs CRDs,
# and is deleted by an EXIT trap.
set -uo pipefail

GROUP=pr416test.example.com
CRD=widgets.$GROUP

cleanup() {
  echo
  echo "=== CLEANUP ==="
  kubectl delete crd "$CRD" --wait=false >/dev/null 2>&1
  echo "  deleted the throwaway CRD $CRD (rbgs CRDs untouched)"
}
trap cleanup EXIT

mkcrd() { # mkcrd <storage-version>
  local storage="$1" v1s v2s
  [ "$storage" = v1 ] && { v1s=true; v2s=false; } || { v1s=false; v2s=true; }
  cat <<YAML | kubectl apply -f - >/dev/null
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: $CRD
spec:
  group: $GROUP
  scope: Namespaced
  names: {plural: widgets, singular: widget, kind: Widget}
  versions:
    - name: v1
      served: true
      storage: $v1s
      schema: {openAPIV3Schema: {type: object, properties: {spec: {type: object, properties: {n: {type: string}}}}}}
    - name: v2
      served: true
      storage: $v2s
      schema: {openAPIV3Schema: {type: object, properties: {spec: {type: object, properties: {n: {type: string}}}}}}
YAML
}

sv() { kubectl get crd "$CRD" -o jsonpath='{.status.storedVersions}' 2>/dev/null; }

echo "=== 1) CRD with v1 as the storage version; write one object ==="
mkcrd v1
kubectl -n default apply -f - >/dev/null <<YAML
apiVersion: $GROUP/v1
kind: Widget
metadata: {name: w1}
spec: {n: "a"}
YAML
sleep 2
echo "   storedVersions: $(sv)          <- expected [\"v1\"]"

echo
echo "=== 2) migrate: v2 becomes the storage version ==="
mkcrd v2
sleep 2
echo "   storedVersions: $(sv)"

echo
echo "=== 3) rewrite EVERY object so nothing is stored as v1 any more ==="
kubectl -n default get widgets.v2.$GROUP -o json 2>/dev/null \
  | kubectl replace -f - >/dev/null 2>&1
kubectl -n default annotate widget w1 migrated=true --overwrite >/dev/null 2>&1
sleep 2
echo "   storedVersions: $(sv)"
echo "   objects now readable as v2: $(kubectl -n default get widgets.v2.$GROUP --no-headers 2>/dev/null | wc -l | tr -d ' ')"

echo
echo "=== VERDICT ==="
final=$(sv)
if printf '%s' "$final" | grep -q '"v1"' || printf '%s' "$final" | grep -q 'v1'; then
  cat <<EOF
   storedVersions is STILL $final after a full migration.

   CONFIRMED STICKY. Kubernetes does not remove the old entry once every object has
   been rewritten -- only an explicit
       kubectl patch crd $CRD --subresource=status --type=merge \\
         -p '{"status":{"storedVersions":["v2"]}}'
   shrinks it.

   So "storedVersions contains v1alpha1" does NOT mean "v1alpha1 objects exist"; it
   means "v1alpha1 was a storage version at some point, and nobody has cleaned up the
   bookkeeping". Gating an upgrade on it would refuse a fully-migrated cluster.
   That is why checkStoredVersions defaults to false: it is a corroborating signal,
   not a gate.
EOF
else
  echo "   storedVersions shrank on its own to $final -- the stickiness claim is WRONG"
  echo "   and the default should be reconsidered."
fi

echo
echo "=== 4) control: the explicit patch does shrink it ==="
kubectl patch crd "$CRD" --subresource=status --type=merge \
  -p '{"status":{"storedVersions":["v2"]}}' >/dev/null 2>&1
sleep 1
echo "   after an explicit operator patch: $(sv)"
