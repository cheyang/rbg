#!/usr/bin/env bash
# check-warning-only-failpolicy.sh — static evidence for F5 / F2(carry):
# parse the SHIPPED deploy/kubectl/manifests.yaml and print, for each v1alpha1
# deprecation webhook, its failurePolicy and operations. Asserts:
#   (a) F2-carry: none of the six registers DELETE (delete path stays unblocked).
#   (b) F5: the four warning-only webhooks (instance, instanceset,
#       scalingadapter, clusterengineruntimeprofile) carry failurePolicy=Fail on
#       CREATE+UPDATE — the outage blast-radius this review flags.
# Exit 0 when the shipped shape matches those descriptions (evidence captured);
# exit 1 when the shipped config changed shape (Harness-update signal).
set -euo pipefail
MANIFEST="${1:-deploy/kubectl/manifests.yaml}"
python3 - "$MANIFEST" <<'PY'
import sys, yaml

docs = [d for d in yaml.safe_load_all(open(sys.argv[1])) if isinstance(d, dict)]
vwcs = [d for d in docs if d.get("kind") == "ValidatingWebhookConfiguration"]
assert vwcs, "no ValidatingWebhookConfiguration found"
hooks = {}
for vwc in vwcs:
    for w in vwc.get("webhooks", []):
        hooks[w["name"]] = w

deprecation = {n: w for n, w in hooks.items() if n.endswith("-v1alpha1-deprecation.kb.io")}
warning_only = {n: w for n, w in deprecation.items()
                if not any(k in n for k in ("vrolebasedgroup-", "vrolebasedgroupset-"))}

print(f"{'webhook':60} {'failurePolicy':13} operations")
for n in sorted(deprecation):
    w = deprecation[n]
    ops = sorted({op for r in w.get("rules", []) for op in r.get("operations", [])})
    print(f"{n:60} {w.get('failurePolicy'):13} {','.join(ops)}")

ok = True
if len(deprecation) != 6:
    print(f"!! expected 6 v1alpha1 deprecation webhooks, found {len(deprecation)}"); ok = False
for n, w in deprecation.items():
    ops = {op for r in w.get("rules", []) for op in r.get("operations", [])}
    if "DELETE" in ops:
        print(f"!! {n} intercepts DELETE (F2 regression)"); ok = False
for n, w in warning_only.items():
    if w.get("failurePolicy") != "Fail":
        print(f"!! {n} failurePolicy={w.get('failurePolicy')} (F5 fixed: warning-only no longer Fail)"); ok = False
print("F5 evidence: warning-only v1alpha1 webhooks ship failurePolicy=Fail on CREATE+UPDATE" if ok else "shape changed")
sys.exit(0 if ok else 1)
PY
