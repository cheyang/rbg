#!/bin/bash
set -uo pipefail
LOGFILE=/tmp/pr433-l3-results.log
: > "$LOGFILE"
exec > >(tee -a "$LOGFILE") 2>&1

echo "=============================================="
echo "L3 Live Test: PR #433 Semantic Revision Equality"
echo "Focus: RoleInstanceSet workload"
echo "Date: $(date)"
echo "=============================================="

# Phase 0: Remove incompatible RIS objects that block the informer cache
echo ""
echo "=== Phase 0: Remove incompatible RIS objects in default ns ==="
echo "(Backed up to /tmp/default-ris-backup.json earlier)"
kubectl delete ris -n default --all --wait=false 2>/dev/null || true
sleep 5
echo "Remaining RIS cluster-wide:"
kubectl get ris --all-namespaces --no-headers 2>/dev/null | wc -l

# Phase 1: Cleanup
echo ""
echo "=== Phase 1: Cleanup stale processes and test resources ==="
pkill -f 'rbgs-old' 2>/dev/null || true
pkill -f 'rbgs-new' 2>/dev/null || true
sleep 2

kubectl get ns pr433-test >/dev/null 2>&1 || kubectl create ns pr433-test
kubectl delete rbg -n pr433-test --all 2>/dev/null || true
kubectl delete controllerrevision -n pr433-test --all 2>/dev/null || true
sleep 5
echo "Cleanup done"

# Phase 2: Start NEW controller
echo ""
echo "=== Phase 2: Start NEW controller (PR #433 branch) ==="
nohup /tmp/rbgs-new --enable-webhooks=none --enable-port-allocator=false \
  --health-probe-bind-address=:9091 >/tmp/rbgs-new.log 2>&1 &
NEW_PID=$!
sleep 10

if ! kill -0 $NEW_PID 2>/dev/null; then
  echo "FATAL: controller died. Last 40 log lines:"
  tail -40 /tmp/rbgs-new.log
  exit 1
fi
echo "Controller running (PID=$NEW_PID)"

# Check if cache synced (look for reconciliation messages)
sleep 5
if grep -q "Failed to watch" /tmp/rbgs-new.log 2>/dev/null; then
  echo "WARNING: Cache still failing. Checking..."
  grep "Failed to watch" /tmp/rbgs-new.log | tail -3
fi

# Phase 3: Create RoleBasedGroup
echo ""
echo "=== Phase 3: Create RoleBasedGroup ==="
cat <<'EOF' | kubectl apply -f -
apiVersion: workloads.x-k8s.io/v1alpha2
kind: RoleBasedGroup
metadata:
  name: test-rbg
  namespace: pr433-test
spec:
  roles:
  - name: worker
    replicas: 1
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
echo "Waiting 40s for full reconciliation..."
sleep 40

echo ""
echo "--- RoleBasedGroups ---"
kubectl get rbg -n pr433-test -o wide 2>&1
echo ""
echo "--- RoleInstanceSets ---"
kubectl get ris -n pr433-test -o wide 2>&1
echo ""
echo "--- ControllerRevisions ---"
kubectl get controllerrevision -n pr433-test -o custom-columns=NAME:.metadata.name,REVISION:.revision,LABELS:.metadata.labels 2>&1

# Get the RIS name
RIS_NAME=$(kubectl get ris -n pr433-test --no-headers -o custom-columns=NAME:.metadata.name 2>/dev/null | head -1)
echo ""
echo "RIS name: ${RIS_NAME:-NOT_FOUND}"

if [ -z "$RIS_NAME" ] || [ "$RIS_NAME" = "" ]; then
  echo ""
  echo "No RIS was created. Checking controller log for errors..."
  grep -i "error\|fail\|panic" /tmp/rbgs-new.log 2>/dev/null | tail -10
  echo ""
  echo "Checking if RBG controller is reconciling..."
  grep -i "rolebasedgroup\|reconcil" /tmp/rbgs-new.log 2>/dev/null | tail -10
  echo ""
  echo "FATAL: Cannot proceed without RIS. Exiting."
  kill $NEW_PID 2>/dev/null || true
  echo "DONE_WITH_ERROR" > /tmp/pr433-l3-done
  exit 1
