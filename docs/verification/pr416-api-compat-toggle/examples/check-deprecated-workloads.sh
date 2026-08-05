#!/bin/bash
# Preflight check for controller.deprecatedWorkloadTypes.enabled=false.
#
# Refuses the install/upgrade when the cluster already contains a RoleBasedGroup or
# RoleBasedGroupSet whose roles use a workload type other than RoleInstanceSet. With
# the toggle off the controller is granted no RBAC for the deprecated types and
# watches none of them, and the validating webhook refuses every write touching one
# -- so such an object would be left unreconcilable, with nothing written to its
# status to say why.
#
# Run as a pre-install,pre-upgrade Helm hook with a weight LOWER than the
# crd-upgrade job's, so the CRDs have not been rewritten yet.
#
# WHY AN ALLOWLIST RATHER THAN A LIST OF DEPRECATED TYPES
#
# api/workloads/constants/external.go declares exactly four workload types, and
# RoleInstanceSet is the only one that is not deprecated, so "anything else" is
# equivalent to naming the three today. It is preferred because it FAILS CLOSED: a
# fourth deprecated type added to isDeprecatedWorkloadType is caught here
# automatically, instead of being silently approved because nobody updated a second
# copy of the list. There is no list here to keep in sync.
#
# The cost is that this is deliberately STRICTER than isDeprecatedWorkloadType: an
# unknown type is refused too. That is the better direction -- such an object cannot
# be reconciled anyway (NewWorkloadReconciler returns "unsupported workload type"),
# and a false refusal names the object and is fixable, whereas a missed deprecated
# type strands objects silently. If a fifth, genuinely supported workload type is
# ever added, add it to SUPPORTED_TYPES below.
#
# Exit codes:
#   0  every role uses a supported workload type (or the toggle is on -- nothing to do)
#   1  offending objects found; they are listed on stdout
#   2  the check could not be completed (API error, missing RBAC). Deliberately NOT 0:
#      a check that passes when it cannot see anything is worse than no check.
#
# Environment:
#   ENABLE_DEPRECATED_WORKLOAD_TYPES  "true"/"false". When true the check is a no-op.
#   MAX_REPORTED                      max offenders to print (default 20)
#   FIXTURE_FILE                      test only: read this JSON instead of the cluster

set -uo pipefail

WORKLOAD_TYPE_ANNOTATION="rbg.workloads.x-k8s.io/role-workload-type"

# constants.RoleInstanceSetWorkloadType -- also the value GetWorkloadType() returns
# for a role that carries no annotation at all.
DEFAULT_WORKLOAD_TYPE="workloads.x-k8s.io/v1alpha2/RoleInstanceSet"
SUPPORTED_TYPES=("$DEFAULT_WORKLOAD_TYPE")

# Overridable so the distinction between "CRD absent" and "query failed" can be
# exercised in tests; not intended to be set in production.
RBG_RESOURCE="${RBG_RESOURCE:-rolebasedgroups.workloads.x-k8s.io}"
RBGSET_RESOURCE="${RBGSET_RESOURCE:-rolebasedgroupsets.workloads.x-k8s.io}"

ENABLE_DEPRECATED_WORKLOAD_TYPES="${ENABLE_DEPRECATED_WORKLOAD_TYPES:-true}"
MAX_REPORTED="${MAX_REPORTED:-20}"
FIXTURE_FILE="${FIXTURE_FILE:-}"

if [ "$ENABLE_DEPRECATED_WORKLOAD_TYPES" != "false" ]; then
  echo "preflight: deprecated workload types are enabled; nothing to check."
  exit 0
fi

