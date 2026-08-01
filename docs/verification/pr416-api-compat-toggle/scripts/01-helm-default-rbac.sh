#!/usr/bin/env bash
# F1 (CONTRACT test) -- the documented default must ship WITHOUT v1alpha1 legacy RBAC.
#
# The chart README table says `compatibility.v1alpha1.enabled` default = `false`, the README
# prose says "By default (compatibility.v1alpha1.enabled=false), the chart ships in restricted
# mode for security", and the PR description says "(default: `false`)" / "When disabled
# (default): ClusterRole omits RBAC for deployments/statefulsets/leaderworkersets".
#
# This test asserts that documented intent. On the PR head it is expected to FAIL,
# because values.yaml actually sets `enabled: true`.
#
# Exit 0 = documented behaviour holds (finding fixed). Exit 1 = finding reproduced.
set -uo pipefail
cd "$(dirname "$0")/../../../.." || exit 2
CHART=deploy/helm/rbgs
HELM=${HELM:-helm}

echo "=== F1: does a DEFAULT chart render omit v1alpha1 legacy RBAC? ==="
echo "chart: $CHART"
echo "helm:  $($HELM version --short)"
echo

echo "--- values.yaml declared default ---"
grep -A 3 '^compatibility:' "$CHART/values.yaml"
echo
echo "--- chart README documented default ---"
grep -n 'compatibility.v1alpha1.enabled' "$CHART/README.md" | head -5
echo

# Render the ClusterRole with *no* overrides at all -- exactly `helm install rbgs <chart>`.
render=$($HELM template rbgs "$CHART" --namespace rbgs-system 2>&1)
rc=$?
if [ $rc -ne 0 ]; then
  echo "HARNESS PROBLEM: default render failed, cannot evaluate F1"
  echo "$render" | head -20
  exit 2
fi

clusterrole=$(printf '%s\n' "$render" | awk '
  /^# Source: /   { insrc = ($3 ~ /rbac\/clusterrole\.yaml$/) }
  insrc           { print }
')

if [ -z "$clusterrole" ]; then
  echo "HARNESS PROBLEM: no clusterrole.yaml document found in the default render"
  exit 2
fi

echo "--- legacy resources present in the DEFAULT-rendered ClusterRole ---"
legacy=$(printf '%s\n' "$clusterrole" \
  | grep -E '^[[:space:]]*- (deployments|statefulsets|leaderworkersets)(/[a-z]+)?[[:space:]]*$' \
  | sed 's/^[[:space:]]*- //' | sort -u)

if [ -n "$legacy" ]; then
  printf '  %s\n' $legacy
else
  echo "  (none)"
fi
echo

# Control: prove the gate *can* bite, so a "no legacy RBAC" result would be meaningful
# and this test is not simply blind to the resources it greps for.
echo "--- CONTROL: same render with compatibility.v1alpha1.enabled=false ---"
ctl=$($HELM template rbgs "$CHART" --namespace rbgs-system \
        --set compatibility.v1alpha1.enabled=false 2>&1 \
      | awk '/^# Source: /{insrc=($3 ~ /rbac\/clusterrole\.yaml$/)} insrc{print}' \
      | grep -cE '^[[:space:]]*- (deployments|statefulsets|leaderworkersets)(/[a-z]+)?[[:space:]]*$')
echo "  legacy resource lines when explicitly disabled: $ctl"
if [ "$ctl" -ne 0 ]; then
  echo "  HARNESS PROBLEM: the gate does not remove legacy RBAC even when explicitly false;"
  echo "  F1 cannot be attributed to the default value alone."
  exit 2
fi
echo "  -> the conditional works; whatever we see above is purely the DEFAULT's doing."
echo

# Also record what the controller is actually told to do by default.
echo "--- controller args in the DEFAULT render ---"
printf '%s\n' "$render" | grep -E '^\s+- --(disable-v1alpha1-compatibility|scheduler-name)' || true
echo

if [ -n "$legacy" ]; then
  echo "RESULT: F1 REPRODUCED -- the default render STILL GRANTS v1alpha1 legacy RBAC."
  echo "        Documented default is 'false' (restricted); values.yaml ships 'true'."
  echo "        The PR's stated purpose (remove excessive RBAC before release) is unmet by default."
  exit 1
fi

echo "RESULT: F1 FIXED -- default render omits legacy RBAC as documented."
exit 0
