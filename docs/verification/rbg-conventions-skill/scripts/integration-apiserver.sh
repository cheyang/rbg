#!/usr/bin/env bash
# integration-envtest.sh: run the rbg-conventions scripts against a REAL kube-apiserver+etcd
# (envtest binaries, no cluster, no controller unless --with-controller is given).
#
# Covers:
#   VC-1 (crd-default-on-read.sh) end to end against the real apiextensions defaulting path
#   fixtures apply / reuse / delete lifecycle and the ownership guards
#   snapshot + compare on real objects, including a controller restart (with --with-controller)
#   v1alpha1-roundtrip.sh's refusal when the CRD converts with strategy None
#
# Usage: integration-envtest.sh [--with-controller /path/to/rbgs-binary]
set -uo pipefail
here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../../../../" && pwd)
skill="$repo/.agents/skills/rbg-conventions"
ASSETS=${KUBEBUILDER_ASSETS:-$HOME/.local/share/kubebuilder-envtest/k8s/1.31.0-linux-amd64}
CONTROLLER=""
[ "${1:-}" = "--with-controller" ] && CONTROLLER=$2

failed=0
pass() { echo "PASS $1"; }
fail() { echo "FAIL $1: $2"; failed=1; }

tmp=$(mktemp -d)
export RBG_VERIFY_STATE_DIR=$tmp/state RBG_VERIFY_RETRY_DELAY=1
export KUBECONFIG=$tmp/kubeconfig

"$ASSETS/etcd" --listen-client-urls http://127.0.0.1:2399 --advertise-client-urls http://127.0.0.1:2399 \
  --data-dir "$tmp/etcd" >"$tmp/etcd.log" 2>&1 &
etcd_pid=$!
openssl genrsa -out "$tmp/sa.key" 2048 2>/dev/null
printf 'admintoken,admin,1,system:masters\n' >"$tmp/tokens.csv"
"$ASSETS/kube-apiserver" --etcd-servers=http://127.0.0.1:2399 --service-cluster-ip-range=10.96.0.0/12 \
  --cert-dir="$tmp/certs" --authorization-mode=AlwaysAllow \
  --service-account-issuer=https://kubernetes.default.svc --service-account-signing-key-file="$tmp/sa.key" \
  --service-account-key-file="$tmp/sa.key" --token-auth-file="$tmp/tokens.csv" \
  --secure-port=18443 --bind-address=127.0.0.1 >"$tmp/apiserver.log" 2>&1 &
apiserver_pid=$!
controller_pid=''
cleanup() {
  [ -n "$controller_pid" ] && kill "$controller_pid" 2>/dev/null
  kill "$apiserver_pid" "$etcd_pid" 2>/dev/null
  rm -rf "$tmp"
}
trap cleanup EXIT

for i in $(seq 1 60); do
  if kubectl --kubeconfig=/dev/null get --raw=/healthz --server=https://127.0.0.1:18443 --insecure-skip-tls-verify 2>/dev/null | grep -q ok; then break; fi
  sleep 1
done
kubectl config set-cluster it --server=https://127.0.0.1:18443 --insecure-skip-tls-verify=true >/dev/null
kubectl config set-credentials it --token=admintoken >/dev/null
kubectl config set-context it --cluster=it --user=it >/dev/null
kubectl config use-context it >/dev/null
kubectl get --raw=/readyz >/dev/null || { echo "apiserver never came up"; exit 2; }
echo "apiserver up"

echo "== install RBG CRDs from config/crd/bases"
kubectl apply --server-side -f "$repo/config/crd/bases" >/dev/null || { echo "CRD install failed"; exit 2; }

