#!/usr/bin/env bash
# Exercises tools/preflight/check-deprecated-workloads.sh across the cases that
# decide whether it is safe to gate an install on it.
#
# The point of most of these is the failure DIRECTION: a preflight that exits 0 when
# it could not actually see the cluster is worse than no preflight, because it
# converts "unknown" into "approved".
#
# READ-ONLY against the cluster (one `kubectl get`); everything else is a fixture.
set -uo pipefail

SCRIPT=${SCRIPT:-docs/verification/pr416-api-compat-toggle/examples/check-deprecated-workloads.sh}
F=$(mktemp -d)
pass=0; fail=0

expect() { # expect <want-rc> <name> -- reads the command from stdin
  local want="$1" name="$2" out rc
  out=$(bash -c "$(cat)" 2>&1); rc=$?
  if [ "$rc" = "$want" ]; then
    printf '  PASS  rc=%s  %s\n' "$rc" "$name"; pass=$((pass+1))
  else
    printf '  FAIL  rc=%s (want %s)  %s\n' "$rc" "$want" "$name"; fail=$((fail+1))
    printf '%s\n' "$out" | sed 's/^/          /' | head -6
  fi
  printf '%s\n' "$out" | sed 's/^/        | /' | head -8
}

cat > "$F/clean.json" <<'JSON'
{"items":[
 {"apiVersion":"workloads.x-k8s.io/v1alpha2","metadata":{"namespace":"a","name":"ok1"},
  "spec":{"roles":[{"name":"worker"}]}},
 {"apiVersion":"workloads.x-k8s.io/v1alpha2","metadata":{"namespace":"a","name":"ok2"},
  "spec":{"roles":[{"name":"w","annotations":{"rbg.workloads.x-k8s.io/role-workload-type":"workloads.x-k8s.io/v1alpha2/RoleInstanceSet"}}]}}
]}
JSON

cat > "$F/offender.json" <<'JSON'
{"items":[
 {"apiVersion":"workloads.x-k8s.io/v1alpha2","metadata":{"namespace":"prod","name":"legacy"},
  "spec":{"roles":[
    {"name":"worker","annotations":{"rbg.workloads.x-k8s.io/role-workload-type":"apps/v1/StatefulSet"}},
    {"name":"ok","annotations":{"rbg.workloads.x-k8s.io/role-workload-type":"workloads.x-k8s.io/v1alpha2/RoleInstanceSet"}}]}}
]}
JSON

cat > "$F/rbgset.json" <<'JSON'
{"items":[
 {"apiVersion":"workloads.x-k8s.io/v1alpha2","kind":"RoleBasedGroupSet",
  "metadata":{"namespace":"prod","name":"set1"},
  "spec":{"groupTemplate":{"spec":{"roles":[
    {"name":"lws","annotations":{"rbg.workloads.x-k8s.io/role-workload-type":"leaderworkerset.x-k8s.io/v1/LeaderWorkerSet"}}]}}}}
]}
JSON

cat > "$F/deployment.json" <<'JSON'
{"items":[
 {"metadata":{"namespace":"d","name":"dep"},
  "spec":{"roles":[{"name":"w","annotations":{"rbg.workloads.x-k8s.io/role-workload-type":"apps/v1/Deployment"}}]}}
]}
JSON

cat > "$F/empty.json" <<'JSON'
{"items":[]}
JSON

python3 - "$F/many.json" <<'PY'
import json,sys
items=[{"metadata":{"namespace":"n%d"%i,"name":"r%d"%i},
        "spec":{"roles":[{"name":"w","annotations":{"rbg.workloads.x-k8s.io/role-workload-type":"apps/v1/StatefulSet"}}]}}
       for i in range(25)]
json.dump({"items":items},open(sys.argv[1],"w"))
PY

echo "### 1) toggle ON -- must be a no-op regardless of what is in the cluster"
expect 0 "enabled=true short-circuits" <<EOF
ENABLE_DEPRECATED_WORKLOAD_TYPES=true FIXTURE_FILE=$F/offender.json bash $SCRIPT
EOF

