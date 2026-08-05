#!/usr/bin/env bash
# Round-3 design e2e: run both candidate guards against a real cluster and record
# what each one leaves behind. Underpins UPGRADE-GUARD-DESIGN.md.
#
# SAFETY: everything happens in throwaway namespaces with throwaway release names,
# using a scratch chart whose only real resource is a ConfigMap. It creates no
# cluster-scoped object, so it cannot collide with the rbgs release that may be
# live on this cluster (whose ClusterRole/webhook names are fixed). Both namespaces
# are deleted at the end, including on early exit.
#
# Usage: bash 12-guard-design-e2e.sh
set -uo pipefail

NS_A=${NS_A:-pr416-guard-test}
NS_B=${NS_B:-pr416-hook-test}
THRESHOLD=0.8.0-alpha.3

cleanup() {
  echo
  echo "=== CLEANUP ==="
  helm uninstall g -n "$NS_A" >/dev/null 2>&1
  helm uninstall h -n "$NS_B" >/dev/null 2>&1
  kubectl delete ns "$NS_A" "$NS_B" --wait=false >/dev/null 2>&1
  echo "  removed releases g/h and namespaces $NS_A, $NS_B"
}
trap cleanup EXIT

# ---------------------------------------------------------------------------
# Part 1 -- the chart-version marker guard (template `fail` + lookup)
# ---------------------------------------------------------------------------
echo "############ Part 1: chart-version marker guard (template-level fail)"
D=/tmp/pr416-guard && mkdir -p $D/templates && cd $D
printf 'apiVersion: v2\nname: guard\nversion: %s\n' "$THRESHOLD" > Chart.yaml
printf 'upgrade:\n  allowUnsupported: false\n' > values.yaml
cat > templates/marker.yaml <<'YAML'
apiVersion: v1
kind: ConfigMap
metadata:
  name: guard-chart-version
  namespace: {{ .Release.Namespace }}
data:
  chartVersion: {{ .Chart.Version | quote }}
YAML
cat > templates/upgrade-guard.yaml <<'YAML'
{{- if .Release.IsUpgrade }}
{{- $threshold := "0.8.0-alpha.3" }}
{{- $marker := lookup "v1" "ConfigMap" .Release.Namespace "guard-chart-version" }}
{{- if .Values.upgrade.allowUnsupported }}
{{- /* operator explicitly accepted the risk */ -}}
{{- else if not $marker }}
{{- fail (printf "cannot determine the installed chart version; upgrading from a release older than %s is unsupported -- uninstall and reinstall, or re-run with --set upgrade.allowUnsupported=true" $threshold) }}
{{- else if semverCompare (printf "< %s" $threshold) (index $marker.data "chartVersion") }}
{{- fail (printf "upgrading from chart %s is unsupported (minimum %s) -- uninstall and reinstall" (index $marker.data "chartVersion") $threshold) }}
{{- end }}
{{- end }}
YAML

kubectl create ns "$NS_A" >/dev/null 2>&1
r() { printf '\n--- %s\n' "$1"; }

r "1) first 'helm upgrade --install' (IsUpgrade=false) -- must succeed"
helm upgrade --install g . -n "$NS_A" 2>&1 | grep -E 'STATUS|Error' | head -2
echo "   marker written: $(kubectl get cm guard-chart-version -n "$NS_A" -o jsonpath='{.data.chartVersion}' 2>&1)"

r "2) re-run 'helm upgrade --install' (idempotent, marker=$THRESHOLD) -- must succeed"
echo "   this is the F4 fix: the documented command works twice on one cluster"
helm upgrade --install g . -n "$NS_A" 2>&1 | grep -E 'STATUS|Error' | head -2

r "3) marker patched to 0.8.0-alpha.1 (simulating an old install) -- must refuse"
kubectl patch cm guard-chart-version -n "$NS_A" --type merge \
  -p '{"data":{"chartVersion":"0.8.0-alpha.1"}}' >/dev/null
