#!/usr/bin/env bash
# L3 / F1 -- reproduce the deploy failure the way an operator hits it, against a
# real API server. This is the live counterpart of 01-helm-render.sh: that one
# proves the rendered text is wrong, this one proves the API server rejects it.
#
# NON-DESTRUCTIVE: every call is a dry run. Nothing is created, updated or
# deleted; no release is installed. Safe to run against a shared cluster.
#
# POLARITY: contract. FAILS while the bug is present.
#
# SIGNATURE-GATED. Round 2 of this review caught this script red-handed emitting
# a FALSE POSITIVE: after the author fixed F1, `helm install --dry-run=server`
# still failed -- because the throwaway release name collided with the ownership
# annotations of the real `rbgs` release already on the cluster -- and the script
# reported "F1 REPRODUCED LIVE". An arbitrary failure must never be attributed to
# F1. So now:
#   * F1 is only ever confirmed on the F1 SIGNATURE ("apiVersion not set").
#   * Any other failure is INCONCLUSIVE (exit 2), never a finding.
#   * The helm arm reuses the EXISTING release name when one is present, so
#     ownership metadata cannot collide.
set -uo pipefail
: "${KUBECONFIG:=$HOME/.kube/config}"
export KUBECONFIG
ROOT="$(git rev-parse --show-toplevel)"
CHART="$ROOT/deploy/helm/rbgs"
WORK=${WORK:-/root/pr414-verify}
NS=${NS:-rbg-system}
SIG='apiVersion not set'
mkdir -p "$WORK"

echo "== cluster =="
kubectl version -o json 2>/dev/null | python3 -c "
import json,sys
print('   server: %s' % json.load(sys.stdin).get('serverVersion',{}).get('gitVersion'))
" || { echo "   NO CLUSTER -- skipping the live arm of F1" >&2; exit 77; }

sig_hit=0     # saw the F1 signature
other=0       # saw some other failure

# --- arm A: the exact CI shape -- template, then server-side apply -----------
# This is what the e2e job's "Deploy controller" step effectively validates, and
# it needs no release at all, so it cannot trip over ownership metadata.
echo
echo "== arm A: helm template | kubectl apply --dry-run=server =="
helm template rbgs "$CHART" --namespace "$NS" \
  --show-only templates/rbac/clusterrole.yaml > "$WORK/f1-clusterrole.yaml" 2>/dev/null
if kubectl apply --dry-run=server -f "$WORK/f1-clusterrole.yaml" \
      > "$WORK/f1-kubectl.out" 2> "$WORK/f1-kubectl.err"; then
  echo "   ACCEPTED:"; sed 's/^/     /' "$WORK/f1-kubectl.out"
else
  echo "   REJECTED:"; sed 's/^/     /' "$WORK/f1-kubectl.err" | head -5
  if grep -qF "$SIG" "$WORK/f1-kubectl.err"; then sig_hit=1; else other=1; fi
fi

# --- arm B: whole-release validation, reusing the real release name ----------
REL=$(helm list -n "$NS" -q 2>/dev/null | head -1)
echo
if [ -n "$REL" ]; then
  echo "== arm B: helm upgrade --dry-run=server (reusing existing release '$REL') =="
  set -- upgrade "$REL" "$CHART" --namespace "$NS" --dry-run=server
else
  echo "== arm B: helm install --dry-run=server (no existing release) =="
  set -- install rbgs "$CHART" --namespace "$NS" --create-namespace --dry-run=server
fi
if helm "$@" > "$WORK/f1-helm.out" 2> "$WORK/f1-helm.err"; then
  echo "   ACCEPTED (whole release renders and validates)"
else
  echo "   REJECTED:"; grep -iE "error" "$WORK/f1-helm.err" | head -3 | sed 's/^/     /'
  if grep -qF "$SIG" "$WORK/f1-helm.err"; then sig_hit=1; else other=1; fi
fi

# --- control: attribute any signature hit to THIS template -------------------
echo
echo "== control: a sibling template from the same chart/cluster =="
if helm template rbgs "$CHART" --namespace "$NS" \
      --show-only templates/rbac/service_account.yaml > "$WORK/f1-control.yaml" 2>/dev/null \
   && [ -s "$WORK/f1-control.yaml" ] \
   && kubectl apply --dry-run=server -f "$WORK/f1-control.yaml" >/dev/null 2>&1; then
  echo "   control ACCEPTED -> cluster and kubeconfig are usable"
  control=ok
else
  echo "   control FAILED -> cannot attribute anything below to this PR" >&2
  control=bad
fi

echo
if [ "$control" != ok ]; then
  echo "RESULT: INCONCLUSIVE -- the control failed, so nothing here is attributable."
  exit 2
fi
if [ "$sig_hit" -eq 1 ]; then
  echo "RESULT: F1 REPRODUCED LIVE -- a rendered document has no apiVersion"
  echo "        (signature: '$SIG'). The chart cannot be installed."
  exit 1
fi
if [ "$other" -eq 1 ]; then
  echo "RESULT: INCONCLUSIVE -- something was rejected, but NOT with the F1 signature"
  echo "        ('$SIG'), so it is not this finding. Read the errors above before"
  echo "        drawing any conclusion; do not report them as F1."
  exit 2
fi
echo "RESULT: F1 FIXED -- the ClusterRole is accepted by a live API server, and the"
echo "        whole release validates server-side."
exit 0