# One filter serves both kinds: `.spec.roles` covers RoleBasedGroup and
# `.spec.groupTemplate.spec.roles` covers RoleBasedGroupSet, so a template cannot
# slip past. `// []` covers a missing roles array, and `(.annotations // {})[...] //
# $default` reproduces GetWorkloadType(): the role annotation, else RoleInstanceSet.
read -r -d '' JQ_FILTER <<'JQ'
[ .items[]
  | .metadata as $m
  | ( (.spec.roles // []), (.spec.groupTemplate.spec.roles // []) )[]
  | { ns: ($m.namespace // "-"), name: $m.name, role: .name,
      wt: ((.annotations // {})[$ann] // $default) }
  | select( .wt as $w | ($supported | index($w)) | not )
  | "  \(.ns)/\(.name)\trole=\(.role)\ttype=\(.wt)" ]
| .[]
JQ

supported_json=$(printf '%s\n' "${SUPPORTED_TYPES[@]}" | jq -R . | jq -s .)

run_filter() { # run_filter <file>
  jq -r --arg ann "$WORKLOAD_TYPE_ANNOTATION" --arg default "$DEFAULT_WORKLOAD_TYPE" \
    --argjson supported "$supported_json" "$JQ_FILTER" "$1"
}

# collect_offenders <resource> -- echoes offender lines, returns:
#   0 queried successfully   3 resource type absent (fresh cluster)   2 query failed
collect_offenders() {
  local resource="$1" tmp stderr rc
  tmp=$(mktemp); stderr=$(mktemp)
  kubectl get "$resource" --all-namespaces -o json >"$tmp" 2>"$stderr"
  rc=$?
  if [ $rc -ne 0 ]; then
    # A missing CRD is expected on a fresh cluster and is not a failure. Anything
    # else (RBAC, unreachable API) must not be mistaken for "nothing found".
    if grep -qiE 'server doesn.t have a resource type|could not find the requested resource' "$stderr"; then
      rm -f "$tmp" "$stderr"; return 3
    fi
    echo "preflight: FAILED to query $resource:" >&2
    sed 's/^/  /' "$stderr" >&2
    rm -f "$tmp" "$stderr"; return 2
  fi
  run_filter "$tmp"
  rm -f "$tmp" "$stderr"
}

offenders=""
checked=0

if [ -n "$FIXTURE_FILE" ]; then
  echo "preflight: reading fixture $FIXTURE_FILE instead of the cluster (test mode)"
  offenders=$(run_filter "$FIXTURE_FILE") || exit 2
  checked=1
else
  for resource in "$RBG_RESOURCE" "$RBGSET_RESOURCE"; do
    out=$(collect_offenders "$resource"); rc=$?
    case $rc in
      0) checked=$((checked + 1)); [ -n "$out" ] && offenders+="${out}"$'\n' ;;
      3) echo "preflight: $resource is not installed yet -- treating as no objects." ;;
      *) exit 2 ;;
    esac
  done
  if [ "$checked" -eq 0 ]; then
    echo "preflight: neither CRD is installed; this is a fresh cluster. OK."
    exit 0
  fi
fi

offenders=$(printf '%s' "$offenders" | sed '/^$/d')

if [ -z "$offenders" ]; then
  echo "preflight: OK -- every role uses $DEFAULT_WORKLOAD_TYPE."
  exit 0
fi

total=$(printf '%s\n' "$offenders" | wc -l | tr -d ' ')
echo
echo "preflight: REFUSING -- controller.deprecatedWorkloadTypes.enabled=false, but $total role(s)"
echo "in this cluster use a workload type other than RoleInstanceSet:"
echo
printf '%s\n' "$offenders" | head -n "$MAX_REPORTED" | column -t -s $'\t' 2>/dev/null \
  || printf '%s\n' "$offenders" | head -n "$MAX_REPORTED"
if [ "$total" -gt "$MAX_REPORTED" ]; then
  echo "  ... and $((total - MAX_REPORTED)) more (set MAX_REPORTED to see them)"
fi
cat <<'EOF'

With the toggle off no RBAC is granted for the deprecated workload types and none of
them are watched, so these objects could not be reconciled, and the validating
webhook would refuse every write to them -- including the controller's own.

Keep controller.deprecatedWorkloadTypes.enabled=true on this cluster. It is only
supported on a fresh installation, until a migration path to RoleInstanceSet ships.
EOF
exit 1
