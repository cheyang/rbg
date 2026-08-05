#!/usr/bin/env bash
# l2-crd-accepts-absolute-maxskew.sh — L2 (cluster) evidence for finding F1.
#
# Part of docs/verification/pr400-coordinated-policy-doc/ (review of
# https://github.com/sgl-project/rbg/pull/400).
#
# CLAIM UNDER TEST
#   PR #400's parameter table (zh:158 / en:165) says scaling.maxSkew "can be an
#   absolute number (e.g. 2) or a percentage". The L1 harness
#   (TestVerifyPR400_F1_*) proves the Go implementation REJECTS absolute values.
#   This script proves the other half of the blast radius: the CRD schema does
#   NOT reject them either, because the field is x-kubernetes-int-or-string.
#
#   So a user following the doc gets a CoordinatedPolicy that passes server-side
#   validation cleanly, and only then does the RBG controller fail its Reconcile
#   on every pass. There is no admission-time signal.
#
# POLARITY: canary (documents current behaviour).
#   Today: `--dry-run=server` ACCEPTS maxSkew: 2  -> the gap is real.
#   After a fix that adds schema validation (e.g. a CEL rule or a string-only
#   pattern), this script's ACCEPTED assertion FLIPS and the script exits 1 —
#   expected; update it to assert rejection and re-label it a contract check.
#
# SAFETY
#   Read-only with respect to the cluster: EVERY apply uses --dry-run=server,
#   which runs full admission + schema validation and then discards. No object
#   is ever persisted, so there is nothing to clean up and the script is
#   naturally idempotent. Honors $KUBECONFIG.
#
# USAGE
#   [KUBECONFIG=...] [NAMESPACE=default] bash l2-crd-accepts-absolute-maxskew.sh
#
# EXIT
#   0 = observed behaviour matches the canary baseline (absolute accepted by the
#       API server, percentage also accepted)
#   1 = behaviour changed (canary flipped) -> read the diagnosis it prints
#   2 = environment problem (no cluster / CRD not installed)
set -uo pipefail

NAMESPACE="${NAMESPACE:-default}"
PASS=0
FAIL=0

log()  { printf '%s\n' "$*"; }
head2() { printf '\n--- %s ---\n' "$*"; }

command -v kubectl >/dev/null 2>&1 || { log "SKIP/ENV: kubectl not on PATH"; exit 2; }

head2 "environment"
if ! kubectl version --request-timeout=15s >/dev/null 2>&1; then
  log "SKIP/ENV: no reachable cluster (KUBECONFIG=${KUBECONFIG:-<default>})"
  exit 2
fi
log "cluster reachable; KUBECONFIG=${KUBECONFIG:-<default>}  namespace=$NAMESPACE"

if ! kubectl get crd coordinatedpolicies.workloads.x-k8s.io >/dev/null 2>&1; then
  log "SKIP/ENV: CRD coordinatedpolicies.workloads.x-k8s.io not installed"
  exit 2
fi

# Show that the schema really is int-or-string for scaling.maxSkew — this is the
# mechanism that lets an absolute value through.
head2 "CRD schema for strategy.scaling.maxSkew"
_SCHEMA_PY="$(mktemp)"
cat > "$_SCHEMA_PY" <<'PYEOF'
import json, sys
d = json.load(sys.stdin)
for v in d["spec"]["versions"]:
    props = v["schema"]["openAPIV3Schema"]["properties"]["spec"]["properties"]
    try:
        rule = props["policies"]["items"]["properties"]["strategy"]["properties"]["scaling"]["properties"]
    except (KeyError, TypeError):
        print("version=%s: no strategy.scaling.properties in schema" % v["name"])
        continue
    ms = rule.get("maxSkew")
    prog = rule.get("progression")
    print("version=%s" % v["name"])
    print("  maxSkew     = %s" % json.dumps(ms))
    print("  progression = %s" % json.dumps(prog))
    # F1 mechanism: int-or-string is what lets an absolute value past the schema.
    if isinstance(ms, dict):
        print("  maxSkew is x-kubernetes-int-or-string? %s"
              % ms.get("x-kubernetes-int-or-string", False))
    # F6 corroboration: no `default` key on progression.
    if isinstance(prog, dict):
        print("  progression has a schema default? %s" % ("default" in prog))
        print("  progression enum = %s" % json.dumps(prog.get("enum")))
PYEOF
kubectl get crd coordinatedpolicies.workloads.x-k8s.io -o json 2>/dev/null \
  | python3 "$_SCHEMA_PY" || log "(could not introspect schema; continuing)"
rm -f "$_SCHEMA_PY"

# apply_dry_run <label> <maxSkew-yaml-literal>
# Emits ACCEPTED / REJECTED plus the server message.
apply_dry_run() {
  local label="$1" skew="$2" name="verify-pr400-f1" out rc
  out="$(kubectl apply --dry-run=server -f - 2>&1 <<EOF
apiVersion: workloads.x-k8s.io/v1alpha2
kind: CoordinatedPolicy
metadata:
  name: $name
  namespace: $NAMESPACE
spec:
  policies:
    - name: prefill-decode-scaling
      roles:
        - prefill
        - decode
      strategy:
        scaling:
          maxSkew: $skew
          progression: OrderScheduled
EOF
)"
  rc=$?
  if [ $rc -eq 0 ]; then
    log "  [$label] maxSkew: $skew  -> ACCEPTED by API server (dry-run)"
    log "           server said: $out"
    return 0
  fi
  log "  [$label] maxSkew: $skew  -> REJECTED by API server"
  log "           server said: $out"
  return 1
}

head2 "F1: does the API server reject an ABSOLUTE maxSkew?"
log "doc claims absolute is valid; L1 proves the controller cannot parse it."
if apply_dry_run "absolute-2" "2"; then
  log "CANARY BASELINE HOLDS: the schema does NOT catch the absolute value."
  log "  => a doc-following user gets a policy that applies cleanly and then"
  log "     breaks the RBG controller's Reconcile with:"
  log "     'failed to parse maxSkew: percentage string must end with %'"
  PASS=$((PASS+1))
else
  log "CANARY FLIPPED: the API server now rejects an absolute maxSkew."
  log "  => schema-level validation appears to have been added. Update this"
  log "     script to assert rejection (contract) and revisit F1's polarity."
  FAIL=$((FAIL+1))
fi

head2 "control: a PERCENTAGE maxSkew (the form that actually works)"
if apply_dry_run "percent-10" '"10%"'; then
  log "as expected: percentage form is accepted at both schema and code level."
  PASS=$((PASS+1))
else
  log "UNEXPECTED: the percentage form was rejected — environment or schema drift."
  FAIL=$((FAIL+1))
fi

head2 "corroboration: quoted absolute string (also doc-plausible, also unparseable)"
# Not a separate finding; recorded because it is the same failure mode.
apply_dry_run "string-2" '"2"' || true

head2 "summary"
log "checks passed: $PASS   failed: $FAIL"
log "NOTE: every apply above used --dry-run=server; nothing was persisted."
if [ "$FAIL" -gt 0 ]; then
  log "RESULT: FAIL (canary flipped or environment drift) — see diagnosis above."
  exit 1
fi
log "RESULT: PASS — F1's schema gap reproduced at the cluster layer."
exit 0
