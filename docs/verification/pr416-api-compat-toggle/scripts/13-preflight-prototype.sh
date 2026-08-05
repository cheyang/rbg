#!/usr/bin/env bash
# Round-3 prototype: the minimal pre-install/pre-upgrade preflight Job proposed for
# R3-F22 (and, with the storedVersions arm, F4). Underpins UPGRADE-GUARD-DESIGN.md.
#
# Verifies four things that decide whether the design is workable:
#   1. enabled=true (default) renders NO preflight objects at all -- no cost on the
#      default path.
#   2. hook ordering: the preflight Job completes BEFORE the crd-upgrade-equivalent
#      Job starts (weight -6 vs -4), so it inspects state the CRD upgrade has not
#      yet rewritten.
#   3. restartPolicy: Never + backoffLimit: 0 fails FAST and the check's own message
#      reaches the operator -- unlike crd-upgrade's OnFailure, which would restart a
#      deliberately-failing check forever and surface only a Helm timeout.
#   4. the check passes on a clean cluster and bites on a v1alpha2 object carrying a
#      deprecated workload annotation.
#
# SAFETY: throwaway namespace, throwaway release, read-only ServiceAccount. The
# offender case is driven from a FIXTURE, deliberately NOT by creating a real
# RoleBasedGroup -- the live rbgs controller on this cluster watches cluster-wide and
# would reconcile one into real StatefulSets. Namespace removed by an EXIT trap.
set -uo pipefail

NS=${NS:-pr416-preflight-test}
IMG=${IMG:-bitnami/kubectl:latest}
D=/tmp/pr416-preflight

cleanup() {
  echo
  echo "=== CLEANUP ==="
  helm uninstall pf -n "$NS" >/dev/null 2>&1
  kubectl delete clusterrole  pf-preflight >/dev/null 2>&1
  kubectl delete clusterrolebinding pf-preflight >/dev/null 2>&1
  kubectl delete ns "$NS" --wait=false >/dev/null 2>&1
  echo "  removed release pf, namespace $NS, and the preflight ClusterRole/Binding"
}
trap cleanup EXIT

mkdir -p $D/templates && cd $D
printf 'apiVersion: v2\nname: pf\nversion: 0.8.0-alpha.3\n' > Chart.yaml
cat > values.yaml <<'YAML'
controller:
  deprecatedWorkloadTypes:
    enabled: true
preflight:
  enabled: true
  # fixture: when set, the check reads this JSON instead of the live cluster.
  # Used only to exercise the offender path without creating a real RoleBasedGroup.
  fixture: ""
image: bitnami/kubectl:latest
YAML

cat > templates/preflight.yaml <<'YAML'
{{- $d := .Values.controller.deprecatedWorkloadTypes | default dict }}
{{- $deprecatedEnabled := or (not (hasKey $d "enabled")) $d.enabled }}
{{- if and .Values.preflight.enabled (not $deprecatedEnabled) }}
apiVersion: v1
kind: ServiceAccount
metadata:
  name: pf-preflight
  namespace: {{ .Release.Namespace }}
  annotations:
    "helm.sh/hook": pre-install,pre-upgrade
    "helm.sh/hook-weight": "-7"
    "helm.sh/hook-delete-policy": hook-succeeded,hook-failed,before-hook-creation
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: pf-preflight
  annotations:
    "helm.sh/hook": pre-install,pre-upgrade
    "helm.sh/hook-weight": "-7"
    "helm.sh/hook-delete-policy": hook-succeeded,hook-failed,before-hook-creation
rules:
  # read-only, and narrower than crd-upgrade's create/update/patch
  - apiGroups: ["workloads.x-k8s.io"]
    resources: ["rolebasedgroups", "rolebasedgroupsets"]
    verbs: ["get", "list"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: pf-preflight
  annotations:
    "helm.sh/hook": pre-install,pre-upgrade
    "helm.sh/hook-weight": "-7"
    "helm.sh/hook-delete-policy": hook-succeeded,hook-failed,before-hook-creation
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: ClusterRole, name: pf-preflight}
subjects:
  - kind: ServiceAccount
    name: pf-preflight
    namespace: {{ .Release.Namespace }}
---
apiVersion: batch/v1
kind: Job
metadata:
  name: pf-preflight
  namespace: {{ .Release.Namespace }}
  annotations:
    "helm.sh/hook": pre-install,pre-upgrade
    # must be lower than the crd-upgrade Job's -4, or the CRDs are already rewritten
    "helm.sh/hook-weight": "-6"
    # NOTE: no hook-failed -- a failed check's pod must survive for diagnosis
    # PROTOTYPE ONLY: production should add hook-succeeded here. Kept off so the
    # succeeded Job survives long enough to read its log and compare timestamps.
    "helm.sh/hook-delete-policy": before-hook-creation
