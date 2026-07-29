#!/usr/bin/env bash
# Chart-wide generalization of F1 (round 2).
#
# F1 was one template losing its `apiVersion` to a left-chomp that ate the
# newline terminating a preceding YAML comment. The fix in 0151936a repairs that
# ONE file. This script asks the broader question: does EVERY template in the
# chart still render into a valid object, under every value shape an operator
# might arrive with?
#
# It is deliberately written so the project can lift it into CI verbatim -- see
# N1 in verify-manifest.json. `lint`, `unit-test`, `envtest` and `build` were all
# green while the chart was uninstallable; only the 30-minute e2e job caught it.
#
# POLARITY: contract. Expected GREEN on 0151936a.
# No cluster required.
set -uo pipefail
ROOT="$(git rev-parse --show-toplevel)"
CHART="$ROOT/deploy/helm/rbgs"
WORK=${WORK:-/root/pr414-verify}
mkdir -p "$WORK"

declare -a SHAPES=(
  "default:"
  "compat-disabled:--set controller.features.v1alpha1Compat.enabled=false"
  "compat-enabled:--set controller.features.v1alpha1Compat.enabled=true"
  "empty-v1alpha1Compat:--set controller.features.v1alpha1Compat=null"
  "empty-features:--set controller.features=null"
)

fail=0
echo "== chart: $CHART =="
echo "== helm $(helm version --short 2>/dev/null) =="
echo

for shape in "${SHAPES[@]}"; do
  name=${shape%%:*}
  args=${shape#*:}
  out="$WORK/allrender-$name.yaml"

  # shellcheck disable=SC2086
  if ! helm template rbgs "$CHART" --namespace rbg-system $args > "$out" 2>"$out.err"; then
    echo "[$name] helm template FAILED:"
    sed 's/^/    /' "$out.err" | head -5
    fail=1
    continue
  fi

  python3 - "$out" "$name" <<'PY'
import sys, yaml
path, shape = sys.argv[1], sys.argv[2]
bad, total = [], 0
try:
    docs = list(yaml.safe_load_all(open(path)))
except Exception as e:
    print("[%s] UNPARSEABLE: %s" % (shape, e))
    sys.exit(1)
for d in docs:
    if not d:
        continue
    total += 1
    name = (d.get("metadata") or {}).get("name") if isinstance(d, dict) else None
    if not isinstance(d, dict) or not d.get("apiVersion") or not d.get("kind"):
        bad.append((d.get("kind") if isinstance(d, dict) else type(d).__name__, name))
print("[%-21s] %2d docs, %d invalid" % (shape, total, len(bad)))
for kind, name in bad:
    print("      INVALID -> kind=%r name=%r (missing apiVersion and/or kind)" % (kind, name))
if total == 0:
    print("      NO DOCS RENDERED -- the harness is not reading the chart it thinks it is")
    sys.exit(2)
sys.exit(1 if bad else 0)
PY
  rc=$?
  [ "$rc" -ne 0 ] && fail=1

  # Belt and braces: catch the exact F1 signature anywhere in the chart, i.e. a
  # top-level key glued onto the end of a comment line.
  glued=$(grep -nE '^#.*[^[:space:]](apiVersion|kind):' "$out" || true)
  if [ -n "$glued" ]; then
    echo "      GLUED-INTO-COMMENT:"
    echo "$glued" | sed 's/^/        /'
    fail=1
  fi
done

echo
if [ "$fail" -ne 0 ]; then
  echo "RESULT: at least one chart template does not render a valid object."
  exit 1
fi
echo "RESULT: every template renders valid objects under all 5 value shapes."
echo "        Suggested CI gate (would have caught F1 in seconds rather than 30 min):"
echo "          helm template rbgs deploy/helm/rbgs | kubectl apply --dry-run=client -f -"
exit 0
