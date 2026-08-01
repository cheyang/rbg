#!/usr/bin/env bash
# F6b -- what each realistic `compatibility:` value shape actually MEANS.
#
# Because the templates dereference .Values.compatibility.v1alpha1.enabled directly (no
# `| default dict`, no explicit nil handling), a partially-written block does not fall back to
# the chart default. Depending on exactly how far the user got, the same intent produces three
# different outcomes: correct, hard render failure, or SILENTLY disabling compatibility and
# stripping the controller's RBAC.
#
# This script is a report, not a pass/fail gate: it prints the outcome per shape so the
# semantics are on the record.
set -uo pipefail
cd "$(dirname "$0")/../../../.." || exit 2
CHART=deploy/helm/rbgs
HELM=${HELM:-helm}

echo "=== F6b: outcome of each realistic 'compatibility' value shape ==="
echo
printf '%-46s | %-14s | %s\n' "values shape" "renders?" "legacy RBAC granted?"
printf '%-46s-+-%-14s-+-%s\n' "----------------------------------------------" "--------------" "--------------------"

probe() {
  local label="$1"; shift
  local vals="$1"; shift

  local vf; vf=$(mktemp)
  printf '%s\n' "$vals" > "$vf"

  local out rc
  out=$($HELM template rbgs "$CHART" --namespace rbgs-system -f "$vf" 2>/tmp/pe.txt)
  rc=$?
  if [ $rc -ne 0 ]; then
    printf '%-46s | %-14s | %s\n' "$label" "FAIL" "n/a (install blocked)"
    rm -f "$vf"; return
  fi

  local n
  n=$(printf '%s\n' "$out" \
      | awk '/^# Source: /{insrc=($3 ~ /rbac\/clusterrole\.yaml$/)} insrc{print}' \
      | grep -cE '^[[:space:]]*- (deployments|statefulsets|leaderworkersets)(/[a-z]+)?[[:space:]]*$')
  if [ "$n" -gt 0 ]; then
    printf '%-46s | %-14s | %s\n' "$label" "ok" "YES ($n rules)"
  else
    printf '%-46s | %-14s | %s\n' "$label" "ok" "no  <-- stripped"
  fi
  rm -f "$vf"
}

probe "(nothing specified -- chart default)"        ""
probe "compatibility.v1alpha1.enabled: true"        "compatibility:
  v1alpha1:
    enabled: true"
probe "compatibility.v1alpha1.enabled: false"       "compatibility:
  v1alpha1:
    enabled: false"
probe "compatibility: (key present, no children)"   "compatibility:"
probe "compatibility.v1alpha1: (no children)"       "compatibility:
  v1alpha1:"
probe "compatibility.v1alpha1.enabled: (no value)"  "compatibility:
  v1alpha1:
    enabled:"

cat <<'EOF'

Reading of the table:
  * The chart default GRANTS legacy RBAC, although the README and the PR description both
    state the default is `false` / restricted (finding F1).
  * A `compatibility:` block that is present but incomplete does NOT fall back to the chart
    default: it either hard-fails the render with an opaque Go template nil-pointer error
    (finding F6), or -- when only `enabled:` is left blank -- silently evaluates falsy and
    STRIPS the controller's RBAC without the operator ever asking for it.
  * Every pre-existing block in this chart avoids all three outcomes by using
    `| default dict` (see 02-render-all-shapes.sh controls). The new block does not.
EOF