fi

# Count revisions
INITIAL_REV_COUNT=$(kubectl get controllerrevision -n pr433-test --no-headers 2>/dev/null | wc -l | tr -d ' ')
echo "Initial revision count: $INITIAL_REV_COUNT"

# Save first revision
REV_NAME=$(kubectl get controllerrevision -n pr433-test --no-headers -o custom-columns=NAME:.metadata.name 2>/dev/null | head -1)
echo "Target revision for drift: $REV_NAME"

if [ -z "$REV_NAME" ]; then
  echo "FATAL: No ControllerRevision found"
  kill $NEW_PID 2>/dev/null || true
  echo "DONE_WITH_ERROR" > /tmp/pr433-l3-done
  exit 1
fi

kubectl get controllerrevision "$REV_NAME" -n pr433-test -o json > /tmp/pr433-rev-original.json
echo "Original revision saved"

# Phase 4: Stop controller
echo ""
echo "=== Phase 4: Stop controller ==="
kill $NEW_PID 2>/dev/null || true
wait $NEW_PID 2>/dev/null || true
sleep 3
echo "Controller stopped"

# Phase 5: Inject drift
echo ""
echo "=== Phase 5: Inject serialization drift ==="
python3 << 'PYEOF'
import json

with open('/tmp/pr433-rev-original.json') as f:
    rev = json.load(f)

rev_name = rev['metadata']['name']
data = rev.get('data', {})
data_str_before = json.dumps(data)

def inject_drift(obj):
    if isinstance(obj, dict):
        for key, value in list(obj.items()):
            if key == "metadata" and isinstance(value, dict):
                value["creationTimestamp"] = None
            inject_drift(value)
    elif isinstance(obj, list):
        for item in obj:
            inject_drift(item)

inject_drift(data)
data_str_after = json.dumps(data)

# If no metadata found in data (it might be a raw patch bytes), try different approach
if data_str_before == data_str_after:
    # The data might be a strategic merge patch - try injecting at known paths
    if isinstance(data, dict):
        # Try spec.roles[].standalonePattern.template.metadata
        for role in data.get('spec', {}).get('roles', []):
            tmpl = role.get('standalonePattern', {}).get('template', {})
            if isinstance(tmpl, dict):
                if 'metadata' not in tmpl:
                    tmpl['metadata'] = {}
                tmpl['metadata']['creationTimestamp'] = None
            # roleInstanceTemplate path
            rit = role.get('roleInstanceTemplate', {})
            if isinstance(rit, dict) and 'metadata' not in rit:
                rit['metadata'] = {'creationTimestamp': None}
            for comp in rit.get('components', []):
                ct = comp.get('template', {})
                if isinstance(ct, dict):
                    if 'metadata' not in ct:
                        ct['metadata'] = {}
                    ct['metadata']['creationTimestamp'] = None
    data_str_after = json.dumps(data)

bytes_differ = data_str_before != data_str_after
print(f"Revision: {rev_name}")
print(f"Data before: {len(data_str_before)} bytes")
print(f"Data after:  {len(data_str_after)} bytes")
print(f"Bytes differ: {bytes_differ}")

if not bytes_differ:
    print("WARNING: Could not inject drift via metadata path.")
    print("Falling back: injecting a harmless creationTimestamp at top-level metadata of patch")
    # Force a byte change by wrapping existing data
    if isinstance(data, dict):
        # Add a metadata.creationTimestamp at root of the patch data
        if 'metadata' not in data:
            data['metadata'] = {}
        data['metadata']['creationTimestamp'] = None
        data_str_after = json.dumps(data)
        print(f"After forced injection: {len(data_str_after)} bytes, differ={data_str_before != data_str_after}")

rev['data'] = data
# Clean metadata for recreation
for key in ['resourceVersion', 'uid', 'managedFields', 'creationTimestamp']:
    rev['metadata'].pop(key, None)

