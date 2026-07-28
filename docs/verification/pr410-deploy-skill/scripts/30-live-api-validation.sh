#!/usr/bin/env bash
# L3 live — ask the real API server / real controller about two doc claims.
#
# F4  CLAUDE.md:120-123 lists `RecreateRBGOnPodRestart` as a valid restartPolicy and
#     presents restartPolicy as a role-level field. Polarity: CONTRACT — a FAIL line
#     means CLAUDE.md is wrong.
# F8  yaml-rules.md:7 says templateRef.patch must always be set (even `{}`). Copilot's
#     review comment says it is optional. Polarity: CONTRACT — asserts the PR is right.
#
# Everything is --dry-run=server or scoped to $VERIFY_NS. Nothing persists except the
# short-lived RBG in F8, deleted at the end.
set -uo pipefail
NS="${VERIFY_NS:-rbg-verify-pr410}"
IMG="${VERIFY_IMAGE:-anolis-registry.cn-zhangjiakou.cr.aliyuncs.com/openanolis/nginx:1.14.1-8.6}"
fail=0
ok()  { echo "PASS  $*"; }
bad() { echo "FAIL  $*"; fail=$((fail+1)); }

echo "=== F4a — the repo's own example uses RecreateRBGOnPodRestart (examples/deprecated/v1alpha1/basics/restart-policy.yaml) ==="
out=$(kubectl apply -n "$NS" --dry-run=server -f examples/deprecated/v1alpha1/basics/restart-policy.yaml 2>&1)
echo "$out" | head -5
if echo "$out" | grep -qi "unsupported value\|Unsupported value\|invalid"; then
  bad "the API server REJECTS RecreateRBGOnPodRestart — CLAUDE.md:122 documents an unusable value (and examples/deprecated/v1alpha1/basics/restart-policy.yaml is broken)"
else
  ok "API server accepted RecreateRBGOnPodRestart"
fi

echo
echo "=== F4b — same value on a v1alpha2 leaderWorkerPattern ==="
out=$(cat <<YAML | kubectl apply -n "$NS" --dry-run=server -f - 2>&1
apiVersion: workloads.x-k8s.io/v1alpha2
kind: RoleBasedGroup
metadata:
  name: f4-restartpolicy
spec:
  roles:
    - name: backend
      replicas: 1
      leaderWorkerPattern:
        size: 2
        restartPolicy: RecreateRBGOnPodRestart
        template:
          spec:
            containers:
              - name: engine
                image: $IMG
YAML
)
echo "$out" | head -5
if echo "$out" | grep -qi "unsupported value\|invalid"; then
  bad "v1alpha2 also rejects RecreateRBGOnPodRestart"
else
  ok "v1alpha2 accepted RecreateRBGOnPodRestart"
fi

echo
echo "=== F4c — CLAUDE.md presents restartPolicy as a role-level field; does v1alpha2 have one? ==="
kubectl get crd rolebasedgroups.workloads.x-k8s.io -o json \
  | jq -r '.spec.versions[]|select(.name=="v1alpha2")|.schema.openAPIV3Schema.properties.spec.properties.roles.items.properties|keys|join(", ")'
if kubectl get crd rolebasedgroups.workloads.x-k8s.io -o json \
  | jq -e '.spec.versions[]|select(.name=="v1alpha2")|.schema.openAPIV3Schema.properties.spec.properties.roles.items.properties|has("restartPolicy")' >/dev/null; then
  ok "roles[].restartPolicy exists in v1alpha2"
else
  bad "v1alpha2 has NO roles[].restartPolicy — it lives under the pattern (leaderWorkerPattern / customComponentsPattern), which CLAUDE.md does not say"
fi

echo
echo "=== F8 — templateRef without patch ==="
kubectl -n "$NS" delete rbg f8-no-patch --ignore-not-found >/dev/null 2>&1
out=$(cat <<YAML | kubectl apply -n "$NS" -f - 2>&1
apiVersion: workloads.x-k8s.io/v1alpha2
kind: RoleBasedGroup
metadata:
  name: f8-no-patch
spec:
  roleTemplates:
    - name: engine-base
      template:
        spec:
          containers:
            - name: engine
              image: $IMG
  roles:
    - name: prefill
      replicas: 1
      standalonePattern:
        templateRef:
          name: engine-base
YAML
)
echo "apply said: $out"
echo "--- the check lives in the controller (rolebasedgroup_controller.go:303), not in admission,"
echo "--- so look at the RBG conditions / events rather than the apply exit code:"
sleep 12
kubectl -n "$NS" get rbg f8-no-patch -o json 2>/dev/null \
  | jq -c '[.status.conditions[]?|{t:.type,s:.status,r:.reason,m:(.message//""|.[0:160])}]'
ev=$(kubectl -n "$NS" get events --field-selector involvedObject.name=f8-no-patch \
       -o jsonpath='{range .items[*]}{.reason}: {.message}{"\n"}{end}' 2>/dev/null)
echo "$ev" | grep -i "patch" | head -3
if echo "$ev" | grep -qiE "template(Ref[.])?[Pp]atch.*required"; then
  ok "controller rejects templateRef without patch — PR #410 rule 3 is CORRECT, the review comment claiming patch is optional is wrong"
else
  bad "no 'templateRef.patch is required' error surfaced; PR #410 rule 3 unproven here"
fi
kubectl -n "$NS" delete rbg f8-no-patch --ignore-not-found >/dev/null 2>&1

echo
echo "=== summary: $fail failing claim(s) ==="
exit $([ $fail -eq 0 ] && echo 0 || echo 1)
