#!/bin/bash
set -uo pipefail
LOGFILE=/tmp/pr433-l3-r2-results.log
: > "$LOGFILE"
exec > >(tee -a "$LOGFILE") 2>&1

export KUBECONFIG=/root/.kube/config
NS=pr433-r2
BIN=/tmp/rbgs-new-r2

echo "=============================================="
echo "L3 Round-2 end-to-end regression — PR #433 head 12915120"
echo "Focus: RBG -> RIS -> RoleInstance -> Pod, no spurious rollout on restart"
echo "Date: $(date)"
echo "Binary: $BIN"
echo "=============================================="

pass=1
fail() { echo ">>> $1"; pass=0; }

# ---- Phase 0: webhook to Ignore so controller-at-0 doesn't block create ----
echo ""
echo "=== Phase 0: relax validating webhook failurePolicy -> Ignore ==="
kubectl patch validatingwebhookconfiguration rbgs-validating-webhook-configuration \
  --type='json' -p='[{"op":"replace","path":"/webhooks/0/failurePolicy","value":"Ignore"}]' 2>&1 || echo "(patch skipped)"

# ---- Phase 1: clean slate ----
echo ""
echo "=== Phase 1: fresh namespace $NS ==="
pkill -f 'rbgs-new-r2' 2>/dev/null || true
sleep 2
kubectl delete ns "$NS" --wait=true --timeout=60s 2>/dev/null || true
kubectl create ns "$NS"

# ---- Phase 2: start NEW controller (out-of-cluster) ----
echo ""
echo "=== Phase 2: start controller (head 12915120) ==="
: > /tmp/rbgs-new-r2.log
nohup "$BIN" --enable-webhooks=none --enable-port-allocator=false \
  --health-probe-bind-address=:9092 --metrics-bind-address=0 >/tmp/rbgs-new-r2.log 2>&1 &
PID=$!
sleep 12
if ! kill -0 $PID 2>/dev/null; then
  echo "FATAL: controller died. Log tail:"; tail -40 /tmp/rbgs-new-r2.log
  echo "DONE" > /tmp/pr433-l3-r2-done; exit 1
fi
echo "Controller running PID=$PID"

# ---- Phase 3: create RBG ----
echo ""
echo "=== Phase 3: create RoleBasedGroup ==="
cat <<EOF | kubectl apply -f -
apiVersion: workloads.x-k8s.io/v1alpha2
kind: RoleBasedGroup
metadata:
  name: test-rbg
  namespace: $NS
spec:
  roles:
  - name: worker
    replicas: 2
    standalonePattern:
      template:
        metadata:
          labels:
            app: test-rbg-worker
        spec:
          containers:
          - name: nginx
            image: nginx:1.25-alpine
            resources:
              requests:
                cpu: 10m
                memory: 16Mi
EOF
echo "Waiting 45s for full reconciliation..."
sleep 45

echo ""
echo "--- RBG ---";  kubectl get rbg -n "$NS" -o wide 2>&1
echo "--- RIS ---";  kubectl get ris -n "$NS" -o wide 2>&1
echo "--- RoleInstance ---"; kubectl get roleinstance -n "$NS" 2>&1 | head
echo "--- Pods ---"; kubectl get pods -n "$NS" 2>&1
echo "--- ControllerRevisions ---"
kubectl get controllerrevision -n "$NS" -o custom-columns=NAME:.metadata.name,REV:.revision,RV:.metadata.resourceVersion 2>&1

RIS_NAME=$(kubectl get ris -n "$NS" --no-headers -o custom-columns=NAME:.metadata.name 2>/dev/null | head -1)
POD_READY=$(kubectl get pods -n "$NS" --no-headers 2>/dev/null | grep -c '1/1.*Running')
[ -z "$RIS_NAME" ] && fail "FAIL: no RIS created"
echo "RIS=$RIS_NAME  ready-pods=$POD_READY"

# ---- Phase 4: record BEFORE (counts + full RV fingerprint) ----
echo ""
echo "=== Phase 4: record BEFORE ==="
BEFORE_CNT=$(kubectl get controllerrevision -n "$NS" --no-headers 2>/dev/null | wc -l | tr -d ' ')
BEFORE_FP=$(kubectl get controllerrevision -n "$NS" -o custom-columns=NAME:.metadata.name,RV:.metadata.resourceVersion --no-headers 2>/dev/null | sort)
BEFORE_RIS_RV=$(kubectl get ris "$RIS_NAME" -n "$NS" -o jsonpath='{.metadata.resourceVersion}' 2>/dev/null)
BEFORE_RIS_GEN=$(kubectl get ris "$RIS_NAME" -n "$NS" -o jsonpath='{.status.observedGeneration}{" upd="}{.status.updatedReplicas}{" ready="}{.status.readyReplicas}' 2>/dev/null)
echo "revisions BEFORE: $BEFORE_CNT"
echo "$BEFORE_FP"
echo "RIS resourceVersion BEFORE: $BEFORE_RIS_RV  status: $BEFORE_RIS_GEN"

