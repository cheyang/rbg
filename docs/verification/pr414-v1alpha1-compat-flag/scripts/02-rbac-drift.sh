#!/usr/bin/env bash
# F2 -- the PR stops `make manifests` from syncing config/rbac/role.yaml into
# deploy/helm/rbgs/templates/rbac/clusterrole.yaml and replaces that with a
# "keep it in sync by hand" comment. This script measures whether the two are
# actually in sync AT THIS HEAD, with v1alpha1 compat ENABLED -- the mode that
# is supposed to be byte-equivalent to the old generated output.
#
# POLARITY: contract. Any drift reported here is real drift; the script turns
# green when the chart matches the generated role again.
#
# No cluster required.
set -uo pipefail
ROOT="$(git rev-parse --show-toplevel)"
CHART="$ROOT/deploy/helm/rbgs"
GEN="$ROOT/config/rbac/role.yaml"
WORK=${WORK:-/root/pr414-verify}
mkdir -p "$WORK"

helm template rbgs "$CHART" --namespace rbg-system \
  --set controller.features.v1alpha1Compat.enabled=true \
  --show-only templates/rbac/clusterrole.yaml > "$WORK/drift-chart.yaml" 2>/dev/null

# The chart may be unparseable (see 01-helm-render.sh). Recover the rules by
# stripping the comment block so drift can still be measured independently of F1.
python3 - "$WORK/drift-chart.yaml" "$GEN" <<'PY'
import sys, yaml

def load_rules(path, strip_comments=False):
    text = open(path).read()
    if strip_comments:
        # Repair the F1 damage: re-break the line where apiVersion got glued
        # onto a comment, so this check measures DRIFT, not F1.
        out = []
        for line in text.splitlines():
            if line.startswith("#") and "apiVersion:" in line:
                idx = line.index("apiVersion:")
                out.append(line[:idx])
                out.append(line[idx:])
            else:
                out.append(line)
        text = "\n".join(out)
    docs = [d for d in yaml.safe_load_all(text) if d]
    for d in docs:
        if d.get("kind") == "ClusterRole":
            return d.get("rules") or []
    return []

def norm(rules):
    """Canonical set of (apiGroup, resource, verb) triples."""
    trips = set()
    for r in rules:
        for g in (r.get("apiGroups") or [""]):
            for res in (r.get("resources") or []):
                for v in (r.get("verbs") or []):
                    trips.add((g, res, v))
    return trips

chart = norm(load_rules(sys.argv[1], strip_comments=True))
gen   = norm(load_rules(sys.argv[2]))

only_gen   = sorted(gen - chart)
only_chart = sorted(chart - gen)

print("generated (config/rbac/role.yaml) triples : %d" % len(gen))
print("chart (compat enabled) triples            : %d" % len(chart))
print("")
if only_gen:
    print("MISSING FROM CHART (%d) -- controller has the kubebuilder marker but the" % len(only_gen))
    print("chart never grants it, so a Helm install is short this permission:")
    for g, res, v in only_gen:
        print("    %-28s %-34s %s" % (g or '(core)', res, v))
    print("")
if only_chart:
    print("EXTRA IN CHART (%d) -- granted by Helm but not generated from markers:" % len(only_chart))
    for g, res, v in only_chart:
        print("    %-28s %-34s %s" % (g or '(core)', res, v))
    print("")

if only_gen or only_chart:
    print("RESULT: F2 REPRODUCED -- chart and generated role are out of sync at this")
    print("        head, with nothing in CI to catch it now that `make manifests`")
    print("        no longer regenerates the chart.")
    sys.exit(1)
print("RESULT: F2 not reproduced -- chart (compat enabled) == generated role.")
PY