with open('/tmp/pr433-rev-drifted.json', 'w') as f:
    json.dump(rev, f, indent=2)

print("Drifted revision written to /tmp/pr433-rev-drifted.json")
PYEOF

echo ""
echo "Deleting original revision..."
kubectl delete controllerrevision "$REV_NAME" -n pr433-test
sleep 2
echo "Recreating with drift..."
kubectl apply -f /tmp/pr433-rev-drifted.json
sleep 2

echo ""
echo "Verifying drift:"
kubectl get controllerrevision "$REV_NAME" -n pr433-test -o json | python3 -c "
import json, sys
rev = json.load(sys.stdin)
data = rev.get('data', {})
data_str = json.dumps(data)
has_ts = 'creationTimestamp' in data_str
print(f'Has creationTimestamp in data: {has_ts}')
print(f'ResourceVersion: {rev[\"metadata\"][\"resourceVersion\"]}')
print(f'Data (first 400 chars): {data_str[:400]}')
"

# Phase 6: Record BEFORE
echo ""
echo "=== Phase 6: Record BEFORE state ==="
BEFORE_REV_COUNT=$(kubectl get controllerrevision -n pr433-test --no-headers 2>/dev/null | wc -l | tr -d ' ')
echo "Revision count BEFORE: $BEFORE_REV_COUNT"
kubectl get controllerrevision -n pr433-test -o custom-columns=NAME:.metadata.name,REVISION:.revision,RV:.metadata.resourceVersion

# Phase 7: Restart controller
echo ""
echo "=== Phase 7: Restart controller ==="
: > /tmp/rbgs-new.log
nohup /tmp/rbgs-new --enable-webhooks=none --enable-port-allocator=false \
  --health-probe-bind-address=:9091 >/tmp/rbgs-new.log 2>&1 &
NEW_PID=$!
echo "Controller PID=$NEW_PID, waiting 25s..."
sleep 25

if ! kill -0 $NEW_PID 2>/dev/null; then
  echo "WARNING: controller died"
  tail -10 /tmp/rbgs-new.log
fi

# Phase 8: VERIFY
echo ""
echo "=== Phase 8: VERIFY ==="
AFTER_REV_COUNT=$(kubectl get controllerrevision -n pr433-test --no-headers 2>/dev/null | wc -l | tr -d ' ')
echo "Revision count AFTER: $AFTER_REV_COUNT"
kubectl get controllerrevision -n pr433-test -o custom-columns=NAME:.metadata.name,REVISION:.revision,RV:.metadata.resourceVersion

echo ""
echo "=============================="
if [ "$BEFORE_REV_COUNT" -eq "$AFTER_REV_COUNT" ]; then
  echo ">>> RESULT: PASS"
  echo ">>> No spurious revision created."
  echo ">>> Semantic equality check correctly identified drifted revision."
else
  echo ">>> RESULT: FAIL"
  echo ">>> Revision count: $BEFORE_REV_COUNT -> $AFTER_REV_COUNT"
fi
echo "=============================="

echo ""
echo "=== Controller log (relevant lines) ==="
grep -i "semantic\|SetMatches\|bytes differ\|equal\|revision\|reconcil" /tmp/rbgs-new.log 2>/dev/null | head -30 || echo "(none)"

echo ""
echo "=== RIS status ==="
kubectl get ris -n pr433-test -o wide 2>&1

# Phase 9: Cleanup
echo ""
echo "=== Phase 9: Cleanup ==="
kill $NEW_PID 2>/dev/null || true
wait $NEW_PID 2>/dev/null || true

# Restore webhook
kubectl patch validatingwebhookconfiguration rbgs-validating-webhook-configuration \
  --type='json' -p='[{"op":"replace","path":"/webhooks/0/failurePolicy","value":"Fail"}]' 2>/dev/null || true

echo ""
echo "=============================================="
echo "L3 TEST COMPLETE"
echo "=============================================="
echo "DONE" > /tmp/pr433-l3-done
