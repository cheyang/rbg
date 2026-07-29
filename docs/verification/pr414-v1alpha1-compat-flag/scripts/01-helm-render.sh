#!/usr/bin/env bash
# F1 (BLOCKER) -- deploy/helm/rbgs/templates/rbac/clusterrole.yaml renders an
# invalid ClusterRole: the first Helm action uses a left-chomp ({{- ) directly
# after a block of literal YAML comments, so the newline that terminates the
# last comment line is eaten and `apiVersion:` is glued onto the end of that
# comment. The whole key disappears into the comment and the document has no
# apiVersion -> `helm install` fails for EVERY value shape, including defaults.
#
# POLARITY: contract. This script FAILS while the bug is present and must turn
# GREEN once the template is fixed. Do not invert it.
#
# No cluster required.
set -uo pipefail
ROOT="$(git rev-parse --show-toplevel)"
CHART="$ROOT/deploy/helm/rbgs"
WORK=${WORK:-/root/pr414-verify}
mkdir -p "$WORK"

# Value shapes an operator can realistically arrive with, including the two
# upgrade shapes that older values.yaml files produce (missing keys).
declare -a SHAPES=(
  "default:"
  "compat-disabled:--set controller.features.v1alpha1Compat.enabled=false"
  "compat-enabled:--set controller.features.v1alpha1Compat.enabled=true"
  "empty-v1alpha1Compat:--set controller.features.v1alpha1Compat=null"
  "empty-features:--set controller.features=null"
)

fail=0
echo "== helm $(helm version --short 2>/dev/null) =="
echo "== chart: $CHART =="
echo

for shape in "${SHAPES[@]}"; do
  name=${shape%%:*}
  args=${shape#*:}
  out="$WORK/clusterrole-$name.yaml"

  # shellcheck disable=SC2086
  if ! helm template rbgs "$CHART" --namespace rbg-system $args \
        --show-only templates/rbac/clusterrole.yaml > "$out" 2>"$out.err"; then
    echo "[$name] helm template FAILED:"
    sed 's/^/    /' "$out.err"
    fail=1
    continue
  fi

  # 1) does a top-level `apiVersion:` key survive rendering?
  if grep -qE '^apiVersion:' "$out"; then
    api_ok=yes
  else
    api_ok=no
  fi

  # 2) does the document actually parse into an object with apiVersion+kind?
  parsed=$(python3 - "$out" <<'PY'
import sys, yaml
docs = [d for d in yaml.safe_load_all(open(sys.argv[1])) if d]
if not docs:
    print("NO-DOCS")
    sys.exit(0)
for d in docs:
    print("%s|%s|%s" % (d.get("apiVersion"), d.get("kind"), (d.get("metadata") or {}).get("name")))
PY
)

  # 3) the exact symptom CI reports: apiVersion swallowed by the comment above it
  glued=$(grep -nE '^#.*[^[:space:]]apiVersion:' "$out" || true)

  printf "[%-21s] apiVersion-key=%-3s parsed=%s\n" "$name" "$api_ok" "$parsed"
  if [ -n "$glued" ]; then
    echo "    GLUED INTO COMMENT -> $glued"
  fi

  case "$parsed" in
    None\|*|NO-DOCS|*None*)
      echo "    => INVALID: helm/kubectl will reject this with 'apiVersion not set'"
      fail=1
      ;;
  esac
  [ "$api_ok" = yes ] || fail=1
done

echo
if [ "$fail" -ne 0 ]; then
  echo "RESULT: F1 REPRODUCED -- the rendered ClusterRole has no apiVersion."
  echo "        Upstream CI hits this as: e2e-test / 'Deploy controller' ->"
  echo "        Error: unable to build kubernetes objects from release manifest:"
  echo "        error validating data: apiVersion not set"
  exit 1
fi
echo "RESULT: F1 NOT reproduced -- every shape rendered a valid ClusterRole."
exit 0
