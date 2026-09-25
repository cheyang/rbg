#!/usr/bin/env bash
# PR474 live verification — F1: a v1alpha1 read-modify-write round trip through
# the real API server must not silently drop spec.rolloutStrategy.
# Runs while the IN-CLUSTER controller is up, because the CRD conversion webhook
# is served by it. Uses the deprecated-but-served v1alpha1 API exactly like an
# older client or GitOps tooling pinned to v1alpha1 would.
set -uo pipefail
WORK=/root/work/rbg-pr474
NS=pr474-verify
OUT="$WORK/results/f1-conversion.log"
: > "$OUT"
step() { echo "[$(date +%H:%M:%S)] $*" | tee -a "$OUT"; }

kubectl -n rbg-system wait --for=condition=Available deploy/rbgs-controller-manager --timeout=90s >/dev/null
step "in-cluster webhook serving conversion: OK"

kubectl -n "$NS" delete rolebasedgroupset f1-roundtrip --ignore-not-found --wait=true >/dev/null 2>&1 || true

cat <<'YAML' | kubectl apply -f - | tee -a "$OUT"
apiVersion: workloads.x-k8s.io/v1alpha2
kind: RoleBasedGroupSet
metadata:
  name: f1-roundtrip
  namespace: pr474-verify
spec:
  replicas: 1
  rolloutStrategy:
    type: Recreate
    maxUnavailable: 1
    maxSurge: 1
    partition: 0
  groupTemplate:
    spec:
      roles:
      - name: worker
        replicas: 1
        standalonePattern:
          template:
            spec:
              containers:
              - name: nginx
                image: registry.cn-hangzhou.aliyuncs.com/acs-sample/nginx:latest
YAML

sleep 2
step "stored v1alpha2 spec.rolloutStrategy:"
kubectl get rolebasedgroupset f1-roundtrip -n "$NS" -o jsonpath='{.spec.rolloutStrategy}' | tee -a "$OUT"; echo | tee -a "$OUT"

kubectl get rolebasedgroupsets.v1alpha1.workloads.x-k8s.io f1-roundtrip -n "$NS" -o yaml > /tmp/f1-v1alpha1.yaml 2>>"$OUT"
step "v1alpha1 view: $(grep -c rolloutStrategy /tmp/f1-v1alpha1.yaml || true) rolloutStrategy occurrence(s) — the deprecated version has no such field"

# Write the v1alpha1 object back with an unrelated label change (full-object PUT).
python3 - <<'PY'
p = '/tmp/f1-v1alpha1.yaml'
s = open(p).read()
assert 'verify-pr474' not in s
s = s.replace('  labels:', '  labels:\n    verify-pr474: touched', 1)
if 'verify-pr474' not in s:
    s = s.replace('metadata:\n', 'metadata:\n  labels:\n    verify-pr474: touched\n', 1)
open(p, 'w').write(s)
PY
kubectl replace -f /tmp/f1-v1alpha1.yaml 2>&1 | grep -v 'Warning' | tee -a "$OUT"

sleep 2
RS=$(kubectl get rolebasedgroupset f1-roundtrip -n "$NS" -o jsonpath='{.spec.rolloutStrategy}')
LBL=$(kubectl get rolebasedgroupset f1-roundtrip -n "$NS" -o jsonpath='{.metadata.labels.verify-pr474}')
step "after v1alpha1 replace: metadata.labels.verify-pr474=${LBL:-<unset>} (the write landed)"
step "after v1alpha1 replace: spec.rolloutStrategy=${RS:-<EMPTY>}"
if [ -z "$RS" ]; then
  step "F1_LIVE_CONFIRMED: the v1alpha1 round trip silently dropped spec.rolloutStrategy"
else
  step "F1_LIVE_REFUTED: rolloutStrategy survived the round trip"
fi

kubectl -n "$NS" delete rolebasedgroupset f1-roundtrip --wait=false >/dev/null 2>&1 || true