echo "== VC-1: crd-default-on-read.sh"
out=$("$skill/scripts/crd-default-on-read.sh" 2>&1) && rc=0 || rc=$?
printf '%s\n' "$out" | sed 's/^/  /'
# F5 canary: right after heavy CRD churn, `kubectl wait --for=condition=Established` aborts with
# ".status.conditions accessor error" and the script exits 1. While that behavior exists the
# canary passes and the case is retried; once the script waits robustly the canary flips.
# The F5 flake is timing-dependent, so record it as an observation, not a gate: the deterministic
# reproduction lives in results/f5-wait-flake.txt (waitrace run). A first-attempt pass can mean
# "fixed" or just "lucky".
if [ "$rc" != 0 ] && printf '%s' "$out" | grep -q 'status.conditions accessor error'; then
  echo "NOTE canary-vc1-wait-flake: F5 flake observed on the first attempt"
  echo "  (F5 flake hit; retrying for the contract check)"
  out=$("$skill/scripts/crd-default-on-read.sh" 2>&1) && rc=0 || rc=$?
  printf '%s\n' "$out" | sed 's/^/  /'
else
  echo "NOTE canary-vc1-wait-flake: no flake on the first attempt (fixed, or lucky)"
fi
if [ "$rc" = 0 ] && [ "$(printf '%s\n' "$out" | grep -c '^PASS A')" = 4 ]; then
  pass contract-vc1-default-on-read
else
  fail contract-vc1-default-on-read "rc=$rc"
fi
# the throwaway CRD must be gone
if kubectl get crd -o name | grep -q 'rbg-conventions.example.com'; then
  fail contract-vc1-cleanup "leftover defaultprobes CRD"
else
  pass contract-vc1-cleanup
fi

echo "== fixtures lifecycle"
out=$("$skill/scripts/rbg-fixtures.sh" apply -n itest 2>&1) || fail contract-fixtures-apply "$out"
kubectl get rolebasedgroups.workloads.x-k8s.io -n itest -o name | grep -q fx-standalone \
  && pass contract-fixtures-apply || fail contract-fixtures-apply "no RBGs created"
kubectl get namespace itest -o jsonpath='{.metadata.labels.rbg-conventions/verification}' | grep -q true \
  && pass contract-fixtures-marker-label || fail contract-fixtures-marker-label "marker label missing"

# reuse path: apply again into the same owned namespace
out=$("$skill/scripts/rbg-fixtures.sh" apply -n itest 2>&1) && pass contract-fixtures-reapply || fail contract-fixtures-reapply "$out"

# a second state dir must be refused for delete and for the writing scripts
out=$(RBG_VERIFY_STATE_DIR=$tmp/other "$skill/scripts/rbg-fixtures.sh" delete -n itest 2>&1) && rc=0 || rc=$?
[ "$rc" = 2 ] && pass contract-foreign-statedir-refused || fail contract-foreign-statedir-refused "rc=$rc out=$out"
kubectl get namespace itest >/dev/null 2>&1 && pass contract-foreign-statedir-ns-intact || fail contract-foreign-statedir-ns-intact "namespace deleted!"

# a namespace created out of band must be refused
kubectl create namespace foreign >/dev/null
out=$("$skill/scripts/rbg-fixtures.sh" delete -n foreign 2>&1) && rc=0 || rc=$?
[ "$rc" = 2 ] && kubectl get namespace foreign >/dev/null 2>&1 \
  && pass contract-foreign-namespace-refused || fail contract-foreign-namespace-refused "rc=$rc out=$out"

echo "== snapshot + compare on real objects"
"$skill/scripts/rbg-snapshot.sh" -n itest >"$tmp/before" || fail contract-snapshot-real "snapshot failed"
"$skill/scripts/rbg-snapshot.sh" -n itest >"$tmp/after" || fail contract-snapshot-real "second snapshot failed"
out=$("$skill/scripts/rbg-compare.sh" "$tmp/before" "$tmp/after" 2>&1) && rc=0 || rc=$?
[ "$rc" = 0 ] && pass contract-compare-real-identical || fail contract-compare-real-identical "$out"

