#!/usr/bin/env bash
# l2-crd-accepts-bogus-strategy.sh -- live-apiserver evidence for finding F3.
#
# Part of the verification harness for PR #415 (sgl-project/rbg).
# POLARITY: canary -- PASSES on the reviewed CRD (the apiserver accepts anything)
# and FLIPS TO RED once an `enum` is added to
# spec.roles[].rolloutStrategy.rollingUpdate.type. That flip is the desired
# signal: invert this script (assert the bogus value is REJECTED) and the docs'
# "default: InPlaceIfPossible" claim becomes schema-backed.
#
# F3: both zh:121 and en:121 document `rollingUpdate.type` as defaulting to
# InPlaceIfPossible, but the CRD schema has neither `default:` nor `enum:` -- the
# default is only back-filled by the controller
# (pkg/reconciler/roleinstanceset_reconciler.go:233-235). Consequence: a
# MISSPELLED strategy name is silently accepted by the apiserver and then handled
# by the `!= RecreatePod` branch, i.e. treated as in-place update.
#
# Safety: uses `kubectl apply --dry-run=server`, so NOTHING is ever persisted --
# the request goes through full apiserver validation/admission and is then
# discarded. No namespace is created and no resource is left behind.
#
# Honors $KUBECONFIG (falls back to kubectl's own default) so it runs unchanged
# on another machine.
#
# Usage: l2-crd-accepts-bogus-strategy.sh [namespace]
# Exit:  0 = canary holds (bogus value accepted + no schema enum);
#        1 = canary flipped (value rejected and/or enum present);
#        2 = environment error (no cluster / CRD not installed).
set -uo pipefail

NS="${1:-default}"
KUBECTL="${KUBECTL:-kubectl}"

echo "=== L2: does the apiserver accept a bogus rollingUpdate.type? ==="
echo "kubeconfig : ${KUBECONFIG:-<kubectl default>}"
echo "namespace  : $NS (dry-run only -- nothing is created)"
echo

command -v "$KUBECTL" >/dev/null || { echo "l2: $KUBECTL not found" >&2; exit 2; }
if ! "$KUBECTL" version --request-timeout=20s >/dev/null 2>&1; then
  echo "l2: cannot reach a cluster (kubectl version failed)" >&2; exit 2
fi
echo "--- cluster ---"
"$KUBECTL" version -o json 2>/dev/null | sed -n 's/.*"gitVersion": *"\([^"]*\)".*/  server gitVersion: \1/p' | tail -1
echo

if ! "$KUBECTL" get crd rolebasedgroups.workloads.x-k8s.io >/dev/null 2>&1; then
  echo "l2: CRD rolebasedgroups.workloads.x-k8s.io is not installed" >&2; exit 2
fi

# ---------------------------------------------------------------- 1. schema read
echo "--- 1. live CRD schema for spec.roles[].rolloutStrategy.rollingUpdate.type ---"
SCHEMA="$("$KUBECTL" get crd rolebasedgroups.workloads.x-k8s.io -o json \
  | python3 -c '
import json,sys
crd=json.load(sys.stdin)
for v in crd["spec"]["versions"]:
    if v["name"]!="v1alpha2": continue
    n=v["schema"]["openAPIV3Schema"]
    for k in ["properties","spec","properties","roles","items","properties",
              "rolloutStrategy","properties","rollingUpdate","properties","type"]:
        n=n.get(k)
        if n is None:
            print("MISSING"); sys.exit(0)
    print(json.dumps(n, sort_keys=True))
    sys.exit(0)
print("NO_V1ALPHA2")
')"
echo "  $SCHEMA"

HAS_ENUM=0
printf '%s' "$SCHEMA" | grep -q '"enum"' && HAS_ENUM=1
HAS_DEFAULT=0
printf '%s' "$SCHEMA" | grep -q '"default"' && HAS_DEFAULT=1
echo "  enum present   : $([ "$HAS_ENUM" -eq 1 ] && echo yes || echo NO)"
echo "  default present: $([ "$HAS_DEFAULT" -eq 1 ] && echo yes || echo NO)"
echo

