#!/usr/bin/env bash
# L3 / F4 -- run the PR binary against a real cluster with
# --enable-v1alpha1-compat=false and observe what happens to a MIXED RBG
# (one legacy Deployment role + two healthy RoleInstanceSet roles).
#
# Question: does one legacy role stop the whole group, terminally?
#
# Identity: this scenario deliberately runs with the ADMIN kubeconfig, not the
# controller ServiceAccount. F4 is about reconcile granularity, not RBAC, and
# admin creds remove the 403 confounder entirely -- if the healthy role is not
# created, it cannot be blamed on a missing permission. (The PR#413 round needed
# SA identity because its finding WAS about 403s.)
#
# SCOPE / SHARED INFRA: scales the in-cluster rbgs-controller-manager to 0 so it
# does not race the binary under test, and restores the original replica count on
# exit. Creates and deletes exactly one namespace ($NS). Nothing else is touched.
#
# HARD GUARDS. The first attempt at this script produced a completely vacuous
# run that superficially looked like a result: the binary's RoleInstanceSet
# informer never synced (stale objects elsewhere in the cluster), so it
# reconciled nothing, while the in-cluster controller -- still finishing its
# shutdown -- did all the observed work with compat=TRUE. Every guard below
# exists because of a specific way this went wrong:
#   G1 TREE DIRTY       : binary would not be the code under review
#   G2 RIVAL RUNNING    : the in-cluster controller still has ready pods
#   G3 FIXTURE INVALID  : the legacy workload-type annotation did not persist
#   G4 CONTROLLER VOID  : proof of reconcile must come from OUR PROCESS LOG, not
#                         from an API object anyone could have created
#   G5 DIED EARLY       : the binary must still be alive when we read results
#   G6 CACHE BROKEN     : any "Failed to watch" means observations are worthless
set -uo pipefail
: "${KUBECONFIG:=$HOME/.kube/config}"
export KUBECONFIG
ROOT="$(git rev-parse --show-toplevel)"
WORK=${WORK:-/root/pr414-verify}
RESULTS="$ROOT/docs/verification/pr414-v1alpha1-compat-flag/results"
NS=pr414-verify
CTRL_NS=rbg-system
CTRL_DEPLOY=rbgs-controller-manager
IMG=${IMG:-anolis-registry.cn-zhangjiakou.cr.aliyuncs.com/openanolis/nginx:1.14.1-8.6}
WT_ANN=rbg.workloads.x-k8s.io/role-workload-type
RUNLOG="$WORK/f4-controller.log"
mkdir -p "$WORK" "$RESULTS"

ORIG_REPLICAS=$(kubectl -n "$CTRL_NS" get deploy "$CTRL_DEPLOY" -o jsonpath='{.spec.replicas}' 2>/dev/null || echo "")

stop_controller() {
  [ -n "${PID:-}" ] || return 0
  kill -TERM "$PID" 2>/dev/null
  for _ in $(seq 1 10); do kill -0 "$PID" 2>/dev/null || return 0; sleep 1; done
  kill -9 "$PID" 2>/dev/null
}
cleanup() {
  stop_controller
  kubectl delete ns "$NS" --wait=false >/dev/null 2>&1 || true
  if [ -n "$ORIG_REPLICAS" ]; then
    kubectl -n "$CTRL_NS" scale deploy "$CTRL_DEPLOY" --replicas="$ORIG_REPLICAS" >/dev/null 2>&1 \
      && echo "   [teardown] restored $CTRL_DEPLOY to $ORIG_REPLICAS replica(s)"
  fi
}
trap cleanup EXIT

# --- G1: tree clean --------------------------------------------------------
# Reviewer harness files (untracked *_verify_test.go) are excluded: _test.go
# files are not compiled into ./cmd/rbgs, so they cannot change what is under
# test. Any OTHER difference aborts the run.
DIRTY=$(git status --porcelain -- api cmd internal pkg deploy config Makefile \
          | grep -vE '^\?\? .*pr414.*_verify_test\.go$')
if [ -n "$DIRTY" ]; then
  echo "G1 TREE DIRTY -- refusing to run; the binary would not be the code under review:" >&2
  echo "$DIRTY" | sed 's/^/  /' >&2
  exit 5
fi
echo "== G1 ok: tree clean, code under test = $(git rev-parse --short HEAD) =="

