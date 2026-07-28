#!/usr/bin/env bash
# L3 setup: install the chart under test with legacy workloads DISABLED, and
# build a kubeconfig that authenticates as the controller ServiceAccount so the
# reduced ClusterRole is actually enforced (an admin kubeconfig would bypass it).
#
# Idempotent. Honors $KUBECONFIG. Restore with 90-live-teardown.sh.
set -euo pipefail
: "${KUBECONFIG:=$HOME/.kube/config}"
export KUBECONFIG
NS=rbg-system
REL=rbgs
SA=rbgs-controller-sa
WORK=${WORK:-/root/pr413-verify}
CHART="$(git rev-parse --show-toplevel)/deploy/helm/rbgs"
LEGACY=${LEGACY:-false}

echo "== chart: $CHART  (legacyWorkloads.enabled=$LEGACY) =="

# Stop the in-cluster controller so it does not race the out-of-cluster binary
# we run for the scenario. Restored by teardown.
kubectl -n "$NS" scale deploy/rbgs-controller-manager --replicas=0 >/dev/null 2>&1 || true
kubectl -n "$NS" rollout status deploy/rbgs-controller-manager --timeout=90s >/dev/null 2>&1 || true
echo "   in-cluster controller scaled to 0 (replicas=$(kubectl -n $NS get deploy rbgs-controller-manager -o jsonpath='{.spec.replicas}'))"

# Apply only the RBAC we are testing. Upgrading the whole release would also
# swap the controller image; we want the ClusterRole from THIS chart revision.
helm template "$REL" "$CHART" \
  --namespace "$NS" \
  --set controller.features.legacyWorkloads.enabled="$LEGACY" \
  --show-only templates/rbac/clusterrole.yaml > "$WORK/clusterrole-legacy-$LEGACY.yaml"
kubectl apply -f "$WORK/clusterrole-legacy-$LEGACY.yaml" >/dev/null
echo "   applied ClusterRole from chart (legacyWorkloads.enabled=$LEGACY)"
echo "   rules: $(grep -c '^- apiGroups' "$WORK/clusterrole-legacy-$LEGACY.yaml")"

# Short-lived SA token -> kubeconfig, so RBAC is genuinely enforced.
TOKEN=$(kubectl -n "$NS" create token "$SA" --duration=8h)
APISERVER=$(kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}')
kubectl config view --raw --minify -o json \
  | python3 -c "
import json,sys
d=json.load(sys.stdin)
ca=d['clusters'][0]['cluster'].get('certificate-authority-data')
print(json.dumps(ca or ''))
" > "$WORK/.ca.json"
CA=$(python3 -c "import json;print(json.load(open('$WORK/.ca.json')))")

cat > "$WORK/sa-kubeconfig.yaml" <<KCFG
apiVersion: v1
kind: Config
clusters:
- name: c
  cluster:
    server: ${APISERVER}
    $( [ -n "$CA" ] && echo "certificate-authority-data: ${CA}" || echo "insecure-skip-tls-verify: true" )
contexts:
- name: c
  context: {cluster: c, user: sa}
current-context: c
users:
- name: sa
  user:
    token: ${TOKEN}
KCFG
chmod 600 "$WORK/sa-kubeconfig.yaml"
echo "   wrote $WORK/sa-kubeconfig.yaml (authenticates as $SA)"

echo "== effective RBAC as $SA =="
for res in deployments statefulsets leaderworkersets.leaderworkerset.x-k8s.io roleinstancesets.workloads.x-k8s.io; do
  for verb in list watch create; do
    printf "   %-8s %-42s %s\n" "$verb" "$res" \
      "$(kubectl auth can-i "$verb" "$res" --as="system:serviceaccount:$NS:$SA" 2>/dev/null || true)"
  done
done
