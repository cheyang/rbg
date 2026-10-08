#!/usr/bin/env python3
"""Verification harness for sgl-project/rbg PR #389 (autoscaling best-practice docs).

Contract-polarity checks: every check encodes the CORRECT state of the docs.
On the PR head under review the checks for F1-F7 FAIL (that failure *is* the
reproduction); once the docs are fixed they PASS.

Usage: check_docs.py [DOC_ROOT] [--offline]
  DOC_ROOT  directory containing the four new files (default: repo cwd)
  --offline skip integration layer (network probes / upstream repo fetches)
"""
import json
import os
import re
import subprocess
import sys
import tempfile
import urllib.request

DOC_ROOT = os.path.abspath(sys.argv[1]) if len(sys.argv) > 1 and not sys.argv[1].startswith("--") else os.getcwd()
OFFLINE = "--offline" in sys.argv

# Upstream planner repo pinned to the commit this harness was built against.
PLANNER_REPO = "rolebasedgroup/rbg-planner"
PLANNER_SHA = "0d35ed762e826292f7f420617a62124b358170d4"

FILES = [
    "doc/best-practice/en/08-configuring-autoscaling.md",
    "doc/best-practice/en/08-configuring-autoscaling-guide.md",
    "doc/best-practice/zh/08-configuring-autoscaling.md",
    "doc/best-practice/zh/08-configuring-autoscaling-guide.md",
]

results = []  # (finding, layer, name, ok, detail)

def record(finding, layer, name, ok, detail=""):
    results.append((finding, layer, name, bool(ok), detail))
    print("[%s][%s] %-55s %s %s" % (finding, layer, name, "PASS" if ok else "FAIL", detail))

def read(rel):
    with open(os.path.join(DOC_ROOT, rel), encoding="utf-8") as f:
        return f.read()

docs = {}
for rel in FILES:
    try:
        docs[rel] = read(rel)
    except FileNotFoundError:
        docs[rel] = None
missing = [r for r, t in docs.items() if t is None]
record("F0", "unit", "four new doc files present", not missing, "missing=%s" % missing)
alltext = "\n".join(t for t in docs.values() if t)

def gh_api(path):
    req = urllib.request.Request("https://api.github.com" + path,
                                 headers={"User-Agent": "pr389-doc-verify", "Accept": "application/vnd.github+json"})
    try:
        with urllib.request.urlopen(req, timeout=20) as r:
            return r.status
    except urllib.error.HTTPError as e:
        return e.code

def fetch_raw(repo, sha, path):
    url = "https://raw.githubusercontent.com/%s/%s/%s" % (repo, sha, path)
    req = urllib.request.Request(url, headers={"User-Agent": "pr389-doc-verify"})
    with urllib.request.urlopen(req, timeout=30) as r:
        return r.read().decode("utf-8")

# ---------------------------------------------------------------- F1: wrong org for rbg-planner
hits = [(rel, i + 1) for rel, t in docs.items() if t
        for i, line in enumerate(t.splitlines())
        if "sgl-project/rbg-planner" in line or "ghcr.io/sgl-project/charts/rbg-planner" in line
        or "ghcr.io/sgl-project/rbg-profiler" in line]
record("F1", "unit", "no sgl-project org references to rbg-planner/profiler/chart",
       not hits, "hits=%s" % hits[:8])
if not OFFLINE:
    wrong = gh_api("/repos/sgl-project/rbg-planner")
    right = gh_api("/repos/%s" % PLANNER_REPO)
    record("F1", "integration", "evidence: sgl-project/rbg-planner does NOT exist today",
           wrong == 404, "GET /repos/sgl-project/rbg-planner -> %s" % wrong)
    record("F1", "integration", "real planner repo exists at rolebasedgroup org (evidence)",
           right == 200, "GET /repos/%s -> %s" % (PLANNER_REPO, right))
    # every github repo link used in the new docs should resolve
    links = sorted(set(re.findall(r"https://github\.com/([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)", alltext)))
    bad = []
    for l in links:
        if "/" not in l or l.endswith("/issues"):
            continue
        code = gh_api("/repos/" + l)
        if code != 200:
            bad.append((l, code))
    record("F1", "integration", "all github repo links in new docs resolve", not bad,
           "unresolvable=%s checked=%s" % (bad, links))

# ------------------------------------------------- F2: metricSource enum vs upstream CRD
crd = None
if not OFFLINE:
    crd = fetch_raw(PLANNER_REPO, PLANNER_SHA,
                    "config/crd/inference-extension.rolebasedgroup.io_autoscalers.yaml")
    import yaml as _yaml
    _crd = _yaml.safe_load(crd)
    _props = _crd["spec"]["versions"][0]["schema"]["openAPIV3Schema"]["properties"]
    _me = _props["spec"]["properties"]["implementation"]["properties"]["DynamoPlanner"]["properties"]["metricsEndpoint"]["properties"]
    enum = sorted(_me["metricSource"]["enum"])
    record("F2", "integration", "upstream CRD metricSource enum is sglang|vllm|patio (evidence)",
           enum == ["patio", "sglang", "vllm"], "enum=%s" % enum)
    doc_lists_dynamo = "sglang | vllm | dynamo" in alltext
    record("F2", "unit", "docs do not advertise invalid metricSource 'dynamo'",
           not doc_lists_dynamo, "'sglang | vllm | dynamo' present=%s" % doc_lists_dynamo)
    doc_covers_enum = all(e in alltext for e in enum) and "patio" in alltext
    record("F2", "unit", "docs cover the full valid metricSource enum (incl. patio)",
           doc_covers_enum, "")

