#!/usr/bin/env bash
# Re-verify PR #436. Resolves the PR head from manifest.pr and the delta start from .last-reviewed.
# Layers: unit, envtest, live-kubectl (live only if KUBECONFIG points at a writable cluster).
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TOP="$(cd "$HERE/.." && pwd)"
MANIFEST="$TOP/verify-manifest.json"
LAST_REVIEWED="$TOP/.last-reviewed"
PR=$(python3 -c "import json,sys;print(json.load(open('$MANIFEST'))['pr'])")
HEAD_SHA=$(python3 -c "import json,sys;print(json.load(open('$MANIFEST'))['headSha'])")
echo "[re-verify] PR=$PR  head=$HEAD_SHA  last-reviewed=$(cat "$LAST_REVIEWED" 2>/dev/null || echo none)"

# Resolve repo path (clone/refresh the PR ref).
REPO="${REPO_PATH:-/root/.cache/github-code-review/repos/sgl-project/rbg}"
if [ ! -d "$REPO/.git" ]; then
  git clone https://github.com/sgl-project/rbg.git "$REPO"
fi
cd "$REPO"
git fetch --quiet origin "pull/436/head:pr/436" || true
git checkout --quiet "$HEAD_SHA"

# --- Layer: unit ---
echo "=== unit: go test ./api/workloads/v1alpha2 ./pkg/reconciler ==="
go test ./api/workloads/v1alpha2 ./pkg/reconciler -count=1

# --- Layer: envtest (real apiserver, PR-head controller in-process) ---
export PATH="$PATH:$(go env GOPATH)/bin"
if [ -z "${KUBEBUILDER_ASSETS:-}" ]; then
  echo "[re-verify] KUBEBUILDER_ASSETS unset; fetching 1.31.x via setup-envtest"
  KUBEBUILDER_ASSETS="$(setup-envtest use 1.31.x --bin-dir /tmp/envtest-bin -p path)"
fi
export KUBEBUILDER_ASSETS
echo "=== envtest: 3 focused cases ==="
go test ./test/envtest/testcase/rbg -run TestRBGController \
  -ginkgo.v -ginkgo.focus='templateRef without patch|invalid update type|invalid update strategy type' \
  -timeout 15m

# --- Layer: live-kubectl (only if a cluster is reachable) ---
if kubectl get nodes >/dev/null 2>&1; then
  echo "=== live: F3 enum rejection + F1 default-omitted (namespace pr436-reverify) ==="
  NS=pr436-reverify
  kubectl create namespace "$NS" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  # apply PR CRDs (enum) from deploy/kubectl/manifests.yaml
  python3 - <<'PY'
import yaml
targets={'rolebasedgroups','rolebasedgroupsets','roleinstancesets'}
with open('deploy/kubectl/manifests.yaml') as f:
    for doc in yaml.safe_load_all(f):
        if doc and doc.get('kind')=='CustomResourceDefinition' and doc['metadata']['name'].split('.')[0] in targets:
            yaml.safe_dump_all([doc], open('/tmp/_crd.yaml','w'))
            import subprocess; subprocess.run(['kubectl','apply','--server-side','--force-conflicts','-f','/tmp/_crd.yaml'])
PY
  # F1: omit type -> admit
  kubectl apply -f - <<'YAML' >/dev/null && echo "[F1] PASS omit type admitted"
apiVersion: workloads.x-k8s.io/v1alpha2
kind: RoleBasedGroup
metadata: {name: r1, namespace: pr436-reverify}
spec:
  roles:
  - {name: w, replicas: 1, rolloutStrategy: {type: RollingUpdate, rollingUpdate: {maxUnavailable: 1}}, pattern: {standalonePattern: {template: {spec: {containers: [{name: c, image: nginx:1.25}]}}}}}
YAML
  # F3: invalid type -> reject
  if kubectl apply -f - <<'YAML' 2>/dev/null
apiVersion: workloads.x-k8s.io/v1alpha2
kind: RoleBasedGroup
metadata: {name: r2, namespace: pr436-reverify}
spec:
  roles:
  - {name: w, replicas: 1, rolloutStrategy: {type: RollingUpdate, rollingUpdate: {type: NotARealStrategy, maxUnavailable: 1}}, pattern: {standalonePattern: {template: {spec: {containers: [{name: c, image: nginx:1.25}]}}}}}
YAML
  then echo "[F3] FAIL invalid type was admitted"; else echo "[F3] PASS invalid type rejected"; fi
  kubectl delete namespace "$NS" --timeout=60s >/dev/null
  echo "[re-verify] live done — CRD restore is left to the operator (re-apply pre-PR CRDs)"
else
  echo "=== live: skipped (no cluster / KUBECONFIG) ==="
fi

# advance last-reviewed
echo "$HEAD_SHA" > "$LAST_REVIEWED"
echo "[re-verify] complete; .last-reviewed -> $HEAD_SHA"