# ------------------------------------------------------- 2. server-side dry runs
manifest() { # $1 = strategy value ("" => field omitted entirely)
  cat <<YAML
apiVersion: workloads.x-k8s.io/v1alpha2
kind: RoleBasedGroup
metadata:
  name: pr415-f3-dryrun-probe
  namespace: $NS
spec:
  roles:
  - name: probe
    replicas: 1
    rolloutStrategy:
      type: RollingUpdate
      rollingUpdate:
$( [ -n "$1" ] && echo "        type: $1" || echo "        maxUnavailable: 1" )
    template:
      spec:
        containers:
        - name: c
          image: busybox:latest
YAML
}

probe() { # $1 = label, $2 = strategy value
  local label="$1" val="$2" out rc
  out="$(manifest "$val" | "$KUBECTL" apply --dry-run=server -f - 2>&1)"; rc=$?
  if [ "$rc" -eq 0 ]; then
    echo "  ACCEPTED  $label"
    printf '            %s\n' "$out"
  else
    echo "  REJECTED  $label"
    printf '            %s\n' "$out" | head -4
  fi
  return $rc
}

echo "--- 2. kubectl apply --dry-run=server (full apiserver validation, no persistence) ---"
BOGUS_ACCEPTED=0
probe 'type: InPlaceIfPossible  (documented default, valid)' 'InPlaceIfPossible' || true
probe 'type: RecreatePod        (valid)'                     'RecreatePod'       || true
probe 'type: InPlaceOnly        (valid constant, no impl)'   'InPlaceOnly'       || true
probe 'type: TotallyBogusValue  (NOT a strategy at all)'     'TotallyBogusValue' && BOGUS_ACCEPTED=1
probe 'type: InPlaceIfPosible   (realistic typo, one "s")'   'InPlaceIfPosible'  || true
echo

# --------------------------------------- 3. what the stored object actually holds
echo "--- 3. is the documented default materialized by the apiserver? ---"
STORED="$(manifest "" | "$KUBECTL" apply --dry-run=server -f - -o json 2>/dev/null \
  | python3 -c '
import json,sys
try: o=json.load(sys.stdin)
except Exception: print("<dry-run returned no object>"); sys.exit(0)
ru=o["spec"]["roles"][0].get("rolloutStrategy",{}).get("rollingUpdate",{})
print(json.dumps({"type": ru.get("type","<ABSENT>")}))
')"
echo "  with rollingUpdate.type omitted, the object the apiserver returns has: $STORED"
echo "  (docs zh:121 / en:121 say the default is InPlaceIfPossible; if the value"
echo "   above is <ABSENT>, that default exists only inside the controller.)"
echo

# ------------------------------------------------------------------- 4. verdict
echo "--- verdict ---"
RC=0
if [ "$BOGUS_ACCEPTED" -eq 1 ] && [ "$HAS_ENUM" -eq 0 ]; then
  echo "CANARY HOLDS: the apiserver accepted 'TotallyBogusValue' and the schema has"
  echo "  no enum -- a misspelled strategy is silently accepted and then handled by"
  echo "  the '!= RecreatePod' branch, i.e. treated as in-place update. F3 confirmed."
  echo "RESULT: PASS (canary)"
else
  echo "CANARY FLIPPED:"
  [ "$BOGUS_ACCEPTED" -eq 0 ] && echo "  - the bogus strategy value is now REJECTED by the apiserver"
  [ "$HAS_ENUM" -eq 1 ]      && echo "  - the schema now carries an enum"
  echo "  F3 has been addressed at the schema layer. Invert this script (assert"
  echo "  rejection) and update the docs to describe schema-enforced validation."
  echo "RESULT: FAIL (canary flipped -- expected after the fix)"
  RC=1
fi
exit $RC
