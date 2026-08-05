#!/usr/bin/env bash
# Round-3 design probe: what can a Helm template actually know at render time?
#
# This underpins UPGRADE-GUARD-DESIGN.md. It answers three questions that decide
# how the upgrade guard (F4) and the deprecated-object check (R3-F22) should be
# built, rather than reasoning about them from documentation.
#
# READ-ONLY. Uses a scratch chart in /tmp and `--dry-run=server` reads only; it
# creates nothing in the cluster and touches no rbgs object.
#
# Usage: bash 11-guard-design-probe.sh
set -uo pipefail

SCRATCH=${SCRATCH:-/tmp/pr416-probe}
CRD=${CRD:-rolebasedgroups.workloads.x-k8s.io}
mkdir -p "$SCRATCH/templates"
cd "$SCRATCH"
printf 'apiVersion: v2\nname: probe\nversion: 0.8.0-alpha.3\n' > Chart.yaml

echo "=== Q1. Does semverCompare handle the prerelease threshold correctly? ==="
echo "    (the guard would compare an installed version against 0.8.0-alpha.3;"
echo "     Masterminds/semver is notoriously fussy about prerelease vs release)"
cat > templates/t.yaml <<'YAML'
{{- $T := "0.8.0-alpha.3" }}
{{- range $v := list "0.5.0-alpha.3" "0.7.0" "0.8.0-alpha.1" "0.8.0-alpha.2" "0.8.0-alpha.3" "0.8.0-alpha.4" "0.8.0" "0.9.0" }}
# {{ printf "%-16s" $v }} < {{ $T }} ? {{ semverCompare (printf "< %s" $T) $v }}
{{- end }}
YAML
helm template probe . 2>/dev/null | grep -E '^# '
echo "    -> expected: true for the four older, false for alpha.3 and everything newer."

echo
echo "=== Q2. When is \`lookup\` actually populated? ==="
echo "    (decides whether a template-level guard can see cluster state at all,"
echo "     and therefore whether 'not found' may be treated as 'old version')"
cat > templates/t.yaml <<'YAML'
{{- $cm := lookup "v1" "ConfigMap" "default" "kube-root-ca.crt" }}
# lookup: {{ if $cm }}POPULATED{{ else }}EMPTY{{ end }}   IsUpgrade: {{ .Release.IsUpgrade }}
YAML
printf '  %-34s ' 'helm template:';                helm template probe .                  2>/dev/null | grep -oE 'lookup:.*'
printf '  %-34s ' 'helm template --is-upgrade:';   helm template probe . --is-upgrade     2>/dev/null | grep -oE 'lookup:.*'
printf '  %-34s ' 'helm template --dry-run=server:'; helm template probe . --dry-run=server 2>/dev/null | grep -oE 'lookup:.*'
printf '  %-34s ' 'helm install --dry-run:';       helm install p . --dry-run             2>/dev/null | grep -oE 'lookup:.*'
echo "    -> EMPTY on every client-side path. A template guard therefore cannot"
echo "       distinguish 'old install' from 'cannot tell', and helm-diff style"
echo "       tooling would false-fail on a fail-closed template check."

echo
echo "=== Q3. Is there an authoritative 'which era is this install' signal? ==="
echo "    (the alternative to a chart-version marker, which cannot work: the"
echo "     marker ships with THIS PR, so an upgrade from the already-released"
echo "     0.8.0-alpha.3 would find no marker at all)"
cat > templates/t.yaml <<YAML
{{- \$crd := lookup "apiextensions.k8s.io/v1" "CustomResourceDefinition" "" "$CRD" }}
# crd: {{ if \$crd }}FOUND{{ else }}ABSENT{{ end }}
{{- if \$crd }}
# storedVersions: {{ \$crd.status.storedVersions }}
# still has v1alpha1 stored: {{ has "v1alpha1" \$crd.status.storedVersions }}
# served versions: {{ range \$crd.spec.versions }}{{ .name }}(storage={{ .storage }}) {{ end }}
{{- end }}
YAML
helm template probe . --dry-run=server 2>/dev/null | grep -E '^# '
echo "    -> readable, and maintained by Kubernetes itself. NOTE it is sticky:"
echo "       storedVersions only shrinks when an operator explicitly patches it,"
echo "       so a fully-migrated cluster can still list v1alpha1. Use it as a"
echo "       corroborating signal, not as the hard gate."

echo
echo "RESULT: state-based checking is feasible; a chart-version marker is not"
echo "        sufficient on its own; and a fail-closed TEMPLATE check is the wrong"
echo "        carrier because lookup is blind on every client-side path."
