#!/usr/bin/env bash
# P0 premise demo (runs against BASE behavior): the old e2e test injects a
# failure via `Status().Update(pod, phase=Failed)` (test/utils SetPodFailed).
# kubelet owns Pod status and overwrites the patched phase:
#   - k8s 1.36 (this cluster): phase flips back to Running, container untouched
#   - kind k8s 1.31 (CI):      kubelet kills the container, nginx exits 0 on
#     SIGTERM, phase becomes Succeeded (see CI events in README)
# Either way the "Failed" trigger evaporates, so a reconcile delayed by the
# 90s restart backoff finds no Failed pod and no container restart, and never
# recreates the instance's pods -> the old test times out.
set -euo pipefail
NS="rbg-p0-demo"
IMAGE="${IMAGE:-registry.cn-hangzhou.aliyuncs.com/acs-sample/nginx:latest}"
kubectl delete ns "$NS" --ignore-not-found --wait=true >/dev/null 2>&1 || true
kubectl create ns "$NS" >/dev/null
kubectl -n "$NS" run nginx-p0 --image="$IMAGE" --restart=Always >/dev/null
kubectl -n "$NS" wait --for=condition=Ready pod/nginx-p0 --timeout=180s >/dev/null
echo "[P0] pod Ready; patching status.phase=Failed (what SetPodFailed does)"
kubectl -n "$NS" patch pod nginx-p0 --subresource=status --type=merge \
  -p '{"status":{"phase":"Failed","reason":"Error","message":"Container exited with error"}}' >/dev/null
for i in $(seq 1 6); do
  phase=$(kubectl -n "$NS" get pod nginx-p0 -o jsonpath='{.status.phase}')
  rc=$(kubectl -n "$NS" get pod nginx-p0 -o jsonpath='{.status.containerStatuses[0].restartCount}')
  echo "[P0] t+$((i*3))s phase=$phase restartCount=$rc"
  sleep 3
done
echo "[P0] kubelet events (none should delete the pod; maybe a Killing on older kubelets):"
kubectl -n "$NS" get events --field-selector involvedObject.name=nginx-p0 --no-headers | tail -5
kubectl delete ns "$NS" --ignore-not-found --wait=true >/dev/null 2>&1 || true
echo "[P0] verdict: if phase returned to Running/Succeeded and restartCount=0,"
echo "      the Failed trigger evaporated -> premise CONFIRMED."
