#!/usr/bin/env bash
# L3 / F1 -- reproduce the deploy failure the way an operator hits it, against a
# real API server. This is the live counterpart of 01-helm-render.sh: that one
# proves the rendered text is wrong, this one proves the API server rejects it.
#
# NON-DESTRUCTIVE: every call is a dry run. Nothing is created, updated or
# deleted; no release is installed. Safe to run against a shared cluster.
#
# POLARITY: contract. FAILS while the bug is present.
set -uo pipefail
: "${KUBECONFIG:=$HOME/.kube/config}"
export KUBECONFIG
ROOT="$(git rev-parse --show-toplevel)"
CHART="$ROOT/deploy/helm/rbgs"
WORK=${WORK:-/root/pr414-verify}
NS=${NS:-rbg-system}
mkdir -p "$WORK"

echo "== cluster =="
kubectl version -o json 2>/dev/null | python3 -c "
import json,sys
d=json.load(sys.stdin)
s=d.get('serverVersion',{})
print('   server: %s' % s.get('gitVersion'))
" || { echo "   NO CLUSTER -- skipping the live arm of F1" >&2; exit 77; }

fail=0

# --- 1) helm install --dry-run=server: full server-side validation ----------
echo
echo "== helm install --dry-run=server (validates against the live API server) =="
if helm install rbgs-f1-dryrun "$CHART" \
      --namespace "$NS" --create-namespace \
      --dry-run=server > "$WORK/f1-helm-dryrun.out" 2> "$WORK/f1-helm-dryrun.err"; then
  echo "   accepted (F1 not reproduced on this path)"
else
  echo "   REJECTED:"
  grep -iE "error|apiVersion" "$WORK/f1-helm-dryrun.err" | head -5 | sed 's/^/     /'
  fail=1
fi

# --- 2) the exact CI shape: template, then apply --dry-run=server ------------
echo
echo "== helm template | kubectl apply --dry-run=server (what the e2e job does) =="
helm template rbgs "$CHART" --namespace "$NS" \
  --show-only templates/rbac/clusterrole.yaml > "$WORK/f1-clusterrole.yaml" 2>/dev/null
if kubectl apply --dry-run=server -f "$WORK/f1-clusterrole.yaml" \
      > "$WORK/f1-kubectl-dryrun.out" 2> "$WORK/f1-kubectl-dryrun.err"; then
  echo "   accepted:"; sed 's/^/     /' "$WORK/f1-kubectl-dryrun.out"
else
  echo "   REJECTED:"
  sed 's/^/     /' "$WORK/f1-kubectl-dryrun.err" | head -5
  fail=1
fi

# --- 3) control: the same chart WITHOUT the new clusterrole template ---------
# Proves the rejection is attributable to this PR's template, not to the chart,
# the cluster, or the reviewer's kubeconfig.
echo
echo "== control: another template from the same chart/cluster must be accepted =="
if helm template rbgs "$CHART" --namespace "$NS" \
      --show-only templates/rbac/service_account.yaml > "$WORK/f1-control.yaml" 2>/dev/null \
   && [ -s "$WORK/f1-control.yaml" ] \
   && kubectl apply --dry-run=server -f "$WORK/f1-control.yaml" >/dev/null 2>&1; then
  echo "   control accepted -> the rejection above is specific to clusterrole.yaml"
else
  echo "   CONTROL FAILED -- cannot attribute the rejection to this PR; investigate the"
  echo "   cluster/kubeconfig before trusting the result above." >&2
  fail=2
fi

echo
if [ "$fail" -eq 1 ]; then
  echo "RESULT: F1 REPRODUCED LIVE -- the chart this PR ships cannot be installed."
  exit 1
elif [ "$fail" -eq 2 ]; then
  echo "RESULT: INCONCLUSIVE (control failed)."
  exit 2
fi
echo "RESULT: F1 not reproduced live."
exit 0
