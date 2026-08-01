#!/usr/bin/env bash
# F4 (CONTRACT test) -- the upgrade guard must not break the project's own documented
# install command.
#
# deploy/helm/rbgs/templates/upgrade-guard.yaml hard-fails every render where
# .Release.IsUpgrade is true. `helm upgrade --install` sets that on the second and every
# later invocation against the same release. That command is what the repo documents and
# what CI/dev tooling runs.
#
# Exit 0 = no documented call site is broken. Exit 1 = finding reproduced.
set -uo pipefail
cd "$(dirname "$0")/../../../.." || exit 2
CHART=deploy/helm/rbgs
HELM=${HELM:-helm}
fail=0

echo "=== F4: upgrade guard vs. the repo's own documented commands ==="
echo
echo "--- the guard ---"
cat "$CHART/templates/upgrade-guard.yaml"
echo

# 1. Prove the guard fires on an upgrade render. `helm template --is-upgrade` sets
#    .Release.IsUpgrade without needing any cluster or prior release, so this is
#    deterministic and side-effect free.
echo "--- install render (.Release.IsUpgrade=false) ---"
if $HELM template rbgs "$CHART" --namespace rbgs-system >/dev/null 2>&1; then
  echo "  install render: OK"
else
  echo "  install render: FAILED (unexpected -- would be a separate, worse bug)"
  fail=1
fi

echo "--- upgrade render (.Release.IsUpgrade=true) ---"
up=$($HELM template rbgs "$CHART" --namespace rbgs-system --is-upgrade 2>&1)
if [ $? -ne 0 ]; then
  echo "  upgrade render: HARD FAIL, as the guard intends:"
  printf '    %s\n' "$(printf '%s\n' "$up" | head -3)"
  guard_fires=1
else
  echo "  upgrade render: succeeded -- guard did NOT fire"
  guard_fires=0
fi
echo

if [ "$guard_fires" -ne 1 ]; then
  echo "RESULT: guard does not fire on upgrade; F4 does not apply as described."
  exit 0
fi

# 2. Inventory every place the repo tells a user (or a machine) to run `helm upgrade`.
#    Each of these works exactly once per cluster and hard-fails afterwards.
echo "--- documented/automated call sites that the guard breaks on re-run ---"
# Match both the literal `helm upgrade` and the Makefile's `$(HELM) upgrade`.
sites=$(grep -rnE '(helm|\$\(HELM\)) upgrade' \
          --include='*.md' --include='*.yml' --include='*.yaml' --include='*.sh' \
          --include='Makefile' . 2>/dev/null \
        | grep -v 'docs/verification/' \
        | grep -viE 'not supported|does \*\*not\*\* support|only supports fresh')
if [ -z "$sites" ]; then
  echo "  (none found)"
else
  printf '%s\n' "$sites" | sed 's/^/  /'
  n=$(printf '%s\n' "$sites" | wc -l | tr -d ' ')
  echo
  echo "  -> $n call site(s) invoke 'helm upgrade'."
  fail=1
fi
echo

# 3. The sharpest instance: the chart's own README forbids upgrade and then uses it as
#    the documented install command, in the same file.
echo "--- self-contradiction inside $CHART/README.md ---"
forbid=$(grep -n 'only supports fresh installation\|does \*\*not\*\* support' "$CHART/README.md" | head -3)
useit=$(grep -n 'helm upgrade --install' "$CHART/README.md" | head -3)
echo "  forbids upgrade at:"; printf '    %s\n' "$forbid"
echo "  yet documents it as the install command at:"; printf '    %s\n' "$useit"
if [ -n "$forbid" ] && [ -n "$useit" ]; then
  echo "  -> same README both forbids 'helm upgrade' and prescribes 'helm upgrade --install'."
  fail=1
fi
echo

# 4. Why CI cannot catch this: every job builds a brand-new cluster, so IsUpgrade is
#    never true. Show that no workflow installs the chart twice.
echo "--- why CI stays green ---"
echo "  kind cluster creations per workflow (fresh cluster => IsUpgrade always false):"
grep -rn 'kind create cluster' .github/workflows/ 2>/dev/null | sed 's/^/    /'
echo "  chart installs per workflow:"
grep -rn 'helm upgrade --install rbgs\|helm install rbgs' .github/workflows/ 2>/dev/null | sed 's/^/    /'
echo

if [ "$fail" -eq 1 ]; then
  echo "RESULT: F4 REPRODUCED -- the guard hard-fails 'helm upgrade --install', which is the"
  echo "        command the repo documents in its READMEs, doc/install.md, Makefile, the"
  echo "        stress-test script and two CI workflows. Each works once per cluster."
  exit 1
fi
echo "RESULT: F4 not reproduced."
exit 0
