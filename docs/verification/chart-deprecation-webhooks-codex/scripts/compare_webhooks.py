#!/usr/bin/env python3
"""Compare the six v1alpha1 deprecation webhook entries between the rendered
Helm chart and the shipped config/webhook/manifests.yaml (PR #552 claim:
'byte-matched ... names, paths, rules, matchPolicy: Exact')."""
import subprocess, sys, yaml, os

os.environ["KUBECONFIG"] = "/tmp/fakekube/config"
REPO = "/home/ecs-user/.pr-debate/homes/codex/repos/sgl-project/rbg"

rendered = subprocess.run(
    ["helm", "template", "rbgs", f"{REPO}/deploy/helm/rbgs", "--namespace", "rbgs-system"],
    capture_output=True, text=True, check=True).stdout

chart_vwc = None
for doc in yaml.safe_load_all(rendered):
    if doc and doc.get("kind") == "ValidatingWebhookConfiguration":
        chart_vwc = doc
        break
assert chart_vwc, "no ValidatingWebhookConfiguration in rendered chart"

cfg_vwc = None
with open(f"{REPO}/config/webhook/manifests.yaml") as f:
    for doc in yaml.safe_load_all(f):
        if doc and doc.get("kind") == "ValidatingWebhookConfiguration":
            cfg_vwc = doc
assert cfg_vwc, "no ValidatingWebhookConfiguration in config/webhook/manifests.yaml"

def norm(entry):
    e = {k: v for k, v in entry.items()}
    # service name/namespace legitimately differ between the two install paths
    e["clientConfig"] = {"service": {"path": entry["clientConfig"]["service"]["path"]}}
    return e

chart_hooks = {w["name"]: norm(w) for w in chart_vwc["webhooks"]}
cfg_hooks = {w["name"]: norm(w) for w in cfg_vwc["webhooks"]}

DEPRECATION = [n for n in cfg_hooks if n.endswith("-v1alpha1-deprecation.kb.io")]
print(f"shipped config deprecation webhooks: {len(DEPRECATION)}")
fail = 0
for name in sorted(DEPRECATION):
    c = chart_hooks.get(name)
    if c is None:
        print(f"MISSING in chart: {name}")
        fail = 1
        continue
    if c != cfg_hooks[name]:
        print(f"MISMATCH: {name}")
        for k in sorted(set(c) | set(cfg_hooks[name])):
            if c.get(k) != cfg_hooks[name].get(k):
                print(f"  {k}: chart={c.get(k)!r} config={cfg_hooks[name].get(k)!r}")
        fail = 1
    else:
        print(f"MATCH: {name}")
# also confirm the chart's pre-existing v1alpha2 webhooks still match the shipped ones
for name in ("vrolebasedgroup.kb.io", "vrolebasedgroupset.kb.io", "vcoordinatedpolicy.kb.io"):
    c, s = chart_hooks.get(name), cfg_hooks.get(name)
    print(f"v1alpha2 {name}: {'MATCH' if c == s else 'DIFF (pre-existing)'}")
sys.exit(fail)
