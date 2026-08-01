#!/usr/bin/env bash
# F5 (CONTRACT test) -- the chart ClusterRole must still match the generated
# config/rbac/role.yaml when compatibility is enabled.
#
# Until this PR, `make manifests` COPIED config/rbac/role.yaml over the chart template, so the
# two could not drift. This PR removes that copy (the chart now carries hand-written
# conditionals) and replaces it with an @echo telling a human to sync by hand. There is no CI
# gate on the result, so the chart's RBAC can now silently rot.
#
# This test normalises both sides to (apiGroup, resource, verb) triples and compares them.
set -uo pipefail
cd "$(dirname "$0")/../../../.." || exit 2
CHART=deploy/helm/rbgs
HELM=${HELM:-helm}

echo "=== F5: chart ClusterRole (compat enabled) vs generated config/rbac/role.yaml ==="
echo
echo "--- the removed sync step ---"
git show "$(git merge-base HEAD origin/main 2>/dev/null || echo HEAD~1)":Makefile 2>/dev/null \
  | grep -n 'cp config/rbac/role.yaml' | sed 's/^/  was: /' || echo "  (baseline Makefile unavailable)"
grep -n 'manually sync' Makefile | sed 's/^/  now: /'
echo

triples() { # stdin: a ClusterRole document -> sorted "group|resource|verb" lines
  python3 -c '
import sys, yaml
for d in yaml.safe_load_all(sys.stdin.read()):
    if not d or d.get("kind") != "ClusterRole": continue
    for r in d.get("rules") or []:
        for g in r.get("apiGroups") or [""]:
            for res in r.get("resources") or []:
                for v in r.get("verbs") or []:
                    print("%s|%s|%s" % (g, res, v))
' | sort -u
}

gen=$(triples < config/rbac/role.yaml)
chart=$($HELM template rbgs "$CHART" --namespace rbgs-system \
          --set compatibility.v1alpha1.enabled=true 2>/dev/null \
        | awk '/^# Source: /{insrc=($3 ~ /rbac\/clusterrole\.yaml$/)} insrc{print}' \
        | triples)

ngen=$(printf '%s\n' "$gen"   | grep -c . )
nchart=$(printf '%s\n' "$chart" | grep -c . )
echo "generated triples: $ngen"
echo "chart triples:     $nchart"
echo

onlygen=$(comm -23 <(printf '%s\n' "$gen") <(printf '%s\n' "$chart"))
onlychart=$(comm -13 <(printf '%s\n' "$gen") <(printf '%s\n' "$chart"))

if [ -n "$onlygen" ]; then
  echo "IN GENERATED BUT MISSING FROM CHART (controller will 403 on these):"
  printf '%s\n' "$onlygen" | sed 's/^/  /'
fi
if [ -n "$onlychart" ]; then
  echo "IN CHART BUT NOT GENERATED (over-granted):"
  printf '%s\n' "$onlychart" | sed 's/^/  /'
fi

if [ -z "$onlygen" ] && [ -z "$onlychart" ]; then
  echo "RESULT: no drift at this head ($ngen == $nchart)."
  echo "        The maintainability risk stands (no CI gate now that make manifests"
  echo "        no longer syncs), but it is a NOTE, not a defect at this commit."
  exit 0
fi
echo
echo "RESULT: F5 REPRODUCED -- the chart ClusterRole has drifted from the generated role."
exit 1