helm upgrade g . -n "$NS_A" 2>&1 | grep -E 'Error' | head -2

r "4) marker deleted (simulating a pre-marker release) -- must refuse"
kubectl delete cm guard-chart-version -n "$NS_A" >/dev/null 2>&1
helm upgrade g . -n "$NS_A" 2>&1 | grep -E 'Error' | head -2
echo "   ^ NOTE: this is the branch an upgrade from the ALREADY-RELEASED 0.8.0-alpha.3"
echo "     lands in, because the marker ships with this PR. The stated intent"
echo "     ('allow upgrades from >= alpha.3') is therefore unreachable by a marker."

r "5) escape hatch --set upgrade.allowUnsupported=true -- must pass"
helm upgrade g . -n "$NS_A" --set upgrade.allowUnsupported=true 2>&1 | grep -E 'STATUS|Error' | head -2

r "6) release state after the two refusals in 3) and 4)"
helm history g -n "$NS_A" 2>/dev/null | tail -4
echo "   -> a template-level fail aborts at RENDER time: no failed revision is"
echo "      recorded, the release never leaves 'deployed'."

# ---------------------------------------------------------------------------
# Part 2 -- a pre-upgrade hook Job that refuses (the state-check carrier)
# ---------------------------------------------------------------------------
echo
echo "############ Part 2: pre-upgrade hook Job (the state-check carrier)"
D=/tmp/pr416-hook && mkdir -p $D/templates && cd $D
printf 'apiVersion: v2\nname: hk\nversion: 0.1.0\n' > Chart.yaml
printf 'failHook: false\n' > values.yaml
printf 'apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: hk-cm\ndata:\n  a: "1"\n' > templates/cm.yaml
cat > templates/hook.yaml <<'YAML'
{{- if .Values.failHook }}
apiVersion: batch/v1
kind: Job
metadata:
  name: hk-precheck
  annotations:
    # weight must be lower than crd-upgrade.yaml's "-4", or the CRDs are already
    # rewritten by the time the check runs and the evidence is gone
    "helm.sh/hook": pre-upgrade
    "helm.sh/hook-weight": "-10"
    "helm.sh/hook-delete-policy": hook-succeeded
spec:
  backoffLimit: 0
  template:
    spec:
      restartPolicy: Never
      containers:
        - name: c
          image: busybox
          command: ["sh","-c","echo PRECHECK-FAILED; exit 1"]
{{- end }}
YAML

kubectl create ns "$NS_B" >/dev/null 2>&1
r "1) install"
helm install h . -n "$NS_B" 2>&1 | grep -E 'STATUS|Error' | head -2

r "2) pre-upgrade hook exits 1 -- what happens to the release?"
helm upgrade h . -n "$NS_B" --set failHook=true --timeout 90s 2>&1 | grep -E 'Error' | head -2
echo "   helm list:"; helm list -n "$NS_B" 2>/dev/null | tail -2 | sed 's/^/     /'
echo "   helm history:"; helm history h -n "$NS_B" 2>/dev/null | tail -3 | sed 's/^/     /'

r "3) can a normal upgrade proceed afterwards WITHOUT --force?"
helm upgrade h . -n "$NS_B" 2>&1 | grep -E 'STATUS|Error' | head -2
echo "   helm history:"; helm history h -n "$NS_B" 2>/dev/null | tail -3 | sed 's/^/     /'

echo
echo "RESULT: both carriers refuse the operation. The difference:"
echo "  - template fail  : release untouched, no failed revision, but lookup is"
echo "                     blind client-side, so fail-closed false-fails helm-diff."
echo "  - hook Job       : leaves a 'failed' revision (recoverable without --force),"
echo "                     but sees real cluster state, can reuse the controller's"
echo "                     own Go constants, and is SKIPPED by helm-diff so it"
echo "                     cannot false-fail."