spec:
  # NOT OnFailure: this check is EXPECTED to fail, and OnFailure would restart it
  # forever, hiding the message behind a Helm timeout
  backoffLimit: 0
  template:
    spec:
      restartPolicy: Never
      serviceAccountName: pf-preflight
      containers:
        - name: preflight
          image: {{ .Values.image }}
          command: ["/bin/sh", "-c"]
          args:
            - |
              set -u
              echo "preflight start $(date -u +%H:%M:%S.%N)"
              K=rbg.workloads.x-k8s.io/role-workload-type
              FIXTURE='{{ .Values.preflight.fixture }}'
              if [ -n "$FIXTURE" ]; then
                echo "(reading fixture instead of the live cluster)"
                echo "$FIXTURE" > /tmp/in.json
              else
                kubectl get rolebasedgroups.workloads.x-k8s.io,rolebasedgroupsets.workloads.x-k8s.io \
                  -A -o json > /tmp/in.json 2>/dev/null || echo '{"items":[]}' > /tmp/in.json
              fi
              OFF=$(jq -r --arg k "$K" '
                [ .items[]
                  | .metadata as $m
                  | ( (.spec.roles // []), (.spec.groupTemplate.spec.roles // []) )[]
                  | select( (.annotations[$k] // "workloads.x-k8s.io/v1alpha2/RoleInstanceSet")
                            | . == "apps/v1/Deployment"
                              or . == "apps/v1/StatefulSet"
                              or . == "leaderworkerset.x-k8s.io/v1/LeaderWorkerSet" )
                  | "  \($m.namespace)/\($m.name)  role=\(.name)  type=\(.annotations[$k])" ]
                | .[]' /tmp/in.json)
              if [ -n "$OFF" ]; then
                echo "REFUSING INSTALL: controller.deprecatedWorkloadTypes.enabled=false, but these objects already use a deprecated workload type:"
                echo "$OFF"
                echo "They would become unreconcilable: no RBAC is granted for those types and the webhook would refuse every write."
                echo "Keep enabled=true on this cluster until a migration path to RoleInstanceSet ships."
                exit 1
              fi
              echo "preflight OK: no object uses a deprecated workload type"
              echo "preflight end   $(date -u +%H:%M:%S.%N)"
{{- end }}
---
# stand-in for the real crd-upgrade Job, purely to observe hook ordering
apiVersion: batch/v1
kind: Job
metadata:
  name: pf-crd-upgrade
  namespace: {{ .Release.Namespace }}
  annotations:
    "helm.sh/hook": pre-install,pre-upgrade
    "helm.sh/hook-weight": "-4"
    "helm.sh/hook-delete-policy": before-hook-creation
spec:
  backoffLimit: 0
  template:
    spec:
      restartPolicy: Never
      containers:
        - name: crd-upgrade
          image: busybox
          command: ["sh","-c","echo crd-upgrade start $(date -u +%H:%M:%S.%N)"]
YAML
printf 'apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: pf-cm\ndata:\n  a: "1"\n' > templates/cm.yaml

FIXTURE='{"items":[{"apiVersion":"workloads.x-k8s.io/v1alpha2","metadata":{"namespace":"ns1","name":"legacy-rbg"},"spec":{"roles":[{"name":"worker","annotations":{"rbg.workloads.x-k8s.io/role-workload-type":"apps/v1/StatefulSet"}}]}}]}'

{ printf 'preflight:\n  fixture: |-\n    '; printf '%s' "$FIXTURE"; printf '\n'; } > "$D/fixture-values.yaml"

kubectl create ns "$NS" >/dev/null 2>&1
r() { printf '\n--- %s\n' "$1"; }

echo "############ 1) default (enabled=true): the preflight must not be rendered at all"
n=$(helm template pf . 2>/dev/null | grep -c 'name: pf-preflight' || true)
echo "   objects named pf-preflight rendered: $n   (expected 0 -- no cost on the default path)"
helm template pf . 2>/dev/null | grep -E '^kind:' | sort | uniq -c | sed 's/^/     /'

echo
echo "############ 2) enabled=false on a CLEAN cluster: install proceeds, ordering observed"
helm install pf . -n "$NS" --set controller.deprecatedWorkloadTypes.enabled=false --timeout 180s 2>&1 \
  | grep -E 'STATUS|Error' | head -3 | sed 's/^/   /'
r "preflight pod log"
kubectl -n "$NS" logs job/pf-preflight 2>/dev/null | sed 's/^/     /'
r "did preflight really finish before crd-upgrade started?"
kubectl -n "$NS" logs job/pf-crd-upgrade 2>/dev/null | sed 's/^/     /'
echo "     ^ compare timestamps: preflight end must precede crd-upgrade start"

echo
echo "############ 3) enabled=false with an OFFENDER: must fail fast with the check's own message"
echo "   (fixture: a v1alpha2 object whose role annotation is apps/v1/StatefulSet --"
echo "    exactly the case status.storedVersions would report as clean)"
helm uninstall pf -n "$NS" >/dev/null 2>&1
# hook resources are NOT removed by uninstall when the delete policy omits
# hook-succeeded, so clear both Jobs or scenario 2's crd-upgrade Job lingers and
# the ordering assertion below reads a stale object.
kubectl -n "$NS" delete job pf-preflight pf-crd-upgrade --ignore-not-found >/dev/null 2>&1
start=$(date +%s)
out=$(helm install pf . -n "$NS" --set controller.deprecatedWorkloadTypes.enabled=false \
        -f "$D/fixture-values.yaml" --timeout 180s 2>&1)
took=$(( $(date +%s) - start ))
echo "   helm exited after ${took}s"
echo "$out" | grep -E 'Error' | head -2 | sed 's/^/   /'
r "the failed pod survived (no hook-failed in the delete policy), and its message is:"
pod=$(kubectl -n "$NS" get pods -l job-name=pf-preflight --no-headers 2>/dev/null | awk '{print $1}' | head -1)
kubectl -n "$NS" logs "$pod" 2>/dev/null | sed 's/^/     /'
r "was the crd-upgrade stand-in ever run?"
if kubectl -n "$NS" get job pf-crd-upgrade >/dev/null 2>&1; then
  echo "     YES -- ordering is broken"
else
  echo "     NO -- refused before the CRD-upgrade stage, as intended"
fi

echo
echo "RESULT: see the four checks above."