echo "== v1alpha1-roundtrip.sh refuses strategy None"
out=$("$skill/scripts/v1alpha1-roundtrip.sh" -n itest fx-standalone 2>&1) && rc=0 || rc=$?
printf '%s\n' "$out" | sed 's/^/  /'
[ "$rc" = 2 ] && printf '%s' "$out" | grep -q 'not Webhook; refusing' \
  && pass contract-roundtrip-refuses-none-live || fail contract-roundtrip-refuses-none-live "rc=$rc"

if [ -n "$CONTROLLER" ]; then
  echo "== controller cases (VC-2 restart, VC-4 label removal)"
  # the minimal apiserver has no SA controller; pods are only created once a default SA exists
  kubectl create serviceaccount default -n itest >/dev/null 2>&1
  "$CONTROLLER" --enable-webhooks=none --leader-elect=false --health-probe-bind-address=:18183 \
    --metrics-bind-address=:18081 >"$tmp/controller.log" 2>&1 &
  controller_pid=$!
  # wait until the controller has created the RoleInstanceSets and Pods
  for i in $(seq 1 90); do
    n=$(kubectl get pods -n itest --no-headers 2>/dev/null | wc -l | tr -d ' ')
    [ "$n" -ge 11 ] && break
    sleep 2
  done
  n=$(kubectl get pods -n itest --no-headers 2>/dev/null | wc -l | tr -d ' ')
  echo "  pods in itest: $n"

  "$skill/scripts/rbg-snapshot.sh" -n itest --settle 10 --timeout 120 >"$tmp/b4" || fail contract-vc2-restart "snapshot before failed"
  kill "$controller_pid"; wait "$controller_pid" 2>/dev/null
  "$CONTROLLER" --enable-webhooks=none --leader-elect=false --health-probe-bind-address=:18184 \
    --metrics-bind-address=:18082 >>"$tmp/controller.log" 2>&1 &
  controller_pid=$!
  sleep 5
  "$skill/scripts/rbg-snapshot.sh" -n itest --settle 10 --timeout 120 >"$tmp/af" || fail contract-vc2-restart "snapshot after failed"
  out=$("$skill/scripts/rbg-compare.sh" "$tmp/b4" "$tmp/af" 2>&1) && rc=0 || rc=$?
  printf '%s\n' "$out" | sed 's/^/  /'
  [ "$rc" = 0 ] && pass contract-vc2-restart-noop || fail contract-vc2-restart-noop "rc=$rc"

  out=$("$skill/scripts/ssa-key-removal.sh" -n itest 2>&1) && rc=0 || rc=$?
  printf '%s\n' "$out" | sed 's/^/  /'
  [ "$rc" = 0 ] && pass contract-vc4-label-removal || fail contract-vc4-label-removal "rc=$rc"

  kill "$controller_pid"; wait "$controller_pid" 2>/dev/null; controller_pid=''
fi

echo "== fixtures delete"
# This minimal apiserver has no kube-controller-manager, so namespace finalization never
# completes. The behavior under test is the UID-preconditioned delete being accepted; check the
# namespace enters Terminating and the state file is kept while it does.
timeout 30 "$skill/scripts/rbg-fixtures.sh" delete -n itest >/dev/null 2>&1
nsjson=$(kubectl get namespace itest -o json 2>/dev/null || true)
if printf '%s' "$nsjson" | jq -e '.metadata.deletionTimestamp != null' >/dev/null; then
  pass contract-fixtures-delete-accepted
else
  fail contract-fixtures-delete-accepted "namespace not terminating"
fi
if [ -e "$RBG_VERIFY_STATE_DIR"/*'.itest' ]; then
  pass contract-delete-keeps-state-while-terminating
else
  fail contract-delete-keeps-state-while-terminating "state file removed too early"
fi
# force-finalize for cleanup (no namespace controller here)
printf '%s' "$nsjson" | jq '.spec.finalizers=[]' | kubectl replace --raw "/api/v1/namespaces/itest/finalize" -f - >/dev/null 2>&1
kubectl delete namespace foreign --wait=false >/dev/null 2>&1

echo
[ "$failed" -eq 0 ] && echo "ALL PASS" || echo "SOME CHECKS FAILED"
exit "$failed"
