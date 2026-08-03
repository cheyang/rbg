#!/usr/bin/env bash
# F1 (round 2) -- is the SHIPPED chart default the same as the DOCUMENTED default,
# and does the toggle actually remove the deprecated-workload RBAC when set to false?
#
# Round 1 found the two disagreed: values.yaml shipped `compatibility.v1alpha1.enabled: true`
# while the README table, the README prose and the PR description all said `false`.
# Round 2 renamed the value to `controller.deprecatedWorkloadTypes.enabled` and settled on
# `true` everywhere, so this script is now a CONTRACT test: it must exit 0.
#
# Parse only stdout -- helm writes kubeconfig permission warnings to stderr, and feeding
# those into the YAML/grep pipeline produced a wrong answer in an earlier round.
set -uo pipefail
cd "$(git rev-parse --show-toplevel)"
CHART=deploy/helm/rbgs
VALUE=controller.deprecatedWorkloadTypes.enabled
rc=0

echo "=== F1: shipped default vs documented default, and does false actually strip RBAC? ==="
echo "chart: $CHART"
helm version --short 2>/dev/null

# --- what values.yaml actually ships -------------------------------------------------
shipped="$(helm template rbgs "$CHART" --show-only templates/manager/manager.yaml 2>/dev/null \
          | grep -oE -- "--enable-deprecated-workload-types=[a-z]+" | head -1 | cut -d= -f2)"
echo
echo "--- shipped default (flag as rendered with no overrides) ---"
echo "  --enable-deprecated-workload-types=${shipped:-<absent>}"

# --- what the docs claim -------------------------------------------------------------
echo
echo "--- documented default (chart README value table) ---"
documented="$(grep -F "\`$VALUE\`" "$CHART/README.md" \
             | grep -oE '\| `(true|false)` \|' | grep -oE 'true|false' | sort -u | tr '\n' ' ')"
grep -nF "\`$VALUE\`" "$CHART/README.md" | sed 's/^/  /' | cut -c1-140
echo "  -> documented default(s): ${documented:-<none found>}"

# --- consistency ---------------------------------------------------------------------
echo
if [ -z "$shipped" ]; then
  echo "  FAIL: the flag is not rendered at all in the default manifest."; rc=1
elif [ -z "$documented" ]; then
  echo "  FAIL: no default documented for $VALUE in the chart README table."; rc=1
elif ! printf '%s' "$documented" | grep -qw "$shipped"; then
  echo "  F1 REPRODUCED: shipped default ($shipped) != documented default ($documented)."; rc=1
else
  echo "  OK: shipped default ($shipped) matches the documented default ($documented)."
fi

# --- does the toggle do anything? ----------------------------------------------------
# Count the deprecated-workload resources granted in the rendered ClusterRole.
legacy_count() { # $@ = extra helm args
  helm template rbgs "$CHART" "$@" --show-only templates/rbac/clusterrole.yaml 2>/dev/null \
    | grep -cE '^[[:space:]]+- (deployments|statefulsets|leaderworkersets)(/(status|finalizers))?$'
}
on="$(legacy_count --set "$VALUE=true")"
off="$(legacy_count --set "$VALUE=false")"
def="$(legacy_count)"
echo
echo "--- deprecated-workload RBAC lines in the rendered ClusterRole ---"
echo "  default         : $def"
echo "  =true  (control): $on"
echo "  =false (subject): $off"
if [ "$off" -ne 0 ]; then
  echo "  FAIL: setting $VALUE=false left $off deprecated-workload RBAC line(s) in place;"
  echo "        the toggle does not remove the permissions it claims to remove."; rc=1
elif [ "$on" -eq 0 ]; then
  echo "  HARNESS PROBLEM: the =true control also yielded 0 lines, so the =false result"
  echo "        proves nothing (the grep may no longer match the rendered shape)."; rc=1
else
  echo "  OK: =false removes all $on line(s); the =true control keeps them (toggle works)."
fi

echo
if [ "$rc" -eq 0 ]; then
  echo "RESULT: F1 FIXED -- default is consistent and the toggle is effective."
else
  echo "RESULT: F1 still failing (see above)."
fi
exit "$rc"
