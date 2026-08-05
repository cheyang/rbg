#!/usr/bin/env bash
# l2-live-service-and-configmap.sh — Layer 2/3 evidence for topic pr402-port-alloc-doc.
#
# Part of the review-finding-verifier harness for PR #402
# (doc/best-practice/{zh,en}/05-port-allocation-and-service-discovery{,-guide}.md).
#
# What it does, in order:
#   [A] Preflight: report whether the deployed controller has --enable-port-allocator
#       and whether the controller image matches the CRD schema. Both are recorded,
#       neither aborts the run.
#   [B] L2 static: `kubectl apply --dry-run=server` every YAML example extracted from
#       the four doc files under review. Server-side dry-run exercises real admission
#       and CRD schema validation without creating anything.
#   [C] L2 live (optional, --live): create ONE minimal RBG in a dedicated namespace
#       and record the generated headless Service name (F5) and the discovery
#       ConfigMap payload key order (F4). Namespace is deleted on exit.
#   [D] L3 pod-level (env injection for F1/F3): attempted only under --live, and
#       expected to be BLOCKED on this sandbox — see the env-limitations note below.
#       The script detects the failure mode and SKIPS rather than hanging.
#
# Usage:
#   ./l2-live-service-and-configmap.sh                 # preflight + dry-run only
#   ./l2-live-service-and-configmap.sh --live          # also create/observe in a namespace
#   NS=my-ns ./l2-live-service-and-configmap.sh --live # override the namespace
#
# Env:
#   KUBECONFIG   honoured as usual (defaults to ~/.kube/config)
#   REPO         repo root (default: auto-detected from this script's location)
#   NS           namespace for the live step (default: pr402-verify)
#   RESULTS      where raw output goes (default: <harness dir>/results)
#
# Exit code: 0 if the dry-run stage found no unexpected rejections. A SKIPPED live
# stage is not a failure — the README records why.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
HARNESS_DIR="$(dirname "$SCRIPT_DIR")"
REPO="${REPO:-$(cd "$HARNESS_DIR/../../.." && pwd)}"
RESULTS="${RESULTS:-$HARNESS_DIR/results}"
NS="${NS:-pr402-verify}"
LIVE=0
[ "${1:-}" = "--live" ] && LIVE=1

mkdir -p "$RESULTS"
LOG="$RESULTS/l2-live.txt"
: > "$LOG"
log() { echo "$@" | tee -a "$LOG"; }

log "=============================================================="
log "L2/L3 live evidence — topic pr402-port-alloc-doc"
log "repo:      $REPO"
log "reviewed:  $(git -C "$REPO" rev-parse HEAD 2>/dev/null || echo '<not a git repo>')"
log "namespace: $NS   live-stage: $([ $LIVE -eq 1 ] && echo yes || echo 'no (pass --live)')"
log "when:      $(date -u +%Y-%m-%dT%H:%M:%SZ)"
log "=============================================================="
log ""

command -v kubectl >/dev/null || { log "FATAL: kubectl not on PATH"; exit 2; }
kubectl version --request-timeout=20s >/dev/null 2>&1 || { log "FATAL: cannot reach the cluster"; exit 2; }

########################################################################
# [A] Preflight — record the two environment limitations up front.
########################################################################
log "=== [A] Preflight ==============================================="

CTRL_JSON="$(kubectl get deploy -A -o json 2>/dev/null)"
CTRL_INFO="$(printf '%s' "$CTRL_JSON" | python3 -c '
import json,sys
d=json.load(sys.stdin)
for i in d.get("items",[]):
    n=i["metadata"]["name"]
    if "rbg" not in n: continue
    for c in i["spec"]["template"]["spec"]["containers"]:
        print("%s/%s image=%s" % (i["metadata"]["namespace"], n, c.get("image")))
        print("  args=%s" % (c.get("args"),))
' 2>/dev/null)"
log "controller deployment(s):"
log "$CTRL_INFO"

