#!/usr/bin/env bash
# LIVE layer (READ-ONLY) -- does a real Kubernetes API server accept what this chart renders?
#
# This is the regression guard for the PR #414 blocker class: there, the chart rendered
# "successfully" but produced a ClusterRole whose apiVersion was swallowed into a comment, and
# only a real API server (or a parser) rejected it. PR #416 adds new conditionals to the same
# template, so the check is re-run here.
#
# SAFETY: nothing is persisted. Every call uses --dry-run=server, and every object is renamed
# with a `pr416dry-` prefix so it cannot be confused with -- or interact with -- the rbgs
# release that already exists on this shared cluster. No cluster-wide or destructive action is
# taken. The mutating arms (a real `helm install`, then a real `helm upgrade` to exercise the
# upgrade guard end to end) are deliberately NOT run here: this chart's ClusterRole,
# ClusterRoleBinding and ValidatingWebhookConfiguration have fixed names, so installing it
# would overwrite the live controller's own cluster-scoped objects. Run those on a throwaway
# cluster instead.
set -uo pipefail
cd "$(dirname "$0")/../../../.." || exit 2
CHART=deploy/helm/rbgs
HELM=${HELM:-helm}
KUBECTL=${KUBECTL:-kubectl}
fail=0

echo "=== LIVE (read-only): server-side validation of the rendered chart ==="
$KUBECTL version -o json 2>/dev/null | python3 -c '
import json,sys
d=json.load(sys.stdin)
print("  server:", d.get("serverVersion",{}).get("gitVersion"))
' || { echo "  no cluster reachable -- skipping live layer"; exit 0; }
echo "  context: $($KUBECTL config current-context 2>/dev/null)"
echo

# Guard: confirm we are NOT about to be confused by the pre-existing release.
echo "--- pre-existing rbgs install on this cluster (left untouched) ---"
$KUBECTL get clusterrole rbgs-controller-role -o jsonpath='{.metadata.name}{"\n"}' 2>/dev/null \
  | sed 's/^/  existing ClusterRole: /' || echo "  (none)"
echo

for shape in "default:" "compat-disabled:--set compatibility.v1alpha1.enabled=false"; do
  name=${shape%%:*}; args=${shape#*:}
  echo "--- shape: $name ---"

  # shellcheck disable=SC2086
  rendered=$($HELM template rbgs "$CHART" --namespace rbgs-system $args 2>/dev/null)

  # Rename every object so this can only ever be a *create* validation of a throwaway name.
  # Rename, and retarget namespaced objects to a namespace that already exists. The chart's
  # own namespace (rbgs-system) is NOT present on this cluster, and creating it just to run a
  # dry-run would be a mutation of shared infrastructure for no benefit -- the schema
  # validation we are after is namespace-independent.
  probe=$(printf '%s\n' "$rendered" | python3 -c '
import sys, yaml
out = []
for d in yaml.safe_load_all(sys.stdin.read()):
    if not d: continue
    md = d.setdefault("metadata", {})
    if md.get("name"):
        md["name"] = "pr416dry-" + md["name"]
    if md.get("namespace"):
        md["namespace"] = "default"
    out.append(d)
print(yaml.safe_dump_all(out))
')

  res=$(printf '%s\n' "$probe" | $KUBECTL apply --dry-run=server -n default -f - 2>&1)
  rc=$?
  ok=$(printf '%s\n' "$res" | grep -c 'server dry run')
  total=$(printf '%s\n' "$probe" | grep -c '^kind:\|^- kind:')
  echo "  objects accepted server-side: $ok"
  if [ $rc -ne 0 ]; then
    echo "  REJECTED:"
    printf '%s\n' "$res" | grep -vi 'server dry run' | head -8 | sed 's/^/    /'
    fail=1
  fi
done
echo

# CONTROL: a deliberately malformed object must be REJECTED by the same command, proving this
# check can actually fail and is not vacuously green.
echo "--- CONTROL: an apiVersion-less object must be rejected ---"
ctl=$(printf 'kind: ClusterRole\nmetadata:\n  name: pr416dry-control\nrules: []\n' \
      | $KUBECTL apply --dry-run=server -f - 2>&1)
if printf '%s\n' "$ctl" | grep -qi 'apiVersion'; then
  echo "  correctly rejected: $(printf '%s\n' "$ctl" | head -1)"
else
  echo "  CONTROL FAILED -- server accepted a document with no apiVersion; this check proves nothing"
  fail=1
fi
echo

if [ "$fail" -eq 0 ]; then
  echo "RESULT: a real API server accepts every document this chart renders (both shapes)."
  echo "        No PR#414-class render blocker at this head."
  exit 0
fi
echo "RESULT: server-side validation FAILED -- see above."
exit 1
