#!/usr/bin/env bash
# inventory-jq-check.sh — CONTRACT check for the migration guide's role-inventory
# command (PR #586, doc/migration-v1alpha1-to-v1alpha2.md §1).
#
# Runs the guide's exact jq pipeline against fixtures/inventory.json, which
# encodes every role-location and workload-selector shape the guide documents:
#   - v1alpha2 RoleBasedGroup        spec.roles (annotation-based selectors)
#   - v1alpha2 RoleBasedGroupSet     spec.groupTemplate.spec.roles
#   - v1alpha1 RoleBasedGroup        spec.roles with direct workload field
#   - v1alpha1 RoleBasedGroupSet     spec.template.roles
# plus negative controls (default RoleInstanceSet, redundant RoleInstanceSet
# annotation) and the InstanceSet selector in both shapes.
#
# Expected output is fixtures/inventory.expected. Exit 0 iff the jq command
# reproduces it exactly (jq >= 1.5 required for test()).
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FIXTURE="$HERE/../fixtures/inventory.json"
EXPECTED="$HERE/../fixtures/inventory.expected"
command -v jq >/dev/null || { echo "inventory-jq-check: jq is required" >&2; exit 2; }

# The guide's command, verbatim (kubectl get replaced by the fixture file):
inventory="$(cat "$FIXTURE")" &&
printf '%s' "$inventory" | jq -r '.items[] | . as $o
  | (.spec.roles // .spec.groupTemplate.spec.roles // .spec.template.roles // [])[]
  | (if .workload.kind then "\(.workload.apiVersion)/\(.workload.kind)"
     else ((.annotations // {})["rbg.workloads.x-k8s.io/role-workload-type"] // "")
     end) as $wt
  | select(($wt | test("/(Deployment|StatefulSet|LeaderWorkerSet)$"))
           or $wt == "workloads.x-k8s.io/v1alpha1/InstanceSet")
  | "\($o.kind) \($o.metadata.namespace)/\($o.metadata.name) role=\(.name) \($wt)"' \
  | diff -u "$EXPECTED" - && echo "inventory-jq-check: PASS (guide jq matches expected inventory)"