if printf '%s' "$CTRL_INFO" | grep -q 'enable-port-allocator'; then
  PA_ENABLED=yes
  log "PREFLIGHT: --enable-port-allocator IS set."
else
  PA_ENABLED=no
  log "PREFLIGHT LIMITATION 1: the deployed controller does NOT pass"
  log "  --enable-port-allocator. pkg/port-allocator is therefore INERT on this"
  log "  cluster (port_allocator.go: AllocateBatch returns ErrPortAllocatorDisabled"
  log "  when !IsEnabled()). Findings F1 and F2 CANNOT be exercised live here; they"
  log "  are proven deterministically at Layer 1 instead."
fi

# Does the RoleInstance CRD's spec.restartPolicy expect an object or a string?
RP_TYPE="$(kubectl get crd roleinstances.workloads.x-k8s.io -o json 2>/dev/null | python3 -c '
import json,sys
try:
    d=json.load(sys.stdin)
except Exception:
    print("unknown"); raise SystemExit
for v in d["spec"]["versions"]:
    p=v["schema"]["openAPIV3Schema"]["properties"]["spec"]["properties"].get("restartPolicy")
    if p is None: print("absent")
    elif "properties" in p: print("object")
    else: print(p.get("type","unknown"))
    break
' 2>/dev/null)"
log ""
log "RoleInstance CRD spec.restartPolicy schema type: ${RP_TYPE:-unknown}"
if [ "$RP_TYPE" = "object" ]; then
  log "PREFLIGHT LIMITATION 2 (expected on this sandbox): the CRD expects"
  log "  spec.restartPolicy to be an OBJECT, while the deployed controller image"
  log "  writes a STRING. Any RoleInstance the controller creates is rejected with"
  log "    spec.restartPolicy: Invalid value: \"string\": ... must be of type object"
  log "  so NO PODS are ever created. The L3 pod-level checks (env-var injection"
  log "  for F1/F3) are therefore expected to be SKIPPED. RoleInstanceSet, Service"
  log "  and ConfigMap ARE still generated, which is what the L2 stage observes."
fi
log ""

########################################################################
# [B] L2 static: server-side dry-run of every YAML block in the doc.
########################################################################
log "=== [B] L2 static: --dry-run=server on the doc's YAML examples ==="

DOCS=(
  "doc/best-practice/zh/05-port-allocation-and-service-discovery.md"
  "doc/best-practice/en/05-port-allocation-and-service-discovery.md"
  "doc/best-practice/zh/05-port-allocation-and-service-discovery-guide.md"
  "doc/best-practice/en/05-port-allocation-and-service-discovery-guide.md"
)

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

# Extract fenced ```yaml blocks that look like a Kubernetes object (have apiVersion+kind).
for d in "${DOCS[@]}"; do
  [ -f "$REPO/$d" ] || { log "  (missing doc: $d)"; continue; }
  python3 - "$REPO/$d" "$WORK" "$(basename "$d" .md)" <<'PY'
import re,sys,os
path,work,tag=sys.argv[1],sys.argv[2],sys.argv[3]
text=open(path,encoding="utf-8").read()
blocks=re.findall(r"```ya?ml\n(.*?)```", text, re.S)
n=0
for b in blocks:
    if "apiVersion:" in b and "kind:" in b:
        n+=1
        with open(os.path.join(work,"%s-%02d.yaml"%(tag,n)),"w",encoding="utf-8") as f:
            f.write(b)
print("  %s: %d k8s-object YAML block(s) extracted (of %d yaml blocks)"%(os.path.basename(path),n,len(blocks)))
PY
done | tee -a "$LOG"

