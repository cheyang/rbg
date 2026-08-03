#!/usr/bin/env bash
# R2-F11 / R2-F12 -- do the rendered FLAG and the rendered RBAC ever disagree?
#
# The chart consumes controller.deprecatedWorkloadTypes.enabled in two different ways:
#
#   manager.yaml:57     PRINTS it:  {{ if hasKey $dwt "enabled" }}{{ $dwt.enabled }}{{ else }}true{{ end }}
#   clusterrole.yaml:12 TESTS it:   or (not (hasKey $dwt "enabled")) $dwt.enabled
#
# Printing and Go-template truthiness are not the same predicate, so the two can
# disagree. This script renders a matrix of value shapes and, for each, extracts
#   (a) the --enable-deprecated-workload-types= value handed to the manager, and
#   (b) whether the deprecated-workload RBAC survives in the ClusterRole,
# then reports every shape where the two tell different stories, plus every shape
# whose flag value the manager's flag.BoolVar cannot parse (pod would crash-loop).
#
# CONTRACT test: it must exit 0. Only stdout is parsed -- helm writes kubeconfig
# permission warnings to stderr, and feeding those into the parser produced a wrong
# answer in an earlier round of this review.
set -uo pipefail
cd "$(git rev-parse --show-toplevel)"
CHART=deploy/helm/rbgs
KEY=controller.deprecatedWorkloadTypes.enabled
WORK="$(mktemp -d)"; trap 'rm -rf "$WORK"' EXIT
rc=0

echo "=== R2-F11/F12: does the rendered flag agree with the rendered RBAC? ==="
helm version --short 2>/dev/null
echo

# render <label> <helm args...>  -> prints "<label>|<flag>|<rbac>"
render() {
  local label="$1"; shift
  local mout="$WORK/m.yaml" cout="$WORK/c.yaml" flag rbac
  if ! helm template rbgs "$CHART" "$@" --show-only templates/manager/manager.yaml \
        >"$mout" 2>"$WORK/m.err"; then
    printf '%s|RENDER-FAIL|RENDER-FAIL\n' "$label"; return
  fi
  helm template rbgs "$CHART" "$@" --show-only templates/rbac/clusterrole.yaml \
        >"$cout" 2>"$WORK/c.err" || { printf '%s|RENDER-FAIL|RENDER-FAIL\n' "$label"; return; }
  # Everything after the '=', so unparseable values ("", map[], []) are captured verbatim.
  flag="$(grep -oE -- '--enable-deprecated-workload-types=.*' "$mout" | head -1 | cut -d= -f2-)"
  if grep -qE '^[[:space:]]+- (deployments|statefulsets|leaderworkersets)(/(status|finalizers))?$' "$cout"; then
    rbac=present
  else
    rbac=stripped
  fi
  printf '%s|%s|%s\n' "$label" "${flag:-<empty>}" "$rbac"
}

# valuesfile <label> <yaml-body> -> renders with that body as a -f values file
valuesfile() {
  local label="$1" body="$2"
  printf '%s\n' "$body" > "$WORK/v.yaml"
  render "$label" -f "$WORK/v.yaml"
}

# What flag.BoolVar (Go stdlib strconv.ParseBool) makes of a flag value.
# Accepts 1,t,T,TRUE,true,True,0,f,F,FALSE,false,False; everything else is a
# parse error and /manager exits non-zero at startup.
parsebool() {
  case "$1" in
    1|t|T|TRUE|true|True)    echo true ;;
    0|f|F|FALSE|false|False) echo false ;;
    *)                       echo UNPARSEABLE ;;
  esac
}