echo
echo "### 2) clean inputs -- must pass"
expect 0 "roles with no annotation default to RoleInstanceSet" <<EOF
ENABLE_DEPRECATED_WORKLOAD_TYPES=false FIXTURE_FILE=$F/clean.json bash $SCRIPT
EOF
expect 0 "no objects at all" <<EOF
ENABLE_DEPRECATED_WORKLOAD_TYPES=false FIXTURE_FILE=$F/empty.json bash $SCRIPT
EOF

echo
echo "### 3) each deprecated type must be caught, on both kinds"
expect 1 "StatefulSet on a RoleBasedGroup" <<EOF
ENABLE_DEPRECATED_WORKLOAD_TYPES=false FIXTURE_FILE=$F/offender.json bash $SCRIPT
EOF
expect 1 "Deployment on a RoleBasedGroup" <<EOF
ENABLE_DEPRECATED_WORKLOAD_TYPES=false FIXTURE_FILE=$F/deployment.json bash $SCRIPT
EOF
expect 1 "LeaderWorkerSet via RoleBasedGroupSet groupTemplate" <<EOF
ENABLE_DEPRECATED_WORKLOAD_TYPES=false FIXTURE_FILE=$F/rbgset.json bash $SCRIPT
EOF

cat > "$F/unknown.json" <<'JSON'
{"items":[
 {"metadata":{"namespace":"n","name":"future"},
  "spec":{"roles":[{"name":"x","annotations":{"rbg.workloads.x-k8s.io/role-workload-type":"acme.io/v1/FutureThing"}}]}}
]}
JSON

echo
echo "### 4) FAIL-CLOSED: a type that is neither RoleInstanceSet nor a known"
echo "     deprecated one must be refused, not waved through. This is the whole"
echo "     reason the check is an allowlist -- a fourth deprecated type added to"
echo "     isDeprecatedWorkloadType is caught here without touching this script."
expect 1 "unknown workload type is refused" <<EOF
ENABLE_DEPRECATED_WORKLOAD_TYPES=false FIXTURE_FILE=$F/unknown.json bash $SCRIPT
EOF

echo
echo "### 5) output is capped, and says so"
expect 1 "25 offenders, MAX_REPORTED=5" <<EOF
ENABLE_DEPRECATED_WORKLOAD_TYPES=false MAX_REPORTED=5 FIXTURE_FILE=$F/many.json bash $SCRIPT
EOF

echo
echo "### 6) FAILURE DIRECTION -- the cases that decide if this is safe to gate on"
echo "     a query it cannot complete must NOT come back as 'approved'"
expect 2 "unreachable API server must exit 2, not 0" <<EOF
ENABLE_DEPRECATED_WORKLOAD_TYPES=false KUBECONFIG=/nonexistent-kubeconfig \
  KUBERNETES_SERVICE_HOST=127.0.0.1 KUBERNETES_SERVICE_PORT=1 bash $SCRIPT
EOF

echo
echo "     ...and the other side of that coin: a CRD that is simply not installed"
echo "     yet must read as 'fresh cluster', NOT as a failure"
expect 0 "both CRDs absent -> fresh cluster, exit 0" <<EOF
ENABLE_DEPRECATED_WORKLOAD_TYPES=false \
  RBG_RESOURCE=nosuchthings.example.com RBGSET_RESOURCE=nosuchsets.example.com bash $SCRIPT
EOF
expect 0 "one CRD absent, the other real and clean" <<EOF
ENABLE_DEPRECATED_WORKLOAD_TYPES=false RBGSET_RESOURCE=nosuchsets.example.com bash $SCRIPT
EOF

echo
echo "### 7) against the real cluster (read-only)"
expect 0 "live cluster, toggle off" <<EOF
ENABLE_DEPRECATED_WORKLOAD_TYPES=false bash $SCRIPT
EOF

echo
echo "======== $pass passed, $fail failed ========"
rm -rf "$F"
[ "$fail" -eq 0 ]