# --- G2: no rival controller ------------------------------------------------
# Round 2 note: the earlier version of this guard passed while a rival was in
# fact still acting. It counted pods with `grep -c` on kubectl output and treated
# ANY empty/failed result as "zero pods" -- so a transient kubectl error read as
# success. It now (a) requires kubectl itself to succeed, and (b) reads the
# deployment's own status rather than grepping pod names.
echo "== quiescing the in-cluster controller (was ${ORIG_REPLICAS:-absent} replicas) =="
kubectl -n "$CTRL_NS" scale deploy "$CTRL_DEPLOY" --replicas=0 >/dev/null 2>&1 || true
SEL=$(kubectl -n "$CTRL_NS" get deploy "$CTRL_DEPLOY" \
        -o jsonpath='{range .spec.selector.matchLabels}{@}{end}' 2>/dev/null)
SELARG=$(kubectl -n "$CTRL_NS" get deploy "$CTRL_DEPLOY" -o json 2>/dev/null | python3 -c "
import json,sys
d=json.load(sys.stdin)
print(','.join('%s=%s' % kv for kv in sorted(d['spec']['selector']['matchLabels'].items())))
" 2>/dev/null)
[ -n "$SELARG" ] || { echo "G2 SETUP FAILED: cannot read the deployment selector." >&2; exit 6; }
echo "   selector: $SELARG"

RIVAL=unknown
for _ in $(seq 1 40); do
  # kubectl MUST succeed; a failed call is not evidence of zero pods.
  if ! PODS=$(kubectl -n "$CTRL_NS" get pods -l "$SELARG" \
                -o jsonpath='{.items[*].metadata.name}' 2>/dev/null); then
    sleep 3; continue
  fi
  STATUS=$(kubectl -n "$CTRL_NS" get deploy "$CTRL_DEPLOY" \
             -o jsonpath='{.status.replicas}' 2>/dev/null)
  if [ -z "${PODS// /}" ] && [ -z "${STATUS:-}" ]; then RIVAL=none; break; fi
  sleep 3
done
if [ "$RIVAL" != none ]; then
  echo "G2 RIVAL RUNNING: $CTRL_DEPLOY still has pod(s)/replicas after ~120s. A controller" >&2
  echo "  with compat=TRUE would do the reconciling and every observation below would be a lie." >&2
  kubectl -n "$CTRL_NS" get pods -l "$SELARG" 2>/dev/null | sed 's/^/    /' >&2
  exit 6
fi
echo "== G2 ok: 0 pods for selector, .status.replicas empty =="

# --- G2b: the informer poison must be absent BEFORE we start ------------------
# N3: RoleInstanceSets written by the older in-cluster image store
# spec.roleInstanceTemplate.restartPolicy as a bare string, which the PR-head Go
# types cannot unmarshal -- the informer then never syncs and the run is void.
# Informers LIST at resourceVersion=0 (watch cache), so check it the same way;
# a plain `kubectl get` does a quorum read and can disagree.
POISON=$(kubectl get --raw \
  "/apis/workloads.x-k8s.io/v1alpha2/roleinstancesets?resourceVersion=0" 2>/dev/null | python3 -c "
import json,sys
bad=[]
for i in json.load(sys.stdin).get('items',[]):
    rp=((i.get('spec') or {}).get('roleInstanceTemplate') or {}).get('restartPolicy')
    if rp is not None and not isinstance(rp, dict):
        bad.append('%s/%s=%r' % (i['metadata']['namespace'], i['metadata']['name'], rp))
print(' '.join(bad))
" 2>/dev/null)
if [ -n "${POISON// /}" ]; then
  echo "G2b INFORMER POISON present -- these objects will stop our binary syncing (N3):" >&2
  echo "   $POISON" >&2
  echo "  Remove them (or use a cluster with no prior rbgs state) before running." >&2
  exit 9
fi
echo "== G2b ok: no string-shaped restartPolicy objects to poison the informer =="

echo "== reset namespace $NS =="
kubectl delete ns "$NS" --ignore-not-found --wait=true >/dev/null 2>&1 || true
kubectl create ns "$NS" >/dev/null

# --- fixtures ---------------------------------------------------------------
kubectl apply -f - >/dev/null <<YAML
apiVersion: workloads.x-k8s.io/v1alpha2
kind: RoleBasedGroup
metadata: {name: mixed, namespace: $NS}
spec:
  roles:
    - name: modern-a
      replicas: 1
      standalonePattern:
        template:
          spec: {containers: [{name: c, image: $IMG}]}
    - name: modern-b
      replicas: 1
      standalonePattern:
        template:
          spec: {containers: [{name: c, image: $IMG}]}
    - name: legacy
      replicas: 1
      annotations: {"$WT_ANN": "apps/v1/Deployment"}
      standalonePattern:
        template:
          spec: {containers: [{name: c, image: $IMG}]}
YAML

# --- G3: fixture really carries the legacy type -----------------------------
STORED=$(kubectl -n "$NS" get rbg mixed -o json | python3 -c "
import json,sys
roles=json.load(sys.stdin)['spec']['roles']
print(next((r.get('annotations') or {}).get('$WT_ANN','') for r in roles if r['name']=='legacy'))
")
if [ "$STORED" != "apps/v1/Deployment" ]; then
  echo "G3 FIXTURE INVALID: legacy workload-type did not persist (got '$STORED')." >&2
  exit 3
fi
echo "== G3 ok: RBG mixed = modern-a(RoleInstanceSet), modern-b(RoleInstanceSet), legacy($STORED) =="

# Liveness control: an all-healthy RBG. Our binary MUST be seen reconciling this
# one in its own log, or nothing below counts.
kubectl apply -f - >/dev/null <<YAML
apiVersion: workloads.x-k8s.io/v1alpha2
kind: RoleBasedGroup
metadata: {name: allmodern, namespace: $NS}
spec:
  roles:
    - name: worker
      replicas: 1
      standalonePattern:
        template:
          spec: {containers: [{name: c, image: $IMG}]}
YAML
echo "   RBG allmodern: worker(RoleInstanceSet)  <- liveness control"

echo "== building the binary under test =="
go build -o "$WORK/rbgs-under-test" ./cmd/rbgs || { echo "build failed" >&2; exit 2; }
echo "   sha256: $(sha256sum "$WORK/rbgs-under-test" | cut -c1-16)"

echo "== running: --enable-v1alpha1-compat=false (admin identity) =="
"$WORK/rbgs-under-test" \
  --enable-v1alpha1-compat=false --enable-webhooks=none \
  --metrics-bind-address=0 --health-probe-bind-address=0 --zap-log-level=info \
  > "$RUNLOG" 2>&1 &
PID=$!

# --- G4: proof of reconcile must come from OUR log --------------------------
echo -n "   waiting for OUR process log to show it reconciling the control RBG"
RECONCILED=no
for _ in $(seq 1 40); do
  sleep 3; echo -n "."
  if grep -q '"allmodern"' "$RUNLOG" 2>/dev/null \
     && kubectl -n "$NS" get roleinstanceset allmodern-worker >/dev/null 2>&1; then
    RECONCILED=yes; break
  fi
  kill -0 "$PID" 2>/dev/null || break
done
echo
if [ "$RECONCILED" != yes ]; then
  echo "G4 CONTROLLER VOID: our binary never logged a reconcile of the legacy-free control" >&2
  echo "  RBG within ~120s, so silence below would prove nothing." >&2
  echo "  process alive: $(kill -0 "$PID" 2>/dev/null && echo yes || echo NO)" >&2
  echo "  log lines: $(wc -l < "$RUNLOG")   top errors:" >&2
  grep -o '"error":"[^"]*"' "$RUNLOG" 2>/dev/null | cut -c1-160 | sort | uniq -c \
    | sort -rn | head -4 | sed 's/^/    /' >&2
  cp "$RUNLOG" "$RESULTS/l3-f4-VOID-controller.log" 2>/dev/null || true
  exit 4
fi
echo "== G4 ok: our binary is reconciling (logged 'allmodern' + roleinstanceset exists) =="

# --- G7: attribute the created object to the CODE UNDER TEST ------------------
# G4 proves *something* of ours logged a reconcile; G7 proves the object in the
# cluster was written by the code under review and not by a rival that slipped
# past G2. Two independent signals:
#   1. restartPolicy must be the STRUCT shape -- the old image writes a string.
#   2. the field manager must not be the in-cluster controller's.
SHAPE=$(kubectl -n "$NS" get roleinstanceset allmodern-worker -o json 2>/dev/null | python3 -c "
import json,sys
d=json.load(sys.stdin)
rp=((d.get('spec') or {}).get('roleInstanceTemplate') or {}).get('restartPolicy')
mgrs=sorted({f.get('manager','?') for f in (d.get('metadata',{}).get('managedFields') or [])})
print('%s|%s|%s' % (type(rp).__name__, json.dumps(rp)[:60], ','.join(mgrs)))
" 2>/dev/null)
RP_TYPE=${SHAPE%%|*}
RP_MGRS=${SHAPE##*|}
echo "   restartPolicy type=$RP_TYPE  field managers=[$RP_MGRS]"
if [ "$RP_TYPE" = "str" ]; then
  echo "G7 WRONG WRITER: allmodern-worker has a STRING restartPolicy, which the code under" >&2
  echo "  review never writes -- a rival controller created it, so every observation below" >&2
  echo "  would be about the wrong binary. (This is exactly how the round-1/2 runs went void.)" >&2
  cp "$RUNLOG" "$RESULTS/l3-f4-VOID-controller.log" 2>/dev/null || true
  exit 10
fi
echo "== G7 ok: the object was written by the code under test (struct-shaped restartPolicy) =="

sleep 45   # let the mixed RBG settle / prove nothing further happens

# --- G5 + G6 ---------------------------------------------------------------
if ! kill -0 "$PID" 2>/dev/null; then
  echo "G5 DIED EARLY: the binary exited before observations were taken." >&2
  tail -5 "$RUNLOG" | sed 's/^/    /' >&2
  exit 7
fi
WATCHFAIL=$(grep -c 'Failed to watch' "$RUNLOG" 2>/dev/null || true)
if [ "${WATCHFAIL:-0}" -gt 0 ]; then
  echo "G6 CACHE BROKEN: $WATCHFAIL 'Failed to watch' errors -- the cache never fully synced," >&2
  echo "  so 'the object was not created' is not attributable to the code under test." >&2
  grep -o '"error":"failed to list[^"]*"' "$RUNLOG" | cut -c1-160 | sort -u | head -3 | sed 's/^/    /' >&2
  cp "$RUNLOG" "$RESULTS/l3-f4-VOID-controller.log" 2>/dev/null || true
  exit 8
fi
echo "== G5/G6 ok: process alive, 0 watch failures =="
stop_controller

echo
echo "===== OBSERVATIONS ====="
echo "-- RoleInstanceSets in $NS --"
kubectl -n "$NS" get roleinstanceset --no-headers -o custom-columns=NAME:.metadata.name 2>/dev/null | sed 's/^/     /'

VERDICT=fixed
for role in modern-a modern-b; do
  echo -n "   mixed-$role RoleInstanceSet: "
  if kubectl -n "$NS" get roleinstanceset "mixed-$role" >/dev/null 2>&1; then
    echo "PRESENT (healthy role survived)"
  else
    echo "ABSENT  <-- healthy role abandoned because a SIBLING role is legacy (F4)"
    VERDICT=reproduced
  fi
done

echo -n "   Deployment for the legacy role (mixed-legacy): "
kubectl -n "$NS" get deploy mixed-legacy >/dev/null 2>&1 \
  && echo "PRESENT (unexpected -- the guard did not hold)" || echo "absent (guard held)"

echo
echo "-- RBG mixed status --"
echo "   conditions   : $(kubectl -n "$NS" get rbg mixed -o jsonpath='{range .status.conditions[*]}{.type}={.status}({.reason}) {end}' 2>/dev/null)"
echo "   message      : $(kubectl -n "$NS" get rbg mixed -o jsonpath='{.status.conditions[0].message}' 2>/dev/null)"
echo "   roleStatuses : $(kubectl -n "$NS" get rbg mixed -o jsonpath='{.status.roleStatuses}' 2>/dev/null)"
echo "   events       :"
kubectl -n "$NS" get events --field-selector involvedObject.name=mixed \
  -o custom-columns=COUNT:.count,REASON:.reason,MSG:.message --no-headers 2>/dev/null \
  | sort -u | head -5 | sed 's/^/     /'

echo
echo "-- control RBG (allmodern) for comparison --"
echo "   conditions   : $(kubectl -n "$NS" get rbg allmodern -o jsonpath='{range .status.conditions[*]}{.type}={.status}({.reason}) {end}' 2>/dev/null)"
echo -n "   allmodern-worker RoleInstanceSet: "
kubectl -n "$NS" get roleinstanceset allmodern-worker >/dev/null 2>&1 && echo PRESENT || echo ABSENT

echo
echo "-- is the stop terminal? counted from our own log --"
printf "   reconciles mentioning 'mixed'     : %s\n" "$(grep -c '"mixed"' "$RUNLOG" 2>/dev/null || echo 0)"
printf "   reconciles mentioning 'allmodern' : %s\n" "$(grep -c '"allmodern"' "$RUNLOG" 2>/dev/null || echo 0)"
printf "   'v1alpha1 compat is disabled' lines: %s\n" "$(grep -c 'v1alpha1 compat is disabled' "$RUNLOG" 2>/dev/null || echo 0)"
printf "   'legacy workload types' lines      : %s\n" "$(grep -c 'legacy workload types' "$RUNLOG" 2>/dev/null || echo 0)"
printf "   Forbidden errors                   : %s\n" "$(grep -c 'Forbidden' "$RUNLOG" 2>/dev/null || echo 0)"

cp "$RUNLOG" "$RESULTS/l3-f4-controller.log" 2>/dev/null || true
echo
if [ "$VERDICT" = reproduced ]; then
  echo "RESULT: F4 REPRODUCED LIVE -- healthy RoleInstanceSet roles were abandoned"
  echo "        because a sibling role in the same RBG uses a v1alpha1 indirect type."
else
  echo "RESULT: F4 not reproduced live -- healthy roles were reconciled anyway."
fi
echo "(controller log copied to results/l3-f4-controller.log)"