rows=()
rows+=("$(render 'default (no overrides)')")
rows+=("$(render '--set enabled=true'        --set "$KEY=true")")
rows+=("$(render '--set enabled=false'       --set "$KEY=false")")
rows+=("$(render '--set-string enabled=false' --set-string "$KEY=false")")
rows+=("$(render '--set-string enabled=0'    --set-string "$KEY=0")")
rows+=("$(render '--set-string enabled=False' --set-string "$KEY=False")")
rows+=("$(render '--set enabled= (empty)'    --set "$KEY=")")
rows+=("$(valuesfile 'enabled: "false" (quoted)' 'controller:
  deprecatedWorkloadTypes:
    enabled: "false"')")
rows+=("$(valuesfile 'enabled: "" (empty str)' 'controller:
  deprecatedWorkloadTypes:
    enabled: ""')")
rows+=("$(valuesfile 'enabled: {} (empty map)' 'controller:
  deprecatedWorkloadTypes:
    enabled: {}')")
rows+=("$(valuesfile 'enabled: [] (empty list)' 'controller:
  deprecatedWorkloadTypes:
    enabled: []')")
rows+=("$(valuesfile 'enabled: null' 'controller:
  deprecatedWorkloadTypes:
    enabled: null')")
rows+=("$(valuesfile 'deprecatedWorkloadTypes: {} (key absent)' 'controller:
  deprecatedWorkloadTypes: {}')")

printf '%-38s %-12s %-10s %-12s %s\n' SHAPE FLAG RBAC 'MANAGER-READS' NOTE
mismatch=0; unparseable=0; controls=0
for row in "${rows[@]}"; do
  label="${row%%|*}"; rest="${row#*|}"; flag="${rest%%|*}"; rbac="${rest##*|}"
  note=""
  if [ "$flag" = "RENDER-FAIL" ]; then
    reads="-"; note="render failed (not judged here)"
  else
    raw="$flag"; [ "$raw" = "<empty>" ] && raw=""
    reads="$(parsebool "$raw")"
    if [ "$reads" = "UNPARSEABLE" ]; then
      note="flag rejected by flag.BoolVar -> CrashLoopBackOff"
      unparseable=$((unparseable+1))
      [ "$rbac" = stripped ] && note="$note; RBAC silently stripped"
    elif [ "$reads" = false ] && [ "$rbac" = present ]; then
      note="MISMATCH: manager disables the types, RBAC keeps them"
      mismatch=$((mismatch+1))
    elif [ "$reads" = true ] && [ "$rbac" = stripped ]; then
      note="MISMATCH: manager enables the types, RBAC removed"
      mismatch=$((mismatch+1))
    else
      note="agrees"; controls=$((controls+1))
    fi
  fi
  printf '%-38s %-12s %-10s %-12s %s\n' "$label" "$flag" "$rbac" "$reads" "$note"
done

echo
if [ "$controls" -lt 3 ]; then
  echo "  HARNESS PROBLEM: only $controls shape(s) agreed. The baseline shapes"
  echo "        (default, =true, =false) must agree, otherwise the mismatch rows"
  echo "        below prove nothing about this value in particular."
  rc=1
else
  echo "  controls: $controls shape(s) agree (incl. default / =true / =false) -- baseline is sound."
fi

if [ "$mismatch" -gt 0 ]; then
  echo "  R2-F11 REPRODUCED: $mismatch shape(s) render a flag and an RBAC set that"
  echo "        disagree. clusterrole.yaml tests Go-template truthiness while"
  echo "        manager.yaml prints the value, so a non-empty string such as"
  echo "        \"false\" is truthy for the RBAC gate but parses as false for the"
  echo "        manager: the operator stops using the deprecated types while"
  echo "        keeping full create/delete/patch RBAC on them. The reduction in"
  echo "        privilege the value advertises silently does not happen."
  rc=1
fi
if [ "$unparseable" -gt 0 ]; then
  echo "  R2-F12 REPRODUCED: $unparseable shape(s) render a flag value that"
  echo "        flag.BoolVar cannot parse, so the manager exits at startup"
  echo "        (CrashLoopBackOff). helm renders them without error or warning."
  rc=1
fi
[ "$rc" -eq 0 ] && echo "  OK: no shape disagrees and every flag value is parseable."

echo
if [ "$rc" -eq 0 ]; then
  echo "RESULT: R2-F11/F12 FIXED -- flag and RBAC agree across all shapes."
else
  echo "RESULT: R2-F11/F12 reproduced (see above)."
fi
exit "$rc"
