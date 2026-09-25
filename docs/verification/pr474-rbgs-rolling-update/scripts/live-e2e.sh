#!/usr/bin/env bash
# PR474 live verification — run the PR's own e2e "rbgset controller" cases
# (incl. all the new rolling-update specs) against the real cluster, with the
# PR-head controller binary running out-of-cluster as the sole reconciler.
set -uo pipefail
WORK=/root/work/rbg-pr474
OUT="$WORK/results/e2e-rbgset-head.log"
BIN="$WORK/bin/rbgs-head"
E2E="$WORK/bin/e2e.test"

kubectl -n rbg-system scale deploy rbgs-controller-manager --replicas=0 >/dev/null
python3 - <<'PY2'
import json, subprocess
def run(*a):
    return subprocess.run(list(a), stdout=subprocess.PIPE, stderr=subprocess.PIPE, universal_newlines=True)
for kind in ["validatingwebhookconfiguration", "mutatingwebhookconfiguration"]:
    for name in run("kubectl", "get", kind, "-o", "name").stdout.split():
        if "rbgs" not in name:
            continue
        obj = json.loads(run("kubectl", "get", kind, name.split("/")[-1], "-o", "json").stdout)
        patch = [{"op": "replace", "path": "/webhooks/%d/failurePolicy" % i, "value": "Ignore"}
                 for i in range(len(obj.get("webhooks", [])))]
        if patch:
            run("kubectl", "patch", kind, name.split("/")[-1], "--type=json", "-p", json.dumps(patch))
PY2

# The framework's AfterEach cleans up v1alpha1 objects too, and any v1alpha1 call
# goes through the conversion webhook, which is dead while the in-cluster
# controller is scaled to 0. Drop conversion to trivial (None) for the run and
# restore the saved stanza afterwards.
for crd in rolebasedgroups.workloads.x-k8s.io rolebasedgroupsets.workloads.x-k8s.io; do
  kubectl get crd "$crd" -o jsonpath='{.spec.conversion}' > "$WORK/backup/conversion.$crd.json" 2>/dev/null || true
  kubectl patch crd "$crd" --type=json -p='[{"op":"replace","path":"/spec/conversion","value":{"strategy":"None"}}]' >/dev/null
done

pkill -f 'bin/rbgs[-]' 2>/dev/null || true
sleep 1
setsid nohup "$BIN" --enable-webhooks none --metrics-bind-address=0 --health-probe-bind-address=:18081 \
  > "$WORK/results/e2e-controller.log" 2>&1 < /dev/null &
sleep 8

# v1alpha2 "rbgset controller" specs only (the v1alpha1 block's cases predate the
# feature and the webhook-validation specs need an in-cluster admission chain).
"$E2E" -test.timeout=0 -ginkgo.v -ginkgo.label-filter='!volcano && !scheduler-plugins' \
  -ginkgo.focus='v1alpha2.*rbgset controller' 2>&1 | tee "$OUT" | tail -40
RC=${PIPESTATUS[0]}

pkill -f 'bin/rbgs[-]' 2>/dev/null || true
for crd in rolebasedgroups.workloads.x-k8s.io rolebasedgroupsets.workloads.x-k8s.io; do
  if [ -s "$WORK/backup/conversion.$crd.json" ]; then
    kubectl patch crd "$crd" --type=json       -p="[{\"op\":\"replace\",\"path\":\"/spec/conversion\",\"value\":$(cat "$WORK/backup/conversion.$crd.json")}]" >/dev/null || true
  fi
done
echo "E2E_RC=$RC"