DRY_OK=0; DRY_FAIL=0
log ""
log "  dry-run results:"
shopt -s nullglob
for f in "$WORK"/*.yaml; do
  name="$(basename "$f")"
  # Non-default namespaces referenced by examples are not created; force default.
  out="$(kubectl apply --dry-run=server -n default -f "$f" 2>&1)"
  rc=$?
  if [ $rc -eq 0 ]; then
    DRY_OK=$((DRY_OK+1))
    log "    OK    $name"
    printf '%s\n' "$out" | sed 's/^/            /' >> "$LOG"
  else
    DRY_FAIL=$((DRY_FAIL+1))
    log "    FAIL  $name"
    printf '%s\n' "$out" | sed 's/^/            /' | tee -a "$LOG"
  fi
done
shopt -u nullglob
log ""
log "  dry-run summary: $DRY_OK accepted, $DRY_FAIL rejected"
log ""

########################################################################
# [C]/[D] Live stage.
########################################################################
if [ $LIVE -ne 1 ]; then
  log "=== [C] live stage SKIPPED (re-run with --live) ================="
  log ""
  log "Done. Raw log: $LOG"
  [ "$DRY_FAIL" -eq 0 ] && exit 0 || exit 1
fi

log "=== [C] L2 live: minimal RBG in namespace $NS =================="
cleanup_ns() {
  log ""
  log "  cleanup: deleting namespace $NS"
  kubectl delete ns "$NS" --wait=false --ignore-not-found >>"$LOG" 2>&1 || true
}
trap 'cleanup_ns; rm -rf "$WORK"' EXIT

kubectl create ns "$NS" >>"$LOG" 2>&1 || log "  (namespace $NS already exists)"

# Minimal two-role RBG mirroring the guide's pd-inference example, so the generated
# Service names (F5) and ConfigMap key order (F4) can be observed directly.
cat > "$WORK/rbg.yaml" <<YAML
apiVersion: workloads.x-k8s.io/v1alpha2
kind: RoleBasedGroup
metadata:
  name: pd-inference
  namespace: $NS
spec:
  roles:
    - name: prefill
      replicas: 2
      servicePorts:
        - name: http
          port: 8000
          targetPort: 8000
      standalonePattern:
        template:
          spec:
            containers:
              - name: server
                image: registry.k8s.io/pause:3.9
                ports:
                  - containerPort: 8000
    - name: decode
      replicas: 2
      servicePorts:
        - name: http
          port: 8000
          targetPort: 8000
      standalonePattern:
        template:
          spec:
            containers:
              - name: server
                image: registry.k8s.io/pause:3.9
                ports:
                  - containerPort: 8000
YAML

log "  applying the minimal RBG:"
kubectl apply -f "$WORK/rbg.yaml" 2>&1 | sed 's/^/    /' | tee -a "$LOG"

log ""
log "  waiting up to 90s for the controller to generate child objects..."
for i in $(seq 1 30); do
  svc_count="$(kubectl get svc -n "$NS" --no-headers 2>/dev/null | wc -l | tr -d ' ')"
  cm_count="$(kubectl get cm -n "$NS" --no-headers 2>/dev/null | grep -c . || true)"
  [ "${svc_count:-0}" -ge 2 ] && break
  sleep 3
done

log ""
log "  --- F5 evidence: generated Services ---"
kubectl get svc -n "$NS" -o custom-columns='NAME:.metadata.name,CLUSTERIP:.spec.clusterIP,PORTS:.spec.ports[*].port' 2>&1 | sed 's/^/    /' | tee -a "$LOG"
log "  (doc zh:32-38 claims the name is s-{rbgName}-{roleName}; with no legacy"
log "   Service present the s- form is what we expect to see here. The legacy-name"
log "   branch is proven at Layer 1 — see pkg/utils/zz_verify_pr402_svcname_test.go.)"

log ""
log "  --- RoleInstanceSet / RoleInstance objects ---"
kubectl get roleinstanceset,roleinstance -n "$NS" 2>&1 | sed 's/^/    /' | tee -a "$LOG"

log ""
log "  --- F4 evidence: discovery ConfigMap payload (key order) ---"
CM="$(kubectl get cm -n "$NS" -o name 2>/dev/null | grep -v kube-root-ca | head -1)"
if [ -n "$CM" ]; then
  log "    configmap: $CM"
  kubectl get "$CM" -n "$NS" -o jsonpath='{.data}' 2>/dev/null | python3 -c '
import json,sys
raw=sys.stdin.read().strip()
try:
    data=json.loads(raw) if raw.startswith("{") else {}
except Exception:
    data={}
for k,v in data.items():
    print("    --- key: %s ---"%k)
    for line in v.splitlines():
        print("      "+line)
' 2>&1 | tee -a "$LOG"
  log "  (F4: expect keys in ALPHABETICAL order — group: name/roles/size, per role:"
  log "   instances before size, and the roles map sorted decode-before-prefill.)"
else
  log "    NO ConfigMap generated. The controller may not have reconciled far enough"
  log "    (see PREFLIGHT LIMITATION 2). F4 is proven at Layer 1 instead —"
  log "    pkg/discovery/zz_verify_pr402_configmap_keyorder_test.go."
fi

########################################################################
log ""
log "=== [D] L3 pod-level (env injection for F1/F3) =================="
POD_COUNT="$(kubectl get pod -n "$NS" --no-headers 2>/dev/null | grep -c . || true)"
log "  pods in $NS: ${POD_COUNT:-0}"
if [ "${POD_COUNT:-0}" -eq 0 ]; then
  log ""
  log "  L3 SKIPPED — no Pods were created."
  log "  Diagnostic (RoleInstanceSet / RBG events, filtered):"
  kubectl get events -n "$NS" --sort-by=.lastTimestamp 2>/dev/null \
    | grep -iE 'restartPolicy|Invalid value|must be of type|Failed|Error' \
    | tail -20 | sed 's/^/    /' | tee -a "$LOG"
  log ""
  log "  Controller error messages for this namespace (stack traces stripped):"
  CTRL_ERR_FILTER="$WORK/ctrl_err.py"
  cat > "$CTRL_ERR_FILTER" <<'PYFILTER'
import json, sys
seen = set()
for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    d = None
    if line.startswith("{"):
        try:
            d = json.loads(line)
        except ValueError:
            d = None
    if d is None:
        # klog-style line, not JSON: print it verbatim (deduped).
        if line not in seen:
            seen.add(line)
            print("    " + line[:300])
        continue
    if d.get("level") not in ("ERROR", "WARN"):
        continue
    err = (d.get("error") or "").replace("\n", "; ")
    msg = "%s | %s" % (d.get("message", ""), err)
    if msg in seen:
        continue
    seen.add(msg)
    print("    " + msg[:300])
PYFILTER
  for p in $(kubectl get pod -n rbg-system -o name 2>/dev/null | head -2); do
    kubectl logs -n rbg-system "$p" --tail=400 2>/dev/null \
      | grep -E "$NS|restartPolicy|must be of type object" \
      | python3 "$CTRL_ERR_FILTER" 2>/dev/null | head -12 | tee -a "$LOG"
  done
  log ""
  log "  This is PREFLIGHT LIMITATION 2 in action: the deployed controller image and"
  log "  the installed CRD disagree on spec.restartPolicy, so RoleInstance creation is"
  log "  rejected and no Pod is ever produced. Env-var injection (F1 references, F3"
  log "  FQDN) cannot be observed live on this sandbox. Both are proven at Layer 1:"
  log "    F1 -> pkg/port-allocator/zz_verify_pr402_doc_claims_test.go"
  log "    F3 -> pkg/component-discovery/zz_verify_pr402_fqdn_test.go"
  log "  NOTE: even with a matched controller/CRD pair, F1 and F2 would still need"
  log "  --enable-port-allocator (PREFLIGHT LIMITATION 1)."
else
  log ""
  log "  Pods exist — dumping env vars relevant to F1/F3:"
  for p in $(kubectl get pod -n "$NS" -o name 2>/dev/null); do
    log "    --- $p ---"
    kubectl get "$p" -n "$NS" -o jsonpath='{range .spec.containers[*]}{range .env[*]}      {.name}={.value}{"\n"}{end}{end}' 2>&1 | tee -a "$LOG"
  done
fi

log ""
log "Done. Raw log: $LOG"
[ "$DRY_FAIL" -eq 0 ] && exit 0 || exit 1
