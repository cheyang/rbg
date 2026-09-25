#!/usr/bin/env bash
# PR474 live verification — prepare a cluster (fresh or pre-existing).
# Idempotent. Backs up what it touches, applies the PR's CRDs (server-side: the
# last-applied annotation overflows the 256KiB annotation limit for these CRDs),
# and installs the chart's controller+webhook stack when the cluster has none
# (the released image serves the conversion/admission webhooks; the rollout
# behavior runs out-of-cluster from the binaries under test).
set -euo pipefail
WORK=/root/work/rbg-pr474
NS=pr474-verify
mkdir -p "$WORK/backup" "$WORK/results"

kubectl get validatingwebhookconfiguration rbgs-validating-webhook-configuration -o yaml \
  > "$WORK/backup/validating-webhook.before.yaml" 2>/dev/null || echo "no validating webhook config yet"
kubectl get mutatingwebhookconfiguration rbgs-mutating-webhook-configuration -o yaml \
  > "$WORK/backup/mutating-webhook.before.yaml" 2>/dev/null || echo "no mutating webhook config yet"

kubectl apply --server-side --field-manager=verify-pr474 --force-conflicts \
  -f "$WORK/repo/config/crd/bases/"

kubectl get crd rolebasedgroupsets.workloads.x-k8s.io -o json > "$WORK/backup/rbgs-crd.after.json"
grep -q rolloutStrategy "$WORK/backup/rbgs-crd.after.json" \
  || { echo "FATAL: rolloutStrategy missing from installed CRD schema"; exit 1; }

# Install the released controller+webhook stack if the cluster has none.
if ! kubectl -n rbg-system get deploy rbgs-controller-manager >/dev/null 2>&1; then
  helm install rbgs "$WORK/repo/deploy/helm/rbgs" -n rbg-system --create-namespace \
    --set crdUpgrade.enabled=false
fi
kubectl -n rbg-system wait --for=condition=Available deploy/rbgs-controller-manager --timeout=240s
kubectl -n rbg-system get deploy rbgs-controller-manager -o jsonpath='{.spec.replicas}' \
  > "$WORK/backup/controller-replicas.before"

# The CRD bases carry no conversion stanza (kustomize/helm add it at deploy time).
# On a cluster whose CRDs lack webhook conversion, add the stanza pointing at the
# in-cluster webhook service, with the CA the manager already synced into the
# admission webhook configurations.
python3 - <<'PY2'
import json, subprocess

def run(*args):
    return subprocess.run(list(args), stdout=subprocess.PIPE, stderr=subprocess.PIPE, universal_newlines=True)

out = run("kubectl", "get", "validatingwebhookconfiguration",
          "rbgs-validating-webhook-configuration", "-o", "json")
ca = None
if out.returncode == 0:
    hooks = json.loads(out.stdout).get("webhooks", [])
    if hooks:
        ca = hooks[0].get("clientConfig", {}).get("caBundle")

for crd in ["rolebasedgroups.workloads.x-k8s.io", "rolebasedgroupsets.workloads.x-k8s.io"]:
    cur = json.loads(run("kubectl", "get", "crd", crd, "-o", "json").stdout)
    conv = cur["spec"].get("conversion") or {}
    if conv.get("strategy") == "Webhook" and conv.get("webhook", {}).get("clientConfig", {}).get("caBundle"):
        continue
    patch = {"spec": {"conversion": {
        "strategy": "Webhook",
        "webhook": {
            "clientConfig": {
                "service": {"name": "rbgs-webhook-service", "namespace": "rbg-system",
                            "path": "/convert", "port": 443},
                "caBundle": ca,
            },
            "conversionReviewVersions": ["v1"],
        },
    }}}
    r = run("kubectl", "patch", "crd", crd, "--type=merge", "-p", json.dumps(patch))
    print(crd, "conversion patch rc =", r.returncode, r.stderr[:200])

PY2

kubectl get ns "$NS" >/dev/null 2>&1 || kubectl create ns "$NS"
kubectl -n "$NS" delete rolebasedgroupset --all --wait=false >/dev/null 2>&1 || true
echo PREPARE_OK
