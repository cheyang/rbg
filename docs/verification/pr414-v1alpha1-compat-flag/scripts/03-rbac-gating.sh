#!/usr/bin/env bash
# CONTROL / harness-bites check for the RBAC gating itself.
#
# This is the "does the feature do what it claims" test, and it is what makes
# the other RBAC findings trustworthy: if this script could not tell enabled
# from disabled, 02-rbac-drift.sh would be measuring noise.
#
# Asserts, with compat DISABLED:
#   - deployments / statefulsets / leaderworkersets rules are gone
#   - controllerrevisions AND controllerrevisions/status survive (they were
#     shuffled out of the gated block by this PR -- easy to lose by accident)
#   - roleinstancesets, services, pods, leases survive
#
# POLARITY: contract. Expected GREEN on this head.
# No cluster required.
set -uo pipefail
ROOT="$(git rev-parse --show-toplevel)"
CHART="$ROOT/deploy/helm/rbgs"
WORK=${WORK:-/root/pr414-verify}
mkdir -p "$WORK"

for mode in true false; do
  helm template rbgs "$CHART" --namespace rbg-system \
    --set controller.features.v1alpha1Compat.enabled=$mode \
    --show-only templates/rbac/clusterrole.yaml > "$WORK/gate-$mode.yaml" 2>/dev/null
done

python3 - "$WORK/gate-true.yaml" "$WORK/gate-false.yaml" <<'PY'
import sys, yaml

def load(path):
    text = open(path).read()
    out = []
    for line in text.splitlines():          # repair F1 so gating is measurable
        if line.startswith("#") and "apiVersion:" in line:
            i = line.index("apiVersion:")
            out.append(line[:i]); out.append(line[i:])
        else:
            out.append(line)
    for d in yaml.safe_load_all("\n".join(out)):
        if d and d.get("kind") == "ClusterRole":
            res = set()
            for r in (d.get("rules") or []):
                for g in (r.get("apiGroups") or [""]):
                    for x in (r.get("resources") or []):
                        res.add((g, x))
            return res
    return set()

on, off = load(sys.argv[1]), load(sys.argv[2])

MUST_BE_GATED = [
    ("apps", "deployments"), ("apps", "statefulsets"),
    ("apps", "deployments/status"), ("apps", "statefulsets/status"),
    ("apps", "deployments/finalizers"), ("apps", "statefulsets/finalizers"),
    ("leaderworkerset.x-k8s.io", "leaderworkersets"),
    ("leaderworkerset.x-k8s.io", "leaderworkersets/status"),
]
MUST_SURVIVE = [
    ("apps", "controllerrevisions"), ("apps", "controllerrevisions/status"),
    ("workloads.x-k8s.io", "roleinstancesets"),
    ("", "services"), ("", "pods"), ("", "events"),
    ("coordination.k8s.io", "leases"),
]

bad = 0
print("resources with compat=true : %d" % len(on))
print("resources with compat=false: %d" % len(off))
print("")
print("-- must be REMOVED when compat=false --")
for k in MUST_BE_GATED:
    present_on, present_off = k in on, k in off
    ok = present_on and not present_off
    if not ok:
        bad += 1
    print("   %-6s %-28s %-32s on=%s off=%s" % ("OK" if ok else "FAIL", k[0] or '(core)', k[1], present_on, present_off))

print("")
print("-- must SURVIVE in both modes --")
for k in MUST_SURVIVE:
    ok = (k in on) and (k in off)
    if not ok:
        bad += 1
    print("   %-6s %-28s %-32s on=%s off=%s" % ("OK" if ok else "FAIL", k[0] or '(core)', k[1], k in on, k in off))

print("")
if bad:
    print("RESULT: gating is WRONG in %d place(s)." % bad)
    sys.exit(1)
print("RESULT: gating behaves as documented -- the harness can tell the two modes")
print("        apart, so the drift/render findings are not measuring noise.")
PY
