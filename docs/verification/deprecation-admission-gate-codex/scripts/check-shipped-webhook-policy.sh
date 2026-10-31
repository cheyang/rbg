#!/usr/bin/env bash
# check-shipped-webhook-policy.sh — shipped-bytes contract for ledger F2 /
# F2-carry: parse the SHIPPED deploy/kubectl/manifests.yaml and print, for each
# v1alpha1 deprecation webhook, its failurePolicy and operations. Asserts the
# FIXED shape (since 22acc570):
#   (a) the four warning-only webhooks (instance, instanceset, scalingadapter,
#       clusterengineruntimeprofile) carry failurePolicy=Ignore on CREATE+UPDATE
#       — their handlers can never reject, so Fail would only block writes
#       during an outage for zero enforcement value;
#   (b) the two enforcing RBG/RBGSet endpoints keep failurePolicy=Fail
#       (fail-closed is the point of the gate);
#   (c) none of the six registers DELETE (delete path stays unblocked).
# Exit 0 when the shipped shape matches; exit 1 on any regression.
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

enforcing = {n: w for n, w in deprecation.items() if n not in warning_only}

ok = True
if len(deprecation) != 6:
    print(f"!! expected 6 v1alpha1 deprecation webhooks, found {len(deprecation)}"); ok = False
for n, w in deprecation.items():
    ops = {op for r in w.get("rules", []) for op in r.get("operations", [])}
    if "DELETE" in ops:
        print(f"!! {n} intercepts DELETE (delete path must stay unblocked)"); ok = False
for n, w in warning_only.items():
    if w.get("failurePolicy") != "Ignore":
        print(f"!! {n} failurePolicy={w.get('failurePolicy')} (warning-only must be Ignore — ledger F2)"); ok = False
for n, w in enforcing.items():
    if w.get("failurePolicy") != "Fail":
        print(f"!! {n} failurePolicy={w.get('failurePolicy')} (enforcing gate must stay Fail)"); ok = False
print("F2 contract holds: warning-only v1alpha1 webhooks ship Ignore, enforcing RBG/RBGSet ship Fail, no DELETE" if ok else "shape changed")
sys.exit(0 if ok else 1)
PY
