#!/usr/bin/env bash
# PR474 live verification — restore the cluster: webhook policies, controller
# replicas, test namespace. Idempotent.
set -uo pipefail
WORK=/root/work/rbg-pr474
pkill -f 'bin/rbgs[-]' 2>/dev/null || true
# Restore failurePolicy=Fail on every rbgs webhook entry (the rollout/e2e
# windows relax them to Ignore while the webhook server is scaled down).
python3 - <<'PY2'
import json, subprocess
def run(*a):
    return subprocess.run(list(a), stdout=subprocess.PIPE, stderr=subprocess.PIPE, universal_newlines=True)
for kind, name in [("validatingwebhookconfiguration", "rbgs-validating-webhook-configuration"),
                   ("mutatingwebhookconfiguration", "rbgs-mutating-webhook-configuration")]:
    cur = run("kubectl", "get", kind, name, "-o", "json")
    if cur.returncode != 0:
        continue
    obj = json.loads(cur.stdout)
    patch = [{"op": "replace", "path": "/webhooks/%d/failurePolicy" % i, "value": "Fail"}
             for i in range(len(obj.get("webhooks", [])))]
    if patch:
        run("kubectl", "patch", kind, name, "--type=json", "-p", json.dumps(patch))
PY2
REPLICAS=$(cat "$WORK/backup/controller-replicas.before" 2>/dev/null || echo 2)
kubectl -n rbg-system scale deploy rbgs-controller-manager --replicas="$REPLICAS"
kubectl -n rbg-system wait --for=condition=Available deploy/rbgs-controller-manager --timeout=180s || true
kubectl delete ns pr474-verify --ignore-not-found --wait=false
echo RESTORE_OK
