#!/usr/bin/env bash
# Renders the chart's NOTES.txt via Helm's template engine in the
# toggle x release-mode matrix and asserts the round-3 guidance branches:
# the cutoff note must demand completed reconciliation and deleted legacy
# workloads (F8), the disabled note must point upgraders back to true, and
# the enabled+install note must recommend false for new installations.
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$HERE/../../../.." && pwd)"
CHART="${CHART_OVERRIDE:-$ROOT/deploy/helm/rbgs}"
OUT="$(cd "$HERE/render-notes" && go run . "$CHART")" || { echo "$OUT"; exit 1; }
printf '%s\n' "$OUT" > "$HERE/../results/notes-render.out"
python3 - "$HERE/../results/notes-render.out" <<'PY'
import sys
out=open(sys.argv[1]).read()
blocks={}; cur=None
for line in out.splitlines():
    if line.startswith('== '):
        cur=line.strip('= ').strip(); blocks[cur]=''
    elif cur: blocks[cur]+=line+'\n'
checks = {
  'install/toggle-default(true)': ['For a new installation, prefer controller.deprecatedWorkloadTypes.enabled=false', 'compatibility for existing deprecated workload types is ENABLED', 'will be removed in 0.10.0'],
  'install/toggle-false':         ['the deprecated workload types are DISABLED', 'set controller.deprecatedWorkloadTypes.enabled=true, migrate', 'Only RoleInstanceSet is accepted'],
  'upgrade/toggle-default(true)': ['only after reconciliation', 'old RBG-owned Deployment, StatefulSet, and', 'deprecated-role inventory'],
  'upgrade/toggle-false':         ['the deprecated workload types are DISABLED'],
}
ok=True
for scen, wants in checks.items():
    for w in wants:
        hit = w in blocks.get(scen,'')
        print(('PASS' if hit else 'FAIL'), scen, '::', w[:64])
        ok = ok and hit
cross = 'For a new installation, prefer' not in blocks.get('install/toggle-false','')
print(('PASS' if cross else 'FAIL'), 'install/toggle-false omits the enabled-only recommendation')
ok = ok and cross
sys.exit(0 if ok else 1)
PY
