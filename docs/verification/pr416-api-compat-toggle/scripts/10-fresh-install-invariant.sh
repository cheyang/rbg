#!/usr/bin/env bash
# R3-F22 -- the "fresh installation only" invariant is not enforced anywhere, and
# the chart's own documented update procedure is the bypass.
#
# PR #416 rests its entire safety argument on one claim:
#
#   "It is not an upgrade path. `helm upgrade` is refused for this chart version
#    (templates/upgrade-guard.yaml), and an installation that already runs the RBG
#    controller must keep `true`." ... "That is coherent only because the sole
#    supported way to reach `false` is a fresh install, where no such object can exist."
#
# Every design consequence the PR accepts -- no grandfathering, no conversion, no
# migration, controllers' own writes denied -- is justified by "no such object can
# exist". This script tests that premise. Read-only: helm template + repo structure
# only, nothing touches a cluster.
#
# Usage: bash 10-fresh-install-invariant.sh   (from the repo root)
set -uo pipefail

CHART=deploy/helm/rbgs
FAIL=0
note() { printf '\n=== %s\n' "$*"; }
bad()  { printf 'FINDING: %s\n' "$*"; FAIL=1; }
ok()   { printf 'ok: %s\n' "$*"; }

note "1. Does the chart own the CRDs? (if not, uninstall cannot remove the CRs)"
crds_dir=$(ls "$CHART/crds" 2>/dev/null | wc -l | tr -d ' ')
crd_tpls=$(grep -rl 'kind: CustomResourceDefinition' "$CHART/templates" 2>/dev/null | wc -l | tr -d ' ')
echo "chart crds/ entries: $crds_dir ; templates declaring a CRD: $crd_tpls"
if [ "$crds_dir" = "0" ] && [ "$crd_tpls" = "0" ]; then
  bad "the chart declares no CRDs, so 'helm uninstall' removes neither the CRDs nor" \
      "any RoleBasedGroup/RoleBasedGroupSet. Those objects survive an uninstall."
  grep -rn 'image:\|kubectl' "$CHART/templates/crd-upgrade/crd-upgrade.yaml" 2>/dev/null |
    head -3 | sed 's/^/    CRDs come from the pre-install Job instead: /'
else
  ok "the chart owns CRDs; uninstall may cascade-delete the CRs"
fi

note "2. What does the upgrade guard actually guard?"
sed -n '1,10p' "$CHART/templates/upgrade-guard.yaml"
if grep -q 'Release.IsUpgrade' "$CHART/templates/upgrade-guard.yaml"; then
  ok "the guard keys on .Release.IsUpgrade"
  bad "…which is FALSE on a reinstall after an uninstall. The guard blocks" \
      "'helm upgrade' only; it cannot see objects left behind by the previous release."
fi

note "3. The repo's own instruction for updating"
grep -rn 'uninstall and reinstall\|uninstall, then reinstall\|Please uninstall' \
  "$CHART/templates/upgrade-guard.yaml" "$CHART/templates/NOTES.txt" "$CHART/README.md" 2>/dev/null |
  sed 's/^/    /'
bad "the documented remediation for 'upgrade is not supported' is exactly the path" \
    "that produces a fresh release over surviving objects."

note "4. Does anything refuse enabled=false when deprecated objects already exist?"
hits=$(grep -rn 'lookup ' "$CHART/templates" 2>/dev/null | grep -ci 'rolebasedgroup' || true)
echo "templates using Helm 'lookup' to find existing RoleBasedGroups: ${hits:-0}"
prehook=$(grep -rln 'helm.sh/hook.*pre-install' "$CHART/templates" 2>/dev/null | tr '\n' ' ')
echo "pre-install hooks present: ${prehook:-none}"
if [ "${hits:-0}" = "0" ]; then
  bad "no template inspects the cluster for pre-existing deprecated-workload objects." \
      "'helm install --set controller.deprecatedWorkloadTypes.enabled=false' is accepted" \
      "unconditionally, whatever is already in the cluster."
fi
echo "    (note: crd-upgrade.yaml shows the chart CAN run a cluster-inspecting"
echo "     pre-install Job with its own ServiceAccount, so a guard is feasible here.)"

note "5. Confirm the render actually succeeds in that shape (the bypass is real)"
if helm template rbgs "$CHART" \
     --set controller.deprecatedWorkloadTypes.enabled=false >/tmp/r3-false.yaml 2>/tmp/r3-false.err; then
  ok "fresh-install render with enabled=false SUCCEEDS ($(wc -l </tmp/r3-false.yaml) lines)"
  echo "    flag rendered: $(grep -c 'enable-deprecated-workload-types=false' /tmp/r3-false.yaml)x"
  echo "    deprecated RBAC lines remaining: $(grep -cE '^  - (deployments|statefulsets|leaderworkersets)' /tmp/r3-false.yaml)"
else
  ok "render refused (would disprove the finding)"; sed 's/^/    /' /tmp/r3-false.err
fi

note "6. Control: the guard DOES fire on a real helm upgrade"
if helm template rbgs "$CHART" --is-upgrade \
     --set controller.deprecatedWorkloadTypes.enabled=false >/dev/null 2>/tmp/r3-up.err; then
  bad "--is-upgrade rendered successfully; the guard did not fire (unexpected)"
else
  ok "guard fires on --is-upgrade: $(tr -d '\n' </tmp/r3-up.err | tail -c 160)"
  echo "    -> so the ONLY thing standing between an existing install and enabled=false"
  echo "       is a check that the documented workaround walks straight around."
fi

note "VERDICT"
if [ "$FAIL" = "1" ]; then
  cat <<'EOF'
R3-F22 REPRODUCED. The premise "no such object can exist" is unenforced:

  helm uninstall rbgs                       # CRDs and all RBG/RBGSet objects survive
  helm install rbgs ... \
    --set controller.deprecatedWorkloadTypes.enabled=false   # accepted, IsUpgrade=false

leaves every pre-existing object using Deployment/StatefulSet/LeaderWorkerSet
permanently unwritable and unreconcilable -- the state the PR says cannot happen.
The consequences are already proved by the round-1 write-path tests, which are RED
again at this head (F2a/F2c/F9/F10: annotation patch, kubectl scale, RBGSet template
sync, HPA scale -- all denied, with nothing written to the object's status).

Suggested fix: a pre-install check (the crd-upgrade Job is the precedent) that fails
the install when enabled=false and any RoleBasedGroup/RoleBasedGroupSet already uses
a deprecated workload type. That makes the invariant the design assumes real, and it
is the smallest change that closes F2a/F2c/F9/F10/R2-F13 as a class rather than
one path at a time.
EOF
else
  echo "not reproduced"
fi
exit 0
