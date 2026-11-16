#!/usr/bin/env bash
# helm-values-edges.sh — probe the chart's value edge cases for the manager
# disruption feature (PR #589). Each check prints PASS/FAIL with the observed fact.
set -u
CHART="${1:-deploy/helm/rbgs}"
fail=0
note() { printf '%-72s %s\n' "$1" "$2"; }

out=$(helm template rbgs "$CHART" --show-only templates/manager/poddisruptionbudget.yaml 2>/dev/null)
[ -n "$out" ] && note "default values: PDB rendered" "PASS" || { note "default values: PDB rendered" "FAIL"; fail=1; }

out=$(helm template rbgs "$CHART" --set controller.podDisruptionBudget.enabled=false --show-only templates/manager/poddisruptionbudget.yaml 2>/dev/null)
[ -z "$out" ] && note "enabled=false: PDB absent" "PASS" || { note "enabled=false: PDB absent" "FAIL"; fail=1; }

# An empty map must NOT disable the feature: helm coalesces chart defaults into
# user-supplied values (guard against the `if $pdb.enabled` default-dict shape).
out=$(helm template rbgs "$CHART" --set-json 'controller.podDisruptionBudget={}' --show-only templates/manager/poddisruptionbudget.yaml 2>/dev/null)
[ -n "$out" ] && note "podDisruptionBudget={} still renders PDB (defaults coalesce)" "PASS" || { note "podDisruptionBudget={} still renders PDB (defaults coalesce)" "FAIL"; fail=1; }

out=$(helm template rbgs "$CHART" --set-json 'controller.topologySpreadConstraints=[]' 2>/dev/null)
echo "$out" | grep -q "topologySpreadConstraints" && { note "topologySpreadConstraints=[] removes constraint" "FAIL"; fail=1; } || note "topologySpreadConstraints=[] removes constraint" "PASS"

out=$(helm template rbgs "$CHART" 2>/dev/null)
echo "$out" | grep -q "topologyKey: kubernetes.io/hostname" && echo "$out" | grep -q "whenUnsatisfiable: ScheduleAnyway" \
  && note "default render: hostname ScheduleAnyway constraint present" "PASS" || { note "default render: hostname ScheduleAnyway constraint present" "FAIL"; fail=1; }

echo "$out" | grep -q "unhealthyPodEvictionPolicy: AlwaysAllow" \
  && note "default render: PDB has unhealthyPodEvictionPolicy=AlwaysAllow" "PASS" || { note "default render: PDB AlwaysAllow" "FAIL"; fail=1; }

exit $fail