# ------------------------------------- F3: metricsEndpoint.port is the planner's own port
if not OFFLINE:
    ctrl = fetch_raw(PLANNER_REPO, PLANNER_SHA, "internal/controller/roleautoscaler_controller.go")
    wired = "PLANNER_PROMETHEUS_PORT" in ctrl and re.search(
        r"PLANNER_PROMETHEUS_PORT.*spec\.metricsPort|metricsPort.*PLANNER_PROMETHEUS_PORT", ctrl, re.S) is not None \
        or 'Name: "PLANNER_PROMETHEUS_PORT", Value: strconv.Itoa(spec.metricsPort)' in ctrl
    record("F3", "integration", "upstream wires metricsEndpoint.port -> PLANNER_PROMETHEUS_PORT (evidence)",
           wired, "")
describes_engine_port = bool(re.search(r"metricsEndpoint\.port[^\n]*engine metrics port", alltext, re.I)) \
    or bool(re.search(r"metricsEndpoint:\n\s+metricSource: \w+\s+# [^\n]*\n\s+port: 8000", alltext))
record("F3", "unit", "docs do not describe metricsEndpoint.port as the engine metrics port",
       not describes_engine_port, "")
sets_8000 = bool(re.search(r"metricsEndpoint:\s*\n\s+metricSource:[^\n]*\n\s+port: 8000", alltext))
record("F3", "unit", "doc AutoScaler examples do not override planner metrics port to 8000",
       not sets_8000, "")

# ------------------------------------------------- F4: planner pod label selector
if not OFFLINE:
    deploy_labels = re.search(r"Template: corev1\.PodTemplateSpec\{\s*ObjectMeta: metav1\.ObjectMeta\{\s*Labels: map\[string\]string\{(.*?)\}", ctrl, re.S)
    has_app_label = deploy_labels and '"app"' in deploy_labels.group(1)
    record("F4", "integration", "upstream planner pods have no 'app' label key (evidence)",
           not has_app_label, "labels=%s" % (deploy_labels.group(1).strip() if deploy_labels else "??"))
uses_bad_selector = "-l app=rbg-planner" in alltext
record("F4", "unit", "docs do not select planner pods via -l app=rbg-planner",
       not uses_bad_selector, "")

# ------------------------------------------------- F5: stale Related Documents TODO
for rel, t in docs.items():
    if not t or rel.endswith("-guide.md"):
        continue
    sec = re.search(r"## (?:Related Documents|相关文档)\n(.*?)\Z", t, re.S)
    if not sec:
        continue
    stale_todo = ("have not been created yet" in sec.group(1)) or ("尚未创建" in sec.group(1))
    plain = [l for l in sec.group(1).splitlines()
             if re.match(r"\+\s+[^[]", l) and "RBG Planner" not in l]
    record("F5", "unit", "%s: Related Documents has no stale TODO / unlinked existing docs" % rel,
           not stale_todo and not plain, "stale_todo=%s plain_entries=%d" % (stale_todo, len(plain)))

# ------------------------------------------------- F6: autocorrect clean (zh)
def autocorrect_lint(path):
    try:
        p = subprocess.run(["npx", "-y", "autocorrect-node", "--lint", path],
                           capture_output=True, text=True, timeout=300)
        out = (p.stdout or "") + (p.stderr or "")
        m = re.search(r"Error: (\d+)", out)
        return int(m.group(1)) if m else (0 if p.returncode == 0 else 1)
    except Exception as e:
        print("autocorrect unavailable: %s" % e)
        return None

for rel in FILES:
    if "/zh/" not in rel or docs.get(rel) is None:
        continue
    n = autocorrect_lint(os.path.join(DOC_ROOT, rel))
    if n is not None:
        record("F6", "unit", "autocorrect --lint clean: %s" % rel, n == 0, "errors=%s" % n)

# ------------------------------------------------- F7: no trailing whitespace
tw = [(rel, i + 1) for rel, t in docs.items() if t
      for i, line in enumerate(t.splitlines()) if line != line.rstrip()]
record("F7", "unit", "no trailing whitespace in new docs", not tw, "hits=%s" % tw[:6])

# ------------------------------------------------- sanity: yaml blocks parse (always-on)
import yaml  # PyYAML
bad_yaml = []
for rel, t in docs.items():
    if not t:
        continue
    for i, block in enumerate(re.findall(r"```yaml\n(.*?)```", t, re.S)):
        try:
            list(yaml.safe_load_all(block))
        except Exception as e:
            bad_yaml.append((rel, i, str(e)[:80]))
record("F0", "unit", "all yaml code blocks parse", not bad_yaml, "bad=%s" % bad_yaml)

failed = [r for r in results if not r[3]]
print("\n==== SUMMARY: %d checks, %d failed ====" % (len(results), len(failed)))
for f in failed:
    print("  FAIL %s [%s] %s" % (f[0], f[1], f[2]))
sys.exit(1 if failed else 0)
