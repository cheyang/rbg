#!/usr/bin/env bash
# REGRESSION GUARD (contract) -- carried over from the PR #414 round, where the equivalent of
# this check caught a blocker: a left-chomp on the first action in clusterrole.yaml swallowed
# the newline after a comment block, so `apiVersion:` ended up inside a comment and the whole
# chart was uninstallable. CI never caught it because no job renders the chart.
#
# PR #416 adds new conditionals to the same file plus a new `fail`-based template, so the same
# class of whitespace/render bug is live again. This renders every document under several value
# shapes and asserts each parses with a non-empty apiVersion/kind.
#
# This script is written to be lifted into CI verbatim (see finding N1).
set -uo pipefail
cd "$(dirname "$0")/../../../.." || exit 2
CHART=deploy/helm/rbgs
HELM=${HELM:-helm}
fail=0

shapes=(
  "default:"
  "compat-enabled:--set compatibility.v1alpha1.enabled=true"
  "compat-disabled:--set compatibility.v1alpha1.enabled=false"
  # A `compatibility:` key that is present but has no children -- what you get from a values
  # file where the child keys are commented out, or from `compatibility: {}`.
  "null-compat:--set compatibility=null"
  "empty-compat-map:--set compatibility={}"
  "null-v1alpha1:--set compatibility.v1alpha1=null"
  # CONTROL: the pre-existing blocks use the defensive `| default dict` idiom, so the same
  # shape is harmless for them. If this control ever FAILS the finding is not specific to the
  # new compatibility block and must be re-scoped.
  "empty-features(CONTROL):--set controller.features=null"
  "empty-global(CONTROL):--set global=null"
)

echo "=== render every chart document under ${#shapes[@]} value shapes (helm $($HELM version --short)) ==="
for entry in "${shapes[@]}"; do
  name=${entry%%:*}
  args=${entry#*:}
  printf '\n--- shape: %s  (%s) ---\n' "$name" "${args:-no overrides}"

  # Keep stderr OUT of $out: helm emits a kubeconfig-permissions warning there, which would
  # otherwise be fed to the YAML parser and misreported as a render defect.
  # shellcheck disable=SC2086
  out=$($HELM template rbgs "$CHART" --namespace rbgs-system $args 2>/tmp/render-err.txt)
  if [ $? -ne 0 ]; then
    echo "  RENDER FAILED:"; grep -v 'WARNING: Kubernetes configuration' /tmp/render-err.txt \
      | head -4 | sed 's/^/    /'
    fail=1
    continue
  fi

  # Parse each document and require apiVersion + kind. This is exactly what caught PR #414's
  # blocker: the render "succeeded" but produced a document with apiVersion=None.
  printf '%s\n' "$out" | python3 -c '
import sys, yaml
bad = 0; n = 0
try:
    docs = list(yaml.safe_load_all(sys.stdin.read()))
except Exception as e:
    print("    YAML PARSE ERROR: %s" % e); sys.exit(1)
for d in docs:
    if d is None: continue
    n += 1
    av, k = d.get("apiVersion"), d.get("kind")
    if not av or not k:
        bad += 1
        print("    BAD DOC: apiVersion=%r kind=%r name=%r" % (
            av, k, (d.get("metadata") or {}).get("name")))
print("    %d document(s), %d bad" % (n, bad))
sys.exit(1 if bad or n == 0 else 0)
'
  [ $? -ne 0 ] && fail=1
done

echo
echo "--- how the pre-existing blocks guard themselves, vs the new one ---"
echo "  defensive '| default dict' uses in the chart:"
grep -rn 'default dict' "$CHART/templates" | sed 's/^/    /' | head -8
echo "  direct .Values.compatibility dereferences (no guard):"
grep -rn '\.Values\.compatibility' "$CHART/templates" | sed 's/^/    /'
echo

if [ "$fail" -eq 0 ]; then
  echo "RESULT: all shapes render valid documents. No PR#414-class render regression."
  exit 0
fi
echo "RESULT: RENDER REGRESSION -- at least one shape produced an invalid/unparseable document."
exit 1