# ---- Phase 5: restart controller (forces full re-reconcile / handleRevisions) ----
echo ""
echo "=== Phase 5: restart controller (re-reconcile existing RBG) ==="
kill $PID 2>/dev/null || true; wait $PID 2>/dev/null || true
sleep 3
: > /tmp/rbgs-new-r2.log
nohup "$BIN" --enable-webhooks=none --enable-port-allocator=false \
  --health-probe-bind-address=:9092 --metrics-bind-address=0 >/tmp/rbgs-new-r2.log 2>&1 &
PID=$!
echo "restarted PID=$PID, waiting 35s for steady-state re-reconcile..."
sleep 35
kill -0 $PID 2>/dev/null || { echo "WARNING: controller died on restart"; tail -20 /tmp/rbgs-new-r2.log; }

# ---- Phase 6: also re-apply identical RBG spec to trigger an explicit reconcile ----
echo ""
echo "=== Phase 6: re-apply identical RBG spec (no-op change) ==="
kubectl apply -f - <<EOF 2>&1
apiVersion: workloads.x-k8s.io/v1alpha2
kind: RoleBasedGroup
metadata:
  name: test-rbg
  namespace: $NS
spec:
  roles:
  - name: worker
    replicas: 2
    standalonePattern:
      template:
        metadata:
          labels:
            app: test-rbg-worker
        spec:
          containers:
          - name: nginx
            image: nginx:1.25-alpine
            resources:
              requests:
                cpu: 10m
                memory: 16Mi
EOF
sleep 20

# ---- Phase 7: record AFTER + verify ----
echo ""
echo "=== Phase 7: record AFTER + verify ==="
AFTER_CNT=$(kubectl get controllerrevision -n "$NS" --no-headers 2>/dev/null | wc -l | tr -d ' ')
AFTER_FP=$(kubectl get controllerrevision -n "$NS" -o custom-columns=NAME:.metadata.name,RV:.metadata.resourceVersion --no-headers 2>/dev/null | sort)
AFTER_RIS_RV=$(kubectl get ris "$RIS_NAME" -n "$NS" -o jsonpath='{.metadata.resourceVersion}' 2>/dev/null)
AFTER_RIS_GEN=$(kubectl get ris "$RIS_NAME" -n "$NS" -o jsonpath='{.status.observedGeneration}{" upd="}{.status.updatedReplicas}{" ready="}{.status.readyReplicas}' 2>/dev/null)
echo "revisions AFTER: $AFTER_CNT"
echo "$AFTER_FP"
echo "RIS resourceVersion AFTER: $AFTER_RIS_RV  status: $AFTER_RIS_GEN"

echo ""
echo "=============================="
[ "$BEFORE_CNT" = "$AFTER_CNT" ] || fail "revision COUNT changed: $BEFORE_CNT -> $AFTER_CNT (spurious revision!)"
if [ "$BEFORE_FP" = "$AFTER_FP" ]; then
  echo "OK: ControllerRevision set + resourceVersions unchanged"
else
  fail "ControllerRevision fingerprint changed (revisions mutated/recreated):"
  echo "--- BEFORE ---"; echo "$BEFORE_FP"
  echo "--- AFTER  ---"; echo "$AFTER_FP"
fi
POD_READY2=$(kubectl get pods -n "$NS" --no-headers 2>/dev/null | grep -c '1/1.*Running')
[ "$POD_READY2" -ge 2 ] || fail "pods not all ready after re-reconcile: $POD_READY2/2"
echo "pods ready after: $POD_READY2/2"

echo ""
if [ "$pass" = "1" ]; then
  echo ">>> RESULT: PASS — full chain reconciled, no spurious rollout on restart/re-apply"
else
  echo ">>> RESULT: FAIL — see messages above"
fi
echo "=============================="

echo ""
echo "=== controller log (revision-related) ==="
grep -iE "semantic|SetMatches|bytes differ|need to be updated|skipping creation|revision" /tmp/rbgs-new-r2.log 2>/dev/null | head -25 || echo "(none)"

# ---- Phase 8: cleanup ----
echo ""
echo "=== Phase 8: cleanup ==="
kill $PID 2>/dev/null || true; wait $PID 2>/dev/null || true
kubectl delete ns "$NS" --wait=false 2>/dev/null || true
kubectl patch validatingwebhookconfiguration rbgs-validating-webhook-configuration \
  --type='json' -p='[{"op":"replace","path":"/webhooks/0/failurePolicy","value":"Fail"}]' 2>&1 || true
echo "webhook restored to Fail; namespace $NS deleting"

echo ""
echo "L3 ROUND-2 COMPLETE"
echo "DONE" > /tmp/pr433-l3-r2-done
